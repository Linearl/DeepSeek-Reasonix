package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

func TestAddEveryProviderPresetAccessInstallsTemplate(t *testing.T) {
	for _, preset := range config.CuratedProviderPresets() {
		t.Run(preset.ID, func(t *testing.T) {
			isolateDesktopUserDirs(t)

			if warning, err := NewApp().AddProviderPresetAccess(preset.ID, "sk-test"); err != nil {
				t.Fatalf("AddProviderPresetAccess(%q): %v", preset.ID, err)
			} else if warning != "" {
				t.Fatalf("AddProviderPresetAccess(%q) warning = %q, want none", preset.ID, warning)
			}

			cfg := config.LoadForEdit(config.UserConfigPath())
			access := providerAccessSet(cfg.Desktop.ProviderAccess)
			for _, entry := range preset.Entries {
				got, ok := cfg.Provider(entry.Name)
				if !ok {
					t.Fatalf("provider %q from preset %q was not saved", entry.Name, preset.ID)
				}
				if !access[entry.Name] {
					t.Fatalf("provider_access for preset %q missing %q: %+v", preset.ID, entry.Name, cfg.Desktop.ProviderAccess)
				}
				if got.Kind != entry.Kind || got.BaseURL != entry.BaseURL || got.DefaultModel() != entry.DefaultModel() || got.APIKeyEnv == entry.APIKeyEnv || !config.CredentialStored(got.APIKeyEnv) || got.AuthHeader != entry.AuthHeader || got.NoProxy != entry.NoProxy {
					t.Fatalf("provider %q core fields = %+v, want template %+v", entry.Name, got, entry)
				}
				if got.PresetID != preset.ID || got.PresetVersion != config.ProviderPresetVersion {
					t.Fatalf("provider %q preset metadata = %q/%d, want %q/%d", entry.Name, got.PresetID, got.PresetVersion, preset.ID, config.ProviderPresetVersion)
				}
				if got.ContextWindow != entry.ContextWindow || got.Thinking != entry.Thinking || got.DefaultEffort != entry.DefaultEffort || got.ReasoningProtocol != entry.ReasoningProtocol {
					t.Fatalf("provider %q capability fields = %+v, want template %+v", entry.Name, got, entry)
				}
				if !reflect.DeepEqual(got.ModelList(), entry.ModelList()) || !reflect.DeepEqual(got.VisionModels, entry.VisionModels) || !reflect.DeepEqual(got.SupportedEfforts, entry.SupportedEfforts) {
					t.Fatalf("provider %q models/capabilities = %+v, want template %+v", entry.Name, got, entry)
				}
				if !reflect.DeepEqual(got.Headers, entry.Headers) || !reflect.DeepEqual(got.ExtraBody, entry.ExtraBody) {
					t.Fatalf("provider %q request extras = %+v, want template %+v", entry.Name, got, entry)
				}
			}

			view := NewApp().Settings()
			var presetView *ProviderPresetView
			for i := range view.ProviderPresets {
				if view.ProviderPresets[i].ID == preset.ID {
					presetView = &view.ProviderPresets[i]
					break
				}
			}
			if presetView == nil || !presetView.Added || presetView.Status != providerPresetStatusInstalled || !presetView.KeySet || !presetView.Configured {
				t.Fatalf("preset view for %q = %+v, want installed/key-set/configured", preset.ID, presetView)
			}
		})
	}
}

