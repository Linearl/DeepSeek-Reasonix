package main

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

func TestSettingsSeedsMissingUserConfigFromLegacyProjectConfig(t *testing.T) {
	isolateDesktopUserDirs(t)

	project := robustTempDir(t)
	if err := os.WriteFile(filepath.Join(project, "reasonix.toml"), []byte(`
default_model = "legacy-provider/legacy-model"

[desktop]
language = "zh"
layout_style = "workbench"
theme = "light"
theme_style = "glacier"
close_behavior = "quit"
status_bar_style = "text"
status_bar_items = ["model", "cache", "balance"]
`), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}

	orig, _ := os.Getwd()
	defer func() { _ = os.Chdir(orig) }()
	if err := os.Chdir(project); err != nil {
		t.Fatalf("chdir project: %v", err)
	}

	app := NewApp()
	got := app.Settings()
	if got.ConfigPath != config.UserConfigPath() {
		t.Fatalf("Settings configPath = %q, want user config %q", got.ConfigPath, config.UserConfigPath())
	}
	// Fork: the desktop keeps the stored text choice instead of upgrading it to
	// the upstream icon default (see release-notes/FORK-vs-upstream.md).
	if got.DefaultModel != "legacy-provider/legacy-model" || got.DesktopLanguage != "zh" || got.DesktopLayoutStyle != "workbench" || got.DesktopTheme != "light" || got.DesktopThemeStyle != "glacier" || got.CloseBehavior != "quit" || got.StatusBarStyle != "text" {
		t.Fatalf("Settings did not seed from legacy project config: %+v", got)
	}
	if want := []string{"model", "cache", "balance"}; !reflect.DeepEqual(got.StatusBarItems, want) {
		t.Fatalf("Settings did not seed status bar items from legacy project config: got %v want %v", got.StatusBarItems, want)
	}
	if _, err := os.Stat(config.UserConfigPath()); !os.IsNotExist(err) {
		t.Fatalf("Settings() should not write user config before an edit, stat err = %v", err)
	}
	if err := app.SetDesktopLanguage("en"); err != nil {
		t.Fatalf("SetDesktopLanguage: %v", err)
	}
	userCfg := config.LoadForEdit(config.UserConfigPath())
	// Fork: text stays text (no upstream icon upgrade), see FORK-vs-upstream.
	if userCfg.DesktopLanguage() != "en" || userCfg.DesktopLayoutStyle() != "workbench" || userCfg.DesktopTheme() != "light" || userCfg.DesktopThemeStyle() != "glacier" || userCfg.DesktopCloseBehavior() != "quit" || userCfg.DesktopStatusBarStyle() != "text" {
		t.Fatalf("saved user config did not preserve seeded desktop prefs: lang:%q layout:%q theme:%q style:%q close:%q status:%q", userCfg.DesktopLanguage(), userCfg.DesktopLayoutStyle(), userCfg.DesktopTheme(), userCfg.DesktopThemeStyle(), userCfg.DesktopCloseBehavior(), userCfg.DesktopStatusBarStyle())
	}
	if want := []string{"model", "cache", "balance"}; !reflect.DeepEqual(userCfg.DesktopStatusBarItems(), want) {
		t.Fatalf("saved user config did not preserve seeded status bar items: got %v want %v", userCfg.DesktopStatusBarItems(), want)
	}
}

