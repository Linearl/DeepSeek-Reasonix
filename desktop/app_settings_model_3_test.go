package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

func TestModelsForTabKeepsUserProvidersWithProjectConfig(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	setDesktopTestCredential(t, "MIMO_API_KEY", "sk-test")

	userCfg := config.Default()
	userCfg.DefaultModel = "mimo-pro/mimo-v2.5-pro"
	userCfg.Desktop.ProviderAccess = []string{"deepseek-flash", "mimo-pro"}
	if err := userCfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save user config: %v", err)
	}

	projectRoot := t.TempDir()
	projectConfig := `default_model = "deepseek-flash/deepseek-v4-flash"

[desktop]
provider_access = ["deepseek-flash"]

[[providers]]
name = "deepseek-flash"
kind = "openai"
base_url = "https://api.deepseek.com"
model = "deepseek-v4-flash"
api_key_env = "DEEPSEEK_API_KEY"
`
	if err := os.WriteFile(filepath.Join(projectRoot, "reasonix.toml"), []byte(projectConfig), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}

	app := NewApp()
	tab := &WorkspaceTab{ID: "project", WorkspaceRoot: projectRoot, Ready: true}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.activeTabID = tab.ID

	models := app.ModelsForTab(tab.ID)
	refs := modelRefsFromView(models)
	for _, want := range []string{
		"deepseek/deepseek-v4-flash",
		"mimo-pro/mimo-v2.5-pro",
	} {
		if !refs[want] {
			t.Fatalf("ModelsForTab refs = %+v, missing %s", models, want)
		}
	}
}

func TestSetModelForTabRejectsProviderOutsideAccess(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	setDesktopTestCredential(t, "MIMO_API_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "deepseek-flash/deepseek-v4-flash"
	cfg.Desktop.ProviderAccess = []string{"deepseek-flash"}
	cfg.Providers = append(cfg.Providers, config.ProviderEntry{Name: "other", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "other-model"})
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()
	tab := &WorkspaceTab{ID: "tab_a", Scope: "global", Ready: true, model: "deepseek-flash/deepseek-v4-flash"}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	err := app.SetModelForTab(tab.ID, "other/other-model")
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("SetModelForTab hidden provider error = %v, want not available", err)
	}
}

