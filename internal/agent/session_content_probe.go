package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// tl1228 阶段一（P0-2 有界恢复）：会话内容探测的有界形态。
//
// 背景（tl1223 实测）：desktop 打开会话的 resolve 相 3.87s/2.15s 100% 落在
// sessionFileHasConversationContent → agent.LoadSession —— 为了回答「这个
// 会话有没有对话内容」一个布尔，把整个 events 日志全量解码成消息对象
// （applyMessage 链，heap cum 397MB 的最大单一喂料点），再全套走
// NormalizeSession/digest/基线登记。本探测回答同一个问题，但从不物化消息体：
//
//   - schema-2 DAG 日志：只解码每条目的标量字段与 role（内容字段被解码器
//     跳过、不分配），重放出与全量重放同构的头/链/rewind/select 结构，沿
//     选中头链走 role —— 判定与 LoadSession(path).HasContent() 同构。
//   - schema-1 原生日志：只解码 replace/append 记录的 role 列表。
//   - 无日志（legacy）：.jsonl 检查点逐行只解码 role。
//
// 与全量重放同源同值的语义锚点：
//   - 字节预算：同一 limitsForSessionLog，超限与全量一样按拒载处理（读作
//     无内容——全量此时报错，desktop 调用方对报错的既有契约即无内容）；
//   - 记录/消息上限：同一 limits，超限同样读作无内容；
//   - torn tail：DAG 侧与 resumePastTornLine 一致——坏行作为洞跳过、继续
//     读后面的完整条目（洞后的内容参与判定）；schema-1 侧重放遇坏行即
//     damaged 停机，探测同样停在坏行、用前缀作答；
//   - 结构性坏行（缺 id、未知条目型等）：全量在此停机/报错，探测同判 false。
//
// 两处已知宽容（均无害向、均有据）：探测不持 save-path 锁（纯读，短暂读到
// 旧字节对布尔无害）；collection 上限不预检（该上限只影响极端会话的拒载，
// 不影响内容布尔本身）。
//
// 快照接口预留两档（与 tl1173 第二步 checkpoint 转正配对，分两档互不阻塞）：
//   - 第一档（快照档）：sessionContentSnapshotProbe 钩子。1173 第二步把
//     308-O2 fold checkpoint 转正后，由其安装「快照可用走快照」的实现；
//     本件只预留接缝并钉死调用次序（先快照、后 events），不实现快照读。
//   - 第二档（events 重建最小集）：下方 light scan，即本件的全部实现，
//     始终兜底 —— 第一档缺席或 ok=false 时走到这里，行为不依赖第一档。

// sessionContentSnapshotProbe is the tier-1 seam of the two-tier content
// probe. A snapshot-backed implementation (tl1173 step-2 checkpoint) installs
// a func here that answers from a persisted snapshot and reports ok=true; ok
// is false whenever the snapshot is absent, stale, or unusable, and the
// caller falls through to the bounded events scan below. nil (today's state:
// the fold checkpoint is experimental and env-gated) means tier 1 is dormant
// and every probe takes the events path. The func receives the SESSION path
// (the same input SessionHasConversationContent takes).
var sessionContentSnapshotProbe func(sessionPath string) (hasContent, ok bool)

// SessionHasConversationContent reports whether a session carries at least
// one user, assistant, or tool message — the bounded form of
// LoadSession(path).HasContent(). It never materializes message bodies, so a
// multi-hundred-MB session costs a byte scan instead of a full decode.
func SessionHasConversationContent(sessionPath string) bool {
	has, err := ProbeSessionConversationContent(sessionPath)
	return err == nil && has
}

