package billing

import "testing"

func TestOfficialCatalogEntriesEmbedsFirstBatch(t *testing.T) {
	entries := OfficialCatalogEntries()
	if len(entries) == 0 {
		t.Fatal("embedded catalog is empty")
	}
	type key struct{ provider, model, currency, band string }
	byKey := map[key]CatalogEntry{}
	for _, e := range entries {
		byKey[key{e.Provider, e.Model, e.Currency, e.RateBand}] = e
	}
	want := []struct {
		provider, model, currency, band string
		cacheHit, input, output         float64
	}{
		{"deepseek", "deepseek-flash", "CNY", "", 0.02, 1, 4},
		{"deepseek", "deepseek-flash", "CNY", RateBandPeak, 0.04, 2, 8},
		{"deepseek", "deepseek-v4-pro", "CNY", "", 0.15, 4.5, 13.5},
		{"deepseek", "deepseek-v4-pro", "CNY", RateBandPeak, 0.30, 9, 27},
		{"longcat", "LongCat-2.0", "CNY", "", 0.04, 2, 8},
		{"longcat", "LongCat-2.0", "USD", "", 0.006, 0.3, 1.2},
		{"mimo", "mimo-v2.5-pro", "CNY", "", 0.025, 3, 6},
		{"mimo", "mimo-v2.5", "CNY", "", 0.02, 1, 2},
		{"mimo", "mimo-v2-flash", "CNY", "", 0.07, 0.7, 2.1},
	}
	for _, w := range want {
		got, ok := byKey[key{w.provider, w.model, w.currency, w.band}]
		if !ok {
			t.Fatalf("embedded catalog missing %s/%s/%s band=%q", w.provider, w.model, w.currency, w.band)
		}
		if got.CacheHit != w.cacheHit || got.Input != w.input || got.Output != w.output {
			t.Fatalf("%s/%s/%s band=%q card = %+v, want %.6g/%.6g/%.6g",
				w.provider, w.model, w.currency, w.band, RateCardFromCatalog(got), w.cacheHit, w.input, w.output)
		}
		if got.BillingMode != BillingModePAYG && got.BillingMode != BillingModeSubscriptionEquivalent {
			t.Fatalf("%s/%s billingMode = %q", w.provider, w.model, got.BillingMode)
		}
		if got.Fingerprint == "" {
			t.Fatalf("%s/%s fingerprint is empty", w.provider, w.model)
		}
	}
	if _, ok := byKey[key{"deepseek", "deepseek-flash", "CNY", RateBandOffPeak}]; ok {
		t.Fatal("base price row must not claim an explicit off-peak band")
	}
}

func TestOfficialScheduleEffectiveAtResolves(t *testing.T) {
	at, ok := OfficialScheduleEffectiveAt("deepseek-v4-flash-2026-10-21")
	if !ok || at.IsZero() {
		t.Fatalf("embedded schedule effective = %v ok=%v", at, ok)
	}
	if _, ok := OfficialScheduleEffectiveAt("unknown-schedule"); ok {
		t.Fatal("unknown schedule must not resolve")
	}
}
