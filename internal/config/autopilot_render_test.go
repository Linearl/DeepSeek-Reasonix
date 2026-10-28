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
	} {
		if strings.Contains(out, key) {
			t.Fatalf("untouched config should not write the autopilot key %q\n---\n%s", key, out)
		}
	}
}

// Task 477: the ask-timeout sub-option rides the same fixed-key autopilot
// block — a key the renderer drops is a switch that flips itself back off.
func TestAutopilotAskTimeoutRoundTripsThroughRender(t *testing.T) {
	c := &Config{}
	c.Desktop.ExperimentalAutopilotAskTimeout = true
	c.Desktop.AutopilotAskWaitSeconds = 90

	out := RenderTOMLForScope(c, RenderScopeUser)
	for _, want := range []string{
		"experimental_autopilot_ask_timeout = true",
		"autopilot_ask_wait_seconds = 90",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered user config is missing %q\n---\n%s", want, out)
		}
	}

	// The off state must be recorded too, and a configured dial survives the
	// switch going off.
	off := &Config{}
	off.Desktop.ExperimentalAutopilotAskTimeout = false
	off.Desktop.AutopilotAskWaitSeconds = 90
	out = RenderTOMLForScope(off, RenderScopeUser)
	if !strings.Contains(out, "experimental_autopilot_ask_timeout = false") {
		t.Fatalf("disabled ask timeout should still render its flag\n---\n%s", out)
	}
	if !strings.Contains(out, "autopilot_ask_wait_seconds = 90") {
		t.Fatalf("the wait dial should survive while it is set\n---\n%s", out)
	}
}

func TestAutopilotAskTimeoutUnsetStaysOutOfTheConfig(t *testing.T) {
	c := &Config{}
	out := RenderTOMLForScope(c, RenderScopeUser)
	for _, key := range []string{
		"experimental_autopilot_ask_timeout",
		"autopilot_ask_wait_seconds",
	} {
		if strings.Contains(out, key) {
			t.Fatalf("untouched config should not write the ask-timeout key %q\n---\n%s", key, out)
		}
	}
}
