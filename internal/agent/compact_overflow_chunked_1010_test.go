package agent

import (
	"context"
	"testing"
)

// 任务 1010 需求 2：overflow 救援道的摘要尺寸失败后必须能在分块道上推进，
// 而不是停在 blocked 收据上永卡（现场：摘要请求 2.1M > 1M 窗，压缩永远
// 失败冻结循环）。pressure 的任务822 契约（单次摘要+一次加倍重试、有界尾窗
// 为最终答案）由 TestPressureCompactionDoesNotCallChunkedFold 继续钉住。
func TestOverflowFoldRescuesThroughChunkedPath(t *testing.T) {
	prov := &extractStubProvider{failFirst: 1, reply: "digest"}
	a := agentOverForce(t, prov, foldableSessionOverForce(6))
	if err := prepareContext(context.Background(), a, CompactionTriggerOverflow); err != nil {
		t.Fatalf("overflow prepare = %v, want the chunked rescue to install a real digest", err)
	}
	if degradedFold(a) {
		t.Fatal("overflow rescue must not degrade to a mechanical digest")
	}
	r := a.sess.compactionState.LastReceipt
	if r == nil || r.Status != "applied" {
		t.Fatalf("receipt = %+v, want an applied fold", r)
	}
	if got := latestDigest(a.sess.compactionState.Projection.Messages); got == "" {
		t.Fatal("the chunked rescue must install the summarized digest")
	}
	// call1 = 封顶单次摘要（截断失败），call2.. = 分块道；至少两次请求。
	if prov.calls < 2 {
		t.Fatalf("provider calls = %d, want the failed summary plus the chunked rescue", prov.calls)
	}
}
