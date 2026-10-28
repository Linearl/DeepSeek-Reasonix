package agent

import (
	"os"
	"time"
)

// sessionHeadState is what a Session knows about its schema-2 log position;
// it stays zero for schema-1 sessions. state caches the replayed graph so a
// save only reads the bytes appended since the last observed tail.
type sessionHeadState struct {
	ref       HeadRef
	dag       bool
	headCount int
	state     *sessionDAGState
	events    []HeadEvent
	pending   []sessionDAGEntry
	openTurn  *sessionDAGTurn
}

// HeadEvent reports a head-level fact a save discovered; the controller turns
// it into a user-facing notice.
type HeadEvent struct {
	Kind        string
	HeadID      string
	OtherWriter string
	// Class attributes the competing writer before the notice is phrased
	// (task 203): HeadDivergenceExternal/Unknown keep the outside-window
	// warning; HeadDivergenceLocal is an in-process writer, which the
	// controller resolves into "dual tab open" (real dual writer) or a
	// logged-only in-process race (the misattributed case).
	Class string
	// OtherPID and OtherHostname carry the competing writer's registered
	// identity (task 646) so the concurrent-writer log names who it was
	// instead of only an opaque writer id. Zero/empty when unknown.
	OtherPID      int
	OtherHostname string
	// UnknownReason names which identity record was missing when Class is
	// HeadDivergenceUnknown (task 646): no_replay_state, no_writer_id,
	// writer_unregistered, or pid_missing. Empty for external/local.
	UnknownReason string
	// 任务 1010: readings for the lag notices. HeadID stays the side this
	// session opened on; OtherHeadID names the competing head.
	//   - SelectedHeadBehind: Messages/OtherMessages are the two chains'
	//     materialized lengths;
	//   - MaterializationLag: Messages is the materialized .jsonl checkpoint
	//     line count, OtherMessages the newest events chain's length.
	Messages      int
	OtherMessages int
	OtherHeadID   string
}

const (
	// HeadEventForkedConcurrent: this writer's head diverged from another
	// writer's appends and continued on a fresh head.
	HeadEventForkedConcurrent = "forked_concurrent"
	// HeadEventMultipleRecentHeads: the session opened on one of several heads
	// that were active within recentHeadWindow; the others remain versions.
	HeadEventMultipleRecentHeads = "multiple_recent_heads"
	// HeadEventSelectedHeadBehind (任务 1010 需求 4): the head this session
	// opened on is strictly dominated by another live head — fewer messages
	// AND older last activity. Readings ride in Messages/OtherMessages/
	// OtherHeadID; switching is a manual versions action.
	HeadEventSelectedHeadBehind = "selected_head_behind"
	// HeadEventMaterializationLag (任务 1010 需求 3): the events log holds a
	// strictly newer, longer chain than the materialized .jsonl checkpoint.
	// Detection only — no file is ever written here; the repair belongs to
	// the manual fix-conversation entry (任务 1011).
	HeadEventMaterializationLag = "materialization_lag"
)

// HeadDivergence classes for HeadEvent.Class (task 203): who wrote the
// competing head, decided where the fork happens — the writer identity lives
// in the replayed DAG writer registry (id + pid + hostname).
const (
	// HeadDivergenceExternal: a different process/host wrote the other head —
	// the outside-window warning is accurate for this one.
	HeadDivergenceExternal = "external"
	// HeadDivergenceLocal: an in-process writer wrote it (delivery channel,
	// second tab, or another save path). The controller resolves this further.
	HeadDivergenceLocal = "local"
	// HeadDivergenceUnknown: no identity record — conservatively treated as
	// external so a real outside writer is never silenced.
	HeadDivergenceUnknown = "unknown"
)

// classifyHeadDivergence attributes the competing writer from the writer
// registry (task 203). Missing identity falls back to unknown, which callers
// treat like external: losing a real outside-writer warning is worse than
// keeping a conservative one.
func classifyHeadDivergence(st *sessionDAGState, otherWriter string) string {
	class, _ := classifyHeadDivergenceReason(st, otherWriter)
	return class
}

