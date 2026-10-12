package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// 任务 831②（大会话快照增量化，clean-load no-op）的 agent 层验收：
// ① 干净加载（无修复/无损伤/无尾截断）后的第一笔防御性快照必须走 no-op——
//   载入本身已证明内存 transcript 与盘上账本一致，重做全量 marshal+digest
//   是纯重复劳动（728MB 会话 resume 后切走的 snapshot_ms=1000 即此形态）；
// ② no-op 不碰盘：事件日志逐字节不变；
// ③ 内存 transcript 变化后 fast path 失效，尾部 delta 正常落盘，读回等价；
// ④ 修复性加载（normalizedDirty）与尾截断加载不得武装 no-op——首轮保存
//   仍须全量，把修复/完整内容写下去（读侧等价：不落尾窗）。

func newSavedFixture(t *testing.T, dir, name string) (string, *Session) {
	t.Helper()
	path := filepath.Join(dir, name)
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "hi"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("fixture SaveSnapshot: %v", err)
	}
	return path, s
}

func eventLogBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(store.SessionEventLog(path))
	if err != nil {
		t.Fatalf("read event log: %v", err)
	}
	return b
}

func TestCleanLoadArmsSnapshotNoOp(t *testing.T) {
	path, _ := newSavedFixture(t, t.TempDir(), "clean-load.jsonl")

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got := loaded.snapshotNoOpReason(path); got != "clean-load" {
		t.Fatalf("snapshotNoOpReason after a clean load = %q, want clean-load", got)
	}

	// The no-op must not touch the disk: snapshot with the log untouched and
	// verify every byte and the ledger revision survive unchanged. (A scribble
	// cannot prove this any more: the disk re-verification guard correctly
	// treats a modified log as "not provably ours" and falls back to the full
	// path, which heals it.)
	before := eventLogBytes(t, path)
	if err := loaded.SaveSnapshot(path); err != nil {
		t.Fatalf("no-op SaveSnapshot after clean load: %v", err)
	}
	if after := eventLogBytes(t, path); string(before) != string(after) {
		t.Fatal("clean-load snapshot rewrote the log — no-op did not fire")
	}
	// A skip keeps the baseline it proved; the next defensive snapshot skips
	// the same way.
	if got := loaded.snapshotNoOpReason(path); got != "clean-load" {
		t.Fatalf("snapshotNoOpReason after the skip = %q, want clean-load", got)
	}
}

func TestTailTruncatedLoadDoesNotArmSnapshotNoOp(t *testing.T) {
	path, _ := newSavedFixture(t, t.TempDir(), "tail.jsonl")

	loaded, err := LoadSessionTail(path)
	if err != nil {
		t.Fatalf("LoadSessionTail: %v", err)
	}
	// A tiny log never truncates (the tail window only arms above the size
	// threshold), so the reason is the ordinary clean-load. Force the
	// truncated state the first-paint loader produces for oversized logs and
	// verify the guard keeps the first save full.
	loaded.mu.Lock()
	loaded.tailTruncated = true
	loaded.mu.Unlock()
	if got := loaded.snapshotNoOpReason(path); got != "" {
		t.Fatalf("snapshotNoOpReason for a tail-truncated load = %q, want empty", got)
	}
}

func TestStaleLedgerLoadKeepsFullFirstSave(t *testing.T) {
	path, _ := newSavedFixture(t, t.TempDir(), "stale-ledger.jsonl")
	// Interrupted-save aftermath: the transcript bytes landed but the sidecar
	// kept an older digest. The first save after such a load must run in full
	// and heal the ledger (the pre-831 behavior, preserved deliberately).
	if err := UpdateBranchMeta(path, false, func(meta *BranchMeta) error {
		meta.ContentDigest = strings.Repeat("0", 64)
		return nil
	}); err != nil {
		t.Fatalf("corrupt ledger digest: %v", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got := loaded.snapshotNoOpReason(path); got != "" {
		t.Fatalf("snapshotNoOpReason with a lagging ledger = %q, want empty", got)
	}
}

func TestCleanLoadThenAppendPersistsTailDelta(t *testing.T) {
	path, _ := newSavedFixture(t, t.TempDir(), "append.jsonl")

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if loaded.snapshotNoOpReason(path) != "clean-load" {
		t.Fatalf("precondition failed: clean load did not arm the no-op")
	}
	// One new turn: the save must run the full path and persist exactly the
	// delta — reading the session back must show the complete transcript.
	loaded.Add(provider.Message{Role: provider.RoleUser, Content: "next"})
	loaded.Add(provider.Message{Role: provider.RoleAssistant, Content: "done too"})
	if got := loaded.snapshotNoOpReason(path); got != "" {
		t.Fatalf("snapshotNoOpReason after Add = %q, want empty", got)
	}
	if err := loaded.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot after Add: %v", err)
	}
	if got := loaded.snapshotNoOpReason(path); got != "save-verified" {
		t.Fatalf("snapshotNoOpReason after the delta save = %q, want save-verified", got)
	}
	reloaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("re-load: %v", err)
	}
	msgs := reloaded.Snapshot()
	// system + user + assistant (baseline) + user + assistant (delta)
	if len(msgs) != 5 {
		t.Fatalf("reloaded message count = %d, want 5 (3 baseline + 2 delta)", len(msgs))
	}
	if got := msgs[4].Content; got != "done too" {
		t.Fatalf("reloaded tail = %q, want %q", got, "done too")
	}
	if got := msgs[1].Content; got != "hello" {
		t.Fatalf("reloaded head = %q, want %q — delta write dropped history", got, "hello")
	}
}

func TestRepairedLoadKeepsFullFirstSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repair.jsonl")
	// Transitional ProviderContent is migrated on load, which flips
	// normalizedDirty — the repaired-load shape.
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "fix the bug", ProviderContent: "<reasoning-language>zh</reasoning-language>\n\nfix the bug"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("fixture SaveSnapshot: %v", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if !loaded.normalizedDirty {
		t.Fatal("precondition failed: migration did not mark the load dirty")
	}
	if got := loaded.snapshotNoOpReason(path); got != "" {
		t.Fatalf("snapshotNoOpReason after a repaired load = %q, want empty", got)
	}
	// The first save must actually persist the repaired transcript: a second
	// load comes back clean (nothing left to migrate) and shows the canonical
	// rendered content, matching TestLoadSessionMigratesTransitional... shapes.
	if err := loaded.SaveSnapshot(path); err != nil {
		t.Fatalf("first save after repaired load: %v", err)
	}
	reloaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("re-load: %v", err)
	}
	if reloaded.normalizedDirty {
		t.Fatal("repaired transcript still dirty after its first save — the repair was not persisted")
	}
	stored := reloaded.Snapshot()
	if stored[1].Content == "" || stored[1].ProviderContent != "" {
		t.Fatalf("repaired transcript not canonical after save: %+v", stored[1])
	}
}
