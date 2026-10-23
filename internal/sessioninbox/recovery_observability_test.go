package sessioninbox

// 任务1094缺陷C：sessioninbox 观测补强——跨进程恢复时打一条 slog + 写一份
// <reasonix home>/logs/inbox-recovery.jsonl 快照（全局单文件，含条目完整 id、
// 状态、时间戳、RunID 前后值、触发点、session path——**绝不存 payload body**）。
// 本测试钉死三件事：
//   ① cross_process_recovery 触发点写出快照行（oldRunId → newRunId、recovered
//      计数、trigger 字段）；
//   ② 快照行的 entries 里只含 id/state/source/at，不含 body/preview（体积可控
//      的关键——body 已在 items 的 blob 里，绝不重复存）；
//   ③ recovered==0 的纯 Consumed 残留恢复（缺陷A 场景①）也照常打快照（观测
//      面不受 A 块判据收窄影响）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withRecoverySnapshotPath points the package-level recovery ledger at a temp
// dir for the duration of one test, then closes and restores it.
func withRecoverySnapshotPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := recoverySnapshotPath
	recoverySnapshotPath = func() string {
		return filepath.Join(dir, "inbox-recovery.jsonl")
	}
	t.Cleanup(func() {
		CloseRecoverySnapshot()
		recoverySnapshotPath = orig
	})
	return filepath.Join(dir, "inbox-recovery.jsonl")
}

