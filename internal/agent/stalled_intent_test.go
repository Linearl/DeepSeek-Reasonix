package agent

import (
	"context"
	"testing"
)

// #6 P0-c: the model announcing the next step instead of taking it.
func TestShouldNudgeStalledIntent(t *testing.T) {
	for _, text := range []string{
		"I'll now run the tests to confirm.",
		"Next, I will check the failing package.",
		"Let me now verify the build.",
		"About to patch the handler.",
	} {
		if !shouldNudgeStalledIntent(text) {
			t.Errorf("%q announces the next step and must be nudged", text)
		}
	}
	for _, text := range []string{
		"",
		"Done: the tests pass and the build is clean.",
		"The fix is applied; here is the diff.",
	} {
		if shouldNudgeStalledIntent(text) {
			t.Errorf("%q is a final answer and must not be nudged", text)
		}
	}
}

// Task 117: normalizeStalledIntentNudgeLimit clamps to [1, hardCap].
func TestNormalizeStalledIntentNudgeLimit(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{0, maxStalledIntentNudges},
		{-1, maxStalledIntentNudges},
		{1, 1},
		{2, 2},
		{3, 3},
		{4, maxStalledIntentNudgeHardCap},
		{100, maxStalledIntentNudgeHardCap},
	}
	for _, c := range cases {
		if got := normalizeStalledIntentNudgeLimit(c.in); got != c.want {
			t.Errorf("normalizeStalledIntentNudgeLimit(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// Task 117: stalledIntentNudgeEnabled fires for ContinuationExplicitFlow OR
// the config opt-in; neither alone is enough for a plain disabled agent.
func TestStalledIntentNudgeEnabled(t *testing.T) {
	// Default: disabled.
	a := &Agent{}
	if a.stalledIntentNudgeEnabled(context.Background()) {
		t.Error("default agent must not enable stalled-intent nudge")
	}
	// Config opt-in.
	a2 := &Agent{stalledIntentNudge: true}
	if !a2.stalledIntentNudgeEnabled(context.Background()) {
		t.Error("config opt-in must enable stalled-intent nudge")
	}
	// ContinuationExplicitFlow via context.
	a3 := &Agent{}
	ctx := WithContinuationPolicy(context.Background(), ContinuationExplicitFlow)
	if !a3.stalledIntentNudgeEnabled(ctx) {
		t.Error("ContinuationExplicitFlow context must enable stalled-intent nudge")
	}
}

// Task 117: stalledIntentNudgeCap returns the configured limit or the default.
func TestStalledIntentNudgeCap(t *testing.T) {
	a := &Agent{}
	if got := a.stalledIntentNudgeCap(); got != maxStalledIntentNudges {
		t.Errorf("default cap = %d, want %d", got, maxStalledIntentNudges)
	}
	a2 := &Agent{stalledIntentNudgeLimit: 2}
	if got := a2.stalledIntentNudgeCap(); got != 2 {
		t.Errorf("configured cap = %d, want 2", got)
	}
	// Hard cap is enforced at construction time via normalize, but the getter
	// also guards against a zero/limit set after construction.
	a3 := &Agent{stalledIntentNudgeLimit: 0}
	if got := a3.stalledIntentNudgeCap(); got != maxStalledIntentNudges {
		t.Errorf("zero limit cap = %d, want %d", got, maxStalledIntentNudges)
	}
}
