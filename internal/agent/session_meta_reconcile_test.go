package agent

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/store"

	"reasonix/internal/provider"
)

// tl1383 追加面：branch-meta 侧车就是 CAS 账本，错代 meta（id 残留别会话+
// 别代 revision/digest）会让每次保存都撞冲突且自愈无门（2026-11-04 9318b1f6
// 转正事故）。对齐规则：id 与文件名不符=错代 → 身份改写+账位对齐事件日志
// 实况；id 相符=本会话自己的账本 → 任何偏差都不动（崩溃窗口 ±1 是合法形态）。

func reconcileStageMessages(t *testing.T) []provider.Message {
	t.Helper()
	return []provider.Message{
		{Role: provider.RoleUser, Content: "turn one"},
		{Role: provider.RoleAssistant, Content: "answer one"},
	}
}

// reconcileStageEventLog 在 path 落一份 replace 事件（账位 base+1），返回
// 日志尾账位与其内容摘要字符串。
func reconcileStageEventLog(t *testing.T, path string, base int64) (int64, string) {
	t.Helper()
	msgs := reconcileStageMessages(t)
	digest, _, err := digestAndSizeSessionMessages(msgs)
	if err != nil {
		t.Fatal(err)
	}
	if err := appendSessionReplaceEvent(path, msgs, digest, base, "reconcile-test"); err != nil {
		t.Fatal(err)
	}
	return base + 1, digestString(digest)
}

func loadMetaForReconcile(t *testing.T, path string) BranchMeta {
	t.Helper()
	meta, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("load meta: ok=%v err=%v", ok, err)
	}
	return meta
}

func TestReconcileBranchMetaIdentityWrongGenerationAlignsLedger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "talk.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tailRevision, tailDigest := reconcileStageEventLog(t, path, 252)

	// 错代 meta：id 残留副本身份、revision/digest 停在更早世代（事故形态）。
	foreignID := "talk-recovery-0000000000000000"
	if err := SaveBranchMeta(path, BranchMeta{
		ID:            foreignID,
		Revision:      420,
		DiskRevision:  420,
		ContentDigest: digestString(sha256.Sum256([]byte("stale-generation-bytes"))),
	}); err != nil {
		t.Fatal(err)
	}

	changed, err := ReconcileBranchMetaIdentity(path)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !changed {
		t.Fatal("a wrong-generation meta must be reconciled")
	}
	got := loadMetaForReconcile(t, path)
	if got.ID != BranchID(path) {
		t.Fatalf("reconciled id = %q, want the file stem id %q", got.ID, BranchID(path))
	}
	if got.Revision != tailRevision || got.DiskRevision != tailRevision {
		t.Fatalf("reconciled revisions = (rev %d, disk %d), want the log tail %d", got.Revision, got.DiskRevision, tailRevision)
	}
	if got.ContentDigest != tailDigest {
		t.Fatalf("reconciled digest = %q, want the log content digest %q", got.ContentDigest, tailDigest)
	}

	// 幂等：对齐后的 meta 再跑一遍是无事可办。
	changed, err = ReconcileBranchMetaIdentity(path)
	if err != nil || changed {
		t.Fatalf("second reconcile = (changed %v, err %v), want a no-op", changed, err)
	}
}

// id 相符的 meta 是本会话自己的账本：revision 与日志的偏差属于崩溃窗口的
// 「已记账未落盘」合法形态，对齐绝不能回拨它。
func TestReconcileBranchMetaIdentityConsistentIdentityUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mine.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reconcileStageEventLog(t, path, 252)
	own := BranchID(path)
	if err := SaveBranchMeta(path, BranchMeta{
		ID:       own,
		Revision: 999, // 已记账未落盘窗口
	}); err != nil {
		t.Fatal(err)
	}
	changed, err := ReconcileBranchMetaIdentity(path)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if changed {
		t.Fatal("a same-identity meta must never be touched")
	}
	if got := loadMetaForReconcile(t, path); got.Revision != 999 {
		t.Fatalf("own-ledger revision was rewound to %d, want 999 kept", got.Revision)
	}
}

func TestReconcileBranchMetaIdentityMissingMetaNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "absent-meta.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := ReconcileBranchMetaIdentity(path)
	if err != nil || changed {
		t.Fatalf("missing-meta reconcile = (changed %v, err %v), want a no-op", changed, err)
	}
	if _, err := os.Stat(store.SessionMeta(path)); !os.IsNotExist(err) {
		t.Fatalf("reconcile must not invent a sidecar: %v", err)
	}
}

// 转正接线验收：tl1352 rename 链把副本侧车搬上主名后，meta 必须落在主身份
// 上、账位对齐副本事件日志实况（2026-11-04 9318b1f6 事故的修复面）。
func TestPromoteRecoveryBranchToMainReconcilesMetaGeneration(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "topic.jsonl")
	copyPath := filepath.Join(dir, "topic-recovery-21b887801d82d805.jsonl")
	for _, p := range []string{main, copyPath} {
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// 副本自己的事件日志：账位真相在 253。
	tailRevision, tailDigest := reconcileStageEventLog(t, copyPath, 252)
	// 副本 meta 带着错代账位（id=副本身份是副本期的正确形态；revision 停在
	// 更早世代模拟 fork 嵌套继承的旧账）。
	if err := SaveBranchMeta(copyPath, BranchMeta{
		ID:            BranchID(copyPath),
		Revision:      256,
		DiskRevision:  420,
		ContentDigest: digestString(sha256.Sum256([]byte("fork1-era-bytes"))),
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := PromoteRecoveryBranchToMain(copyPath, main); err != nil {
		t.Fatalf("promote: %v", err)
	}
	got := loadMetaForReconcile(t, main)
	if got.ID != BranchID(main) {
		t.Fatalf("promoted meta id = %q, want the main stem id %q", got.ID, BranchID(main))
	}
	if got.Revision != tailRevision || got.DiskRevision != tailRevision {
		t.Fatalf("promoted meta revisions = (rev %d, disk %d), want the copy log tail %d", got.Revision, got.DiskRevision, tailRevision)
	}
	if got.ContentDigest != tailDigest {
		t.Fatalf("promoted meta digest = %q, want the copy log digest %q", got.ContentDigest, tailDigest)
	}
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Fatalf("promoted copy still on disk: %v", err)
	}
}
