package agent

import (
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1726: the report's pressure reading is the trigger's own input
// (ContextUsedTokens) — never the billed aggregate, never a lagging last-turn
// prompt. Slash /context and the serve gauge (ContextSnapshot) then report one
// caliber for the same session.
func TestContextReportReadsTheTriggerInputNotTheBilledAggregate(t *testing.T) {
	sess := NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: strings.Repeat("x", 400)})
	a := New(&fakeProvider{reply: "unused"}, tool.NewRegistry(), sess, Options{
		ContextWindow: 100_000, RecentKeep: 2,
	}, event.Discard)

	a.sess.output.lastUsage.Store(&provider.Usage{
		PromptTokens:        363_000, // three recovery attempts, billed
		ContextPromptTokens: 122_000, // what the last request actually carried
		RequestCount:        3,
	})

	rep := a.ContextReport()
	if rep.LatestPrompt != a.ContextUsedTokens() {
		t.Fatalf("report reading %d differs from the trigger input %d", rep.LatestPrompt, a.ContextUsedTokens())
	}
	if rep.LatestPrompt >= 122_000 {
		t.Fatalf("report reading %d leaked billed/last-request usage; want the next-request view estimate", rep.LatestPrompt)
	}
}

// tl1726: one window per session. The gauge window must equal the trigger's
// effective window (destination config min learned), so a hot model switch
// cannot leave slash /context, the serve gauge, and the compaction trigger
// each dividing by a different number.
func TestContextWindowMatchesTheTriggerDenominator(t *testing.T) {
	a := New(&fakeProvider{reply: "unused"}, tool.NewRegistry(), NewSession("sys"), Options{
		ContextWindow: 200_000, CompactRatio: 0.85, RecentKeep: 2,
	}, event.Discard)

	if got := a.ContextWindow(); got != a.effectiveContextWindow() {
		t.Fatalf("ContextWindow = %d, want the effective window %d", got, a.effectiveContextWindow())
	}
	// A smaller learned window (the hot-model-switch shape) must flow into the
	// gauge window AND stay the trigger's denominator.
	a.learnContextBudget(120_000, 4096, false)
	want := a.effectiveContextWindow()
	if want != 120_000 {
		t.Fatalf("effective window = %d, want the learned 120000", want)
	}
	if got := a.ContextWindow(); got != want {
		t.Fatalf("ContextWindow = %d, want the learned window %d", got, want)
	}
	if fold := a.compactTrigger(); fold != int(float64(want)*0.85) {
		t.Fatalf("compactTrigger = %d, want %d (0.85 of the gauge window)", fold, int(float64(want)*0.85))
	}
}

// The sole trigger must come from the same helper the decision uses.
func TestContextReportThresholdsMatchTheDecision(t *testing.T) {
	a := New(&fakeProvider{reply: "unused"}, tool.NewRegistry(), NewSession("sys"), Options{
		ContextWindow: 200_000, CompactRatio: 0.85, RecentKeep: 2,
	}, event.Discard)

	rep := a.ContextReport()
	fold := a.compactTrigger()
	if rep.FoldThreshold != fold {
		t.Errorf("report FoldThreshold %d differs from compactTrigger %d", rep.FoldThreshold, fold)
	}
	if rep.SoftThreshold != 0 || rep.SnipThreshold != 0 || rep.ForceThreshold != 0 {
		t.Errorf("legacy multi-threshold fields should stay zero: soft=%d snip=%d force=%d",
			rep.SoftThreshold, rep.SnipThreshold, rep.ForceThreshold)
	}
}

// A zero window disables maintenance; the thresholds then mean nothing and must
// not be presented as if they did.
func TestContextReportLeavesThresholdsZeroWhenDisabled(t *testing.T) {
	a := New(&fakeProvider{reply: "unused"}, tool.NewRegistry(), NewSession("sys"), Options{
		ContextWindow: 0, RecentKeep: 2,
	}, event.Discard)

	rep := a.ContextReport()
	if rep.Window != 0 || rep.FoldThreshold != 0 || rep.ForceThreshold != 0 {
		t.Errorf("disabled maintenance reported thresholds: %+v", rep)
	}
}