func TestAddOpenCodeGoRecommendedPresetCompletesMissingRoutes(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("OPENCODE_GO_API_KEY", "")
	os.Unsetenv("OPENCODE_GO_API_KEY")

	preset, ok := config.CuratedProviderPreset("opencode-go-recommended")
	if !ok || len(preset.Entries) != 3 {
		t.Fatalf("recommended preset = %+v, found=%v", preset, ok)
	}
	cfg := config.Default()
	seed := preset.Entries[0]
	seed.PresetID = "opencode-go"
	if err := cfg.UpsertProvider(seed); err != nil {
		t.Fatalf("seed existing OpenCode Go route: %v", err)
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save seed config: %v", err)
	}
	partial := providerPresetViewByID(t, NewApp().Settings(), preset.ID)
	if partial.Status != providerPresetStatusPartial || len(partial.MissingProviderNames) != 2 {
		t.Fatalf("recommended preset partial view = %+v, want two missing routes", partial)
	}

	if warning, err := NewApp().AddProviderPresetAccess(preset.ID, "sk-opencode"); err != nil {
		t.Fatalf("AddProviderPresetAccess: %v", err)
	} else if warning != "" {
		t.Fatalf("AddProviderPresetAccess warning = %q, want none", warning)
	}

	cfg = config.LoadForEdit(config.UserConfigPath())
	for _, entry := range preset.Entries {
		if _, ok := cfg.Provider(entry.Name); !ok {
			t.Fatalf("missing recommended route %q after completion", entry.Name)
		}
	}
	data, err := os.ReadFile(config.UserCredentialsPath())
	if err != nil {
		t.Fatalf("read saved credentials: %v", err)
	}
	for _, route := range preset.Entries {
		entry, _ := cfg.Provider(route.Name)
		if entry.APIKeyEnv == "OPENCODE_GO_API_KEY" || !strings.Contains(string(data), entry.APIKeyEnv+"=sk-opencode") {
			t.Fatalf("route %s did not receive an isolated credential reference", route.Name)
		}
	}
}

func TestAddOpenCodeGoRecommendedPresetSelectsUsableDefaultForFreshSetup(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("OPENCODE_GO_API_KEY", "")
	os.Unsetenv("OPENCODE_GO_API_KEY")

	cfg := config.Default()
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save fresh config: %v", err)
	}
	if _, err := NewApp().AddProviderPresetAccess("opencode-go-recommended", "sk-opencode"); err != nil {
		t.Fatalf("AddProviderPresetAccess: %v", err)
	}

	got := config.LoadForEdit(config.UserConfigPath())
	if got.DefaultModel != "opencode-go/glm-5.3" {
		t.Fatalf("default model = %q, want ready-to-use OpenCode Go default", got.DefaultModel)
	}
}

func TestAddOpenCodeGoRecommendedPresetPreservesConfiguredDefault(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("OPENCODE_GO_API_KEY", "")
	os.Unsetenv("OPENCODE_GO_API_KEY")

	cfg := config.Default()
	if err := cfg.UpsertProvider(config.ProviderEntry{
		Name:    "local-ready",
		Kind:    "openai",
		BaseURL: "http://127.0.0.1:11434/v1",
		Models:  []string{"local-model"},
		Default: "local-model",
	}); err != nil {
		t.Fatalf("upsert configured provider: %v", err)
	}
	if err := cfg.SetDefaultModel("local-ready/local-model"); err != nil {
		t.Fatalf("set configured default: %v", err)
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save configured default: %v", err)
	}

	if _, err := NewApp().AddProviderPresetAccess("opencode-go-recommended", "sk-opencode"); err != nil {
		t.Fatalf("AddProviderPresetAccess: %v", err)
	}
	if got := config.LoadForEdit(config.UserConfigPath()).DefaultModel; got != "local-ready/local-model" {
		t.Fatalf("default model = %q, want existing configured default preserved", got)
	}
}

func TestAddOpenCodeGoRecommendedPresetPreservesModifiedRoute(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("OPENCODE_GO_API_KEY", "")
	os.Unsetenv("OPENCODE_GO_API_KEY")

	preset, ok := config.CuratedProviderPreset("opencode-go-recommended")
	if !ok || len(preset.Entries) != 3 {
		t.Fatalf("recommended preset = %+v, found=%v", preset, ok)
	}
	cfg := config.Default()
	modified := preset.Entries[0]
	modified.BaseURL = "https://custom.example/v1"
	modified.PresetID = "opencode-go"
	if err := cfg.UpsertProvider(modified); err != nil {
		t.Fatalf("seed modified OpenCode Go route: %v", err)
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save modified config: %v", err)
	}

	if _, err := NewApp().AddProviderPresetAccess(preset.ID, "sk-opencode"); err != nil {
		t.Fatalf("AddProviderPresetAccess: %v", err)
	}
	cfg = config.LoadForEdit(config.UserConfigPath())
	got, ok := cfg.Provider("opencode-go")
	if !ok || got.BaseURL != "https://custom.example/v1" {
		t.Fatalf("modified route = %+v, want preserved custom endpoint", got)
	}
	for _, name := range []string{"opencode-go-anthropic", "opencode-go-responses"} {
		if _, ok := cfg.Provider(name); !ok {
			t.Fatalf("missing route %q after completing bundle around modified route", name)
		}
	}
}

