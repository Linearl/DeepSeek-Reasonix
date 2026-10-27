package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// baselineFixture builds an agent whose session mirrors the folded-shape
// fixtures and whose trigger sits at 50% of a 5K window (fold = 2500, hard =
// 5000-256). The visible transcript estimates at roughly 1K tokens under the
// cold ~4-chars-per-token fallback — far below the fold — so any Prepare that
// goes past the fast path must have judged on the real-usage baseline.
func baselineFixture(t *testing.T) (*Agent, *failingSummaryProvider) {
	t.Helper()
	prov := &failingSummaryProvider{}
	a := agentOverForceWindow(t, prov, foldableSessionOverForce(2), 5000)
	return a, prov
}

// storeBaseline writes a trusted anchor the way storeLatestRequestUsage would
// after a real sampling response: paired shape + provider-counted prompt.
func storeBaseline(a *Agent, prompt, completion int, coveredChars int64) {
	shape := requestCalibrationShape{
		requestChars: coveredChars,
		compactChars: coveredChars,
	}
	a.sess.output.activeReqShape.Store(&shape)
	a.storeContextBaseline(&provider.Usage{
		PromptTokens:            prompt,
		CompletionTokens:        completion,
		ContextPromptTokens:     prompt,
		ContextCompletionTokens: completion,
	})
}

