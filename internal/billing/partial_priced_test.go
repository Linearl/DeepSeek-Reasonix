package billing

import (
	"testing"
	"time"
)

func TestPartialPricedQuoteBillsZeroWithoutFailingSession(t *testing.T) {
	occurred := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	q := PartialPricedQuote(QuoteInput{
		Usage:      UsageTokens{PromptTokens: 1_000_000, CompletionTokens: 500_000},
		OccurredAt: occurred,
		ModelRef:   "mimo-api/mimo-v2.6-pro-ultraspeed",
	}, "CNY")
	if q.Original.Amount != "0" || NormalizeCurrency(q.Original.Currency) != "CNY" {
		t.Fatalf("partial_priced original = %+v, want 0 CNY", q.Original)
	}
	if !q.CostComplete || !q.Complete || !q.DisplayComplete {
		t.Fatalf("partial_priced quote must keep the session cost usable: %+v", q)
	}
	if q.DisplayStatus != DisplayStatusPartialPriced || q.IncompleteReason != "missing_price_or_usage" {
		t.Fatalf("partial_priced status = %q reason = %q", q.DisplayStatus, q.IncompleteReason)
	}
	if q.Selected == nil || q.Selected.Amount != "0" {
		t.Fatalf("partial_priced selected = %+v, want zero selection", q.Selected)
	}
	if q.PricingFingerprint == "" {
		t.Fatal("partial_priced quote must carry a fingerprint so the ledger keys the bucket")
	}
}

func TestAggregateKeepsPricedTotalWhenSegmentIsPartial(t *testing.T) {
	priced := BuildQuote(QuoteInput{
		Usage:           UsageTokens{PromptTokens: 1_000_000},
		Rates:           RateCard{CacheHit: 0.02, Input: 1, Output: 2, Currency: "CNY"},
		OccurredAt:      time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC),
		ProviderKind:    "mimo",
		ModelID:         "mimo-v2.5",
		DisplayCurrency: "CNY",
	})
	partial := PartialPricedQuote(QuoteInput{
		Usage:      UsageTokens{PromptTokens: 2_000_000},
		OccurredAt: time.Date(2026, 10, 21, 8, 1, 0, 0, time.UTC),
	}, "CNY")
	total := AggregateQuotes([]CostQuote{priced, partial}, "CNY")
	if !total.CostComplete || !total.Complete {
		t.Fatalf("aggregate with a partial segment must stay usable: %+v", total)
	}
	if total.Selected == nil || total.Selected.Amount != "1" {
		t.Fatalf("aggregate selected = %+v, want priced segment total 1", total.Selected)
	}
}

func TestPartialPricedQuoteDefaultsToCNYWhenCurrencyUnknown(t *testing.T) {
	q := PartialPricedQuote(QuoteInput{Usage: UsageTokens{PromptTokens: 1}}, "")
	if NormalizeCurrency(q.Original.Currency) != "CNY" {
		t.Fatalf("currency = %q, want CNY fallback", q.Original.Currency)
	}
}