// classifyHeadDivergenceReason is classifyHeadDivergence with the missing-
// identity cause spelled out (task 646): the reason lands in the
// concurrent-writer log so an unknown attribution can be investigated from
// the log alone instead of by re-deriving the registry state.
func classifyHeadDivergenceReason(st *sessionDAGState, otherWriter string) (string, string) {
	if st == nil {
		return HeadDivergenceUnknown, "no_replay_state"
	}
	if otherWriter == "" {
		return HeadDivergenceUnknown, "no_writer_id"
	}
	w := st.writers[otherWriter]
	if w == nil {
		return HeadDivergenceUnknown, "writer_unregistered"
	}
	if w.pid == 0 {
		return HeadDivergenceUnknown, "pid_missing"
	}
	if w.pid != os.Getpid() {
		return HeadDivergenceExternal, ""
	}
	if host, err := os.Hostname(); err == nil && w.hostname != "" && host != "" && w.hostname != host {
		return HeadDivergenceExternal, ""
	}
	return HeadDivergenceLocal, ""
}

// recentHeadWindow bounds how old a competing head may be before opening a
// conversation stops mentioning it.
const recentHeadWindow = 24 * time.Hour

// loadHeadEvents derives the events a fresh load should surface. sessionPath
// backs the materialization-gap detection (任务 1010 需求 3); it may be empty
// in tests that only exercise the head-side facts.
func loadHeadEvents(st *sessionDAGState, selected, sessionPath string) []HeadEvent {
	if st == nil {
		return nil
	}
	h := st.heads[selected]
	if h == nil {
		return nil
	}
	var events []HeadEvent
	for _, id := range st.liveHeads() {
		if id == selected {
			continue
		}
		if other := st.heads[id]; other != nil && h.lastActivity.Sub(other.lastActivity) < recentHeadWindow {
			events = append(events, HeadEvent{Kind: HeadEventMultipleRecentHeads, HeadID: selected, OtherWriter: other.writer})
			break
		}
	}
	// 任务 1010 需求 4: an open that landed on a strictly dominated head says
	// so with readings — the user sees WHICH chain is longer and newer and
	// switches manually in the versions dialog. Pure observation.
	if domID, selLen, domLen, dominated := st.dominatingLiveHead(selected); dominated {
		events = append(events, HeadEvent{
			Kind:          HeadEventSelectedHeadBehind,
			HeadID:        selected,
			OtherHeadID:   domID,
			Messages:      selLen,
			OtherMessages: domLen,
		})
	}
	if ev, ok := detectMaterializationGap(sessionPath, st); ok {
		events = append(events, ev)
	}
	return events
}