// 验收①: with a trusted baseline the trigger judges on the provider's real
// count. The estimate (~1K) sits below the fold (2500); the real count (3K)
// sits above it. Prepare must fire the summarizer — and the identical session
// without a baseline must not.
func TestTriggerJudgesOnRealUsageBaseline(t *testing.T) {
	a, prov := baselineFixture(t)
	// Controls the guard arithmetic: baseline coveredChars must not exceed the
	// live view, or the shrink guard would (correctly) reject the anchor.
	visible := a.modelVisibleMessages()
	covered := int64(charsOfMessages(visible)) - 60
	if covered <= 0 {
		t.Fatalf("fixture view too small: %d chars", covered)
	}
	storeBaseline(a, 3000, 0, covered)

	if obs, ok := a.observedContextTokens(visible); !ok {
		t.Fatalf("observedContextTokens rejected a trusted baseline")
	} else if obs < a.compactTrigger() {
		t.Fatalf("observed = %d, want >= fold %d — fixture no longer exercises the trigger", obs, a.compactTrigger())
	}

	if _, err := a.contextManager().Prepare(context.Background(), ContextPreparePolicy{Trigger: CompactionTriggerPressure}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got := prov.calls; got < 1 {
		t.Fatalf("Prepare with real usage above the fold issued %d summary calls, want >= 1", got)
	}
}

// 验收②: without a trusted baseline the trigger keeps today's estimate path —
// byte-for-byte the old behavior. Same session, same fixture: no baseline, no
// summary call, fast-path early return.
func TestTriggerKeepsEstimateWithoutBaseline(t *testing.T) {
	a, prov := baselineFixture(t)
	if _, ok := a.observedContextTokens(a.modelVisibleMessages()); ok {
		t.Fatalf("observedContextTokens accepted a session with no baseline")
	}
	if _, err := a.contextManager().Prepare(context.Background(), ContextPreparePolicy{Trigger: CompactionTriggerPressure}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got := prov.calls; got != 0 {
		t.Fatalf("estimate-only session issued %d summary calls, want 0 (below fold)", got)
	}
}

// 验收②（续）: every untrusted shape must leave the baseline unset so the
// fallback stays exactly the pre-1183 behavior.
func TestBaselineRejectsUntrustedUsage(t *testing.T) {
	cases := []struct {
		name         string
		usage        *provider.Usage
		shapeChars   int64
		storeNoShape bool
	}{
		{"reconstructed estimate", &provider.Usage{PromptTokens: 500, Estimated: true}, 1000, false},
		{"unknown usage", &provider.Usage{PromptTokens: 500, Unknown: true}, 1000, false},
		{"zero prompt", &provider.Usage{CompletionTokens: 500}, 1000, false},
		{"no request shape", &provider.Usage{PromptTokens: 500}, 0, true},
		{"zero covered chars", &provider.Usage{PromptTokens: 500}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := baselineFixture(t)
			if !tc.storeNoShape {
				a.sess.output.activeReqShape.Store(&requestCalibrationShape{requestChars: tc.shapeChars, compactChars: tc.shapeChars})
			}
			a.storeContextBaseline(tc.usage)
			if a.sess.output.contextBaseline.Load() != nil {
				t.Fatalf("%s: baseline was stored", tc.name)
			}
			if _, ok := a.observedContextTokens(a.modelVisibleMessages()); ok {
				t.Fatalf("%s: observedContextTokens accepted untrusted state", tc.name)
			}
		})
	}
}

// The shrink guard is the convergence guarantee: once a fold lands (or a
// rewind rewrites history) the view is smaller than what the baseline
// measured. Replaying the stale count would re-add the folded prefix and the
// session would compact forever.
func TestObservedTokensRejectShrunkenView(t *testing.T) {
	a, _ := baselineFixture(t)
	visible := a.modelVisibleMessages()
	storeBaseline(a, 3000, 0, int64(charsOfMessages(visible))+1)
	if _, ok := a.observedContextTokens(visible); ok {
		t.Fatalf("observedContextTokens accepted a view smaller than its baseline")
	}
}

// Hot model switch: the anchor follows the request destination (same reset
// semantics as the 1190 EWMA factor). A stale provider's count must not price
// the new one, so a name mismatch falls back to the estimate.
func TestObservedTokensRekeysOnProviderSwitch(t *testing.T) {
	a, _ := baselineFixture(t)
	visible := a.modelVisibleMessages()
	storeBaseline(a, 3000, 0, int64(charsOfMessages(visible))-10)
	if _, ok := a.observedContextTokens(visible); !ok {
		t.Fatalf("precondition: same-destination read must accept the anchor")
	}
	// Switch the destination; a.svc.prov is named by the fixture provider.
	// Re-key through a differently named provider.
	a.svc.prov = &namedOutputBudgetProvider{name: "other-provider"}
	if _, ok := a.observedContextTokens(visible); ok {
		t.Fatalf("observedContextTokens priced the new provider with the old one's count")
	}
}

// The anchor is real data: no provider-level correction may stack on top of
// it. With the 1190 EWMA factor pinned at its clamp, the observed reading is
// unchanged — while the cold estimator on the same session does carry the
// factor (that split is 1190's own contract, pinned there; here we pin only
// that the trigger anchor stays clean).
func TestObservedTokensIndependentOfPromptCorrectionEWMA(t *testing.T) {
	a, _ := baselineFixture(t)
	visible := a.modelVisibleMessages()
	storeBaseline(a, 3000, 120, int64(charsOfMessages(visible))-10)
	clean, ok := a.observedContextTokens(visible)
	if !ok {
		t.Fatalf("precondition: trusted baseline rejected")
	}
	promptCorrectionCache.Lock()
	promptCorrectionCache.entries["failing-summary"] = promptCorrectionEntry{factor: promptCorrectionFactorMax, expiresAt: time.Now().Add(promptCorrectionTTL)}
	promptCorrectionCache.Unlock()
	defer func() {
		promptCorrectionCache.Lock()
		delete(promptCorrectionCache.entries, "")
		promptCorrectionCache.Unlock()
	}()
	after, ok := a.observedContextTokens(visible)
	if !ok || after != clean {
		t.Fatalf("EWMA factor leaked into the real-usage anchor: %d -> %d (ok=%v)", clean, after, ok)
	}
}

// 验收④: the threshold line is orthogonal to the measurement caliber. The
// baseline changes neither compactTrigger() (the fold line the 978 experiment
// reshapes) nor the observed reading under the early-fold experiment toggle —
// 978 moves the threshold, 1183 moves the measurement; the comparison
// `est >= fold` composes both without either touching the other.
func TestTriggerLineOrthogonalToBaselineAnd978(t *testing.T) {
	a, _ := baselineFixture(t)
	visible := a.modelVisibleMessages()
	storeBaseline(a, 3000, 0, int64(charsOfMessages(visible))-10)

	lineBefore := a.compactTrigger()
	obs, ok := a.observedContextTokens(visible)
	if !ok {
		t.Fatalf("precondition: trusted baseline rejected")
	}

	// Clearing the baseline must not move the fold line.
	a.sess.output.contextBaseline.Store(nil)
	if line := a.compactTrigger(); line != lineBefore {
		t.Fatalf("baseline state moved the fold line: %d -> %d", lineBefore, line)
	}
	// Restore the anchor for the toggle half below.
	storeBaseline(a, 3000, 0, int64(charsOfMessages(visible))-10)

	// The 978 experiment toggle reshapes only the fold line, never the
	// measurement: same observed reading on both sides of the switch.
	saved := earlyFoldEnabled
	defer func() { earlyFoldEnabled = saved }()
	earlyFoldEnabled = true
	if line := a.compactTrigger(); line != lineBefore {
		// earlyFoldEnabled alone does not move the line (it needs an active
		// fallback); pin that invariant so this test cannot silently rot into
		// testing a different mechanism.
		t.Fatalf("earlyFoldEnabled without a fallback moved the fold line: %d -> %d", lineBefore, line)
	}
	if obs2, ok2 := a.observedContextTokens(visible); !ok2 || obs2 != obs {
		t.Fatalf("early-fold toggle moved the measurement: %d (ok=%v) -> %d (ok=%v)", obs, ok, obs2, ok2)
	}
}

// The baseline survives SetSession-style resets only as far as lastUsage does:
// reset() must clear it (a replaced transcript must not inherit the old
// transcript's anchor), while the paired shape stays untouched.
func TestBaselineResetWithTranscript(t *testing.T) {
	a, _ := baselineFixture(t)
	visible := a.modelVisibleMessages()
	storeBaseline(a, 3000, 0, int64(charsOfMessages(visible))-10)
	if a.sess.output.contextBaseline.Load() == nil {
		t.Fatalf("precondition: baseline missing")
	}
	a.sess.output.reset()
	if a.sess.output.contextBaseline.Load() != nil {
		t.Fatalf("reset() left a stale transcript baseline behind")
	}
}

// Delta pricing sanity: growth over the baseline is priced at the session's
// tokPerChar, completion joins the context, and the anchor dominates the sum.
func TestObservedTokensFormula(t *testing.T) {
	a, _ := baselineFixture(t)
	visible := a.modelVisibleMessages()
	baseChars := int64(charsOfMessages(visible))
	storeBaseline(a, 10_000, 500, baseChars)
	obs, ok := a.observedContextTokens(visible)
	if !ok {
		t.Fatalf("trusted baseline rejected")
	}
	if obs != 10_500 {
		t.Fatalf("observed = %d, want baseline+completion = 10500", obs)
	}
	// Append one user turn: delta chars price at tokPerChar (fallback 0.25 —
	// no calibration stored in this fixture). msgChars counts bare content
	// bytes, so the delta is exactly the appended text.
	grown := append(append([]provider.Message(nil), visible...), provider.Message{Role: provider.RoleUser, Content: strings.Repeat("x", 100)})
	want := 10_500 + int(float64(100)*fallbackTokPerChar)
	if obs, ok := a.observedContextTokens(grown); !ok || obs != want {
		t.Fatalf("grown observed = %d (ok=%v), want %d", obs, ok, want)
	}
}
