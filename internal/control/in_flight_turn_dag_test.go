package control

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/store"
	"reasonix/internal/tool"
)

func dagLogEntryTypes(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(store.SessionEventLog(path))
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		_, rest, _ := strings.Cut(line, `"type":"`)
		typ, _, _ := strings.Cut(rest, `"`)
		types = append(types, typ)
	}
	return types
}

// TestInterruptedTurnRecoveryUsesSidecarMarker pins the crash contract now
// that the schema-2 log markers left with the write-path slow lane (tl1280):
// a turn left open in the sidecar is closed on resume by dropping its tail
// with a rewind marker, keeping the user prompt, and the earlier bytes are
// never truncated. Recovery is idempotent.
func TestInterruptedTurnRecoveryUsesSidecarMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	c := New(Options{Executor: exec, SessionDir: dir, SessionPath: path, Label: "test", Sink: event.Discard})
	if err := c.Snapshot(); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	start := sess.Len()
	marker := c.markInFlightTurn(start, true)
	if marker.ID == "" {
		t.Fatal("markInFlightTurn must install a sidecar marker")
	}
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "second"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "c1", Name: "bash", Arguments: `{}`}}})
	// Persist the partial turn so the crash leaves the open turn on disk; the
	// sidecar marker carries the "a turn was open" fact the next runtime reads.
	if err := c.SnapshotDurable(); err != nil {
		t.Fatalf("mid-turn snapshot: %v", err)
	}
	meta, ok, err := agent.LoadBranchMeta(path)
	if err != nil || !ok || meta.InFlightTurn == nil || meta.InFlightTurn.ID != marker.ID {
		t.Fatalf("mid-turn sidecar marker = %+v ok=%v err=%v, want the turn's marker", meta.InFlightTurn, ok, err)
	}
	logBefore, _ := os.ReadFile(store.SessionEventLog(path))
	if len(logBefore) == 0 {
		t.Fatal("mid-turn transcript must be on disk")
	}

	// "Crash": a fresh runtime resumes the same path. The partial transcript
	// reloads as-is; only the sidecar marker carries the open-turn fact.
	recovered, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered.Snapshot()) <= start {
		t.Fatalf("crash fixture must hold the partial turn on disk, got %d messages", len(recovered.Snapshot()))
	}
	exec2 := agent.New(nil, nil, recovered, agent.Options{}, event.Discard)
	sink := &noticeSink{}
	c2 := New(Options{Executor: exec2, SessionDir: dir, SessionPath: path, Label: "test", Sink: sink})
	c2.recoverInterruptedTurn(path)

	got := exec2.Session().Snapshot()
	// Recovery keeps the user prompt (preserveUser) and drops the half-finished
	// assistant/tool tail of the open turn; the tail is then replaced by the
	// local-only interrupted-turn display record (recordInterruptedDisplay).
	var keptPrompt, displayRecord bool
	for _, m := range got {
		if m.Role == provider.RoleUser && m.Content == "second" {
			keptPrompt = true
		}
		if m.LocalOnly && m.InterruptedTurn != nil {
			displayRecord = true
		}
	}
	if !keptPrompt {
		t.Fatalf("recovery must keep the interrupted turn's user prompt, got %+v", got)
	}
	if !displayRecord {
		t.Fatalf("recovery must leave an interrupted-turn display record, got %+v", got)
	}
	logAfter, _ := os.ReadFile(store.SessionEventLog(path))
	if len(logAfter) < len(logBefore) {
		t.Fatalf("recovery must not shrink the log: %d < %d bytes", len(logAfter), len(logBefore))
	}
	if metaAfter, okAfter, errAfter := agent.LoadBranchMeta(path); errAfter != nil || !okAfter || metaAfter.InFlightTurn != nil {
		t.Fatalf("recovery must clear the sidecar marker: %+v ok=%v err=%v", metaAfter.InFlightTurn, okAfter, errAfter)
	}
	reloaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Messages) != len(got) {
		t.Fatalf("reloaded transcript length %d, want %d", len(reloaded.Messages), len(got))
	}
	// A second resume finds nothing to recover and changes nothing.
	c2.recoverInterruptedTurn(path)
	again, _ := os.ReadFile(store.SessionEventLog(path))
	if len(again) != len(logAfter) {
		t.Fatalf("idempotent recovery appended further entries: %d -> %d bytes", len(logAfter), len(again))
	}
}

func TestFinishedTurnClearsSidecarMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	sess := agent.NewSession("sys")
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	c := New(Options{Executor: exec, SessionDir: dir, SessionPath: path, Label: "test", Sink: event.Discard})
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	if err := c.Snapshot(); err != nil {
		t.Fatal(err)
	}
	start := sess.Len()
	marker := c.markInFlightTurn(start, true)
	if marker.ID == "" {
		t.Fatal("markInFlightTurn must install a sidecar marker")
	}
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "second"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "two"})
	c.finishInFlightTurn(start, marker)
	meta, ok, err := agent.LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta: ok=%v err=%v", ok, err)
	}
	if meta.InFlightTurn != nil {
		t.Fatalf("finished turn must clear the sidecar marker, got %+v", meta.InFlightTurn)
	}
	reloaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Messages) < 4 {
		t.Fatalf("transcript after finished turn = %d messages, want the turn persisted", len(reloaded.Messages))
	}
}

// TestConcurrentWriterLoserForksRecoveryCopyAndIsPrompted（原
// TestConcurrentWriterEmitsNoticeOnBothSides）改述：task 203 的双 tab 通知
// 产生于 schema-2 写路径的 head 事件（emitHeadEvents × DrainHeadEvents），
// tl1280 把写路径慢车道退役后该产生器恒空——concurrentDualTabProbe 与
// session_concurrent_dual_tab / session_head_switched 通知码已死，并发写分类
// 由保存冲突的 conflict-copy 机制（tl1352 家族）接管。本测试改钉接管后契约：
// 输家在保存时命中快照冲突 → 收 session_recovery_forked 提示并迁到主文件旁的
// 副本上继续；赢家的主文件自始至终自洽、零通知；副本以 recovered 子分支登记
// 供侧栏/恢复面展示。
func TestConcurrentWriterLoserForksRecoveryCopyAndIsPrompted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.jsonl")
	const systemPrompt = "SYS"
	reply := [][]provider.Chunk{{{Type: provider.ChunkText, Text: "ok"}, {Type: provider.ChunkDone}}}
	sinkA, sinkB := &noticeSink{}, &noticeSink{}
	execA := agent.New(&recordingProvider{streams: reply}, tool.NewRegistry(), agent.NewSession(systemPrompt), agent.Options{}, event.Discard)
	ctrlA := New(Options{Runner: execA, Executor: execA, SystemPrompt: systemPrompt, SessionDir: dir, SessionPath: path, Label: "a", Sink: sinkA})
	if err := ctrlA.RunTurn(context.Background(), "first from A"); err != nil {
		t.Fatal(err)
	}
	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	execB := agent.New(&recordingProvider{streams: reply}, tool.NewRegistry(), agent.NewSession(systemPrompt), agent.Options{}, event.Discard)
	ctrlB := New(Options{Runner: execB, Executor: execB, SystemPrompt: systemPrompt, SessionDir: dir, SessionPath: path, Label: "b", Sink: sinkB})
	ctrlB.Resume(loaded, path)
	if err := ctrlA.RunTurn(context.Background(), "second from A"); err != nil {
		t.Fatal(err)
	}
	if err := ctrlB.RunTurn(context.Background(), "second from B"); err != nil {
		t.Fatal(err)
	}
	// The losing writer is prompted with the conflict-copy notice and moved
	// onto the recovery copy (the retired machinery used to emit
	// session_concurrent_dual_tab here).
	notice, ok := sinkB.lastNotice()
	if !ok || notice.Code != event.NoticeCodeSessionRecoveryForked {
		t.Fatalf("B notice = %+v ok=%v, want the conflict-copy recovery notice", notice, ok)
	}
	if ctrlB.SessionPath() == path {
		t.Fatalf("the losing writer must continue on a recovery copy, still on %q", ctrlB.SessionPath())
	}
	// The copy is registered beside the main as a recovered child so the
	// sidebar/recovery UI can offer it.
	branches, err := agent.ListBranches(dir)
	if err != nil {
		t.Fatal(err)
	}
	recovered := false
	for _, b := range branches {
		if b.Recovered && b.ParentID == agent.BranchID(path) {
			recovered = true
		}
	}
	if !recovered {
		t.Fatalf("no recovered child branch registered under %q: %+v", path, branches)
	}
	// The untouched winner never sees a notice: reopening the main path loads
	// A's transcript coherently. (The retired machinery used to emit
	// session_head_switched on reopen; with head replay gone there is no second
	// head on the main to switch to.)
	if notice, ok := sinkA.lastNotice(); ok {
		t.Fatalf("the winning writer must stay silent, got notice %+v", notice)
	}
}