// Head reports the head this session was loaded from or last saved to. ok is
// false for sessions that still live in a schema-1 log or a bare .jsonl.
func (s *Session) Head() (HeadRef, bool) {
	if s == nil {
		return HeadRef{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.head.ref, s.head.dag
}

// DrainHeadEvents returns and clears the head events recorded by saves.
func (s *Session) DrainHeadEvents() []HeadEvent {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	events := s.head.events
	s.head.events = nil
	return events
}

// selectedHead picks the head a plain open lands on: the last explicit select
// while that head is alive, otherwise the most recently active live head, with
// the larger log offset breaking ties so every reader of the same bytes agrees.
func (st *sessionDAGState) selectedHead() string {
	if h := st.heads[st.selected]; h != nil && !h.retired {
		return h.id
	}
	pick := func(includeRetired bool) string {
		best := ""
		for _, id := range st.headOrder {
			h := st.heads[id]
			if h == nil || (h.retired && !includeRetired) {
				continue
			}
			if best == "" {
				best = id
				continue
			}
			b := st.heads[best]
			if h.lastActivity.After(b.lastActivity) || (h.lastActivity.Equal(b.lastActivity) && h.lastOffset > b.lastOffset) {
				best = id
			}
		}
		return best
	}
	if best := pick(false); best != "" {
		return best
	}
	if best := pick(true); best != "" {
		return best
	}
	return SessionMainHead
}

// dominatingLiveHead reports whether another live head strictly dominates the
// given head: strictly more chain messages AND strictly newer last activity
// (任务 1010 需求 4). Both conditions must hold, so an ordinary rewind or a
// deliberate switch to a review branch — same length, or touched more recently
// — is never judged dominated, and rotation keeps honoring it.
// Caller holds st.mu for reading (replay-mutation readers).
func (st *sessionDAGState) dominatingLiveHead(headID string) (domID string, headLen, domLen int, ok bool) {
	h := st.heads[headID]
	if h == nil || h.retired {
		return "", 0, 0, false
	}
	headLen = len(st.chainIDs(headID))
	for _, id := range st.liveHeads() {
		if id == headID {
			continue
		}
		o := st.heads[id]
		if o == nil || o.retired {
			continue
		}
		oLen := len(st.chainIDs(id))
		if oLen > headLen && o.lastActivity.After(h.lastActivity) {
			return id, headLen, oLen, true
		}
	}
	return "", headLen, 0, false
}

func (st *sessionDAGState) headRecord(id string, selected string) SessionHead {
	h := st.heads[id]
	msgs, _ := st.materialize(id)
	preview, turns := SessionPreviewFromMessages(msgs)
	return SessionHead{
		ID:           h.id,
		Kind:         h.kind,
		Name:         h.name,
		ParentHead:   h.parentHead,
		ForkFrom:     h.forkFrom,
		Writer:       h.writer,
		LeafID:       h.leaf,
		CreatedAt:    h.createdAt,
		LastActivity: h.lastActivity,
		Retired:      h.retired,
		Selected:     h.id == selected,
		MessageCount: len(msgs),
		Turns:        turns,
		Preview:      preview,
	}
}

// headList lists every head in declaration order with the selected one marked
// and covered heads flagged: retiring one of those loses no message.
func (st *sessionDAGState) headList() []SessionHead {
	selected := st.selectedHead()
	onChain := map[string]struct{}{}
	for _, id := range st.chainIDs(selected) {
		onChain[id] = struct{}{}
	}
	out := make([]SessionHead, 0, len(st.headOrder))
	for _, id := range st.headOrder {
		h := st.heads[id]
		if h == nil {
			continue
		}
		rec := st.headRecord(id, selected)
		if id != selected && !h.retired {
			_, rec.Covered = onChain[h.leaf]
			rec.Covered = rec.Covered || h.leaf == ""
		}
		out = append(out, rec)
	}
	return out
}

// liveHeads returns the ids of heads that have not been retired.
func (st *sessionDAGState) liveHeads() []string {
	out := make([]string, 0, len(st.headOrder))
	for _, id := range st.headOrder {
		if h := st.heads[id]; h != nil && !h.retired {
			out = append(out, id)
		}
	}
	return out
}

// reachable returns every node id on the chain of any live head.
func (st *sessionDAGState) reachable() map[string]struct{} {
	keep := map[string]struct{}{}
	for _, id := range st.liveHeads() {
		for _, mid := range st.chainIDs(id) {
			keep[mid] = struct{}{}
		}
	}
	return keep
}

// ListSessionHeads replays a schema-2 log and returns its heads. A schema-1
// session has no heads and returns nil, nil.
//
// tl1227 读侧单路径化：schema-2 头重放已退役。存量 DAG 日志由
// MigrateSessionDAGLog 迁移为 schema-1（无头形态），残余 schema-2 日志弃读
// 封存。本入口保留签名供 desktop 版本/lineage 面调用，恒返回空 = 「无版本
// 对象」的被动收敛（tl1173 §4.2 同款：版本 UI 自然无对象可展示）。
func ListSessionHeads(path string) ([]SessionHead, error) {
	return nil, nil
}
