package control

import (
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
)

// tl1383 追加面验收：同一冲突指纹连续复燃的具名升级——2026-11-04 9318b1f6
// 转正事故里 base/disk 指纹每 15s 原样复燃、persist admission 循环拒绝、
// 会话不可写。钉住：错代 meta（id 残留+账位错代）在第 conflictReconcileStrikes
// 次同指纹冲突时被对齐到日志实况、盘上转录被采纳；每个指纹串只自愈一次。
func TestRepeatedConflictFingerprintReconcilesWrongGenerationMeta(t *testing.T) {
	dir := t.TempDir()
	ctrlA, _, path := divergedControllers1352(t, dir, "shared.jsonl", event.Discard)

	// 人工布置错代 meta（转正/改名把别代 meta 搬进来的事故形态）：id 指名
	// 别会话、账位停在别代。
	foreignID := "shared-recovery-0000000000000000"
	if err := agent.UpdateBranchMeta(path, false, func(meta *agent.BranchMeta) error {
		meta.ID = foreignID
		meta.Revision = 99999
		meta.DiskRevision = 99999
		return nil
	}); err != nil {
		t.Fatalf("stage wrong-generation meta: %v", err)
	}

	conflict := &agent.SessionSnapshotConflictError{
		Path:             path,
		Kind:             agent.SessionSnapshotConflictDiverged,
		BaseRevision:     253,
		DiskRevision:     99999,
		ExistingMessages: 5,
		SnapshotMessages: 5,
	}

	// strike 1：只计数，不动 meta。
	if ctrlA.reconcileWrongGenerationMetaOnRepeat(path, conflict) {
		t.Fatal("the first strike must not escalate")
	}
	meta := loadMetaForStrikeCheck(t, path)
	if meta.ID != foreignID {
		t.Fatalf("meta id changed after one strike: %q", meta.ID)
	}

	// strike 2：达到阈值——错代 meta 对齐、盘上转录被采纳（一次调用收敛）。
	if !ctrlA.reconcileWrongGenerationMetaOnRepeat(path, conflict) {
		t.Fatal("the second identical strike must escalate and adopt the reconciled disk")
	}
	meta = loadMetaForStrikeCheck(t, path)
	if meta.ID != agent.BranchID(path) {
		t.Fatalf("reconciled meta id = %q, want the file stem id %q", meta.ID, agent.BranchID(path))
	}
	if meta.Revision == 99999 {
		t.Fatal("the foreign-generation ledger position survived the reconciliation")
	}

	// strike 3：同一指纹串只自愈一次，之后回落既有梯子（不再重复自愈）。
	if ctrlA.reconcileWrongGenerationMetaOnRepeat(path, conflict) {
		t.Fatal("a healed fingerprint must not escalate again")
	}
}

// 身份一致的 meta 不是错代形态： strikes 走满也只回落既有梯子，账位不动。
func TestConsistentMetaNeverReconciledOnRepeatedConflicts(t *testing.T) {
	dir := t.TempDir()
	ctrlA, _, path := divergedControllers1352(t, dir, "shared2.jsonl", event.Discard)

	conflict := &agent.SessionSnapshotConflictError{
		Path:             path,
		Kind:             agent.SessionSnapshotConflictDiverged,
		BaseRevision:     253,
		DiskRevision:     253,
		ExistingMessages: 5,
		SnapshotMessages: 5,
	}
	for range conflictReconcileStrikes + 2 {
		if ctrlA.reconcileWrongGenerationMetaOnRepeat(path, conflict) {
			t.Fatal("a consistent meta must never escalate to adoption")
		}
	}
}

func loadMetaForStrikeCheck(t *testing.T, path string) agent.BranchMeta {
	t.Helper()
	meta, ok, err := agent.LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("load meta: ok=%v err=%v", ok, err)
	}
	return meta
}
