package config

import (
	"strings"
	"testing"
)

// Task 394: the batch-context injection dial rides the autopilot block —
// off (the default) writes nothing, the level renders through the normalized
// accessor, and the plan path renders only while set.

func TestAutopilotBatchContextDialRendersThroughNormalizedLevel(t *testing.T) {
	c := &Config{}
	c.Desktop.Autopilot = true
	if err := c.SetExperimentalAutopilotBatchContext("minimal"); err != nil {
		t.Fatal(err)
	}
	out := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(out, `experimental_autopilot_batch_context = "minimal"`) {
		t.Fatalf("minimal dial must reach the config file\n---\n%s", out)
	}

	if err := c.SetExperimentalAutopilotBatchContext("full"); err != nil {
		t.Fatal(err)
	}
	out = RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(out, `experimental_autopilot_batch_context = "full"`) {
		t.Fatalf("full dial must reach the config file\n---\n%s", out)
	}
}

func TestAutopilotBatchContextOffAndUntouchedStayOutOfTheConfig(t *testing.T) {
	// 铁律 2: a user who never touched the dial keeps a byte-identical config —
	// both the absent and the explicit-off state render nothing.
	for _, level := range []string{"", "off"} {
		c := &Config{}
		c.Desktop.Autopilot = true
		if level != "" {
			if err := c.SetExperimentalAutopilotBatchContext(level); err != nil {
				t.Fatal(err)
			}
		}
		out := RenderTOMLForScope(c, RenderScopeUser)
		if strings.Contains(out, "experimental_autopilot_batch_context") {
			t.Fatalf("level %q must not render the key\n---\n%s", level, out)
		}
		if strings.Contains(out, "autopilot_batch_plan") {
			t.Fatalf("level %q with no plan must not render the plan key\n---\n%s", level, out)
		}
	}
}

func TestAutopilotBatchPlanRendersOnlyWhileSet(t *testing.T) {
	c := &Config{}
	c.Desktop.Autopilot = true
	if err := c.SetExperimentalAutopilotBatchContext("minimal"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetAutopilotBatchPlan("tasks/batch7-plan.md"); err != nil {
		t.Fatal(err)
	}
	out := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(out, `autopilot_batch_plan = "tasks/batch7-plan.md"`) {
		t.Fatalf("set plan path must reach the config file\n---\n%s", out)
	}

	if err := c.SetAutopilotBatchPlan("   "); err != nil {
		t.Fatal(err)
	}
	if c.Desktop.AutopilotBatchPlan != "" {
		t.Fatalf("whitespace-only path must clear the override, got %q", c.Desktop.AutopilotBatchPlan)
	}
	out = RenderTOMLForScope(c, RenderScopeUser)
	if strings.Contains(out, "autopilot_batch_plan") {
		t.Fatalf("cleared plan path must not render\n---\n%s", out)
	}
}

func TestAutopilotBatchContextLevelNormalization(t *testing.T) {
	// Unknown values and a nil config read as off — a typo must never
	// silently turn injection on, and never silently widen minimal to full.
	cases := map[string]string{
		"":         "",
		"off":      "",
		"minimal":  "minimal",
		"full":     "full",
		"MINIMAL":  "",
		"max":      "",
		" minimal": "",
	}
	for in, want := range cases {
		c := &Config{}
		c.Desktop.ExperimentalAutopilotBatchContext = in
		if got := c.AutopilotBatchContextLevel(); got != want {
			t.Fatalf("level(%q) = %q, want %q", in, got, want)
		}
	}
	var nilConfig *Config
	if got := nilConfig.AutopilotBatchContextLevel(); got != "" {
		t.Fatalf("nil config must read off, got %q", got)
	}
}

func TestSetAutopilotBatchContextRejectsUnknownValues(t *testing.T) {
	c := &Config{}
	if err := c.SetExperimentalAutopilotBatchContext("aggressive"); err == nil {
		t.Fatal("unknown level must be refused so a typo cannot enable injection")
	}
	if c.Desktop.ExperimentalAutopilotBatchContext != "" {
		t.Fatalf("refused level must not be stored, got %q", c.Desktop.ExperimentalAutopilotBatchContext)
	}
	for _, ok := range []string{"off", "minimal", "full"} {
		if err := c.SetExperimentalAutopilotBatchContext(ok); err != nil {
			t.Fatalf("level %q must be accepted: %v", ok, err)
		}
	}
}
