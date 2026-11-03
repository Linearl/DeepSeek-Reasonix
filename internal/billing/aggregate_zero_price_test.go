package billing

import (
	"testing"
	"time"
)

// tl1379 口径落地：无定价条目按 0 计入会话总额，聚合区分
// complete / partial-with-zero（partial_priced + 计数）/ 真不可算 三态。

func TestAggregatePureZeroPricedSegmentsStayCompleteWithCount(t *testing.T) {
	first := PartialPricedQuote(QuoteInput{
		Usage:      UsageTokens{PromptTokens: 1_000_000},
		OccurredAt: time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC),
		ModelRef:   "mimo-token-plan/mimo-v2.6-flash",
	}, "CNY")
	second := PartialPricedQuote(QuoteInput{
		Usage:      UsageTokens{PromptTokens: 2_000_000, CompletionTokens: 1_000_000},
		OccurredAt: time.Date(2026, 11, 4, 8, 1, 0, 0, time.UTC),
		ModelRef:   "mimo-token-plan/mimo-v2.6-flash",
	}, "CNY")
	total := AggregateQuotes([]CostQuote{first, second}, "CNY")
	if !total.CostComplete || !total.Complete {
		t.Fatalf("pure zero-priced aggregate must stay usable: %+v", total)
	}
	if total.DisplayStatus != DisplayStatusPartialPriced {
		t.Fatalf("display status = %q, want partial_priced", total.DisplayStatus)
	}
	if total.PartialPricedCount != 2 {
		t.Fatalf("partial priced count = %d, want 2", total.PartialPricedCount)
	}
	if total.Selected == nil || total.Selected.Amount != "0" {
		t.Fatalf("selected = %+v, want explicit zero", total.Selected)
	}
}

func TestAggregateCountsZeroPricedSegmentAlongsidePriced(t *testing.T) {
	priced := BuildQuote(QuoteInput{
		Usage:           UsageTokens{PromptTokens: 1_000_000},
		Rates:           RateCard{CacheHit: 0.02, Input: 1, Output: 2, Currency: "CNY"},
		OccurredAt:      time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC),
		ProviderKind:    "mimo",
		ModelID:         "mimo-v2.6-flash",
		DisplayCurrency: "CNY",
	})
	partial := PartialPricedQuote(QuoteInput{
		Usage:      UsageTokens{PromptTokens: 3_000_000},
		OccurredAt: time.Date(2026, 11, 4, 8, 1, 0, 0, time.UTC),
	}, "CNY")
	total := AggregateQuotes([]CostQuote{priced, partial}, "CNY")
	if !total.CostComplete || !total.Complete {
		t.Fatalf("mixed aggregate must stay usable: %+v", total)
	}
	if total.DisplayStatus != DisplayStatusPartialPriced {
		t.Fatalf("display status = %q, want partial_priced", total.DisplayStatus)
	}
	if total.PartialPricedCount != 1 {
		t.Fatalf("partial priced count = %d, want 1", total.PartialPricedCount)
	}
	if total.Selected == nil || total.Selected.Amount != "1" {
		t.Fatalf("selected = %+v, want priced segment total 1", total.Selected)
	}
}

// Pre-1044 sidecars persist no_price segments as CostComplete=false with no
// currency fact. The tl1379 口径 upgrades them to zero-priced segments instead
// of failing the whole session total.
func TestLegacyNoPriceSegmentUpgradesToPartialPriced(t *testing.T) {
	legacy := CostQuote{
		Estimated:        true,
		CostComplete:     false,
		DisplayComplete:  false,
		Complete:         false,
		DisplayStatus:    DisplayStatusUnavailable,
		IncompleteReason: "no_price",
		ModelRef:         "mimo-token-plan/mimo-v2.6-flash",
		UsageSource:      "executor",
	}
	upgraded := NormalizeQuote(legacy)
	if !upgraded.CostComplete || !upgraded.Complete || !upgraded.DisplayComplete {
		t.Fatalf("legacy no_price must upgrade to a usable zero-priced fact: %+v", upgraded)
	}
	if upgraded.DisplayStatus != DisplayStatusPartialPriced {
		t.Fatalf("display status = %q, want partial_priced", upgraded.DisplayStatus)
	}
	priced := BuildQuote(QuoteInput{
		Usage:           UsageTokens{PromptTokens: 1_000_000},
		Rates:           RateCard{CacheHit: 0.02, Input: 1, Output: 2, Currency: "CNY"},
		OccurredAt:      time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC),
		ProviderKind:    "mimo",
		ModelID:         "mimo-v2.6-flash",
		DisplayCurrency: "CNY",
	})
	total := AggregateQuotes([]CostQuote{priced, legacy}, "CNY")
	if !total.CostComplete || !total.Complete {
		t.Fatalf("aggregate with legacy no_price segment must stay usable: %+v", total)
	}
	if total.PartialPricedCount != 1 {
		t.Fatalf("partial priced count = %d, want 1", total.PartialPricedCount)
	}
	if total.Selected == nil || total.Selected.Amount != "1" {
		t.Fatalf("selected = %+v, want priced segment total 1", total.Selected)
	}
}

