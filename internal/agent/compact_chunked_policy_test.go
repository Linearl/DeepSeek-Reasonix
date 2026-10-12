package agent

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/tool"
)

func TestPressureCompactionDoesNotCallChunkedFold(t *testing.T) {
	prov := &extractStubProvider{failFirst: 64, reply: "digest"}
	a := agentOverForce(t, prov, foldableSessionOverForce(12))
	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("prepare = %v, want the failure absorbed with an admissible view, not a chunked projection", err)
	}
	if degradedFold(a) {
		t.Fatal("pressure compaction must not install a fabricated summary")
	}
	// 任务822: since 任务719 the over-ceiling session's bounded tail view is
	// already admissible, so the truncation rescue loses its premise (see the
	// compact_summary_guard contract) and the summary-budget rejection stays
	// visible as the blocked receipt. What must NOT happen is the chunked /
	// tree-reduce fold: the retry ladder stays at one summary plus one
	// doubled-budget retry.
	r := a.sess.compactionState.LastReceipt
	if r == nil || r.Status != "blocked" || r.Action != "summary" {
		t.Fatalf("receipt = %+v, want the blocked summary receipt, not a chunked digest or a mechanical fold", r)
	}
	if !strings.Contains(r.Reason, "output token limit") {
		t.Fatalf("receipt reason = %q, want the summarizer output truncation", r.Reason)
	}
	if prov.calls > 2 {
		t.Fatalf("provider calls = %d, want at most one summary plus one retry, not chunked/tree-reduce", prov.calls)
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
