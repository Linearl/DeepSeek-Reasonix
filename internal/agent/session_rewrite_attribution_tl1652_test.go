package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// tl1652 回归钉矩阵（验收：改写后立即杀进程，重启后归因可查——测试进程内
// 模拟：写 → 不 drain → 直接读侧车/重开载入，断言归因可查）。
//
//	A1  Rewrite 后不 drain，侧车立即可读（完成即落盘——不依赖下次请求）。
//	A2  重启形态：LoadSession 载入后归因仍可查（跨进程读回）。
//	A3  baseline 未建立时的 Rewrite 欠账，首次 save 后补写落盘。
//	A4  drain 只清内存队列，不清侧车（归因机制不重构、持久层独立）。
//	A5  五个队列入口（rewrite/replace/note/system_prompt/tool_guard）全部留痕。
//	A5b 侧车写失败：记录退回缓冲重试，路径恢复后补写成功（审计级不丢）。
//	A6  .rewritelog.jsonl 是侧车不是转录：分类名单排除 + 删除清单包含。
//	A7  缓冲上限：重试缓冲封顶，不无限增长；落盘轨迹不受上限截断。

func tl1652NewSavedSession(t *testing.T, dir string) (*Session, string) {
	t.Helper()
	path := filepath.Join(dir, "session.jsonl")
	s := NewSession("system")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "hi"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	return s, path
}

func tl1652Backlog(t *testing.T, s *Session) int {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pendingAttribution)
}

// A1: Rewrite → no drain → sidecar readable immediately.
func TestTL1652RewriteAttributionReadableWithoutDrain(t *testing.T) {
	dir := t.TempDir()
	s, path := tl1652NewSavedSession(t, dir)

	s.Rewrite(append([]provider.Message(nil), s.Snapshot()[:2]...), "rewind_truncate")

	recs, err := ListSessionRewriteAttribution(path)
	if err != nil {
		t.Fatalf("ListSessionRewriteAttribution: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("attribution records = %d, want 1 (written at rewrite time, not at drain time)", len(recs))
	}
	if recs[0].Reason != "rewind_truncate" || recs[0].Source != attributionSourceRewrite {
		t.Fatalf("record = %+v, want reason=rewind_truncate source=rewrite", recs[0])
	}
	if recs[0].Writer != SessionWriterID() || recs[0].At == "" {
		t.Fatalf("record missing writer/at: %+v", recs[0])
	}
	if recs[0].BeforeMsgs != 3 || recs[0].AfterMsgs != 2 {
		t.Fatalf("record msg counts = %d/%d, want 3/2", recs[0].BeforeMsgs, recs[0].AfterMsgs)
	}
}

// A2: restart shape — reload the session from disk, attribution still queryable
// (the rewrite never reached a provider request in the old process).
func TestTL1652AttributionSurvivesReloadWithoutDrain(t *testing.T) {
	dir := t.TempDir()
	s, path := tl1652NewSavedSession(t, dir)

	msgs := append([]provider.Message(nil), s.Snapshot()[:2]...)
	s.Rewrite(msgs, "guardian_merge")

	// Simulated restart: a fresh Session object loaded from disk. The old
	// process (and its pendingContentReasons queue) is gone from here on.
	reloaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got := reloaded.DrainContentRewriteReasons(); len(got) != 0 {
		t.Fatalf("reloaded session drained %v, want empty (queue never crossed the process boundary)", got)
	}
	recs, err := ListSessionRewriteAttribution(path)
	if err != nil {
		t.Fatalf("ListSessionRewriteAttribution: %v", err)
	}
	found := false
	for _, rec := range recs {
		if rec.Reason == "guardian_merge" && rec.Source == attributionSourceRewrite {
			found = true
		}
	}
	if !found {
		t.Fatalf("guardian_merge attribution missing after reload; records = %+v", recs)
	}
}

// A3: rewrite before the first save (no baseline path) buffers; the first
// successful save flushes the backlog to the sidecar.
func TestTL1652AttributionBeforeFirstSaveFlushesOnSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fresh.jsonl")
	s := NewSession("system")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "hi"})

	s.Rewrite(s.Snapshot(), "compact_auto")

	if recs, err := ListSessionRewriteAttribution(path); err != nil || len(recs) != 0 {
		t.Fatalf("pre-baseline sidecar should not exist yet (recs=%v err=%v)", recs, err)
	}
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	recs, err := ListSessionRewriteAttribution(path)
	if err != nil {
		t.Fatalf("ListSessionRewriteAttribution: %v", err)
	}
	if len(recs) != 1 || recs[0].Reason != "compact_auto" {
		t.Fatalf("post-save records = %+v, want the buffered compact_auto record", recs)
	}
}

// A4: draining attribution for the next request must not erase the durable
// trail — the sidecar outlives the in-memory queue.
func TestTL1652DrainDoesNotEraseSidecar(t *testing.T) {
	dir := t.TempDir()
	s, path := tl1652NewSavedSession(t, dir)

	s.Rewrite(s.Snapshot(), "snip")
	if got := s.DrainContentRewriteReasons(); len(got) != 1 || got[0] != "snip" {
		t.Fatalf("drain = %v, want [snip]", got)
	}
	recs, err := ListSessionRewriteAttribution(path)
	if err != nil {
		t.Fatalf("ListSessionRewriteAttribution: %v", err)
	}
	if len(recs) != 1 || recs[0].Reason != "snip" {
		t.Fatalf("sidecar after drain = %+v, want [snip]", recs)
	}
}