func TestSettingsSubagentDefaultsRoundTrip(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
default_model = "deepseek/deepseek-v4-flash"

[[providers]]
name = "deepseek"
kind = "openai"
base_url = "https://api.deepseek.com"
models = ["deepseek-v4-flash", "deepseek-v4-pro"]
default = "deepseek-v4-flash"
api_key_env = "DEEPSEEK_API_KEY"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	app := NewApp()
	if got := app.Settings().Agent.MaxSubagentDepth; got != agent.DefaultMaxSubagentDepth {
		t.Fatalf("default max subagent depth = %d, want %d", got, agent.DefaultMaxSubagentDepth)
	}
	if err := app.SetSubagentModel("deepseek/deepseek-v4-pro"); err != nil {
		t.Fatalf("SetSubagentModel: %v", err)
	}
	if err := app.SetSubagentEffort("max"); err != nil {
		t.Fatalf("SetSubagentEffort: %v", err)
	}
	if err := app.SetMaxSubagentDepth(1); err != nil {
		t.Fatalf("SetMaxSubagentDepth(1): %v", err)
	}
	if err := app.SetMaxSubagentDepth(2); err != nil {
		t.Fatalf("SetMaxSubagentDepth(2): %v", err)
	}

	got := app.Settings()
	if got.SubagentModel != "deepseek/deepseek-v4-pro" || got.SubagentEffort != "max" {
		t.Fatalf("subagent settings = model:%q effort:%q", got.SubagentModel, got.SubagentEffort)
	}
	if got.Agent.MaxSubagentDepth != 2 {
		t.Fatalf("max subagent depth = %d, want 2", got.Agent.MaxSubagentDepth)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	if cfg.Agent.SubagentModel != "deepseek/deepseek-v4-pro" || cfg.Agent.SubagentEffort != "max" {
		t.Fatalf("saved config = model:%q effort:%q", cfg.Agent.SubagentModel, cfg.Agent.SubagentEffort)
	}
	if cfg.Agent.MaxSubagentDepth != 2 {
		t.Fatalf("saved max_subagent_depth = %d, want 2", cfg.Agent.MaxSubagentDepth)
	}
}

func TestSettingsSurfacesOfficialProviderTemplatesSeparately(t *testing.T) {
	isolateDesktopUserDirs(t)

	got := NewApp().Settings()
	providers := providerAccessSet(providerNamesFromView(got.Providers))
	official := providerAccessSet(providerNamesFromView(got.OfficialProviders))
	if providers["mimo-api"] {
		t.Fatalf("mimo-api should not be mixed into configured providers: %+v", got.Providers)
	}
	if !official["deepseek"] || official["mimo-api"] || official["mimo-token-plan"] {
		t.Fatalf("official providers = %+v, want only deepseek", got.OfficialProviders)
	}
}

func TestSettingsRepairsLegacyOfficialProviderWithoutModel(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
default_model = "deepseek-flash"

[[providers]]
name = "deepseek-flash"
kind = "openai"
base_url = "https://api.deepseek.com"
api_key_env = "DEEPSEEK_API_KEY"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got := NewApp().Settings()
	for _, p := range got.Providers {
		if p.Name != "deepseek" {
			continue
		}
		if !p.BuiltIn {
			t.Fatalf("deepseek provider should be marked built-in for official endpoint: %+v", p)
		}
		if !p.Added || !p.KeySet || len(p.Models) != 3 || p.Models[0] != "deepseek-v4-flash" || p.Models[1] != "deepseek-v4-pro" || p.Models[2] != "deepseek-v4-flash-vision-exp" || !slices.Equal(p.VisionModels, []string{"deepseek-v4-flash-vision-exp"}) || p.Default != "deepseek-v4-flash" {
			t.Fatalf("deepseek provider = %+v, want added repaired official model list", p)
		}
		if got.DefaultModel != "deepseek/deepseek-v4-flash" {
			t.Fatalf("default_model = %q, want deepseek/deepseek-v4-flash", got.DefaultModel)
		}
		return
	}
	t.Fatalf("settings providers missing deepseek: %+v", got.Providers)
}

func TestSettingsTreatsReservedProviderNameWithExternalEndpointAsCustom(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
default_model = "deepseek/deepseek-v4-Flash"

[desktop]
provider_access = ["deepseek"]

[[providers]]
name = "deepseek"
kind = "openai"
base_url = "https://opencode.ai/zen/go/v1"
models = ["deepseek-v4-Flash", "deepseek-v4-pro", "glm-5"]
default = "deepseek-v4-Flash"
api_key_env = "DEEPSEEK_API_KEY"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got := NewApp().Settings()
	var custom *ProviderView
	for i := range got.Providers {
		if got.Providers[i].Name == "deepseek" {
			custom = &got.Providers[i]
			break
		}
	}
	if custom == nil {
		t.Fatalf("settings providers missing deepseek: %+v", got.Providers)
	}
	if custom.BuiltIn {
		t.Fatalf("external deepseek endpoint should be custom, got built-in provider: %+v", *custom)
	}
	if !custom.Added || !custom.KeySet || custom.BaseURL != "https://opencode.ai/zen/go/v1" {
		t.Fatalf("external deepseek provider = %+v, want added key-set custom opencode endpoint", *custom)
	}
	for _, p := range got.OfficialProviders {
		if p.Name == "deepseek" && p.Added {
			t.Fatalf("official DeepSeek template should not be marked added by external endpoint: %+v", p)
		}
	}
}

func TestSettingsInfersLegacyProviderAccessWhenMissing(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	setDesktopTestCredential(t, "MIMO_API_KEY", "sk-test")
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
default_model = "deepseek-flash/deepseek-v4-pro"

[[providers]]
name = "deepseek-flash"
kind = "openai"
base_url = "https://api.deepseek.com"
models = ["deepseek-v4-flash", "deepseek-v4-pro"]
default = "deepseek-v4-flash"
api_key_env = "DEEPSEEK_API_KEY"

[[providers]]
name = "mimo-pro"
kind = "openai"
base_url = "https://token-plan-cn.xiaomimimo.com/v1"
model = "mimo-v2.5-pro"
api_key_env = "MIMO_API_KEY"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got := NewApp().Settings()
	providers := map[string]ProviderView{}
	for _, p := range got.Providers {
		providers[p.Name] = p
	}
	if !providers["deepseek"].Added || !providers["deepseek"].KeySet {
		t.Fatalf("deepseek provider = %+v, want inferred added key-set provider", providers["deepseek"])
	}
	if !providers["mimo-pro"].Added || !providers["mimo-pro"].KeySet || providers["mimo-pro"].BuiltIn {
		t.Fatalf("mimo-pro provider = %+v, want inferred custom key-set provider", providers["mimo-pro"])
	}
	if got.DefaultModel != "deepseek/deepseek-v4-pro" {
		t.Fatalf("default_model = %q, want deepseek/deepseek-v4-pro", got.DefaultModel)
	}
}

func TestSettingsDoesNotInferProviderAccessWhenExplicitlyEmpty(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
default_model = "deepseek-flash/deepseek-v4-flash"

[desktop]
provider_access = []

[[providers]]
name = "deepseek-flash"
kind = "openai"
base_url = "https://api.deepseek.com"
models = ["deepseek-v4-flash"]
default = "deepseek-v4-flash"
api_key_env = "DEEPSEEK_API_KEY"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got := NewApp().Settings()
	for _, p := range got.Providers {
		if p.Added {
			t.Fatalf("provider %+v should not be inferred as added when provider_access is explicit empty", p)
		}
	}
}

func TestSettingsInfersConfiguredBuiltInsWithoutConfigFile(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	setDesktopTestCredential(t, "MIMO_API_KEY", "sk-test")

	got := NewApp().Settings()
	providers := map[string]ProviderView{}
	for _, p := range got.Providers {
		providers[p.Name] = p
	}
	if !providers["deepseek"].Added || !providers["deepseek"].KeySet {
		t.Fatalf("deepseek provider = %+v, want inferred added provider from configured key", providers["deepseek"])
	}
	if _, ok := providers["mimo-token-plan"]; ok {
		t.Fatalf("mimo-token-plan should not be inferred from MIMO_API_KEY alone: %+v", providers["mimo-token-plan"])
	}
}

func TestSettingsDoesNotInferBuiltInsWithoutKeys(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("MIMO_API_KEY", "")

	got := NewApp().Settings()
	for _, p := range got.Providers {
		if p.Added {
			t.Fatalf("provider %+v should not be inferred as added without a configured key", p)
		}
	}
}

func TestAddOfficialProviderAccessReplacesLegacyProviderWithoutModel(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	os.Unsetenv("DEEPSEEK_API_KEY")
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
default_model = "deepseek-flash"

[[providers]]
name = "deepseek-flash"
kind = "openai"
base_url = "https://api.deepseek.com"
api_key_env = "DEEPSEEK_API_KEY"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := NewApp().AddOfficialProviderAccess("deepseek", "test-key"); err != nil {
		t.Fatalf("AddOfficialProviderAccess: %v", err)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	p, ok := cfg.Provider("deepseek")
	if !ok {
		t.Fatal("deepseek provider not saved")
	}
	if len(p.Models) != 3 || p.Models[0] != "deepseek-v4-flash" || p.Models[1] != "deepseek-v4-pro" || p.Models[2] != "deepseek-v4-flash-vision-exp" || !slices.Equal(p.VisionModels, []string{"deepseek-v4-flash-vision-exp"}) || p.Default != "deepseek-v4-flash" {
		t.Fatalf("deepseek provider after add = %+v, want official model list", p)
	}
	if !providerAccessSet(cfg.Desktop.ProviderAccess)["deepseek"] {
		t.Fatalf("provider_access missing deepseek: %+v", cfg.Desktop.ProviderAccess)
	}
	if cfg.DefaultModel != "deepseek/deepseek-v4-flash" {
		t.Fatalf("default_model = %q, want deepseek/deepseek-v4-flash", cfg.DefaultModel)
	}
}

func TestSettingsSurfacesCuratedProviderPresets(t *testing.T) {
	isolateDesktopUserDirs(t)

	view := NewApp().Settings()
	if len(view.ProviderPresets) < 18 {
		t.Fatalf("Settings().ProviderPresets length = %d, want curated custom presets", len(view.ProviderPresets))
	}
	got := map[string]ProviderPresetView{}
	for _, preset := range view.ProviderPresets {
		got[preset.ID] = preset
	}
	for _, curated := range config.CuratedProviderPresets() {
		id := curated.ID
		preset, ok := got[id]
		if !ok {
			t.Fatalf("Settings().ProviderPresets missing %q: %+v", id, view.ProviderPresets)
		}
		if preset.KeyEnv == "" || len(preset.ProviderNames) == 0 || len(preset.Models) == 0 {
			t.Fatalf("preset %q view has missing fields: %+v", id, preset)
		}
		if preset.ID == "opencode-go-recommended" && (preset.DisplayGroup != "opencode" || preset.DisplaySection != "go" || preset.DisplayTier != "primary" || preset.RouteKind != "bundle") {
			t.Fatalf("recommended OpenCode metadata = %+v", preset)
		}
	}
}

func providerPresetViewByID(t *testing.T, view SettingsView, id string) ProviderPresetView {
	t.Helper()
	for _, preset := range view.ProviderPresets {
		if preset.ID == id {
			return preset
		}
	}
	t.Fatalf("Settings().ProviderPresets missing %q: %+v", id, view.ProviderPresets)
	return ProviderPresetView{}
}

func TestSettingsMarksPresetAddedWhenSameNameProviderExistsWithoutAccess(t *testing.T) {
	isolateDesktopUserDirs(t)
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
[desktop]
provider_access = []

[[providers]]
name = "mimo-api"
kind = "openai"
base_url = "https://custom.example/v1"
models = ["custom-model"]
default = "custom-model"
api_key_env = "MIMO_API_KEY"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	view := NewApp().Settings()
	presetView := providerPresetViewByID(t, view, "mimo-api")
	if !presetView.Added || presetView.Status != providerPresetStatusNameConflict || !reflect.DeepEqual(presetView.StatusProviderNames, []string{"mimo-api"}) {
		t.Fatalf("mimo-api preset view = %+v, want name-conflict because a different same-name provider exists", presetView)
	}

	var providerView *ProviderView
	for i := range view.Providers {
		if view.Providers[i].Name == "mimo-api" {
			providerView = &view.Providers[i]
			break
		}
	}
	if providerView == nil {
		t.Fatal("mimo-api provider view missing")
	}
	if providerView.Added {
		t.Fatalf("mimo-api provider Added = true, want false until provider_access explicitly enables it")
	}
}

func TestSettingsMarksLegacyEquivalentPresetAsInstalled(t *testing.T) {
	isolateDesktopUserDirs(t)
	preset, ok := config.CuratedProviderPreset("mimo-api")
	if !ok || len(preset.Entries) == 0 {
		t.Fatal("missing mimo-api preset")
	}
	legacy := preset.Entries[0]
	legacy.PresetID = ""
	legacy.PresetVersion = 0
	cfg := config.Default()
	if err := cfg.UpsertProvider(legacy); err != nil {
		t.Fatalf("upsert legacy provider: %v", err)
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	view := NewApp().Settings()
	presetView := providerPresetViewByID(t, view, "mimo-api")
	if !presetView.Added || presetView.Status != providerPresetStatusInstalled || !reflect.DeepEqual(presetView.StatusProviderNames, []string{"mimo-api"}) {
		t.Fatalf("mimo-api preset view = %+v, want installed for legacy equivalent config", presetView)
	}
}

func TestSettingsMarksPresetWithChangedCoreConfigAsModified(t *testing.T) {
	isolateDesktopUserDirs(t)
	preset, ok := config.CuratedProviderPreset("mimo-api")
	if !ok || len(preset.Entries) == 0 {
		t.Fatal("missing mimo-api preset")
	}
	modified := preset.Entries[0]
	modified.BaseURL = "https://custom.example/v1"
	cfg := config.Default()
	if err := cfg.UpsertProvider(modified); err != nil {
		t.Fatalf("upsert modified provider: %v", err)
	}
	cfg.Desktop.ProviderAccess = []string{"mimo-api"}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	view := NewApp().Settings()
	presetView := providerPresetViewByID(t, view, "mimo-api")
	if !presetView.Added || presetView.Status != providerPresetStatusInstalledModified || !reflect.DeepEqual(presetView.StatusProviderNames, []string{"mimo-api"}) {
		t.Fatalf("mimo-api preset view = %+v, want installed-modified for edited preset provider", presetView)
	}
}

func TestSettingsPreservesStepFunRegionalPresetBaseURLs(t *testing.T) {
	isolateDesktopUserDirs(t)

	cfg := config.Default()
	stepfun, ok := config.CuratedProviderPreset("stepfun")
	if !ok || len(stepfun.Entries) != 1 {
		t.Fatal("missing stepfun preset")
	}
	stepfunEntry := stepfun.Entries[0]
	stepfunEntry.BaseURL = "https://api.stepfun.ai/step_plan/v1"
	stepfunAnthropic, ok := config.CuratedProviderPreset("stepfun-anthropic")
	if !ok || len(stepfunAnthropic.Entries) != 1 {
		t.Fatal("missing stepfun-anthropic preset")
	}
	stepfunAnthropicEntry := stepfunAnthropic.Entries[0]
	stepfunAnthropicEntry.BaseURL = "https://api.stepfun.ai/step_plan"
	cfg.Providers = append(cfg.Providers, stepfunEntry, stepfunAnthropicEntry)
	cfg.Desktop.ProviderAccess = []string{"stepfun", "stepfun-anthropic"}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	view := NewApp().Settings()
	for _, id := range []string{"stepfun", "stepfun-anthropic"} {
		presetView := providerPresetViewByID(t, view, id)
		if !presetView.Added || presetView.Status != providerPresetStatusInstalledModified {
			t.Fatalf("%s preset view = %+v, want installed-modified for a preserved regional endpoint", id, presetView)
		}
	}

	loaded := config.LoadForEdit(config.UserConfigPath())
	stepfunEntryView, ok := loaded.Provider("stepfun")
	if !ok {
		t.Fatal("stepfun provider missing after load")
	}
	if got := stepfunEntryView.BaseURL; got != "https://api.stepfun.ai/step_plan/v1" {
		t.Fatalf("stepfun base_url = %q, want preserved regional URL", got)
	}
	stepfunAnthropicEntryView, ok := loaded.Provider("stepfun-anthropic")
	if !ok {
		t.Fatal("stepfun-anthropic provider missing after load")
	}
	if got := stepfunAnthropicEntryView.BaseURL; got != "https://api.stepfun.ai/step_plan" {
		t.Fatalf("stepfun-anthropic base_url = %q, want preserved regional URL", got)
	}
}

func TestSettingsMarksSimilarProviderPresetWithoutBlockingAdd(t *testing.T) {
	isolateDesktopUserDirs(t)
	preset, ok := config.CuratedProviderPreset("mimo-api")
	if !ok || len(preset.Entries) == 0 {
		t.Fatal("missing mimo-api preset")
	}
	similar := preset.Entries[0]
	similar.Name = "my-mimo"
	similar.PresetID = ""
	similar.PresetVersion = 0
	cfg := config.Default()
	if err := cfg.UpsertProvider(similar); err != nil {
		t.Fatalf("upsert similar provider: %v", err)
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	view := NewApp().Settings()
	presetView := providerPresetViewByID(t, view, "mimo-api")
	if presetView.Added || presetView.Status != providerPresetStatusSimilarExisting || !reflect.DeepEqual(presetView.StatusProviderNames, []string{"my-mimo"}) {
		t.Fatalf("mimo-api preset view = %+v, want non-blocking similar-existing status", presetView)
	}
}

func TestAddProviderPresetAccessSavesEditableProviderAndKey(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("MIMO_API_KEY", "")
	os.Unsetenv("MIMO_API_KEY")

	if warning, err := NewApp().AddProviderPresetAccess("mimo-api", "sk-mimo"); err != nil {
		t.Fatalf("AddProviderPresetAccess: %v", err)
	} else if warning != "" {
		t.Fatalf("AddProviderPresetAccess warning = %q, want none", warning)
	}

	cfg := config.LoadForEdit(config.UserConfigPath())
	p, ok := cfg.Provider("mimo-api")
	if !ok {
		t.Fatal("mimo-api provider not saved")
	}
	if p.Kind != "openai" || p.BaseURL != "https://api.xiaomimimo.com/v1" || p.Default != "mimo-v2.5-pro" {
		t.Fatalf("mimo-api provider after preset add = %+v", p)
	}
	if p.PresetID != "mimo-api" || p.PresetVersion != config.ProviderPresetVersion {
		t.Fatalf("mimo-api preset metadata = %q/%d, want mimo-api/%d", p.PresetID, p.PresetVersion, config.ProviderPresetVersion)
	}
	if !p.NoProxy {
		t.Fatal("mimo-api preset should save no_proxy = true")
	}
	if !p.HasVisionModel("mimo-v2.5") || p.HasVisionModel("mimo-v2.5-pro") {
		t.Fatalf("mimo vision_models = %+v, want only vision-capable MiMo models", p.VisionModels)
	}
	if price := p.PriceForModel("mimo-v2.5-pro"); price == nil || price.Currency != "¥" {
		t.Fatalf("mimo-v2.5-pro price = %+v, want RMB pricing", price)
	}
	if !providerAccessSet(cfg.Desktop.ProviderAccess)["mimo-api"] {
		t.Fatalf("provider_access missing mimo-api: %+v", cfg.Desktop.ProviderAccess)
	}
	data, err := os.ReadFile(config.UserCredentialsPath())
	if err != nil {
		t.Fatalf("read saved credentials: %v", err)
	}
	if p.APIKeyEnv == "MIMO_API_KEY" || !strings.Contains(string(data), p.APIKeyEnv+"=sk-mimo") {
		t.Fatal("saved credentials missing the isolated MiMo key reference")
	}

	view := NewApp().Settings()
	var presetView *ProviderPresetView
	var providerView *ProviderView
	for i := range view.ProviderPresets {
		if view.ProviderPresets[i].ID == "mimo-api" {
			presetView = &view.ProviderPresets[i]
		}
	}
	for i := range view.Providers {
		if view.Providers[i].Name == "mimo-api" {
			providerView = &view.Providers[i]
		}
	}
	if presetView == nil || !presetView.Added || presetView.Status != providerPresetStatusInstalled || !presetView.KeySet {
		t.Fatalf("mimo-api preset view = %+v, want installed/key-set", presetView)
	}
	if providerView == nil || providerView.BuiltIn || !providerView.Added || !providerView.KeySet {
		t.Fatalf("mimo provider view = %+v, want editable added custom provider with key", providerView)
	}
}

func TestAddProviderPresetAccessDoesNotOverwriteExistingProvider(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("MIMO_API_KEY", "")
	os.Unsetenv("MIMO_API_KEY")
	setDesktopTestCredential(t, "MIMO_API_KEY", "sk-original")

	cfg := config.Default()
	custom := config.ProviderEntry{
		Name:      "mimo-api",
		Kind:      "openai",
		BaseURL:   "https://custom.example/v1",
		Models:    []string{"custom-model"},
		Default:   "custom-model",
		APIKeyEnv: "MIMO_API_KEY",
		Headers:   map[string]string{"X-Custom": "keep-me"},
	}
	if err := cfg.UpsertProvider(custom); err != nil {
		t.Fatalf("upsert custom provider: %v", err)
	}
	cfg.Desktop.ProviderAccess = []string{"mimo-api"}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	if warning, err := NewApp().AddProviderPresetAccess("mimo-api", "sk-new"); err == nil {
		t.Fatal("AddProviderPresetAccess unexpectedly overwrote an existing provider")
	} else if !strings.Contains(err.Error(), "provider name(s) already exist") {
		t.Fatalf("AddProviderPresetAccess error = %v, want name-exists guard", err)
	} else if warning != "" {
		t.Fatalf("AddProviderPresetAccess warning = %q, want none on rejected add", warning)
	}

	cfg = config.LoadForEdit(config.UserConfigPath())
	got, ok := cfg.Provider("mimo-api")
	if !ok {
		t.Fatal("mimo-api provider missing after rejected add")
	}
	if got.BaseURL != custom.BaseURL || got.DefaultModel() != custom.DefaultModel() || !reflect.DeepEqual(got.ModelList(), custom.ModelList()) || !reflect.DeepEqual(got.Headers, custom.Headers) {
		t.Fatalf("mimo-api provider was overwritten: %+v, want custom %+v", got, custom)
	}
	data, err := os.ReadFile(config.UserCredentialsPath())
	if err != nil {
		t.Fatalf("read saved credentials: %v", err)
	}
	if strings.Contains(string(data), "sk-new") || !strings.Contains(string(data), "MIMO_API_KEY=sk-original") {
		t.Fatalf("credentials changed after rejected add:\n%s", data)
	}
}

func TestResetProviderPresetAccessOverwritesSameNameProvider(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("MIMO_API_KEY", "")
	os.Unsetenv("MIMO_API_KEY")
	setDesktopTestCredential(t, "MIMO_API_KEY", "sk-original")

	cfg := config.Default()
	custom := config.ProviderEntry{
		Name:      "mimo-api",
		Kind:      "openai",
		BaseURL:   "https://custom.example/v1",
		Models:    []string{"custom-model"},
		Default:   "custom-model",
		APIKeyEnv: "MIMO_API_KEY",
		Headers:   map[string]string{"X-Custom": "remove-me"},
	}
	if err := cfg.UpsertProvider(custom); err != nil {
		t.Fatalf("upsert custom provider: %v", err)
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	if err := NewApp().ResetProviderPresetAccess("mimo-api"); err != nil {
		t.Fatalf("ResetProviderPresetAccess: %v", err)
	}

	cfg = config.LoadForEdit(config.UserConfigPath())
	got, ok := cfg.Provider("mimo-api")
	if !ok {
		t.Fatal("mimo-api provider missing after reset")
	}
	if got.BaseURL != "https://api.xiaomimimo.com/v1" || got.DefaultModel() != "mimo-v2.5-pro" || got.PresetID != "mimo-api" || got.PresetVersion != config.ProviderPresetVersion {
		t.Fatalf("mimo-api provider after reset = %+v, want preset template", got)
	}
	if len(got.Headers) != 0 {
		t.Fatalf("mimo-api headers after reset = %+v, want preset headers", got.Headers)
	}
	if !providerAccessSet(cfg.Desktop.ProviderAccess)["mimo-api"] {
		t.Fatalf("provider_access missing mimo-api after reset: %+v", cfg.Desktop.ProviderAccess)
	}
	data, err := os.ReadFile(config.UserCredentialsPath())
	if err != nil {
		t.Fatalf("read saved credentials: %v", err)
	}
	if !strings.Contains(string(data), "MIMO_API_KEY=sk-original") {
		t.Fatalf("credentials changed after reset:\n%s", data)
	}

	presetView := providerPresetViewByID(t, NewApp().Settings(), "mimo-api")
	if !presetView.Added || presetView.Status != providerPresetStatusInstalled {
		t.Fatalf("mimo-api preset view = %+v, want installed after reset", presetView)
	}
}

func TestResetProviderPresetAccessRejectsMissingSameNameProvider(t *testing.T) {
	isolateDesktopUserDirs(t)

	if err := NewApp().ResetProviderPresetAccess("mimo-api"); err == nil {
		t.Fatal("ResetProviderPresetAccess unexpectedly reset a missing provider")
	} else if !strings.Contains(err.Error(), "no same-name provider exists") {
		t.Fatalf("ResetProviderPresetAccess error = %v, want missing same-name provider guard", err)
	}
}
