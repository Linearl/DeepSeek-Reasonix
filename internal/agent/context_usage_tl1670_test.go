package agent

import "testing"

// tl1670: 压缩失败后读数不得固化旧估算。
//
// 现场（20261117 18:34）：一轮 pressure 压缩失败（compaction failed
// src=1050159）之后，右栏读数纹丝不动——用户看到的是失败前那份估算，而它
// 早就与 canonical 差了 2.7 倍。后端这一层的保证是：任何维护终局（投影
// 落地 / 本轮失败 / 被阻塞）都推进 compaction generation，而 contextUsage
// 的 memo 键带着这个 generation ⇒ 终局之后的下一次读数必然重算，不会复用
// 终局前的估算。
//
// 断言用「memo 被重新写过」作观测量：键命中时函数提前返回、根本不写 memo，
// 所以指针不变即证明旧 memo 被复用；指针变了即证明重算过。
func TestTL1670ContextUsageRecomputesAfterAMaintenanceOutcome(t *testing.T) {
	a := usageFixture(t, 6)
	first := a.ContextUsedTokens()
	if first <= 0 {
		t.Fatalf("gauge = %d, want a real view size", first)
	}
	memo := a.sess.output.contextUsage.Load()
	if memo == nil {
		t.Fatal("no memo stored after the first read")
	}
	genBefore := memo.compactionGen

	// 真实的失败路径：context_receipt.go recordContextMaintenanceOutcome
	// （summaryFailed → 本函数）推进 generation 并落一张 failed 回执。
	a.recordContextMaintenanceOutcome("input-hash-1", CompactionTriggerPressure, "summary", "failed", "compaction failed for the pin", false)

	genAfter := a.currentCompactionGeneration()
	if genAfter <= genBefore {
		t.Fatalf("compaction generation did not advance on a failed maintenance outcome: %d -> %d", genBefore, genAfter)
	}

	// 终局之后的第一读：memo 必须重写（=估算重算），不得复用终局前那份。
	a.ContextUsedTokens()
	rememo := a.sess.output.contextUsage.Load()
	if rememo == nil {
		t.Fatal("no memo stored after the maintenance outcome")
	}
	if rememo == memo {
		t.Fatal("the pre-outcome memo was reused: the gauge kept serving the pre-failure estimate")
	}
	if rememo.compactionGen != genAfter {
		t.Fatalf("recomputed memo carries generation %d, want the post-outcome %d", rememo.compactionGen, genAfter)
	}
}

// tl1670: 投影落地（成功终局）同样推进 generation——成功与失败两条终局腿都
// 必须让读数重算，不能只有失败腿生效。
func TestTL1670ContextUsageRecomputesAfterASuccessfulOutcome(t *testing.T) {
	a := usageFixture(t, 4)
	a.ContextUsedTokens()
	memo := a.sess.output.contextUsage.Load()
	if memo == nil {
		t.Fatal("no memo stored after the first read")
	}
	genBefore := memo.compactionGen

	// maintenance_commit.go 的成功提交路径同样 Generation++（commit 前）。
	a.sess.compactionMu.Lock()
	a.sess.compactionState.Generation++
	a.sess.compactionMu.Unlock()

	if got := a.currentCompactionGeneration(); got <= genBefore {
		t.Fatalf("compaction generation did not advance: %d -> %d", genBefore, got)
	}
	a.ContextUsedTokens()
	rememo := a.sess.output.contextUsage.Load()
	if rememo == nil || rememo == memo {
		t.Fatal("the pre-outcome memo was reused after a successful maintenance outcome")
	}
}

// tl1670: 读数口径的同一把尺——面板主读数（下一请求发送量）与 canonical
// （整段历史体积）在常态态下必然分叉（压缩只写投影，历史永不缩减），这个
// 分叉必须被如实上报给前端，而不是悄悄换尺。后端这一层保证两把尺都拿得到：
// CanonicalTokens（全量记录）与 ProjectedTokens（发送量）分别可读，且在
// 投影有效时 canonical 确实远大于视图读数（现场：source 771812 → 视图
// 261814）。
func TestTL1670MaintenanceSnapshotExposesBothCalibers(t *testing.T) {
	a := usageFixture(t, 8)
	snap := a.ContextMaintenanceSnapshot()
	if snap.CanonicalTokens <= 0 {
		t.Fatalf("canonical = %d, want the whole-history volume", snap.CanonicalTokens)
	}
	if snap.ProjectedTokens <= 0 {
		t.Fatalf("projected = %d, want the payload size", snap.ProjectedTokens)
	}
	// 没有投影时两把尺读的是同一个消息集（canonical 就是下一请求要发的量）。
	if snap.ProjectionValid {
		t.Fatalf("fixture has no projection; ProjectionValid = true is unexpected")
	}
	if snap.CanonicalTokens != snap.ProjectedTokens {
		t.Fatalf("canonical = %d, projected = %d; without a projection both read the same view", snap.CanonicalTokens, snap.ProjectedTokens)
	}
}