// readRecoverySnapshotLines reads every non-empty JSON line from the ledger.
func readRecoverySnapshotLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read recovery snapshot: %v", err)
	}
	var out []map[string]any
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("unmarshal snapshot line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// TestCrossProcessRecoveryWritesSnapshot: a real cross-process recovery (RunID
// rewritten to a foreign value, StateRunning → StateUncertain) must append one
// JSON line to the ledger with oldRunId → newRunId, recovered count, trigger,
// and content-free entry fingerprints (no body).
func TestCrossProcessRecoveryWritesSnapshot(t *testing.T) {
	snapshotPath := withRecoverySnapshotPath(t)
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	st, err := Open(session, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := st.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: "secret-body-text"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetState(rec.ItemID, StateRunning, ""); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// Rewrite the manifest's RunID to simulate another process, then reopen.
	manPath := filepath.Join(st.Dir(), "manifest.json")
	data, err := os.ReadFile(manPath)
	if err != nil {
		t.Fatal(err)
	}
	origRunID := st.runID
	data = []byte(strings.Replace(string(data), origRunID, "other-run-id-0000", 1))
	if err := os.WriteFile(manPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	// Prior tests in the same package may have left the package-level ledger
	// handle non-nil (pointing at THEIR temp dir). Force it closed so the
	// reopen below lands at THIS test's path, not a stale one.
	CloseRecoverySnapshot()
	s2, err := Open(session, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	lines := readRecoverySnapshotLines(t, snapshotPath)
	if len(lines) == 0 {
		t.Fatal("snapshot lines = 0, want at least 1 (one cross-process recovery)")
	}
	entry := lines[0]
	if got := entry["trigger"]; got != RecoveryTriggerCrossProcess {
		t.Fatalf("trigger = %v, want %q", got, RecoveryTriggerCrossProcess)
	}
	if got := entry["oldRunId"]; got != "other-run-id-0000" {
		t.Fatalf("oldRunId = %v, want other-run-id-0000", got)
	}
	if got, _ := entry["newRunId"].(string); got == "" || got == "other-run-id-0000" {
		t.Fatalf("newRunId = %v, want the fresh ProcessRunID", got)
	}
	if got := entry["recovered"].(float64); got != 1 {
		t.Fatalf("recovered = %v, want 1 (StateRunning → Uncertain)", got)
	}
	if got := entry["session"].(string); got == "" {
		t.Fatal("session path missing from snapshot")
	}
	// Body-freedom: the entry's serialized form must not contain the item's
	// body text (体积可控的关键——body 已在 items 的 blob 里，绝不重复存).
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-body-text") {
		t.Fatalf("snapshot leaked the message body: %s", raw)
	}
	// entries[] must carry id/state/source/at only — no body/preview keys.
	entries, _ := entry["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	row, _ := entries[0].(map[string]any)
	for _, forbidden := range []string{"body", "preview", "displayText", "submitText", "rawText"} {
		if _, present := row[forbidden]; present {
			t.Fatalf("entry row carried a forbidden key %q: %v", forbidden, row)
		}
	}
	if _, present := row["id"]; !present {
		t.Fatalf("entry row missing id: %v", row)
	}
	if _, present := row["state"]; !present {
		t.Fatalf("entry row missing state: %v", row)
	}
}

// TestConsumedResidueOnlyStillWritesSnapshot (1094缺陷C × 缺陷A 场景①)：纯
// StateSteerConsumed 残留的跨进程恢复（recovered==0，不弹门）也必须照常写快照
// ——观测面不受 A 块判据收窄影响，「谁把信箱标成 Paused」这类问题的证据链
// 不能因为不再弹门就消失。
func TestConsumedResidueOnlyStillWritesSnapshot(t *testing.T) {
	snapshotPath := withRecoverySnapshotPath(t)
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	st, err := Open(session, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := st.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: "already consumed"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetState(rec.ItemID, StateSteerConsumed, ""); err != nil {
		t.Fatal(err)
	}
	st.Close()

	manPath := filepath.Join(st.Dir(), "manifest.json")
	data, err := os.ReadFile(manPath)
	if err != nil {
		t.Fatal(err)
	}
	origRunID := st.runID
	data = []byte(strings.Replace(string(data), origRunID, "other-run-id-0000", 1))
	if err := os.WriteFile(manPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(session, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	// 注意：recovered==0 时既有代码不 commit manifest 到盘（recovered>0 ||
	// migrated 才 commit），所以 s.man 只在内存里更新了 RunID——后续任何一次
	// loadOrInitLocked（含 Snapshot 触发的重读）都会重新走一遍 cross-process
	// 分支、再写一行快照。这是既有行为，不是本件引入的；测试断言「至少一行、
	// 且最后一行符合语义」而不是「恰好一行」，以免把既有无 commit 行为误当
	// 回归。
	snap := s2.Snapshot()
	if snap.Paused || snap.Recovered {
		t.Fatalf("consumed-residue-only must not pause (缺陷A 场景①), got %+v", snap)
	}
	lines := readRecoverySnapshotLines(t, snapshotPath)
	if len(lines) == 0 {
		t.Fatal("snapshot lines = 0, want at least 1 (观测面不受 A 块收窄影响)")
	}
	last := lines[len(lines)-1]
	if got := last["recovered"].(float64); got != 0 {
		t.Fatalf("recovered = %v, want 0 (consumed residue is not recovery work)", got)
	}
	if got := last["trigger"]; got != RecoveryTriggerCrossProcess {
		t.Fatalf("trigger = %v, want %q", got, RecoveryTriggerCrossProcess)
	}
}

// TestRecoverySnapshotPathEmptySkipsGracefully: a test that fails to resolve
// the reasonix home must skip the snapshot without erroring the recovery
// itself (observability must never gate recovery).
func TestRecoverySnapshotPathEmptySkipsGracefully(t *testing.T) {
	orig := recoverySnapshotPath
	recoverySnapshotPath = func() string { return "" }
	t.Cleanup(func() {
		CloseRecoverySnapshot()
		recoverySnapshotPath = orig
	})
	// A recovery with an empty snapshot path must not panic or fail the open.
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	st, err := Open(session, Limits{})
	if err != nil {
		t.Fatalf("open with empty snapshot path must not fail recovery: %v", err)
	}
	st.Close()
}