func TestSetModelForTabRefreshesCarriedSystemPromptWithoutChangingDefaults(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")
	setDesktopTestCredential(t, "NEW_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old", "new"}
	cfg.Providers = []config.ProviderEntry{
		{Name: "old", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "old-model", APIKeyEnv: "OLD_MODEL_KEY"},
		{Name: "new", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "new-model", APIKeyEnv: "NEW_MODEL_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if err := os.MkdirAll(config.MemoryUserDir(), 0o755); err != nil {
		t.Fatalf("mkdir memory dir: %v", err)
	}
	const freshRule = "Fresh global AGENTS rule for model switch"
	if err := os.WriteFile(filepath.Join(config.MemoryUserDir(), "AGENTS.md"), []byte(freshRule), 0o644); err != nil {
		t.Fatalf("write global AGENTS.md: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	oldSession := agent.NewSession("old system prompt without memory")
	oldSession.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	oldExec := agent.New(nil, nil, oldSession, agent.Options{}, event.Discard)
	oldPath := filepath.Join(dir, "old.jsonl")
	oldCtrl := control.New(control.Options{Executor: oldExec, SessionDir: dir, SessionPath: oldPath, Label: "old", Sink: event.Discard})

	app := NewApp()
	app.ctx = context.Background()
	tab := &WorkspaceTab{
		ID:          "tab_a",
		Scope:       "global",
		Ready:       true,
		model:       "old/old-model",
		Ctrl:        oldCtrl,
		sink:        &tabEventSink{tabID: "tab_a", app: app},
		disabledMCP: map[string]ServerView{},
	}
	sibling := &WorkspaceTab{
		ID:          "tab_b",
		Scope:       "global",
		Ready:       true,
		model:       "old/old-model",
		disabledMCP: map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab, sibling.ID: sibling}
	app.tabOrder = []string{tab.ID, sibling.ID}
	app.activeTabID = tab.ID
	var switchTiming modelSwitchTiming
	app.modelSwitchTimingHook = func(timing modelSwitchTiming) { switchTiming = timing }
	t.Cleanup(func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
	})

	if err := app.SetModelForTab(tab.ID, "new/new-model"); err != nil {
		t.Fatalf("SetModelForTab: %v", err)
	}
	history := tab.Ctrl.History()
	if len(history) < 2 {
		t.Fatalf("history length = %d, want system + user", len(history))
	}
	if history[0].Role != provider.RoleSystem {
		t.Fatalf("first message role = %s, want system", history[0].Role)
	}
	if !strings.Contains(history[0].Content, freshRule) {
		t.Fatalf("refreshed system prompt missing global AGENTS rule:\n%s", history[0].Content)
	}
	if history[1].Role != provider.RoleUser || history[1].Content != "hello" {
		t.Fatalf("carried user message changed: %+v", history[1])
	}
	if got := config.LoadForEdit(config.UserConfigPath()).DefaultModel; got != "old/old-model" {
		t.Fatalf("default model after session switch = %q, want old/old-model", got)
	}
	if sibling.model != "old/old-model" {
		t.Fatalf("sibling tab model after session switch = %q, want old/old-model", sibling.model)
	}
	if switchTiming.Outcome != "ok" || switchTiming.Total <= 0 {
		t.Fatalf("model switch timing = %+v, want successful non-zero observation", switchTiming)
	}
	if switchTiming.Build <= 0 || switchTiming.LeaseAndResume <= 0 || switchTiming.SwapAndPersist <= 0 {
		t.Fatalf("model switch stage timing incomplete: %+v", switchTiming)
	}
}

// TestSetModelForTabRestoresSessionAuthorizations pins the fix for a model
// switch dropping same-session "Allow for this session" tool grants and
// Plan-mode read-only command trust, forcing the user to re-approve something already granted this session after every model/effort/token-mode switch.
func TestSetModelForTabRestoresSessionAuthorizations(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")
	setDesktopTestCredential(t, "NEW_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old", "new"}
	cfg.Providers = []config.ProviderEntry{
		{Name: "old", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "old-model", APIKeyEnv: "OLD_MODEL_KEY"},
		{Name: "new", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "new-model", APIKeyEnv: "NEW_MODEL_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	oldExec := agent.New(nil, nil, agent.NewSession("old system prompt"), agent.Options{}, event.Discard)
	oldPath := filepath.Join(dir, "old.jsonl")
	oldCtrl := control.New(control.Options{Executor: oldExec, SessionDir: dir, SessionPath: oldPath, Label: "old", Sink: event.Discard})
	oldCtrl.RestoreSessionAuthorizations(control.SessionAuthorizations{
		Grants:                   []string{"bash|go test ./..."},
		PlanModeReadOnlyCommands: []string{"go test ./..."},
	})

	app := NewApp()
	app.ctx = context.Background()
	tab := &WorkspaceTab{
		ID:          "tab_a",
		Scope:       "global",
		Ready:       true,
		model:       "old/old-model",
		Ctrl:        oldCtrl,
		sink:        &tabEventSink{tabID: "tab_a", app: app},
		disabledMCP: map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
	})

	if err := app.SetModelForTab(tab.ID, "new/new-model"); err != nil {
		t.Fatalf("SetModelForTab: %v", err)
	}

	newCtrl, ok := tab.Ctrl.(*control.Controller)
	if !ok {
		t.Fatalf("tab.Ctrl = %T, want *control.Controller", tab.Ctrl)
	}
	got := newCtrl.SessionAuthorizations()
	if len(got.Grants) != 1 || got.Grants[0] != "bash|go test ./..." {
		t.Fatalf("restored grants = %+v, want [\"bash|go test ./...\"]", got.Grants)
	}
	if len(got.PlanModeReadOnlyCommands) != 1 || got.PlanModeReadOnlyCommands[0] != "go test ./..." {
		t.Fatalf("restored plan-mode read-only commands = %+v, want [\"go test ./...\"]", got.PlanModeReadOnlyCommands)
	}
}

// TestRebuildSettingLockedRestoresSessionAuthorizations covers the same
// dropped-session-authorization bug for the settings-change rebuild path
// (also used by the deferred-rebuild retry loop), independent from SetModelForTab's own rebuild.
func TestRebuildSettingLockedRestoresSessionAuthorizations(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old"}
	cfg.Providers = []config.ProviderEntry{
		{Name: "old", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "old-model", APIKeyEnv: "OLD_MODEL_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	oldExec := agent.New(nil, nil, agent.NewSession("old system prompt"), agent.Options{}, event.Discard)
	oldPath := filepath.Join(dir, "old.jsonl")
	oldCtrl := control.New(control.Options{Executor: oldExec, SessionDir: dir, SessionPath: oldPath, Label: "old", Sink: event.Discard})
	oldCtrl.RestoreSessionAuthorizations(control.SessionAuthorizations{
		Grants:                   []string{"bash|go test ./..."},
		PlanModeReadOnlyCommands: []string{"go test ./..."},
	})

	app := NewApp()
	app.ctx = context.Background()
	tab := &WorkspaceTab{
		ID:          "tab_a",
		Scope:       "global",
		Ready:       true,
		model:       "old/old-model",
		Ctrl:        oldCtrl,
		sink:        &tabEventSink{tabID: "tab_a", app: app},
		disabledMCP: map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	app.readyHook = func() {}
	t.Cleanup(func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
	})

	if err := app.rebuildSetting("settings"); err != nil {
		t.Fatalf("rebuildSetting: %v", err)
	}

	newCtrl, ok := tab.Ctrl.(*control.Controller)
	if !ok {
		t.Fatalf("tab.Ctrl = %T, want *control.Controller", tab.Ctrl)
	}
	got := newCtrl.SessionAuthorizations()
	if len(got.Grants) != 1 || got.Grants[0] != "bash|go test ./..." {
		t.Fatalf("restored grants = %+v, want [\"bash|go test ./...\"]", got.Grants)
	}
	if len(got.PlanModeReadOnlyCommands) != 1 || got.PlanModeReadOnlyCommands[0] != "go test ./..." {
		t.Fatalf("restored plan-mode read-only commands = %+v, want [\"go test ./...\"]", got.PlanModeReadOnlyCommands)
	}
}

func TestSetModelForTabContinuesRecoveryPathAfterSnapshotConflict(t *testing.T) {
	isolateDesktopUserDirsSchemaOne(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")
	setDesktopTestCredential(t, "NEW_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old", "new"}
	cfg.Providers = []config.ProviderEntry{
		{Name: "old", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "old-model", APIKeyEnv: "OLD_MODEL_KEY"},
		{Name: "new", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "new-model", APIKeyEnv: "NEW_MODEL_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	originalPath := filepath.Join(dir, "model-switch-conflict.jsonl")
	current := agent.NewSession("old system prompt")
	current.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	current.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	current.Add(provider.Message{Role: provider.RoleUser, Content: "disk second"})
	if err := current.Save(originalPath); err != nil {
		t.Fatalf("save current session: %v", err)
	}

	stale := agent.NewSession("old system prompt")
	stale.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	stale.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	stale.Add(provider.Message{Role: provider.RoleUser, Content: "local second"})
	oldExec := agent.New(nil, nil, stale, agent.Options{}, event.Discard)

	app := NewApp()
	app.ctx = context.Background()
	app.runtimeEvents.emit = func(context.Context, string, ...any) {}
	tab := &WorkspaceTab{
		ID:          "tab_recovery_model",
		Scope:       "global",
		SessionPath: originalPath,
		Ready:       true,
		model:       "old/old-model",
		disabledMCP: map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	oldCtrl := control.New(control.Options{
		Executor:            oldExec,
		SessionDir:          dir,
		SessionPath:         originalPath,
		Label:               "old",
		Sink:                tab.sink,
		SessionRecoveryMeta: app.tabSessionRecoveryMeta(tab),
		OnSessionRecovered:  app.handleTabSessionRecovered(tab),
	})
	tab.Ctrl = oldCtrl
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
		tab.releaseSessionLease()
	})

	if err := app.SetModelForTab(tab.ID, "new/new-model"); err != nil {
		t.Fatalf("SetModelForTab: %v", err)
	}
	recoveryPath := tab.Ctrl.SessionPath()
	if recoveryPath == "" || recoveryPath == originalPath || !strings.Contains(filepath.Base(recoveryPath), "-recovery-") {
		t.Fatalf("model switch session path = %q, want recovery path distinct from %q", recoveryPath, originalPath)
	}
	if got := tab.currentSessionPath(); got != recoveryPath {
		t.Fatalf("tab current session path = %q, want recovery path %q", got, recoveryPath)
	}
	if tab.sessionLease == nil || sessionRuntimeKey(tab.sessionLease.Path()) != sessionRuntimeKey(recoveryPath) {
		t.Fatalf("tab lease path = %q, want recovery path %q", tab.sessionLeaseRuntimeKey(), recoveryPath)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "*-recovery-*.jsonl"))
	if err != nil {
		t.Fatalf("glob recovery branches: %v", err)
	}
	matches = primarySessionFiles(matches)
	if len(matches) != 1 || matches[0] != recoveryPath {
		t.Fatalf("recovery branches after model switch = %v, want only %q", matches, recoveryPath)
	}
	if err := tab.Ctrl.Snapshot(); err != nil {
		t.Fatalf("Snapshot after model switch recovery: %v", err)
	}
	matches, err = filepath.Glob(filepath.Join(dir, "*-recovery-*.jsonl"))
	if err != nil {
		t.Fatalf("glob recovery branches after snapshot: %v", err)
	}
	matches = primarySessionFiles(matches)
	if len(matches) != 1 || matches[0] != recoveryPath {
		t.Fatalf("recovery branches after follow-up snapshot = %v, want only %q", matches, recoveryPath)
	}
}

func TestSetModelForTabReusesCurrentSessionLease(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")
	setDesktopTestCredential(t, "NEW_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old", "new"}
	cfg.Providers = []config.ProviderEntry{
		{Name: "old", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "old-model", APIKeyEnv: "OLD_MODEL_KEY"},
		{Name: "new", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "new-model", APIKeyEnv: "NEW_MODEL_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	oldSession := agent.NewSession("old system prompt")
	oldSession.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	oldExec := agent.New(nil, nil, oldSession, agent.Options{}, event.Discard)
	oldPath := filepath.Join(dir, "leased-model-switch.jsonl")
	oldCtrl := control.New(control.Options{Executor: oldExec, SessionDir: dir, SessionPath: oldPath, Label: "old", Sink: event.Discard})

	app := NewApp()
	app.ctx = context.Background()
	tab := &WorkspaceTab{
		ID:          "tab_a",
		Scope:       "global",
		Ready:       true,
		model:       "old/old-model",
		Ctrl:        oldCtrl,
		sink:        &tabEventSink{tabID: "tab_a", app: app},
		disabledMCP: map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
		tab.releaseSessionLease()
	})

	if err := tab.ensureSessionLease(oldPath); err != nil {
		t.Fatalf("ensureSessionLease: %v", err)
	}
	if err := app.SetModelForTab(tab.ID, "new/new-model"); err != nil {
		t.Fatalf("SetModelForTab: %v", err)
	}
	if tab.Ctrl == nil || tab.Ctrl == oldCtrl {
		t.Fatalf("tab controller was not rebuilt")
	}
	if got := tab.model; got != "new/new-model" {
		t.Fatalf("tab model = %q, want new/new-model", got)
	}
	if tab.sessionLease == nil || sessionRuntimeKey(tab.sessionLease.Path()) != sessionRuntimeKey(oldPath) {
		t.Fatalf("session lease path = %q, want %q", tab.currentSessionPath(), oldPath)
	}
	history := tab.Ctrl.History()
	if len(history) < 2 || history[1].Role != provider.RoleUser || history[1].Content != "hello" {
		t.Fatalf("carried history = %+v, want original user message", history)
	}
}

func TestSetModelForTabWaitsForConcurrentBlankSessionLease(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")
	setDesktopTestCredential(t, "NEW_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old", "new"}
	cfg.Providers = []config.ProviderEntry{
		{Name: "old", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "old-model", APIKeyEnv: "OLD_MODEL_KEY"},
		{Name: "new", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "new-model", APIKeyEnv: "NEW_MODEL_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	path := filepath.Join(dir, "blank-model-switch-race.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write blank session: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()
	tab := &WorkspaceTab{
		ID:            "tab_blank_race",
		Scope:         "global",
		WorkspaceRoot: globalTabWorkspaceRoot(),
		SessionPath:   path,
		Ready:         true,
		model:         "old/old-model",
		sink:          &tabEventSink{tabID: "tab_blank_race", app: app},
		disabledMCP:   map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
		tab.releaseSessionLease()
	})

	acquired := make(chan struct{})
	releaseHook := make(chan struct{})
	var once sync.Once
	sessionLeaseAcquireHookForTest = func() {
		once.Do(func() {
			close(acquired)
			<-releaseHook
		})
	}
	t.Cleanup(func() { sessionLeaseAcquireHookForTest = nil })

	buildErr := make(chan error, 1)
	go func() {
		buildErr <- tab.ensureSessionLease(path)
	}()

	select {
	case <-acquired:
	case err := <-buildErr:
		t.Fatalf("background lease acquire returned before hook: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("background lease acquire did not start")
	}

	switchErr := make(chan error, 1)
	go func() {
		switchErr <- app.SetModelForTab(tab.ID, "new/new-model")
	}()

	select {
	case err := <-switchErr:
		t.Fatalf("SetModelForTab returned before concurrent lease was bound: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseHook)
	if err := <-buildErr; err != nil {
		t.Fatalf("background ensureSessionLease: %v", err)
	}
	if err := <-switchErr; err != nil {
		t.Fatalf("SetModelForTab: %v", err)
	}
	if tab.Ctrl == nil {
		t.Fatal("model switch did not build a controller")
	}
	if got := tab.model; got != "new/new-model" {
		t.Fatalf("tab model = %q, want new/new-model", got)
	}
	if tab.sessionLease == nil || sessionRuntimeKey(tab.sessionLease.Path()) != sessionRuntimeKey(path) {
		t.Fatalf("session lease path = %q, want %q", tab.currentSessionPath(), path)
	}
}

func TestSetModelForTabLeaseHeldKeepsCurrentController(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")
	setDesktopTestCredential(t, "NEW_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old", "new"}
	cfg.Providers = []config.ProviderEntry{
		{Name: "old", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "old-model", APIKeyEnv: "OLD_MODEL_KEY"},
		{Name: "new", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "new-model", APIKeyEnv: "NEW_MODEL_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	oldPath := filepath.Join(dir, "externally-leased-model-switch.jsonl")
	if err := os.WriteFile(oldPath, nil, 0o644); err != nil {
		t.Fatalf("write placeholder session: %v", err)
	}
	externalLease, err := agent.TryAcquireSessionLease(oldPath)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}
	defer externalLease.Release()

	oldSession := agent.NewSession("old system prompt")
	oldSession.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	oldExec := agent.New(nil, nil, oldSession, agent.Options{}, event.Discard)
	oldCtrl := control.New(control.Options{Executor: oldExec, SessionDir: dir, SessionPath: oldPath, Label: "old", Sink: event.Discard})
	defer oldCtrl.Close()

	app := NewApp()
	app.ctx = context.Background()
	tab := &WorkspaceTab{
		ID:          "tab_a",
		Scope:       "global",
		Ready:       true,
		model:       "old/old-model",
		Ctrl:        oldCtrl,
		sink:        &tabEventSink{tabID: "tab_a", app: app},
		disabledMCP: map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	err = app.SetModelForTab(tab.ID, "new/new-model")
	if !errors.Is(err, agent.ErrSessionLeaseHeld) {
		t.Fatalf("SetModelForTab err = %v, want ErrSessionLeaseHeld", err)
	}
	if strings.Contains(err.Error(), oldPath) || strings.Contains(err.Error(), "held by") {
		t.Fatalf("SetModelForTab surfaced raw lease details: %v", err)
	}
	if tab.Ctrl != oldCtrl {
		t.Fatalf("tab controller changed after failed switch")
	}
	if got := tab.model; got != "old/old-model" {
		t.Fatalf("tab model = %q, want old/old-model", got)
	}
	info, err := os.Stat(oldPath)
	if err != nil {
		t.Fatalf("stat session: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("session file size = %d, want unchanged empty file", info.Size())
	}
}

func TestSetModelForTabReattachesDetachedRuntime(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")
	setDesktopTestCredential(t, "NEW_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old", "new"}
	cfg.Providers = []config.ProviderEntry{
		{Name: "old", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "old-model", APIKeyEnv: "OLD_MODEL_KEY"},
		{Name: "new", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "new-model", APIKeyEnv: "NEW_MODEL_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, "detached-model-switch.jsonl")
	oldSession := agent.NewSession("old system prompt")
	oldSession.Add(provider.Message{Role: provider.RoleUser, Content: "hello from detached"})
	oldExec := agent.New(nil, nil, oldSession, agent.Options{}, event.Discard)
	oldCtrl := control.New(control.Options{Executor: oldExec, SessionDir: dir, SessionPath: path, Label: "old", Sink: event.Discard})
	lease, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()
	key := sessionRuntimeKey(path)
	detached := &WorkspaceTab{
		ID:             detachedRuntimeTabID(key),
		Scope:          "global",
		SessionPath:    path,
		Ctrl:           oldCtrl,
		Ready:          true,
		model:          "old/old-model",
		disabledMCP:    map[string]ServerView{},
		SharedHostKey:  "detached-host",
		ActivityStatus: "",
	}
	detached.adoptSessionLease(lease)
	tab := &WorkspaceTab{
		ID:          "tab_a",
		Scope:       "global",
		SessionPath: path,
		Ready:       true,
		model:       "old/old-model",
		sink:        &tabEventSink{tabID: "tab_a", app: app},
		disabledMCP: map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.detachedSessions = map[string]*WorkspaceTab{key: detached}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
		tab.releaseSessionLease()
		if detached.sessionLease != nil {
			detached.releaseSessionLease()
		}
	})

	if err := app.SetModelForTab(tab.ID, "new/new-model"); err != nil {
		t.Fatalf("SetModelForTab: %v", err)
	}
	if _, ok := app.detachedSessions[key]; ok {
		t.Fatal("detached runtime was not consumed")
	}
	if tab.Ctrl == nil || tab.Ctrl == oldCtrl {
		t.Fatalf("tab controller was not rebuilt from detached runtime")
	}
	if got := tab.model; got != "new/new-model" {
		t.Fatalf("tab model = %q, want new/new-model", got)
	}
	if tab.sessionLease == nil || sessionRuntimeKey(tab.sessionLease.Path()) != key {
		t.Fatalf("session lease path = %q, want %q", tab.currentSessionPath(), path)
	}
	history := tab.Ctrl.History()
	if len(history) < 2 || history[1].Content != "hello from detached" {
		t.Fatalf("carried history = %+v, want detached user message", history)
	}
}
