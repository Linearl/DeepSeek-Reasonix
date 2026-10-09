package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// forkLines returns every raw fork entry of the session's event log, so tests
// can assert on the wire format rather than on in-memory state.
func forkLines(t *testing.T, sessionPath string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(store.SessionEventLog(sessionPath))
	if err != nil {
		t.Fatalf("read event log: %v", err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		if e["type"] == sessionDAGTypeFork {
			out = append(out, e)
		}
	}
	return out
}

// 任务669 acceptance: every fork event carries from. The 2026-10-08 日常杂务
// incident shipped a concurrent fork whose transcript shared nothing with the
// persisted chain (a rebuilt prefix), and the entry went out without a from —
// the new head's leaf started empty on replay and nothing before the fork was
// reachable again. This test reproduces that topology end to end through the
// public save path and pins the wire format plus the steady state that follows.
func TestConcurrentForkDetachedTranscriptRecordsForkFrom(t *testing.T) {
	path := dagTestSession(t)
	dagSavedSession(t, path, "q1", "a1")
	b, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	// A second writer replaces its transcript wholesale (the host-snapshot
	// rebuild): it owns the leaf, so it rewinds main to the root and re-appends
	// a detached transcript. main's chain no longer shares a single message id
	// with b's in-memory transcript.
	c, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	c.Rewrite([]provider.Message{
		dagMsg(provider.RoleSystem, "sys-rebuilt", ""),
		dagMsg(provider.RoleUser, "fresh-start", ""),
	}, "rebuilt")
	if err := c.Save(path); err != nil {
		t.Fatalf("rebuilt writer save: %v", err)
	}
	st := dagReplay(t, path)
	if got := dagChain(st, SessionMainHead); len(got) != 2 {
		t.Fatalf("main chain after rebuild = %v", got)
	}
	// b still holds the pre-rebuild transcript: saving it must fork, and the
	// fork entry must name main's tip as the fork point.
	b.Add(provider.Message{Role: provider.RoleUser, Content: "q-from-b"})
	if err := b.Save(path); err != nil {
		t.Fatalf("detached writer save: %v", err)
	}
	refB, _ := b.Head()
	if refB.HeadID == SessionMainHead || refB.HeadID == "" {
		t.Fatalf("b head = %+v, want a fresh concurrent head", refB)
	}
	forks := forkLines(t, path)
	if len(forks) != 1 {
		t.Fatalf("fork entries = %d, want 1", len(forks))
	}
	st = dagReplay(t, path)
	tip := st.heads[SessionMainHead].leaf
	if forks[0]["from"] != tip || tip == "" {
		t.Fatalf("fork from = %v, want main tip %q", forks[0]["from"], tip)
	}
	// The live session keeps its own transcript: continuing on the fork head
	// appends without forking again and without detaching the chain.
	if events := b.DrainHeadEvents(); len(events) != 1 || events[0].Kind != HeadEventForkedConcurrent {
		t.Fatalf("events = %+v", events)
	}
	b.Add(provider.Message{Role: provider.RoleAssistant, Content: "a-from-b"})
	if err := b.Save(path); err != nil {
		t.Fatalf("second save on the fork head: %v", err)
	}
	if forks := forkLines(t, path); len(forks) != 1 {
		t.Fatalf("fork entries after second save = %d, want 1", len(forks))
	}
	st = dagReplay(t, path)
	if len(st.heads) != 2 {
		t.Fatalf("heads = %d, want 2", len(st.heads))
	}
	if got := dagChain(st, refB.HeadID); len(got) != 5 {
		t.Fatalf("fork chain after continuation = %v", got)
	}
	if got := dagChain(st, SessionMainHead); len(got) != 2 {
		t.Fatalf("main chain after continuation = %v", got)
	}
	heads, err := ListSessionHeads(path)
	if err != nil {
		t.Fatal(err)
	}
	var forked *SessionHead
	for i := range heads {
		if heads[i].ID == refB.HeadID {
			forked = &heads[i]
		}
	}
	if forked == nil || forked.ForkFrom != tip {
		t.Fatalf("listed fork head = %+v, want fork_from %q", forked, tip)
	}
}

// 任务669: the shutdown append path mints its own concurrent fork; its fork
// point comes from the first appended message's parent, with the plan's tip as
// the fallback, and an empty old head honestly has none.
func TestShutdownHeadBatchForkFromFallback(t *testing.T) {
	now := time.Now().UTC()
	msg := sessionDAGEntry{Type: sessionDAGTypeMessage, Head: "old", ID: "m1", Parent: ""}
	plan := &dagWritePlan{head: "old", forkFrom: "tip-1", entries: []sessionDAGEntry{msg}}
	out := shutdownHeadBatch(plan, nil, now)
	if len(out) == 0 || out[0].Type != sessionDAGTypeFork || out[0].From != "tip-1" {
		t.Fatalf("batch = %+v, want fork with from tip-1", out)
	}
	if out[0].Head != "old" || out[0].NewHead == "" || out[0].Kind != HeadKindConcurrent {
		t.Fatalf("fork header = %+v", out[0])
	}
	empty := &dagWritePlan{head: "old", entries: []sessionDAGEntry{msg}}
	out = shutdownHeadBatch(empty, nil, now)
	if len(out) == 0 || out[0].Type != sessionDAGTypeFork || out[0].From != "" {
		t.Fatalf("empty-head batch = %+v, want fork without from", out)
	}
	rewound := &dagWritePlan{head: "old", entries: []sessionDAGEntry{{Type: sessionDAGTypeRewind, Head: "old", To: "t0"}}}
	out = shutdownHeadBatch(rewound, nil, now)
	if len(out) == 0 || out[0].Type != sessionDAGTypeFork || out[0].From != "t0" {
		t.Fatalf("rewind batch = %+v, want fork with from t0", out)
	}
}

// 任务669: replay derives the fork point of a legacy from-less concurrent fork
// from the parent head's tip, so the versions UI and rotation re-emit regain a
// parent pointer without touching leaf semantics; deliberate fresh-start forks
// (kind fork, no from) stay untouched.
func TestReplayDerivesForkFromForLegacyConcurrentFork(t *testing.T) {
	path := dagTestSession(t)
	at := time.Now().UTC().Add(-time.Hour)
	m1 := dagMessageEntry(t, SessionMainHead, "", "", dagMsg(provider.RoleUser, "q1", "m1"), at)
	m2 := dagMessageEntry(t, SessionMainHead, "m1", "", dagMsg(provider.RoleAssistant, "a1", "m2"), at)
	legacy := sessionDAGEntry{Type: sessionDAGTypeFork, Head: SessionMainHead, NewHead: "h-legacy", Kind: HeadKindConcurrent, At: at}
	detached := dagMessageEntry(t, "h-legacy", "", "", dagMsg(provider.RoleUser, "q2", "m3"), at)
	dagAppend(t, path, m1, m2, legacy, detached)

	st := dagReplay(t, path)
	h := st.heads["h-legacy"]
	if h == nil {
		t.Fatal("legacy fork head missing")
	}
	if h.forkFrom != "m2" || h.parentHead != SessionMainHead {
		t.Fatalf("forkFrom = %q parentHead = %q, want m2/main", h.forkFrom, h.parentHead)
	}
	// leaf semantics are unchanged: the detached message is the chain, the
	// parent history is not aliased into it.
	if h.leaf != "m3" || strings.Join(dagChain(st, "h-legacy"), ",") != "q2" {
		t.Fatalf("leaf = %q chain = %v, want m3/[q2]", h.leaf, dagChain(st, "h-legacy"))
	}
	if strings.Join(dagChain(st, SessionMainHead), ",") != "q1,a1" {
		t.Fatalf("main chain = %v", dagChain(st, SessionMainHead))
	}

	path2 := dagTestSession(t)
	fresh := sessionDAGEntry{Type: sessionDAGTypeFork, Head: SessionMainHead, NewHead: "h-fresh", At: at}
	dagAppend(t, path2, m1, m2, fresh)
	st2 := dagReplay(t, path2)
	if h2 := st2.heads["h-fresh"]; h2 == nil || h2.forkFrom != "" {
		t.Fatalf("deliberate fresh fork forkFrom = %v, want empty", h2)
	}
}

// 任务669 acceptance, real-sample instance: the sanitized tail of the 日常杂务
// events.jsonl (contents redacted, ids/parents/heads intact) must list both
// heads with the derived fork point, and the old head must materialize its
// full chain through the version-switch entry point.
func Test669BrokenForkSampleRetrospective(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "task669-broken-fork-sample.jsonl"))
	if err != nil {
		t.Fatalf("sample fixture: %v", err)
	}
	sample := string(raw)
	var brokenForkHasFrom bool
	for _, line := range strings.Split(strings.TrimSpace(sample), "\n") {
		var e map[string]any
		if json.Unmarshal([]byte(line), &e) == nil && e["type"] == sessionDAGTypeFork {
			_, brokenForkHasFrom = e["from"]
		}
	}
	if brokenForkHasFrom {
		t.Fatal("sample fixture drift: the incident fork must lack from")
	}

	path := filepath.Join(t.TempDir(), "sample.jsonl")
	if err := os.WriteFile(store.SessionEventLog(path), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	const oldHead = "01M3NWNR81CAXHKBC74BR3Q1EW"
	const newHead = "01M4DQE965AFEMC8RXQHGXPF0G"
	const oldTip = "01M4DMSXQ6GDC6YA133VK87NH2"

	heads, err := ListSessionHeads(path)
	if err != nil {
		t.Fatalf("list heads: %v", err)
	}
	byID := map[string]SessionHead{}
	for _, h := range heads {
		if h.MessageCount > 0 {
			byID[h.ID] = h
		}
	}
	if len(byID) != 2 {
		t.Fatalf("heads with messages = %d (%+v), want the two incident heads", len(byID), heads)
	}
	old, newer := byID[oldHead], byID[newHead]
	if old.MessageCount != 3 || newer.MessageCount != 3 {
		t.Fatalf("message counts old=%d new=%d, want 3/3", old.MessageCount, newer.MessageCount)
	}
	if newer.Kind != HeadKindConcurrent || newer.ParentHead != oldHead {
		t.Fatalf("new head = %+v", newer)
	}
	if newer.ForkFrom != oldTip {
		t.Fatalf("derived fork_from = %q, want the old head tip %q", newer.ForkFrom, oldTip)
	}

	// The version-switch entry point: loading the old head materializes the
	// pre-fork history (the bluetooth-era messages the user was looking for).
	s, err := LoadSessionHeadForMigration(context.Background(), path, oldHead)
	if err != nil {
		t.Fatalf("load old head: %v", err)
	}
	msgs := s.Snapshot()
	if len(msgs) != 3 {
		t.Fatalf("old head messages = %d, want 3", len(msgs))
	}
	if msgs[0].ID != "01M4DMQB0J29HQXSA7JQ64A5XS" || msgs[2].ID != oldTip {
		t.Fatalf("old head chain = %v", ids(msgs))
	}
	if _, ok := s.Head(); !ok {
		t.Fatal("loaded head lost its dag position")
	}
	s2, err := LoadSessionHeadForMigration(context.Background(), path, newHead)
	if err != nil {
		t.Fatalf("load new head: %v", err)
	}
	if got := s2.Snapshot(); len(got) != 3 || got[0].Role != provider.RoleSystem {
		t.Fatalf("new head chain = %v", ids(got))
	}
}

func ids(msgs []provider.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.ID)
	}
	return out
}