// Pure legacy no_price sessions (no currency fact anywhere) still resolve to a
// usable zero total instead of the bucketed/unavailable dead end.
func TestPureLegacyNoPriceSessionResolvesZero(t *testing.T) {
	legacy := CostQuote{
		Estimated:        true,
		CostComplete:     false,
		DisplayComplete:  false,
		Complete:         false,
		DisplayStatus:    DisplayStatusUnavailable,
		IncompleteReason: "no_price",
		ModelRef:         "mimo-token-plan/mimo-v2.6-flash",
	}
	total := AggregateQuotes([]CostQuote{legacy}, "")
	if !total.Complete {
		t.Fatalf("pure legacy no_price session must stay usable: %+v", total)
	}
	if total.DisplayStatus != DisplayStatusPartialPriced || total.PartialPricedCount != 1 {
		t.Fatalf("status = %q count = %d, want partial_priced/1", total.DisplayStatus, total.PartialPricedCount)
	}
	if total.Selected == nil || total.Selected.Amount != "0" {
		t.Fatalf("selected = %+v, want zero", total.Selected)
	}
}

// 真异常保持不可算：usage_unknown 与 legacy 抹除记录不参与按 0 口径。
func TestTrueAnomaliesStayUnavailable(t *testing.T) {
	unknown := NormalizeQuote(CostQuote{IncompleteReason: "usage_unknown"})
	if unknown.Complete || unknown.DisplayStatus == DisplayStatusPartialPriced {
		t.Fatalf("usage_unknown must stay unavailable: %+v", unknown)
	}
	wiped := NormalizeQuote(CostQuote{IncompleteReason: "legacy_wiped_or_zero"})
	if wiped.Complete || wiped.DisplayStatus == DisplayStatusPartialPriced {
		t.Fatalf("legacy_wiped_or_zero must stay unavailable: %+v", wiped)
	}
	priced := BuildQuote(QuoteInput{
		Usage:           UsageTokens{PromptTokens: 1_000_000},
		Rates:           RateCard{Input: 1, Currency: "CNY"},
		OccurredAt:      time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC),
		DisplayCurrency: "CNY",
	})
	total := AggregateQuotes([]CostQuote{priced, wiped}, "CNY")
	if total.Complete || total.DisplayStatus == DisplayStatusPartialPriced {
		t.Fatalf("wiped segment must keep the aggregate unavailable: %+v", total)
	}
	if total.PartialPricedCount != 0 {
		t.Fatalf("partial priced count = %d, want 0", total.PartialPricedCount)
	}
}

// Ledger 全链路：recordUsage 式 Add + Total 在零价段上保持可用并带计数。
func TestLedgerTotalKeepsZeroPricedSegmentsUsable(t *testing.T) {
	ledger := NewLedger()
	ledger.Add(PartialPricedQuote(QuoteInput{
		Usage:      UsageTokens{PromptTokens: 500_000},
		OccurredAt: time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC),
		ModelRef:   "mimo-token-plan/mimo-v2.6-flash",
	}, "CNY"), UsageTokens{PromptTokens: 500_000}, time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC))
	ledger.Add(PartialPricedQuote(QuoteInput{
		Usage:      UsageTokens{CompletionTokens: 200_000},
		OccurredAt: time.Date(2026, 11, 4, 8, 1, 0, 0, time.UTC),
		ModelRef:   "mimo-token-plan/mimo-v2.6-pro",
	}, "CNY"), UsageTokens{CompletionTokens: 200_000}, time.Date(2026, 11, 4, 8, 1, 0, 0, time.UTC))
	total := ledger.Total("CNY")
	if !total.Complete || total.DisplayStatus != DisplayStatusPartialPriced {
		t.Fatalf("ledger total = %+v, want usable partial_priced", total)
	}
	if total.PartialPricedCount != 2 {
		t.Fatalf("partial priced count = %d, want 2", total.PartialPricedCount)
	}
}
