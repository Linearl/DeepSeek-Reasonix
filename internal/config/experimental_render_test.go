package config

import (
	"strings"
	"testing"
)

// Same failure mode as the autopilot keys next door: the user-config renderer writes
// a fixed key set, so a preference it does not list is dropped on save even though
// Settings reported success - the switch then reads back off and cannot be turned on.
// Both experiment switches shipped that way until 2026-09-15 (task 81's
// restart-and-update and task 123's session monitor never reached config.toml).
func TestExperimentalSwitchesRoundTripThroughRender(t *testing.T) {
	c := &Config{}
	c.Desktop.ExperimentalRestartUpdate = true
	c.Desktop.ExperimentalSessionMonitor = true

	out := RenderTOMLForScope(c, RenderScopeUser)
	for _, want := range []string{
		"experimental_restart_update = true",
		"experimental_session_monitor = true",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered user config is missing %q\n---\n%s", want, out)
		}
	}
}

// Unlike autopilot (which stays out of an untouched config), these two are rendered
// unconditionally: turning one back off has to be recorded, otherwise the file keeps
// the stale true and the switch springs back on at the next load.
func TestExperimentalSwitchesRenderWhenOff(t *testing.T) {
	c := &Config{}
	out := RenderTOMLForScope(c, RenderScopeUser)
	for _, want := range []string{
		"experimental_restart_update = false",
		"experimental_session_monitor = false",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("a disabled switch should still render %q\n---\n%s", want, out)
		}
	}
}
