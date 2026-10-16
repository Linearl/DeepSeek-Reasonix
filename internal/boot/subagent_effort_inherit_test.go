package boot

import (
	"context"
	"strings"
	"sync"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/skill"
)

func undeclaredEffortConfig() *config.Config {
	cfg := config.Default()
	cfg.DefaultModel = "custom/some-model"
	cfg.Agent.PlannerModel = ""
	cfg.Providers = []config.ProviderEntry{{Name: "custom", Kind: "openai", BaseURL: "https://example.com/v1", Model: "some-model"}}
	cfg.Agent.SubagentEffort = "max"
	return cfg
}

// Regression for upstream #10503 (task 901): agent.subagent_effort is a
// cross-model default. When agent.subagent_model is unset, subagents follow the
// execution model — a persisted level that model cannot take must be dropped
// (with a notice) instead of reaching every subagent dispatch after a model
// switch. The persisted setting stays untouched so it applies again on a model
// that supports it.
func TestInheritedGlobalSubagentEffortYieldsToExecutionModel(t *testing.T) {
	cfg := undeclaredEffortConfig()
	entry, ok := cfg.ResolveModel(cfg.DefaultModel)
	if !ok {
		t.Fatal("execution model does not resolve")
	}
	if effort, dropped := inheritedSubagentEffort(cfg, entry); effort != "" || dropped != "max" {
		t.Fatalf("inheritedSubagentEffort = (%q, %q), want (\"\", \"max\")", effort, dropped)
	}
	if cfg.Agent.SubagentEffort != "max" {
		t.Fatal("the decision rewrote the configured agent.subagent_effort")
	}
}

// The decision is one-sided: a level the model cannot take drops, a level it
// declares keeps — so switching back to a supporting model restores the
// persisted selection without the user re-saving it.
func TestInheritedGlobalSubagentEffortKeptWhenModelDeclaresIt(t *testing.T) {
	cfg := undeclaredEffortConfig()
	cfg.Providers[0].SupportedEfforts = []string{"high", "max"}
	entry, ok := cfg.ResolveModel(cfg.DefaultModel)
	if !ok {
		t.Fatal("execution model does not resolve")
	}
	if effort, dropped := inheritedSubagentEffort(cfg, entry); effort != "max" || dropped != "" {
		t.Fatalf("inheritedSubagentEffort = (%q, %q), want (\"max\", \"\")", effort, dropped)
	}
}

// Strict companions stay strict (upstream #10783): agent.subagent_efforts.<name>
// is a per-profile override and never passes through the inherited decision —
// subagentEffortRef prefers it over any defaultEffort. An effort paired with an
// explicit agent.subagent_model is a model-specific selection, so the raw value
// passes through and dispatch validates it against the paired model.
func TestExplicitSubagentEffortsStayStrict(t *testing.T) {
	cfg := undeclaredEffortConfig()
	cfg.Agent.SubagentEfforts = map[string]string{"task": "max"}

	if got := subagentEffortRef(cfg, skill.Skill{Name: "task", RunAs: skill.RunSubagent}, ""); got != "max" {
		t.Fatalf("per-profile effort = %q, want max (stays strict, bypasses the inherited drop)", got)
	}

	cfg = undeclaredEffortConfig()
	cfg.Agent.SubagentModel = "custom/some-model"
	entry, ok := cfg.ResolveModel(cfg.DefaultModel)
	if !ok {
		t.Fatal("execution model does not resolve")
	}
	if effort, dropped := inheritedSubagentEffort(cfg, entry); effort != "max" || dropped != "" {
		t.Fatalf("effort paired with agent.subagent_model must stay strict, got (%q, %q)", effort, dropped)
	}
}

// The drop decision mirrors dispatch: a level the model does declare is
// validated through the same NormalizeEffort + capability gates that
// resolveSubagentProvider applies, so a kept value is a sendable value.
func TestInheritedSubagentEffortPassesDispatchGates(t *testing.T) {
	cfg := undeclaredEffortConfig()
	cfg.Providers[0].SupportedEfforts = []string{"high", "max"}
	entry, ok := cfg.ResolveModel(cfg.DefaultModel)
	if !ok {
		t.Fatal("execution model does not resolve")
	}
	effort, dropped := inheritedSubagentEffort(cfg, entry)
	if effort != "max" || dropped != "" {
		t.Fatalf("inheritedSubagentEffort = (%q, %q), want (\"max\", \"\")", effort, dropped)
	}
	if _, err := config.NormalizeEffort(entry, effort); err != nil {
		t.Fatalf("kept effort %q would be rejected by dispatch NormalizeEffort: %v", effort, err)
	}
	if err := config.ReasoningCapabilityForEntry(entry).Validate(entry.Model, effort); err != nil {
		t.Fatalf("kept effort %q would be rejected by dispatch Validate: %v", effort, err)
	}
}

type effortNoticeCollector struct {
	mu    sync.Mutex
	texts []string
}

func (c *effortNoticeCollector) Emit(e event.Event) {
	if e.Kind != event.Notice {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.texts = append(c.texts, e.Text+" "+e.Detail)
}

// Behavioural acceptance (task 901 b): a legacy persisted global subagent
// effort must not break the model it was inherited by. Before upstream #10783
// was ported, Build fed the raw value into the task tool and the skill
// runners, so every task/subagent dispatch after switching to a weaker model
// failed with an effort-validation error; the value now drops with a notice and
// the task runs.
func TestLegacyGlobalSubagentEffortDoesNotBreakSwitchedModel(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)

	registerHeadlessTaskTestProvider()
	prov := &headlessTaskTestProvider{}
	setHeadlessTaskTestProvider(t, prov)
	writeFile(t, dir, "reasonix.toml", `
default_model = "test-model"

[agent]
subagent_effort = "max"

[[providers]]
name = "test-model"
kind = "boot-headless-test"
model = "x"
`)

	sink := &effortNoticeCollector{}
	ctrl, err := Build(context.Background(), Options{Sink: sink})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer ctrl.Close()

	if err := ctrl.Run(context.Background(), "use a task subagent"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var toolContent strings.Builder
	for _, msg := range ctrl.History() {
		if msg.Role == provider.RoleTool {
			toolContent.WriteString("\n" + msg.Content)
		}
	}
	if !strings.Contains(toolContent.String(), "subagent answer") {
		t.Fatalf("task subagent failed under the legacy global effort: %s", toolContent.String())
	}

	sink.mu.Lock()
	defer sink.mu.Unlock()
	named := false
	for _, text := range sink.texts {
		if strings.Contains(text, "agent.subagent_effort") && strings.Contains(text, `"max"`) {
			named = true
		}
	}
	if !named {
		t.Fatalf("no notice names the dropped agent.subagent_effort: %q", sink.texts)
	}
}
