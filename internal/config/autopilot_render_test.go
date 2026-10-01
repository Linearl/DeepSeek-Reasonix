package config

import (
	"strings"
	"testing"
)

// The user-config renderer writes a fixed set of keys, so a preference that is
// not listed there can be set in Settings and still vanish on save - which is
// exactly how the Autopilot switch behaved: it flipped back to off because the
// flag never reached the file. These assertions pin the keys to the renderer.
func TestAutopilotPreferencesRoundTripThroughRender(t *testing.T) {
	c := &Config{}
	c.Desktop.Autopilot = true
	c.Desktop.AutopilotMaxRuntime = "8h"
	c.Desktop.AutopilotApprovalGrace = "20s"

	out := RenderTOMLForScope(c, RenderScopeUser)
	for _, want := range []string{
		"autopilot = true",
		`autopilot_max_runtime = "8h"`,
		`autopilot_approval_grace = "20s"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered user config is missing %q\n---\n%s", want, out)
		}
	}
}

func TestAutopilotDisabledStillRendersItsFlag(t *testing.T) {
	// Turning the switch off must be recorded too: a bound left behind would
	// otherwise keep the keys alive with a stale limit.
	c := &Config{}
	c.Desktop.Autopilot = false
	c.Desktop.AutopilotMaxRuntime = "8h"

	out := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(out, "autopilot = false") {
		t.Fatalf("disabled autopilot should still render its flag\n---\n%s", out)
	}
	if !strings.Contains(out, `autopilot_max_runtime = "8h"`) {
		t.Fatalf("the bound should survive while it is set\n---\n%s", out)
	}
}

func TestAutopilotUnsetStaysOutOfTheConfig(t *testing.T) {
	// A config nobody touched should not grow an unattended-run section.
	// Key-level check, not a substring scan: the task-254 resume dial's value
	// "goal_autopilot" legitimately contains the word without being an
	// autopilot preference key.
	c := &Config{}
	out := RenderTOMLForScope(c, RenderScopeUser)
	for _, key := range []string{
		"\nautopilot = ",
		"autopilot_max_runtime",
		"autopilot_approval_grace",
		"autopilot_guard_interval",
		"autopilot_guard_quiescent",
	} {
		if strings.Contains(out, key) {
			t.Fatalf("untouched config should not write the autopilot key %q\n---\n%s", key, out)
		}
	}
}

// Task 326: both guard dials live in the autopilot block of the renderer. A key
// the fixed-key-set renderer does not emit is dropped on save, which is exactly
// how the autopilot switch used to flip straight back to off — so the dials are
// pinned here rather than discovered in production.
func TestAutopilotGuardDialsRoundTripThroughRender(t *testing.T) {
	c := &Config{}
	c.Desktop.AutopilotGuardInterval = 7
	c.Desktop.AutopilotGuardQuiescent = "destroy"

	out := RenderTOMLForScope(c, RenderScopeUser)
	for _, want := range []string{
		"autopilot_guard_interval = 7",
		`autopilot_guard_quiescent = "destroy"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered user config is missing %q\n---\n%s", want, out)
		}
	}

	// The defaults read back through the accessors, not through raw field
	// inspection, so an empty dial keeps its documented meaning.
	empty := &Config{}
	if got := empty.AutopilotGuardIntervalMinutes(); got != AutopilotGuardDefaultIntervalMinutes {
		t.Fatalf("default guard interval = %d, want %d", got, AutopilotGuardDefaultIntervalMinutes)
	}
	if got := empty.AutopilotGuardQuiescentPolicy(); got != "disable" {
		t.Fatalf("default self-close policy = %q, want disable", got)
	}
	for _, raw := range []string{"disable", "standby", "destroy"} {
		empty.Desktop.AutopilotGuardQuiescent = raw
		if got := empty.AutopilotGuardQuiescentPolicy(); got != raw {
			t.Fatalf("policy %q read back as %q", raw, got)
		}
	}
}
