package main

import (
	"time"

	"reasonix/internal/billing"
)

// tl1379 功能层: subscription-plan sessions accrue tokens meaningfully while
// their cumulative token cost is not a billable fact, so the session cost
// surface needs a reset. This clears ONLY the cost bookkeeping — the CostLedger
// occurrence facts, the session cost scalars, and the per-source cost
// accumulators — and rewrites the telemetry sidecar so a reload keeps the
// zeroed state. Token statistics (prompt/completion/cache counters, request
// counts, elapsed time) are deliberately untouched.

// ResetSessionCost resets the active tab's accrued session cost.
func (a *App) ClearSessionCost() error {
	return a.ClearSessionCostForTab("")
}

// ClearSessionCostForTab resets the requested tab's accrued session cost
// regardless of later focus changes. Idempotent: resetting an already-zero
// session rewrites the same zero state and reports no error.
func (a *App) ClearSessionCostForTab(tabID string) error {
	tab := a.tabByID(tabID)
	if tab == nil {
		return a.workspaceNotReadyErr(nil)
	}
	if a.tabIsReadOnly(tab) {
		return readOnlyChannelErr()
	}
	tab.clearSessionCost()
	if path := tab.currentSessionPath(); path != "" {
		_ = saveTelemetry(path+".telemetry.json", tab.telemetrySnapshot())
	}
	return nil
}

// resetSessionCost zeroes the cost bookkeeping under the telemetry lock and
// bumps the runtime generation so in-flight display hints cannot rebind a
// stale quote onto the reset state.
func (t *WorkspaceTab) clearSessionCost() {
	if t == nil {
		return
	}
	t.telemMu.Lock()
	defer t.telemMu.Unlock()
	t.usageTelemetry.SessionCost = 0
	t.usageTelemetry.SessionCurrency = ""
	t.usageTelemetry.SessionCostUsd = 0
	t.usageTelemetry.SessionCostComplete = true
	t.usageTelemetry.CostLedger = billing.NewLedger()
	quote := resetZeroCostQuote()
	t.usageTelemetry.SessionCostQuote = &quote
	for name, src := range t.usageTelemetry.Sources {
		src.SessionCost = 0
		src.SessionCostUsd = 0
		src.SessionCurrency = ""
		t.usageTelemetry.Sources[name] = src
	}
	t.runtimeCostQuote = nil
	t.runtimeCostGeneration++
}

// resetZeroCostQuote is the explicit post-reset quote: a complete, matched zero
// so surfaces render ¥0.00 instead of「不可估算」— a deliberate reset is a cost
// fact, unlike a fresh session that merely has no estimate yet.
func resetZeroCostQuote() billing.CostQuote {
	zero := billing.MoneyOf(billing.Zero, "CNY")
	return billing.CostQuote{
		Original: zero,
		Valuations: map[string]billing.Valuation{
			"CNY": {Money: zero, Basis: billing.BasisIdentity, Source: "session_cost_reset", AsOf: time.Now().UTC().Format("2006-01-02")},
		},
		Selected:        &zero,
		Estimated:       true,
		CostComplete:    true,
		DisplayComplete: true,
		Complete:        true,
		DisplayStatus:   billing.DisplayStatusMatched,
		AggregateMode:   billing.AggregateModeSingleCurrency,
	}
}
