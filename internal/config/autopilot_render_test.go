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
	c := &Config{}
	out := RenderTOMLForScope(c, RenderScopeUser)
	if strings.Contains(out, "autopilot") {
		t.Fatalf("untouched config should not mention autopilot\n---\n%s", out)
	}
}