func TestResumeEmitsAlsoOpenWhenForeignLeaseHeld(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.jsonl")
	const systemPrompt = "SYS"
	reply := [][]provider.Chunk{{{Type: provider.ChunkText, Text: "ok"}, {Type: provider.ChunkDone}}}
	sinkA := &noticeSink{}
	execA := agent.New(&recordingProvider{streams: reply}, tool.NewRegistry(), agent.NewSession(systemPrompt), agent.Options{}, event.Discard)
	ctrlA := New(Options{Runner: execA, Executor: execA, SystemPrompt: systemPrompt, SessionDir: dir, SessionPath: path, Label: "a", Sink: sinkA})
	if err := ctrlA.RunTurn(context.Background(), "seed"); err != nil {
		t.Fatal(err)
	}
	// Single instance: resume of the session this process just wrote must stay
	// silent — no also-open false positive.
	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	sinkQuiet := &noticeSink{}
	execQuiet := agent.New(&recordingProvider{streams: reply}, tool.NewRegistry(), agent.NewSession(systemPrompt), agent.Options{}, event.Discard)
	ctrlQuiet := New(Options{Runner: execQuiet, Executor: execQuiet, SystemPrompt: systemPrompt, SessionDir: dir, SessionPath: path, Label: "quiet", Sink: sinkQuiet})
	ctrlQuiet.Resume(loaded, path)
	if notice, ok := sinkQuiet.lastNotice(); ok && notice.Code == event.NoticeCodeSessionAlsoOpen {
		t.Fatalf("single-instance resume emitted also-open notice: %+v", notice)
	}
	// A foreign runtime holds the lease: the next resume must surface it with
	// the stable code so desktop can localize.
	release, err := agent.HoldForeignSessionLeaseForTest(path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	sinkB := &noticeSink{}
	execB := agent.New(&recordingProvider{streams: reply}, tool.NewRegistry(), agent.NewSession(systemPrompt), agent.Options{}, event.Discard)
	ctrlB := New(Options{Runner: execB, Executor: execB, SystemPrompt: systemPrompt, SessionDir: dir, SessionPath: path, Label: "b", Sink: sinkB})
	loadedAgain, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	ctrlB.Resume(loadedAgain, path)
	var alsoOpen *event.Event
	for i := range sinkB.events {
		if sinkB.events[i].Kind == event.Notice && sinkB.events[i].Code == event.NoticeCodeSessionAlsoOpen {
			alsoOpen = &sinkB.events[i]
			break
		}
	}
	if alsoOpen == nil {
		t.Fatalf("resume under foreign lease emitted no session_also_open; events=%+v", sinkB.events)
	}
	if alsoOpen.Level != event.LevelWarn || alsoOpen.Audience != event.NoticeAudienceOperator {
		t.Fatalf("also-open notice scope = %+v, want operator warn", *alsoOpen)
	}
}
