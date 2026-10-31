package agent

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/tool"
)

// tl1083 §5.2-P0② 契约翻转（原名 TestPressureCompactionDoesNotCallChunkedFold）：
// pressure 在封顶单次摘要 + 加倍重试都尺寸失败后，分块道现在会接手（与
// overflow 的任务1010 需求 2 同道）。本钉保留的是旧测试的安全内核：分块道
// 也失败时，失败必须保持可见（blocked summary 收据、截断原因）、绝不装上
// 伪造摘要、视图仍以有界尾窗离场。调用数断言从 ≤2 翻为 >2——分块道确已
// 介入，这是 ② 的直接读数，不是副作用。
func TestPressurePersistentSizeFailureStaysVisibleAfterChunkedLane(t *testing.T) {
	prov := &extractStubProvider{failFirst: 64, reply: "digest"}
	a := agentOverForce(t, prov, foldableSessionOverForce(12))
	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("prepare = %v, want the failure absorbed with an admissible view, not a hard error", err)
	}
	if degradedFold(a) {
		t.Fatal("pressure compaction must not install a fabricated summary")
	}
	r := a.sess.compactionState.LastReceipt
	if r == nil || r.Status != "blocked" || r.Action != "summary" {
		t.Fatalf("receipt = %+v, want the blocked summary receipt, not a chunked digest or a mechanical fold", r)
	}
	if !strings.Contains(r.Reason, "output token limit") {
		t.Fatalf("receipt reason = %q, want the summarizer output truncation", r.Reason)
	}
	if prov.calls <= 2 {
		t.Fatalf("provider calls = %d, want >2: the chunked lane must have engaged after the summary + doubled retry", prov.calls)
	}
	if est, hard := a.estimatedVisibleRequestTokens(a.modelVisibleMessages()), a.hardInputCeiling(); est >= hard {
		t.Fatalf("visible view = %d tokens, want the bounded tail window under the ceiling %d", est, hard)
	}
}

func TestExplicitChunkedFallbackStillRuns(t *testing.T) {
	prov := &extractStubProvider{failFirst: 1, reply: "digest"}
	a := New(prov, tool.NewRegistry(), extractStubSession(), Options{}, event.Discard)
	fold := a.Session().Snapshot()
	if _, _, err := a.foldSummaryWithChunkedFallback(context.Background(), CompactionTriggerManual, fold, "focus", 321, SummaryInputCachePrefix); err != nil {
		t.Fatalf("explicit chunked fallback: %v", err)
	}
	if prov.calls < 2 {
		t.Fatalf("provider calls = %d, want the failed summary plus chunked recovery", prov.calls)
	}
}
