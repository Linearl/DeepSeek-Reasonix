package control

import (
	"context"
	"os"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/store"
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

// tl1383 集成钉（任务书原话：测试钉 meta 错代形态不永久死锁）——9318b1f6
// 转正事故端到端：布景用事故的搬运行为本身（副本转录+全部侧车原样搬上主
// 名、无任何身份改写，即 tl1352 rename 转正链修复前的形态）把错代 meta 存
// 量布置在主名上——meta.id 残留副本身份、账位停在副本世代，盘上字节是
// fork 的转录，而 ctrlB 的内存转录与盘上互不相认（事故的三方互不相认形
// 态）。此后连打 Snapshot 断言收敛：真实冲突同指纹复燃把 strike 梯子挂上
// （base=ctrlB 基线、disk=外来账位，每次保存失败都不变——真分歧一次隔离
// 后不会再有同指纹，只有错代账本能原样复燃），既有梯子 fork 隔离、延迟转
// 正结算时 tl1383 修复①对齐 meta 身份，最终保存成功、可写、副本清零。
// strike 的精确阈值语义由上面两件单测钉死；本件钉的是真实 Snapshot 链的
// 接线活性与「错代存量可被消化」这一存活性质。
func TestWrongGenerationMetaPromotionConvergesWithoutDeadlock(t *testing.T) {
	dir := t.TempDir()
	ctrlA, ctrlB, path := divergedControllers1352(t, dir, "shared9.jsonl", event.Discard)

	// divergedControllers1352 收尾时：ctrlA 已被真分歧冲突隔离到 fork 副本
	// （延迟转正未结算），主名上是 B 的转录，ctrlB 内存与主名一致。
	recovery := ctrlA.SessionPath()
	if recovery == path {
		t.Fatal("the diverged controller must sit isolated on its fork copy")
	}

	// 字面复现事故转正：副本转录与全部侧车原样搬上主名，无身份改写——
	// meta 落在主名时带着副本身份与副本世代账位（错代存量）。
	mainSidecars := store.SessionSidecarFiles(path)
	recoverySidecars := store.SessionSidecarFiles(recovery)
	for i, src := range recoverySidecars {
		b, err := os.ReadFile(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue // 副本没有这个侧车
			}
			t.Fatal(err)
		}
		if err := os.WriteFile(mainSidecars[i], b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// 转正语义收尾：副本随 rename 消失（转录本体与侧车一并）。
	if err := os.Remove(recovery); err != nil {
		t.Fatal(err)
	}
	for _, src := range recoverySidecars {
		_ = os.Remove(src)
	}

	// 布景核验：主名 meta 现在是副本身份的错代存量。
	staged := loadMetaForStrikeCheck(t, path)
	if staged.ID == agent.BranchID(path) {
		t.Fatalf("staging: the copied meta must carry the copy identity, got %q", staged.ID)
	}

	// ctrlB 新回合后连打 Snapshot：内存 B 转录 vs 盘上 fork 字节 = 真分歧
	// 冲突，指纹含错代账位所以每次保存原样复燃。
	if err := ctrlB.RunTurn(context.Background(), "third from B"); err != nil {
		t.Fatalf("B third turn: %v", err)
	}
	converged := false
	for range 8 {
		if err := ctrlB.SnapshotDurable(); err != nil {
			t.Fatalf("snapshot during convergence: %v", err)
		}
		if ctrlB.SessionPath() == path {
			if got := loadMetaForStrikeCheck(t, path); got.ID == agent.BranchID(path) {
				converged = true
				break
			}
		}
	}
	if !converged {
		t.Fatalf("the wrong-generation shape never converged: path=%s meta id=%q (deadlock)",
			ctrlB.SessionPath(), loadMetaForStrikeCheck(t, path).ID)
	}
	if ctrlB.conflictStrikes < 1 {
		t.Fatal("the real snapshot conflict must have armed the strike ladder")
	}

	// 收敛后可写：再走一个回合、再存一次，全部直接成功（不永久死锁）。
	if err := ctrlB.RunTurn(context.Background(), "fourth from B"); err != nil {
		t.Fatalf("post-convergence turn: %v", err)
	}
	if err := ctrlB.SnapshotDurable(); err != nil {
		t.Fatalf("post-convergence snapshot: %v", err)
	}
	// 副本清零（转正把隔离副本 rename 回主名）。
	if n := recoveryCopyCount1352(t, path); n != 0 {
		t.Fatalf("recovery copies after convergence = %d, want 0", n)
	}
	// 盘上可加载且保住冲突另一方的字节（fork 隔离保底，被逐下的 A 转录在
	// 布景归档/副本里零丢失——这里只断主名可加载）。
	if _, err := agent.LoadSession(path); err != nil {
		t.Fatalf("load converged session: %v", err)
	}
}