func TestAddOpenCodeGoRecommendedPresetRejectsConflictAtomically(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("OPENCODE_GO_API_KEY", "")
	os.Unsetenv("OPENCODE_GO_API_KEY")

	cfg := config.Default()
	conflict := config.ProviderEntry{
		Name:          "opencode-go",
		Kind:          "openai",
		BaseURL:       "https://custom.example/v1",
		Models:        []string{"custom-model"},
		Default:       "custom-model",
		APIKeyEnv:     "OPENCODE_GO_API_KEY",
		PresetID:      "custom",
		PresetVersion: 1,
	}
	if err := cfg.UpsertProvider(conflict); err != nil {
		t.Fatalf("upsert conflicting provider: %v", err)
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save conflict config: %v", err)
	}

	if warning, err := NewApp().AddProviderPresetAccess("opencode-go-recommended", "sk-should-not-save"); err == nil {
		t.Fatal("AddProviderPresetAccess unexpectedly accepted same-name conflict")
	} else if !strings.Contains(err.Error(), "opencode-go") {
		t.Fatalf("AddProviderPresetAccess error = %v, want opencode-go conflict", err)
	} else if warning != "" {
		t.Fatalf("AddProviderPresetAccess warning = %q, want none", warning)
	}

	cfg = config.LoadForEdit(config.UserConfigPath())
	if _, ok := cfg.Provider("opencode-go-anthropic"); ok {
		t.Fatal("conflicting bundle partially installed Anthropic route")
	}
	if _, err := os.Stat(config.UserCredentialsPath()); err == nil {
		data, readErr := os.ReadFile(config.UserCredentialsPath())
		if readErr != nil {
			t.Fatalf("read credentials: %v", readErr)
		}
		if strings.Contains(string(data), "sk-should-not-save") {
			t.Fatalf("conflicting bundle saved credentials: %s", data)
		}
	}
}

func TestAddOfficialProviderAccessPreservesBackgroundJobsWhenSavingKey(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	os.Unsetenv("DEEPSEEK_API_KEY")

	app := NewApp()
	app.readyHook = func() {}
	app.setTestCtrl(newBackgroundJobController(t, "provider-access-job"), "deepseek-flash/deepseek-v4-flash")

	_, err := app.AddOfficialProviderAccess("deepseek", "sk-test")
	if err != nil || !controllerHasActiveRuntimeWork(app.activeCtrl()) {
		t.Fatalf("AddOfficialProviderAccess interrupted background work: %v", err)
	}
	p, _ := config.LoadForEdit(config.UserConfigPath()).Provider("deepseek")
	if !config.CredentialStored(p.APIKeyEnv) || p.APIKeyEnv == "DEEPSEEK_API_KEY" {
		t.Fatal("official key was not committed to a new reference")
	}
}

