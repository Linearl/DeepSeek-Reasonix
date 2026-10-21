package billing

import (
	_ "embed"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// pricingCatalogJSON is the first batch of the embedded price book (task 1044).
// Keeping the numbers in data rather than Go literals means a vendor repricing
// is a one-file data edit with no recompilation of catalog logic.
//
//go:embed pricing_catalog.json
var pricingCatalogJSON []byte

// pricingScheduleEffectives pins each schedule's go-live instant in UTC so the
// occurrence-time resolver stays locale-independent without inventing another
// schedule constant per row.
var pricingScheduleEffectives = map[string]time.Time{
	"deepseek-v4-flash-2026-10-21": time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC),
}

// embeddedPricingCatalog is the parsed shape of pricing_catalog.json.
type embeddedPricingCatalog struct {
	Version   int                         `json:"version"`
	Source    string                      `json:"source"`
	Schedules map[string]embeddedSchedule `json:"schedules,omitempty"`
	Entries   []embeddedPricingEntry      `json:"entries"`
}

// embeddedSchedule describes one price generation's metadata.
type embeddedSchedule struct {
	ID             string  `json:"id"`
	EffectiveFrom  string  `json:"effectiveFrom,omitempty"`
	PeakMultiplier float64 `json:"peakMultiplier,omitempty"`
	PeakBand       string  `json:"peakBand,omitempty"`
	DocURL         string  `json:"docURL,omitempty"`
	Notes          string  `json:"notes,omitempty"`
}

// embeddedPricingEntry is one provider+model+currency price row.
type embeddedPricingEntry struct {
	Provider    string  `json:"provider"`
	Model       string  `json:"model"`
	Currency    string  `json:"currency"`
	CacheHit    float64 `json:"cacheHit"`
	Input       float64 `json:"input"`
	Output      float64 `json:"output"`
	ScheduleID  string  `json:"scheduleId,omitempty"`
	RateBand    string  `json:"rateBand,omitempty"`
	DocURL      string  `json:"docURL,omitempty"`
	BillingMode string  `json:"billingMode,omitempty"`
	Notes       string  `json:"notes,omitempty"`
}

var embeddedCatalog = func() embeddedPricingCatalog {
	var c embeddedPricingCatalog
	if err := json.Unmarshal(pricingCatalogJSON, &c); err != nil {
		panic("billing: pricing_catalog.json: " + err.Error())
	}
	return c
}()

// OfficialCatalogEntries returns the embedded official price book. Rows carrying
// a scheduleId and no rateBand represent the vendor's base (off-peak) price; the
// peak row for a schedule is derived by multiplying the base card by the
// schedule's peak multiplier, which is how DeepSeek publishes its tables (peak
// = off-peak × 2, no separate peak price list).
func OfficialCatalogEntries() []CatalogEntry {
	out := make([]CatalogEntry, 0, len(embeddedCatalog.Entries))
	for _, row := range embeddedCatalog.Entries {
		base := CatalogEntry{
			Provider:    row.Provider,
			Model:       row.Model,
			Currency:    NormalizeCurrency(row.Currency),
			CacheHit:    row.CacheHit,
			Input:       row.Input,
			Output:      row.Output,
			ScheduleID:  row.ScheduleID,
			RateBand:    row.RateBand,
			DocURL:      row.DocURL,
			BillingMode: normalizeBillingMode(row.BillingMode),
			Notes:       row.Notes,
		}
		out = append(out, base)
		if row.ScheduleID != "" && row.RateBand == "" {
			if schedule, ok := embeddedCatalog.Schedules[row.ScheduleID]; ok && schedule.PeakMultiplier > 1 {
				peak := base
				peak.RateBand = RateBandPeak
				peak.CacheHit = base.CacheHit * schedule.PeakMultiplier
				peak.Input = base.Input * schedule.PeakMultiplier
				peak.Output = base.Output * schedule.PeakMultiplier
				peak.Notes = base.Notes + "（高峰 ×" + formatMultiplier(schedule.PeakMultiplier) + "）"
				out = append(out, peak)
			}
		}
	}
	for i := range out {
		out[i].Fingerprint = PricingFingerprint(RateCardFromCatalog(out[i]))
	}
	return out
}

// OfficialScheduleEffectiveAt reports when an embedded schedule took effect.
func OfficialScheduleEffectiveAt(scheduleID string) (time.Time, bool) {
	at, ok := pricingScheduleEffectives[scheduleID]
	return at, ok
}

// OfficialRateCardFor returns the embedded official base price for a vendor
// model, preferring the row matching preferredCurrency when the table carries
// one. Rows with an explicit rateBand are vendor-published peak rows and are
// only reachable through a trusted schedule resolution, never by model name.
func OfficialRateCardFor(provider, model, preferredCurrency string) (RateCard, bool) {
	want := NormalizeCurrency(preferredCurrency)
	var fallback RateCard
	found := false
	for _, e := range OfficialCatalogEntries() {
		if e.ScheduleID != "" && e.RateBand != "" {
			continue
		}
		if e.Provider != strings.ToLower(strings.TrimSpace(provider)) || e.Model != strings.TrimSpace(model) {
			continue
		}
		card := RateCardFromCatalog(e)
		if NormalizeCurrency(card.Currency) == want {
			return card, true
		}
		if !found {
			fallback = card
			found = true
		}
	}
	return fallback, found
}

func formatMultiplier(m float64) string {
	if m == float64(int(m)) {
		return strconv.Itoa(int(m))
	}
	return strconv.FormatFloat(m, 'g', -1, 64)
}