// A5: every pendingContentReasons producer also journals, with its source tag.
func TestTL1652AllQueueEntryPointsJournal(t *testing.T) {
	dir := t.TempDir()
	s, path := tl1652NewSavedSession(t, dir)

	// rewrite
	s.Rewrite(s.Snapshot(), "compact_manual")
	// replace (fork path — provider-visible, reasonless)
	s.Replace(s.Snapshot())
	// note (projection install / resume migration)
	s.NoteContentRewrite("projection_install")
	// system prompt migration
	if !s.SetLeadingSystemPromptWithReason("system v2", "system_prompt_refresh") {
		t.Fatal("SetLeadingSystemPromptWithReason reported no change")
	}
	// tool guard against a persisted tool message
	s.Add(provider.Message{Role: provider.RoleTool, ToolCallID: "c1", Name: "edit", Content: "stale"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot with tool msg: %v", err)
	}
	s.updateBatchGuardResults(
		[]provider.ToolCall{{ID: "c1", Name: "edit"}},
		[]string{"fixed"})

	recs, err := ListSessionRewriteAttribution(path)
	if err != nil {
		t.Fatalf("ListSessionRewriteAttribution: %v", err)
	}
	sources := map[string]int{}
	for _, rec := range recs {
		sources[rec.Source]++
	}
	for _, want := range []string{attributionSourceRewrite, attributionSourceReplace, attributionSourceNote, attributionSourceSystemPmt, attributionSourceToolGuard} {
		if sources[want] != 1 {
			t.Fatalf("source %q count = %d, want 1; all = %+v (%d records)", want, sources[want], sources, len(recs))
		}
	}
	if recs[0].Reason != "compact_manual" {
		t.Fatalf("first record reason = %q, want compact_manual", recs[0].Reason)
	}
}

// A5b: a failed sidecar write returns records to the buffer for retry instead
// of losing them (attribution is audit-grade: transient failure must heal).
func TestTL1652AttributionWriteFailureRetainsRecords(t *testing.T) {
	dir := t.TempDir()
	s, path := tl1652NewSavedSession(t, dir)

	// Replace the sidecar path with a directory: every open-for-append fails.
	badPath := store.SessionRewriteAttributionLog(path)
	if err := os.MkdirAll(badPath, 0o700); err != nil {
		t.Fatalf("stage failing sidecar target: %v", err)
	}
	s.Rewrite(s.Snapshot(), "rewind_restore")
	if backlog := tl1652Backlog(t, s); backlog != 1 {
		t.Fatalf("failed write must retain the record in the retry buffer; backlog = %d", backlog)
	}
	// Heal the path and re-trigger: the record reaches the sidecar.
	if err := os.Remove(badPath); err != nil {
		t.Fatalf("heal sidecar target: %v", err)
	}
	s.NoteContentRewrite("post_heal_note")
	recs, err := ListSessionRewriteAttribution(path)
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	if len(recs) != 2 || recs[0].Reason != "rewind_restore" || recs[1].Reason != "post_heal_note" {
		t.Fatalf("records after heal = %+v, want retried rewind_restore then post_heal_note", recs)
	}
}

// A6: the journal is a sidecar — never a session transcript, always deleted
// with its session.
func TestTL1652RewritelogClassifiedAsSidecar(t *testing.T) {
	if store.IsSessionTranscriptName("session.rewritelog.jsonl") {
		t.Fatal(".rewritelog.jsonl must not classify as a session transcript")
	}
	if store.IsLoadableSessionTranscriptName("session.rewritelog.jsonl") {
		t.Fatal(".rewritelog.jsonl must not be loadable as a session")
	}
	sidecars := store.SessionSidecarFiles(filepath.Join(t.TempDir(), "session.jsonl"))
	found := false
	for _, p := range sidecars {
		if strings.HasSuffix(p, store.SessionRewriteAttributionLogSuffix) {
			found = true
		}
	}
	if !found {
		t.Fatalf("SessionSidecarFiles must include the rewrite attribution log: %v", sidecars)
	}
}

// A7: the retry buffer is capped; the durable trail itself is not truncated by
// the cap (each successful flush moves records out of the buffer).
func TestTL1652AttributionBufferCapBoundsRetryBuffer(t *testing.T) {
	dir := t.TempDir()
	s, path := tl1652NewSavedSession(t, dir)

	// Break the sidecar so records pile up in the retry buffer.
	badPath := store.SessionRewriteAttributionLog(path)
	if err := os.MkdirAll(badPath, 0o700); err != nil {
		t.Fatalf("stage failing sidecar target: %v", err)
	}
	for i := 0; i < rewriteAttributionMaxPending+10; i++ {
		s.NoteContentRewrite("overflow_note")
	}
	if backlog := tl1652Backlog(t, s); backlog != rewriteAttributionMaxPending {
		t.Fatalf("retry buffer len = %d, want capped at %d", backlog, rewriteAttributionMaxPending)
	}
	// Heal and flush: the retained window reaches the sidecar, nothing grows.
	if err := os.Remove(badPath); err != nil {
		t.Fatalf("heal sidecar target: %v", err)
	}
	s.NoteContentRewrite("post_cap_note")
	recs, err := ListSessionRewriteAttribution(path)
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	if len(recs) != rewriteAttributionMaxPending {
		t.Fatalf("sidecar records = %d, want %d (capped window + post-cap note)", len(recs), rewriteAttributionMaxPending)
	}
}
