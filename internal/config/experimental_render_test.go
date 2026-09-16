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
	c.Desktop.ExperimentalSplitView = true
	c.Desktop.ExperimentalFeedback = true
	c.Desktop.ExperimentalParallelFullAccess = true
	c.Desktop.ExperimentalPathRules = true

	out := RenderTOMLForScope(c, RenderScopeUser)
	for _, want := range []string{
		"experimental_restart_update = true",
		"experimental_session_monitor = true",
		"experimental_split_view = true",
		"experimental_feedback = true",
		"experimental_parallel_full_access = true",
		"experimental_path_rules = true",
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
		"experimental_split_view = false",
		"experimental_feedback = false",
		"experimental_parallel_full_access = false",
		"experimental_path_rules = false",
		"experimental_dream = false",
		"trace_as_state = false",
		"stalled_intent_nudge = false",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("a disabled switch should still render %q\n---\n%s", want, out)
		}
	}
}

func TestExperimentalDreamRoundTripThroughRender(t *testing.T) {
	c := &Config{}
	c.Agent.ExperimentalDream = true
	out := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(out, "experimental_dream = true") {
		t.Fatalf("rendered user config is missing experimental_dream = true\n---\n%s", out)
	}
}

func TestStalledIntentNudgeRoundTripThroughRender(t *testing.T) {
	c := &Config{}
	c.Agent.StalledIntentNudge = true
	c.Agent.StalledIntentNudgeLimit = 2
	out := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(out, "stalled_intent_nudge = true") {
		t.Fatalf("rendered user config is missing stalled_intent_nudge = true\n---\n%s", out)
	}
	if !strings.Contains(out, "stalled_intent_nudge_limit = 2") {
		t.Fatalf("rendered user config is missing stalled_intent_nudge_limit = 2\n---\n%s", out)
	}
}

func TestTraceAsStateRoundTripThroughRender(t *testing.T) {
	c := &Config{}
	c.Agent.TraceAsState = true
	out := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(out, "trace_as_state = true") {
		t.Fatalf("rendered user config is missing trace_as_state = true\n---\n%s", out)
	}
}
