package config

import (
	"strings"
	"testing"
)

// Task 172: the feedback touchpoint dial must round-trip through the render
// table (the 81/123 lesson: an unlisted key is dropped on every save and the
// switch springs back off), and the parent experimental_feedback switch must
// win at read time.
func TestFeedbackNudgeRoundTripAndParentSwitch(t *testing.T) {
	c := &Config{}
	if c.FeedbackNudgeEnabled() {
		t.Fatal("zero config must not enable the feedback nudge")
	}
	c.Desktop.ExperimentalFeedbackNudge = true
	if c.FeedbackNudgeEnabled() {
		t.Fatal("sub-switch alone must not enable the nudge (parent wins)")
	}
	if err := c.SetExperimentalFeedbackNudge(true); err != nil {
		t.Fatalf("SetExperimentalFeedbackNudge: %v", err)
	}
	if !c.Desktop.ExperimentalFeedbackNudge {
		t.Fatal("setter did not store the sub-switch")
	}
	if err := c.SetExperimentalFeedback(true); err != nil {
		t.Fatalf("SetExperimentalFeedback: %v", err)
	}
	if !c.FeedbackNudgeEnabled() {
		t.Fatal("parent + sub switch must enable the nudge")
	}
	c.Desktop.ExperimentalFeedbackNudge = false
	if c.FeedbackNudgeEnabled() {
		t.Fatal("parent without the sub-switch must not enable the nudge")
	}

	out := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(out, "experimental_feedback_nudge = false") {
		t.Fatalf("rendered user config is missing the nudge key (off)\n---\n%s", out)
	}
	c.Desktop.ExperimentalFeedbackNudge = true
	out = RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(out, "experimental_feedback_nudge = true") {
		t.Fatalf("rendered user config is missing the nudge key (on)\n---\n%s", out)
	}
}
