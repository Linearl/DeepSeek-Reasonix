package main

import (
	"testing"
	"time"

	"reasonix/internal/billing"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// tl1379 口径落地（desktop 侧）：无定价条目按 0 计入，零价会话保持可用；
// 真不可算不再抹除已积累的历史费用。

func zeroPricedQuote(modelRef string) billing.CostQuote {
	return billing.PartialPricedQuote(billing.QuoteInput{
		Usage:      billing.UsageTokens{PromptTokens: 100_000},
		OccurredAt: time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC),
		ModelRef:   modelRef,
	}, "CNY")
}

func TestZeroPricedUsageKeepsSessionCostUsable(t *testing.T) {
	tab := &WorkspaceTab{}
	tab.recordUsage(event.Event{
		Usage:       &provider.Usage{PromptTokens: 100_000, TotalTokens: 100_000},
		CostQuote:   func() *billing.CostQuote { q := zeroPricedQuote("mimo-token-plan/mimo-v2.6-flash"); return &q }(),
		UsageSource: event.UsageSourceExecutor,
	})
	got := tab.telemetrySnapshot().Usage
	if !got.SessionCostComplete {
		t.Fatalf("zero-priced session must stay complete (tl1379 口径): %+v", got.SessionCostQuote)
	}
	if got.SessionCost != 0 {
		t.Fatalf("session cost = %f, want 0", got.SessionCost)
	}
	if got.SessionCurrency != "CNY" {
		t.Fatalf("session currency = %q, want CNY", got.SessionCurrency)
	}
	quote := got.SessionCostQuote
	if quote == nil || quote.DisplayStatus != billing.DisplayStatusPartialPriced || quote.PartialPricedCount < 1 {
		t.Fatalf("session quote = %+v, want partial_priced with count", quote)
	}
}

func TestMixedPricedAndZeroPricedUsageSumsPricedOnly(t *testing.T) {
	tab := &WorkspaceTab{}
	tab.recordUsage(event.Event{
		Usage:       &provider.Usage{PromptTokens: 100_000, TotalTokens: 100_000},
		CostQuote:   func() *billing.CostQuote { q := zeroPricedQuote("mimo-token-plan/mimo-v2.6-flash"); return &q }(),
		UsageSource: event.UsageSourceExecutor,
	})
	tab.recordUsage(event.Event{
		Usage:       &provider.Usage{PromptTokens: 1_000_000, TotalTokens: 1_000_000},
		Pricing:     &provider.Pricing{Input: 1, Currency: "CNY"},
		UsageSource: event.UsageSourceExecutor,
	})
	got := tab.telemetrySnapshot().Usage
	if !got.SessionCostComplete {
		t.Fatalf("mixed session must stay complete: %+v", got.SessionCostQuote)
	}
	if got.SessionCost < 0.99 || got.SessionCost > 1.01 {
		t.Fatalf("session cost = %f, want priced segment ~1", got.SessionCost)
	}
	if quote := got.SessionCostQuote; quote == nil || quote.PartialPricedCount != 1 {
		t.Fatalf("session quote = %+v, want partial count 1", quote)
	}
}

// 真不可算（不可恢复记录）：标记 incomplete 但保留历史费用，不再清零。
func TestTrueAnomalyKeepsAccruedCost(t *testing.T) {
	tab := &WorkspaceTab{}
	tab.recordUsage(event.Event{
		Usage:       &provider.Usage{PromptTokens: 1_000_000, TotalTokens: 1_000_000},
		Pricing:     &provider.Pricing{Input: 1, Currency: "CNY"},
		UsageSource: event.UsageSourceExecutor,
	})
	before := tab.telemetrySnapshot().Usage.SessionCost
	if before <= 0 {
		t.Fatalf("priced event must accrue cost, got %f", before)
	}
	anomalous := billing.CostQuote{
		Estimated:        true,
		CostComplete:     false,
		DisplayComplete:  false,
		Complete:         false,
		DisplayStatus:    billing.DisplayStatusUnavailable,
		IncompleteReason: "legacy_wiped_or_zero",
		ModelRef:         "legacy/migrated",
	}
	tab.recordUsage(event.Event{
		Usage:       &provider.Usage{PromptTokens: 10, TotalTokens: 10},
		CostQuote:   &anomalous,
		UsageSource: event.UsageSourceExecutor,
	})
	got := tab.telemetrySnapshot().Usage
	if got.SessionCostComplete {
		t.Fatalf("anomalous segment must mark the quote incomplete: %+v", got.SessionCostQuote)
	}
	if got.SessionCost != before {
		t.Fatalf("accrued cost must survive an anomalous segment: before=%f after=%f", before, got.SessionCost)
	}
}

// 显示快照（wallet hint 覆盖路径）同样不抹历史费用。
func TestDisplaySnapshotKeepsAccruedCostOnAnomaly(t *testing.T) {
	tab := &WorkspaceTab{}
	tab.recordUsage(event.Event{
		Usage:       &provider.Usage{PromptTokens: 1_000_000, TotalTokens: 1_000_000},
		Pricing:     &provider.Pricing{Input: 1, Currency: "CNY"},
		UsageSource: event.UsageSourceExecutor,
	})
	anomalous := billing.CostQuote{
		Estimated:        true,
		CostComplete:     false,
		DisplayComplete:  false,
		Complete:         false,
		DisplayStatus:    billing.DisplayStatusUnavailable,
		IncompleteReason: "legacy_wiped_or_zero",
	}
	tab.recordUsage(event.Event{
		Usage:       &provider.Usage{PromptTokens: 10, TotalTokens: 10},
		CostQuote:   &anomalous,
		UsageSource: event.UsageSourceExecutor,
	})
	snapshot := tab.displayTelemetrySnapshot()
	usage := snapshot.Usage
	if usage.SessionCostComplete {
		t.Fatal("anomalous overlay must stay incomplete")
	}
	if usage.SessionCost <= 0 {
		t.Fatalf("display snapshot wiped accrued cost: %f", usage.SessionCost)
	}
}