func TestSetProviderKeyRestoresOfficialProviderAccess(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	os.Unsetenv("DEEPSEEK_API_KEY")
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
default_model = "deepseek/deepseek-v4-flash"

[desktop]
provider_access = []

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

	if _, err := NewApp().SetProviderKey("DEEPSEEK_API_KEY", "sk-test"); err != nil {
		t.Fatalf("SetProviderKey: %v", err)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	if !providerAccessSet(cfg.Desktop.ProviderAccess)["deepseek"] {
		t.Fatalf("provider_access = %+v, want deepseek restored", cfg.Desktop.ProviderAccess)
	}
	got := NewApp().Settings()
	for _, p := range got.Providers {
		if p.Name == "deepseek" {
			if !p.Added || !p.KeySet {
				t.Fatalf("deepseek settings = %+v, want added and key-set", p)
			}
			return
		}
	}
	t.Fatalf("settings providers missing deepseek: %+v", got.Providers)
}

func TestSetProviderKeyKeepsCustomAliasProviderAccess(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("PROXY_DEEPSEEK_KEY", "")
	os.Unsetenv("PROXY_DEEPSEEK_KEY")
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
[desktop]
provider_access = []

[[providers]]
name = "deepseek-flash"
kind = "openai"
base_url = "https://proxy.example/v1"
model = "deepseek-v4-flash"
api_key_env = "PROXY_DEEPSEEK_KEY"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := NewApp().SetProviderKey("PROXY_DEEPSEEK_KEY", "sk-test"); err != nil {
		t.Fatalf("SetProviderKey: %v", err)
	}
	cfg := config.LoadForEditWithoutCredentials(config.UserConfigPath())
	access := providerAccessSet(cfg.Desktop.ProviderAccess)
	if !access["deepseek-flash"] {
		t.Fatalf("provider_access = %+v, want custom alias deepseek-flash", cfg.Desktop.ProviderAccess)
	}
	if access["deepseek"] {
		t.Fatalf("provider_access = %+v, should not canonicalize custom proxy to deepseek", cfg.Desktop.ProviderAccess)
	}
}

func TestSetProviderKeyLeaseHeldKeepsCurrentController(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old"}
	cfg.Providers = []config.ProviderEntry{
		{Name: "old", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "old-model", APIKeyEnv: "OLD_MODEL_KEY"},
		{Name: "longcat", Kind: "openai", BaseURL: "https://longcat.example/v1", Model: "longcat-chat", APIKeyEnv: "LONGCAT_API_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	sessionPath := filepath.Join(dir, "externally-leased-provider-key.jsonl")
	if err := os.WriteFile(sessionPath, nil, 0o644); err != nil {
		t.Fatalf("write placeholder session: %v", err)
	}
	externalLease, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}
	defer externalLease.Release()

	oldSession := agent.NewSession("old system prompt")
	oldSession.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	oldExec := agent.New(nil, nil, oldSession, agent.Options{}, event.Discard)
	oldCtrl := control.New(control.Options{Executor: oldExec, SessionDir: dir, SessionPath: sessionPath, Label: "old", Sink: event.Discard})
	defer oldCtrl.Close()

	app := NewApp()
	app.ctx = context.Background()
	tab := &WorkspaceTab{
		ID:          "tab_provider",
		Scope:       "global",
		SessionPath: sessionPath,
		Ready:       true,
		model:       "old/old-model",
		Ctrl:        oldCtrl,
		sink:        &tabEventSink{tabID: "tab_provider", app: app},
		disabledMCP: map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	warning, err := app.SetProviderKey("LONGCAT_API_KEY", "sk-longcat")
	if err != nil {
		t.Fatalf("SetProviderKey: %v", err)
	}
	if warning != "" {
		t.Fatalf("SetProviderKey warning = %q; saving does not acquire the session lease", warning)
	}
	if strings.Contains(warning, sessionPath) || strings.Contains(warning, "held by") {
		t.Fatalf("SetProviderKey surfaced raw lease details: %v", warning)
	}
	if tab.Ctrl != oldCtrl {
		t.Fatalf("tab controller changed after failed provider-key rebuild")
	}
	if tab.StartupErr != "" {
		t.Fatalf("tab startup error = %q, want unchanged current session", tab.StartupErr)
	}
	if got := tab.Ctrl.History(); len(got) < 2 || got[1].Content != "hello" {
		t.Fatalf("history after failed provider-key rebuild = %+v", got)
	}
	if access := providerAccessSet(config.LoadForEditWithoutCredentials(config.UserConfigPath()).Desktop.ProviderAccess); !access["longcat"] {
		t.Fatalf("provider_access should still persist longcat after key save")
	}
}

func TestSetProviderKeyPreservesInFlightStartupBuild(t *testing.T) {
	isolateDesktopUserDirs(t)

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old"}
	cfg.Providers = []config.ProviderEntry{{
		Name:      "old",
		Kind:      "openai",
		BaseURL:   "https://example.invalid/v1",
		Model:     "old-model",
		APIKeyEnv: "OLD_MODEL_KEY",
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	sessionPath := filepath.Join(dir, "startup-build-in-flight.jsonl")
	if err := os.WriteFile(sessionPath, nil, 0o644); err != nil {
		t.Fatalf("write placeholder session: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	// Model the async startup build still being in flight: no controller yet,
	// a live build generation, and a cancellable build context.
	buildCtx, buildCancel := context.WithCancel(context.Background())
	const startupGeneration = 1
	tab := &WorkspaceTab{
		ID:              "tab_key_rebuild",
		Scope:           "global",
		SessionPath:     sessionPath,
		model:           "old/old-model",
		buildGeneration: startupGeneration,
		buildCancel:     buildCancel,
		disabledMCP:     map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(tab.releaseSessionLease)

	if _, err := app.SetProviderKey("OLD_MODEL_KEY", "sk-new"); err != nil {
		t.Fatalf("SetProviderKey: %v", err)
	}
	if tab.Ctrl != nil || tab.buildGeneration != startupGeneration || buildCtx.Err() != nil {
		t.Fatal("saving a key published or cancelled an in-flight startup; publication owns its version check")
	}
	buildCancel()
}

func TestSaveProviderWithKeyLeaseHeldPersistsCustomProvider(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old"}
	cfg.Providers = []config.ProviderEntry{{
		Name:      "old",
		Kind:      "openai",
		BaseURL:   "https://example.invalid/v1",
		Model:     "old-model",
		APIKeyEnv: "OLD_MODEL_KEY",
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	sessionPath := filepath.Join(dir, "externally-leased-custom-provider.jsonl")
	if err := os.WriteFile(sessionPath, nil, 0o644); err != nil {
		t.Fatalf("write placeholder session: %v", err)
	}
	externalLease, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}
	defer externalLease.Release()

	oldSession := agent.NewSession("old system prompt")
	oldSession.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	oldExec := agent.New(nil, nil, oldSession, agent.Options{}, event.Discard)
	oldCtrl := control.New(control.Options{Executor: oldExec, SessionDir: dir, SessionPath: sessionPath, Label: "old", Sink: event.Discard})
	defer oldCtrl.Close()

	app := NewApp()
	app.ctx = context.Background()
	tab := &WorkspaceTab{
		ID:          "tab_custom_provider",
		Scope:       "global",
		SessionPath: sessionPath,
		Ready:       true,
		model:       "old/old-model",
		Ctrl:        oldCtrl,
		sink:        &tabEventSink{tabID: "tab_custom_provider", app: app},
		disabledMCP: map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	warning, err := app.SaveProviderWithKey(ProviderView{
		Name:      "proxy",
		Kind:      "openai",
		BaseURL:   "https://proxy.example/v1",
		Models:    []string{"model-a", "model-b"},
		Default:   "model-a",
		APIKeyEnv: "PROXY_API_KEY",
	}, "sk-proxy")
	if err != nil {
		t.Fatalf("SaveProviderWithKey: %v", err)
	}
	if warning != "" {
		t.Fatalf("SaveProviderWithKey warning = %q; saving does not acquire the session lease", warning)
	}
	if strings.Contains(warning, sessionPath) || strings.Contains(warning, "held by") {
		t.Fatalf("SaveProviderWithKey surfaced raw lease details: %v", warning)
	}
	if tab.Ctrl != oldCtrl {
		t.Fatalf("tab controller changed after failed provider rebuild")
	}
	gotCfg := config.LoadForEditWithoutCredentials(config.UserConfigPath())
	got, ok := gotCfg.Provider("proxy")
	if !ok {
		t.Fatal("custom provider was not saved")
	}
	if want := []string{"model-a", "model-b"}; !reflect.DeepEqual(got.ModelList(), want) {
		t.Fatalf("custom provider models = %v, want %v", got.ModelList(), want)
	}
	if !providerAccessSet(gotCfg.Desktop.ProviderAccess)["proxy"] {
		t.Fatalf("provider_access = %+v, want proxy", gotCfg.Desktop.ProviderAccess)
	}
	data, err := os.ReadFile(config.UserCredentialsPath())
	if err != nil {
		t.Fatalf("read credentials: %v", err)
	}
	if got.APIKeyEnv == "PROXY_API_KEY" || !strings.Contains(string(data), got.APIKeyEnv+"=sk-proxy") {
		t.Fatal("provider key was not saved with an isolated reference")
	}
}

func TestConfigChangeLeaseHeldPersistsAndDefersRefresh(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old"}
	cfg.Providers = []config.ProviderEntry{{
		Name:      "old",
		Kind:      "openai",
		BaseURL:   "https://example.invalid/v1",
		Model:     "old-model",
		APIKeyEnv: "OLD_MODEL_KEY",
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	sessionPath := filepath.Join(dir, "externally-leased-settings.jsonl")
	if err := os.WriteFile(sessionPath, nil, 0o644); err != nil {
		t.Fatalf("write placeholder session: %v", err)
	}
	externalLease, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}
	defer externalLease.Release()

	oldExec := agent.New(nil, nil, agent.NewSession("old system prompt"), agent.Options{}, event.Discard)
	oldCtrl := control.New(control.Options{Executor: oldExec, SessionDir: dir, SessionPath: sessionPath, Label: "old", Sink: event.Discard})
	defer oldCtrl.Close()

	app := NewApp()
	app.ctx = context.Background()
	tab := &WorkspaceTab{
		ID:          "tab_settings",
		Scope:       "global",
		SessionPath: sessionPath,
		Ready:       true,
		model:       "old/old-model",
		Ctrl:        oldCtrl,
		sink:        &tabEventSink{tabID: "tab_settings", app: app},
		disabledMCP: map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	if err := app.SetMaxSubagentDepth(1); err != nil {
		t.Fatalf("SetMaxSubagentDepth should defer lease-held refresh instead of failing: %v", err)
	}
	if tab.Ctrl != oldCtrl {
		t.Fatalf("tab controller changed after deferred settings rebuild")
	}
	got := config.LoadForEditWithoutCredentials(config.UserConfigPath())
	if got.Agent.MaxSubagentDepth != 1 {
		t.Fatalf("saved max_subagent_depth = %d, want 1", got.Agent.MaxSubagentDepth)
	}
}

func TestDeferredRebuildRetryAppliesAfterLeaseRelease(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old"}
	cfg.Providers = []config.ProviderEntry{{
		Name:      "old",
		Kind:      "openai",
		BaseURL:   "https://example.invalid/v1",
		Model:     "old-model",
		APIKeyEnv: "OLD_MODEL_KEY",
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	sessionPath := filepath.Join(dir, "deferred-rebuild-retry.jsonl")
	if err := os.WriteFile(sessionPath, nil, 0o644); err != nil {
		t.Fatalf("write placeholder session: %v", err)
	}
	externalLease, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}
	released := false
	defer func() {
		if !released {
			externalLease.Release()
		}
	}()

	oldExec := agent.New(nil, nil, agent.NewSession("old system prompt"), agent.Options{}, event.Discard)
	oldCtrl := control.New(control.Options{Executor: oldExec, SessionDir: dir, SessionPath: sessionPath, Label: "old", Sink: event.Discard})
	defer oldCtrl.Close()

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	tab := &WorkspaceTab{
		ID:          "tab_deferred_retry",
		Scope:       "global",
		SessionPath: sessionPath,
		Ready:       true,
		model:       "old/old-model",
		Ctrl:        oldCtrl,
		sink:        &tabEventSink{tabID: "tab_deferred_retry", app: app},
		disabledMCP: map[string]ServerView{},
	}
	installNoopRuntimeEvents(app, tab.sink)
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		if c := app.controllerForTab(tab); c != nil && c != oldCtrl {
			c.Close()
		}
		tab.releaseSessionLease()
	})

	if err := app.SetAgentParams(0.2, 0, 0, "updated prompt"); err != nil {
		t.Fatalf("SetAgentParams: %v", err)
	}
	if !app.deferredRebuildPending(tab.ID) {
		t.Fatal("deferred rebuild was not scheduled while the lease is held")
	}
	if app.controllerForTab(tab) != oldCtrl {
		t.Fatal("controller changed while the lease is still held")
	}

	externalLease.Release()
	released = true

	app.deferredRebuildTick(false)
	if app.deferredRebuildPending(tab.ID) {
		t.Fatal("deferred rebuild is still pending after the lease was released")
	}
	if c := app.controllerForTab(tab); c == nil || c == oldCtrl {
		t.Fatalf("controller was not rebuilt after the lease release: got %p", c)
	}
}

func TestDeferredRebuildScheduleAfterStopIsNoop(t *testing.T) {
	app := NewApp()
	app.stopDeferredRebuildRetry()
	app.scheduleDeferredRebuild("tab_x", "settings")
	if app.deferredRebuildPending("tab_x") {
		t.Fatal("schedule after stop should not register pending work")
	}
}