// ProbeSessionConversationContent is SessionHasConversationContent with the
// refusal surfaced: err is non-nil exactly where LoadSession would have
// returned an error (missing transcript, future schema, replay refusal,
// undecodable body), so a caller that must treat "undecodable" differently
// from "decodes to no content" can. Callers:
//   - sessionFileHasConversationContent maps err to false (its LoadSession
//     error contract predates the probe);
//   - runtimeSessionTranscriptBlank maps err to "not blank" (conservative
//     row retention, its own documented contract).
//
// Both mappings are byte-for-byte the pre-probe behavior of their LoadSession
// call site; the split exists so one primitive serves both without either
// caller changing direction on undecodable input.
func ProbeSessionConversationContent(sessionPath string) (hasContent bool, err error) {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return false, nil
	}
	// The transcript itself may be archived at rest (tl1174); mirror
	// loadSessionTranscript's materialization short-circuit before any read.
	if _, err := ensureSessionTranscriptPlain(sessionPath); err != nil {
		return false, err
	}
	limits := limitsForSessionLog(sessionPath, defaultSessionReplayLimits)
	if sessionContentSnapshotProbe != nil {
		if has, ok := sessionContentSnapshotProbe(sessionPath); ok {
			return has, nil
		}
	}
	probe, err := probeSessionEventLogWithLimits(sessionPath, limits)
	if err != nil {
		return false, err
	}
	if probe.futureSchema {
		// A newer writer owns the log: LoadSession refuses with an error.
		return false, errProbeFutureSchema(sessionPath)
	}
	logPath := store.SessionEventLog(sessionPath)
	if probe.dag {
		return probeDAGHasConversationContent(logPath, limits)
	}
	if probe.native && probe.size > 0 {
		return probeEventLogHasConversationContent(logPath, limits)
	}
	return probeJSONLHasConversationContent(sessionPath)
}

// errProbeFutureSchema mirrors the loadSessionTranscript refusal for a log a
// newer schema owns.
func errProbeFutureSchema(sessionPath string) error {
	return errors.New("session content probe: " + sessionPath + " uses a newer log schema")
}

// errProbeReplayLimit mirrors the refusal shape the replay produces for a log
// this build cannot safely decode (budget/cap/structural). Callers translate
// it the same way they translate a LoadSession error.
func errProbeReplayLimit(logPath string) error {
	return errors.New("session content probe: " + logPath + " exceeds safe replay limits")
}

// probeRoleMessage is the role-only shadow of provider.Message: decoding into
// it parses the role and skips every payload field without materializing it.
type probeRoleMessage struct {
	Role provider.Role `json:"role"`
}

