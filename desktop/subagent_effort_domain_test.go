package main

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/skill"
)

// Task 1151: the settings views expose the wire-legal effort domain of the
// model a subagent effort setting applies to — an explicit agent.subagent_model
// wins, else the main (default) model — so the row's menu always matches what
// dispatch accepts instead of a static preset list.

func writeSubagentDomainConfig(t *testing.T, extra string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	content := `
default_model = "mimo/mimo-v2.6-flash"

[desktop]
provider_access = ["mimo", "deepseek"]

[[providers]]
name = "mimo"
kind = "openai"
base_url = "https://api.xiaomimimo.com/v1"
models = ["mimo-v2.6-flash", "mimo-v2.5-pro"]
default = "mimo-v2.6-flash"
supported_efforts = ["low", "medium", "high"]

[[providers]]
name = "deepseek"
kind = "openai"
base_url = "https://api.deepseek.com"
api_key_env = "DEEPSEEK_API_KEY"
models = ["deepseek-v4-flash", "deepseek-v4-pro"]
default = "deepseek-v4-flash"
` + extra
	if err := os.WriteFile(config.UserConfigPath(), []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// Inherit chain: agent.subagent_model unset — the domain follows the main
// model (the declared MiMo vocabulary, max excluded up front), so the row
// never offers a level the boot path would drop with a warning.
func TestSettingsSubagentEffortDomainFollowsMainModel(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	writeSubagentDomainConfig(t, "")

	app := NewApp()
	view := app.Settings()
	want := []string{"auto", "low", "medium", "high"}
	if !reflect.DeepEqual(view.SubagentEffortLevels, want) {
		t.Fatalf("subagentEffortLevels = %v, want %v", view.SubagentEffortLevels, want)
	}
	if view.SubagentEffortAliasFold {
		t.Fatal("a config-declared vocabulary must not be alias-folded")
	}
	if !view.SubagentEffortApplies {
		t.Fatal("empty stored effort is auto and always applies")
	}

	// Switching the subagent model re-targets the domain: the deepseek entry
	// has no declared vocabulary, so the capability registry answers — the
	// honest max set including disabled.
	if err := app.SetSubagentModel("deepseek/deepseek-v4-pro"); err != nil {
		t.Fatalf("SetSubagentModel: %v", err)
	}
	view = app.Settings()
	wantDeepseek := []string{"auto", "disabled", "low", "high", "max"}
	if !reflect.DeepEqual(view.SubagentEffortLevels, wantDeepseek) {
		t.Fatalf("after switching subagent model: levels = %v, want %v", view.SubagentEffortLevels, wantDeepseek)
	}
}

// A persisted level the effective model cannot take is flagged: the field
// stays visible (raw value kept) but subagentEffortApplies=false so the UI can
// explain the boot drop instead of leaving a silent mismatch.
func TestSettingsSubagentEffortDomainFlagsDroppedLevel(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	writeSubagentDomainConfig(t, `
[agent]
subagent_effort = "max"
`)

	app := NewApp()
	view := app.Settings()
	if view.SubagentEffort != "max" {
		t.Fatalf("subagentEffort = %q, want the persisted max", view.SubagentEffort)
	}
	if view.SubagentEffortApplies {
		t.Fatal("max must be flagged as dropped for a model whose vocabulary tops at high")
	}
	if !slices.Contains(view.SubagentEffortLevels, "high") || slices.Contains(view.SubagentEffortLevels, "max") {
		t.Fatalf("levels = %v, want the declared menu without max", view.SubagentEffortLevels)
	}

	// An explicit subagent model with an honest max set makes the same
	// persisted level legal again — the flag flips without rewriting config.
	if err := app.SetSubagentModel("deepseek/deepseek-v4-pro"); err != nil {
		t.Fatalf("SetSubagentModel: %v", err)
	}
	view = app.Settings()
	if !view.SubagentEffortApplies {
		t.Fatal("max applies on deepseek-v4-pro; the flag must recover with the model switch")
	}
}

// EffortDomainForRef is the profile editor's lookup: an explicit ref resolves
// that model's domain; an empty ref resolves the inherit chain; an unknown ref
// yields an empty domain so the editor offers only the inherit option.
func TestEffortDomainForRefResolutions(t *testing.T) {
	isolateDesktopUserDirs(t)
	writeSubagentDomainConfig(t, "")

	app := NewApp()

	mimo := app.EffortDomainForRef("mimo/mimo-v2.6-flash")
	if want := []string{"auto", "low", "medium", "high"}; !reflect.DeepEqual(mimo.Levels, want) || !mimo.Supported {
		t.Fatalf("mimo domain = %+v, want %v", mimo, want)
	}
	deepseek := app.EffortDomainForRef("deepseek/deepseek-v4-pro")
	if want := []string{"auto", "disabled", "low", "high", "max"}; !reflect.DeepEqual(deepseek.Levels, want) || !deepseek.Supported {
		t.Fatalf("deepseek domain = %+v, want %v", deepseek, want)
	}
	inherited := app.EffortDomainForRef("")
	if want := []string{"auto", "low", "medium", "high"}; !reflect.DeepEqual(inherited.Levels, want) {
		t.Fatalf("empty ref (inherit chain) = %+v, want the main model domain %v", inherited, want)
	}
	unknown := app.EffortDomainForRef("nowhere/no-such-model")
	if unknown.Supported || len(unknown.Levels) != 0 {
		t.Fatalf("unknown ref = %+v, want an empty domain", unknown)
	}
}

// SkillView carries the per-profile domain: the per-name model override wins
// over the inherit chain, mirroring boot's subagentModelRef resolution.
func TestSkillsSettingsSubagentEffortDomain(t *testing.T) {
	isolateDesktopUserDirs(t)
	home := os.Getenv("HOME")
	st := skill.New(skill.Options{HomeDir: home})
	a := NewApp()
	a.setTestCtrl(control.New(control.Options{AllSkillStore: st, SkillStore: st}), "")
	t.Cleanup(func() { a.activeCtrl().Close() })
	writeSubagentDomainConfig(t, `
[agent.subagent_models]
explore = "deepseek/deepseek-v4-pro"
`)

	views := a.SkillsSettings().Skills
	var explore *SkillView
	for i := range views {
		if views[i].Name == "explore" {
			explore = &views[i]
		}
	}
	if explore == nil {
		t.Fatalf("builtin explore not found in %d views", len(views))
	}
	if want := []string{"auto", "disabled", "low", "high", "max"}; !reflect.DeepEqual(explore.EffortLevels, want) {
		t.Fatalf("explore effortLevels = %v, want the per-name override's domain %v", explore.EffortLevels, want)
	}
	if !explore.EffortApplies {
		t.Fatal("empty configured effort is auto and always applies")
	}
}