// probeDAGEntry is the scalar shadow of sessionDAGEntry: every field the chain
// structure needs, none of the payloads. Msgs/Targets decode roles only; the
// decoder skips content/arguments bodies by scanning, not materializing.
type probeDAGEntry struct {
	SchemaVersion int       `json:"schema_version"`
	Type          string    `json:"type"`
	ID            string    `json:"id,omitempty"`
	Head          string    `json:"head,omitempty"`
	Parent        string    `json:"parent,omitempty"`
	At            time.Time `json:"at"`

	Target string `json:"target,omitempty"`

	NewHead string `json:"new_head,omitempty"`
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`

	Msgs    []probeRoleMessage            `json:"msgs,omitempty"`
	Targets map[string][]probeRoleMessage `json:"targets,omitempty"`
}

// probeDAGNode is the role-and-shape shadow of sessionDAGNode.
type probeDAGNode struct {
	role   provider.Role
	parent string
}

// probeDAGHead is the chain-position shadow of sessionDAGHead.
type probeDAGHead struct {
	leaf         string
	retired      bool
	lastActivity time.Time
	lastOffset   int64
}

// probeDAGState mirrors the parts of sessionDAGState a content walk needs:
// heads with their leaves, the explicit selection, and per-node roles.
type probeDAGState struct {
	nodes    map[string]probeDAGNode
	heads    map[string]*probeDAGHead
	selected string
	// patchRoles/redactRoles carry the role a patched or redacted body
	// presents on read (appliedMessage). Redact targets need no existence
	// check — the real replay stores them unconditionally and the walk simply
	// never reaches unknown ids.
	patchRoles  map[string]provider.Role
	redactRoles map[string]provider.Role
	headOrder   []string
	records     int
}

func newProbeDAGState() *probeDAGState {
	st := &probeDAGState{
		nodes: map[string]probeDAGNode{},
		heads: map[string]*probeDAGHead{},
	}
	st.heads[SessionMainHead] = &probeDAGHead{}
	st.headOrder = []string{SessionMainHead}
	return st
}

// headFor mirrors sessionDAGState.headFor: an undeclared head is created
// rather than dropped, and an empty id means the main head.
func (st *probeDAGState) headFor(id string, at time.Time) *probeDAGHead {
	if id == "" {
		id = SessionMainHead
	}
	h := st.heads[id]
	if h == nil {
		h = &probeDAGHead{}
		st.heads[id] = h
		st.headOrder = append(st.headOrder, id)
	}
	return h
}

// selectedHead mirrors sessionDAGState.selectedHead: the last explicit select
// while that head is alive, else the most recently active live head with the
// larger log offset breaking ties, else main.
func (st *probeDAGState) selectedHead() string {
	if h := st.heads[st.selected]; h != nil && !h.retired {
		return st.selected
	}
	pick := func(includeRetired bool) string {
		best := ""
		var bestH *probeDAGHead
		for _, id := range st.headOrder {
			h := st.heads[id]
			if h == nil || (h.retired && !includeRetired) {
				continue
			}
			if best == "" {
				best, bestH = id, h
				continue
			}
			if h.lastActivity.After(bestH.lastActivity) ||
				(h.lastActivity.Equal(bestH.lastActivity) && h.lastOffset > bestH.lastOffset) {
				best, bestH = id, h
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

// chainHasConversationContent walks the head's chain from leaf to root the way
// chainIDs does (stop at a missing node — an orphan root or a rotated-away
// prefix — and at a cycle) and reports whether any node reads as a
// non-system message after patch/redaction substitution.
func (st *probeDAGState) chainHasConversationContent(headID string) bool {
	h := st.heads[headID]
	if h == nil {
		return false
	}
	seen := map[string]struct{}{}
	for id := h.leaf; id != ""; {
		if _, cyc := seen[id]; cyc {
			break
		}
		seen[id] = struct{}{}
		n, ok := st.nodes[id]
		if !ok {
			break
		}
		role := n.role
		if r, ok := st.patchRoles[id]; ok {
			role = r
		}
		if r, ok := st.redactRoles[id]; ok {
			role = r
		}
		if role != "" && role != provider.RoleSystem {
			return true
		}
		id = n.parent
	}
	return false
}

// probeDAGHasConversationContent light-scans a schema-2 log line by line:
// same budget, same entry-level stop rules, and the same torn-line rule as
// resumePastTornLine (a bad line is a hole — skip it and keep reading the
// complete entries behind it), none of the payload materialization.
func probeDAGHasConversationContent(logPath string, limits sessionReplayLimits) (bool, error) {
	f, err := os.Open(logPath)
	if err != nil {
		return false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	if info.Size() > limits.maxBytes {
		return false, errProbeReplayLimit(logPath)
	}
	st := newProbeDAGState()
	// maxBytes+1 mirrors the replay's LimitedReader budget trick: only a log
	// that fits the budget passes, and a read that runs past the budget trips
	// the same refusal the decoder-based replay produces.
	reader := bufio.NewReader(io.LimitReader(f, limits.maxBytes+1))
	for {
		line, err := readProbeLine(reader)
		if err != nil {
			if errors.Is(err, errProbeBudgetExceeded) {
				return false, errProbeReplayLimit(logPath)
			}
			break // EOF: answer from what was scanned
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue // whitespace, same as the decoder's skip
		}
		var e probeDAGEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // torn line: a hole; the replay continues behind it
		}
		if e.SchemaVersion != sessionDAGSchemaVersion {
			return false, errProbeReplayLimit(logPath) // mid-log schema switch: the replay hard-errors
		}
		if st.records >= limits.maxRecords || len(st.nodes) >= limits.maxMessages {
			return false, errProbeReplayLimit(logPath)
		}
		st.records++
		// Entry-order proxy for the replay's byte offsets: only comparisons
		// feed the selected-head tie-break, and scan order equals offset order
		// on an append-only log.
		offset := int64(st.records)
		switch e.Type {
		case sessionDAGTypeMessage:
			if e.ID == "" || len(e.Msgs) != 1 {
				// decodeOne's ok=false: the replay stops damaged (a SUCCESS
				// carrying a prefix), so the prefix answer stands, no error.
				return st.chainHasConversationContent(st.selectedHead()), nil
			}
			if _, dup := st.nodes[e.ID]; dup {
				continue // duplicate: the replay warns and skips
			}
			st.nodes[e.ID] = probeDAGNode{role: e.Msgs[0].Role, parent: e.Parent}
			h := st.headFor(e.Head, e.At)
			h.leaf = e.ID
			h.lastActivity, h.lastOffset = e.At, offset
		case sessionDAGTypePatch:
			if _, known := st.nodes[e.Target]; !known {
				continue // the replay ignores patches for unknown targets
			}
			if len(e.Msgs) != 1 {
				return st.chainHasConversationContent(st.selectedHead()), nil // damaged stop, prefix answer
			}
			if st.patchRoles == nil {
				st.patchRoles = map[string]provider.Role{}
			}
			st.patchRoles[e.Target] = e.Msgs[0].Role
		case sessionDAGTypeRedact:
			if st.redactRoles == nil {
				st.redactRoles = map[string]provider.Role{}
			}
			for id, raw := range e.Targets {
				if len(raw) == 1 {
					st.redactRoles[id] = raw[0].Role
				}
			}
		case sessionDAGTypeFork:
			if e.NewHead == "" {
				// The only undecodable head marker: damaged stop with the
				// prefix answer, exactly like the replay.
				return st.chainHasConversationContent(st.selectedHead()), nil
			}
			if _, exists := st.heads[e.NewHead]; exists {
				continue
			}
			// headFor on the parent keeps the head-map parity with the real
			// replay (an undeclared parent is created, never dropped); the
			// forkFrom derivation is display metadata with no chain effect.
			st.headFor(e.Head, e.At)
			h := &probeDAGHead{leaf: e.From, lastActivity: e.At, lastOffset: offset}
			st.heads[e.NewHead] = h
			st.headOrder = append(st.headOrder, e.NewHead)
		case sessionDAGTypeRewind:
			h := st.headFor(e.Head, e.At)
			h.leaf = e.To
			h.lastActivity, h.lastOffset = e.At, offset
		case sessionDAGTypeSelect:
			st.selected = e.Head
		case sessionDAGTypeRetire:
			st.headFor(e.Head, e.At).retired = true
		case sessionDAGTypeSystem, sessionDAGTypeWriter, sessionDAGTypeLog, sessionDAGTypeRename,
			sessionDAGTypeTurnBegin, sessionDAGTypeTurnEnd, sessionDAGTypeCompaction, sessionDAGTypeCheckpoint:
			// No chain effect: these entries never carry chain position or a
			// role the content walk can see (system rides on the head, not the
			// node map; HasContent skips system messages anywhere).
		default:
			return false, errProbeReplayLimit(logPath) // unsupported entry type: the replay hard-errors
		}
	}
	return st.chainHasConversationContent(st.selectedHead()), nil
}

// probeEventRecord is the role-only shadow of sessionEventWireRecord.
type probeEventRecord struct {
	SchemaVersion int                `json:"schema_version"`
	Type          string             `json:"type"`
	MessageIndex  int                `json:"message_index"`
	Messages      []probeRoleMessage `json:"messages"`
}

// probeEventLogHasConversationContent light-scans a schema-1 log: replace
// resets the role list, append must land exactly at its recorded index (same
// damaged rule as the real replay, which stops at a bad line and keeps its
// prefix). Budget and cap refusals read as no-content, matching the desktop
// callers' treatment of a failed load.
func probeEventLogHasConversationContent(logPath string, limits sessionReplayLimits) (bool, error) {
	f, err := os.Open(logPath)
	if err != nil {
		return false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	if info.Size() > limits.maxBytes {
		return false, errProbeReplayLimit(logPath)
	}
	var roles []provider.Role
	records := 0
	reader := bufio.NewReader(io.LimitReader(f, limits.maxBytes+1))
	for {
		line, err := readProbeLine(reader)
		if err != nil {
			if errors.Is(err, errProbeBudgetExceeded) {
				return false, errProbeReplayLimit(logPath)
			}
			return rolesHaveConversationContent(roles), nil
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var rec probeEventRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			// The schema-1 replay stops damaged at a bad line and keeps the
			// prefix — same verdict here.
			return rolesHaveConversationContent(roles), nil
		}
		if rec.SchemaVersion != sessionEventSchemaVersion {
			return false, errProbeReplayLimit(logPath)
		}
		if records >= limits.maxRecords || len(roles)+len(rec.Messages) > limits.maxMessages {
			return false, errProbeReplayLimit(logPath)
		}
		records++
		switch rec.Type {
		case sessionEventTypeReplace:
			roles = make([]provider.Role, 0, len(rec.Messages))
			for _, m := range rec.Messages {
				roles = append(roles, m.Role)
			}
		case sessionEventTypeAppend:
			if rec.MessageIndex != len(roles) {
				return rolesHaveConversationContent(roles), nil // damaged stop, prefix answer
			}
			for _, m := range rec.Messages {
				roles = append(roles, m.Role)
			}
		default:
			return false, errProbeReplayLimit(logPath) // unknown record type: the replay hard-errors
		}
	}
}

// probeJSONLHasConversationContent scans a legacy .jsonl checkpoint one message
// per line, roles only — the bounded shadow of loadSessionMessagesFromJSONL.
func probeJSONLHasConversationContent(sessionPath string) (bool, error) {
	f, err := os.Open(sessionPath)
	if err != nil {
		return false, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	for {
		var m probeRoleMessage
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				return false, nil
			}
			// Undecodable checkpoint: loadSessionMessagesFromJSONL errors and
			// LoadSession surfaces it — same refusal here.
			return false, errProbeReplayLimit(sessionPath)
		}
		if m.Role != "" && m.Role != provider.RoleSystem {
			return true, nil
		}
	}
}

func rolesHaveConversationContent(roles []provider.Role) bool {
	for _, role := range roles {
		if role != "" && role != provider.RoleSystem {
			return true
		}
	}
	return false
}

// errProbeBudgetExceeded marks that the scan ran past the replay byte budget.
var errProbeBudgetExceeded = errors.New("session content probe: replay budget exceeded")

// readProbeLine reads one newline-terminated line. Line length has no ceiling
// of its own — the enclosing LimitedReader is the ceiling, and a line that
// runs past it surfaces as errProbeBudgetExceeded (the same refusal shape the
// decoder-based replay produces at the budget).
func readProbeLine(reader *bufio.Reader) ([]byte, error) {
	var out []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			out = append(out, chunk...)
			// Distinguish "line longer than the bufio buffer" from "the
			// underlying budget is spent": a Peek succeeds only while the
			// LimitReader still has bytes.
			if _, nerr := reader.Peek(1); nerr != nil {
				return nil, errProbeBudgetExceeded
			}
			continue
		}
		if err == nil {
			if len(out) == 0 {
				return chunk, nil
			}
			return append(out, chunk...), nil
		}
		if errors.Is(err, io.EOF) {
			if len(out) > 0 {
				return append(out, chunk...), nil
			}
			return nil, io.EOF
		}
		return nil, err
	}
}
