package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/store"
)

func TestDeferredRebuildAppliesToItsInactiveTarget(t *testing.T) {
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
	sessionPath := filepath.Join(dir, "deferred-rebuild-inactive.jsonl")
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

	otherCtrl := control.New(control.Options{Label: "other"})
	defer otherCtrl.Close()

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	tab := &WorkspaceTab{
		ID:          "tab_pending",
		Scope:       "global",
		SessionPath: sessionPath,
		Ready:       true,
		model:       "old/old-model",
		Ctrl:        oldCtrl,
		sink:        &tabEventSink{tabID: "tab_pending", app: app},
		disabledMCP: map[string]ServerView{},
	}
	installNoopRuntimeEvents(app, tab.sink)
	other := &WorkspaceTab{
		ID:          "tab_other",
		Scope:       "global",
		Ready:       true,
		model:       "old/old-model",
		Ctrl:        otherCtrl,
		sink:        &tabEventSink{tabID: "tab_other", app: app},
		disabledMCP: map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab, other.ID: other}
	app.tabOrder = []string{tab.ID, other.ID}
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

	// A concrete target retains its rebuild ownership when focus moves.
	app.mu.Lock()
	app.activeTabID = other.ID
	app.mu.Unlock()
	externalLease.Release()
	released = true
	app.deferredRebuildTick(false)
	if app.deferredRebuildPending(tab.ID) || app.controllerForTab(tab) == oldCtrl {
		t.Fatal("inactive target was not rebuilt")
	}
	if app.controllerForTab(other) != otherCtrl {
		t.Fatal("retry rebuilt the focused sibling")
	}
}

func TestSetEffortForTabLeaseHeldKeepsOldControllerAlive(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old"}
	cfg.Providers = []config.ProviderEntry{{
		Name:             "old",
		Kind:             "openai",
		BaseURL:          "https://example.invalid/v1",
		Model:            "old-model",
		APIKeyEnv:        "OLD_MODEL_KEY",
		SupportedEfforts: []string{"low", "max"},
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	sessionPath := filepath.Join(dir, "externally-leased-effort-switch.jsonl")
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
	tab := &WorkspaceTab{
		ID:          "tab_effort",
		Scope:       "global",
		SessionPath: sessionPath,
		Ready:       true,
		model:       "old/old-model",
		Ctrl:        oldCtrl,
		sink:        &tabEventSink{tabID: "tab_effort", app: app},
		disabledMCP: map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		if c := app.controllerForTab(tab); c != nil && c != oldCtrl {
			c.Close()
		}
		tab.releaseSessionLease()
	})

	err = app.SetEffortForTab(tab.ID, "max")
	if !errors.Is(err, agent.ErrSessionLeaseHeld) {
		t.Fatalf("SetEffortForTab err = %v, want ErrSessionLeaseHeld", err)
	}
	if strings.Contains(err.Error(), sessionPath) || strings.Contains(err.Error(), "held by") {
		t.Fatalf("SetEffortForTab surfaced raw lease details: %v", err)
	}
	if tab.Ctrl != oldCtrl {
		t.Fatal("tab controller changed after failed effort switch")
	}

	// The failed switch must leave the old runtime alive: after the other
	// window releases the lease, retrying from the same tab has to succeed.
	// (The old code closed the old controller before acquiring the lease, so this retry died on a snapshot of a closed session.)
	externalLease.Release()
	released = true
	if err := app.SetEffortForTab(tab.ID, "max"); err != nil {
		t.Fatalf("SetEffortForTab retry after lease release: %v", err)
	}
	if tab.Ctrl == oldCtrl {
		t.Fatal("retry did not rebuild the controller")
	}
}

func TestSetEffortForTabReanchorsDepthCapRecoveryBranch(t *testing.T) {
	isolateDesktopUserDirsSchemaOne(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old"}
	cfg.Providers = []config.ProviderEntry{{
		Name:             "old",
		Kind:             "openai",
		BaseURL:          "https://example.invalid/v1",
		Model:            "old-model",
		APIKeyEnv:        "OLD_MODEL_KEY",
		SupportedEfforts: []string{"low", "max"},
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	recoveryPath := filepath.Join(dir, "effort-switch-conflict-recovery-deadbeef.jsonl")
	disk := agent.NewSession("old system prompt")
	disk.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	disk.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	disk.Add(provider.Message{Role: provider.RoleUser, Content: "disk second"})
	if err := disk.Save(recoveryPath); err != nil {
		t.Fatalf("save recovery branch: %v", err)
	}
	meta, ok, err := agent.LoadBranchMeta(recoveryPath)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta ok=%v err=%v", ok, err)
	}
	meta.Recovered = true
	meta.ParentID = "effort-switch-conflict"
	meta.RecoveryReason = "snapshot conflict"
	meta.RecoveryDepth = agent.SessionRecoveryMaxDepth
	if err := agent.SaveBranchMeta(recoveryPath, meta); err != nil {
		t.Fatalf("SaveBranchMeta: %v", err)
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
		ID:          "tab_depth_cap_effort",
		Scope:       "global",
		SessionPath: recoveryPath,
		Ready:       true,
		model:       "old/old-model",
		disabledMCP: map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	oldCtrl := control.New(control.Options{
		Executor:            oldExec,
		SessionDir:          dir,
		SessionPath:         recoveryPath,
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
	stale.IncrementRewrite()

	if err := app.SetEffortForTab(tab.ID, "max"); err != nil {
		t.Fatalf("SetEffortForTab: %v", err)
	}
	isolatedPath := tab.Ctrl.SessionPath()
	if isolatedPath == recoveryPath || !strings.Contains(isolatedPath, "-recovery-") {
		t.Fatalf("session path after effort switch = %q, want an isolated recovery branch", isolatedPath)
	}
	if got := tab.currentSessionPath(); got != isolatedPath {
		t.Fatalf("tab current session path = %q, want %q", got, isolatedPath)
	}
	if tab.sessionLease == nil || sessionRuntimeKey(tab.sessionLease.Path()) != sessionRuntimeKey(isolatedPath) {
		t.Fatalf("tab lease path = %q, want %q", tab.sessionLeaseRuntimeKey(), isolatedPath)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*-recovery-*.jsonl"))
	if err != nil {
		t.Fatalf("glob recovery branches: %v", err)
	}
	matches = primarySessionFiles(matches)
	if len(matches) != 2 || !slices.Contains(matches, recoveryPath) || !slices.Contains(matches, isolatedPath) {
		t.Fatalf("recovery branches after effort switch = %v, want canonical and isolated paths", matches)
	}

	lines := readConflictLogLines(t, store.SessionConflictLog(recoveryPath))
	if len(lines) != 1 {
		t.Fatalf("conflict log lines = %v, want one recovery diagnostic", lines)
	}
	if !strings.Contains(lines[0], `"outcome":"forked_recovery_branch"`) {
		t.Fatalf("conflict diagnostic = %s, want stable recovery fork", lines[0])
	}
	if strings.Contains(lines[0], dir) || strings.Contains(lines[0], recoveryPath) {
		t.Fatalf("conflict diagnostic leaked local path: %s", lines[0])
	}

	if err := tab.Ctrl.Snapshot(); err != nil {
		t.Fatalf("Snapshot after effort switch recovery: %v", err)
	}
	afterLines := readConflictLogLines(t, store.SessionConflictLog(recoveryPath))
	if len(afterLines) != len(lines) {
		t.Fatalf("follow-up snapshot appended conflict diagnostics: before=%v after=%v", lines, afterLines)
	}
	matches, err = filepath.Glob(filepath.Join(dir, "*-recovery-*.jsonl"))
	if err != nil {
		t.Fatalf("glob recovery branches after snapshot: %v", err)
	}
	matches = primarySessionFiles(matches)
	if len(matches) != 2 || !slices.Contains(matches, recoveryPath) || !slices.Contains(matches, isolatedPath) {
		t.Fatalf("recovery branches after follow-up snapshot = %v, want canonical and isolated paths", matches)
	}
}

func TestSetEffortForTabSameLevelShortCircuit(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old"}
	cfg.Providers = []config.ProviderEntry{{
		Name:             "old",
		Kind:             "openai",
		BaseURL:          "https://example.invalid/v1",
		Model:            "old-model",
		APIKeyEnv:        "OLD_MODEL_KEY",
		SupportedEfforts: []string{"low", "max"},
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	exec := agent.New(nil, nil, agent.NewSession("old system prompt"), agent.Options{}, event.Discard)

	app := NewApp()
	app.ctx = context.Background()
	app.runtimeEvents.emit = func(context.Context, string, ...any) {}
	tab := &WorkspaceTab{
		ID:          "tab_effort_short_circuit",
		Scope:       "global",
		Ready:       true,
		model:       "old/old-model",
		disabledMCP: map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	tab.Ctrl = control.New(control.Options{
		Executor:    exec,
		SessionDir:  dir,
		SessionPath: filepath.Join(dir, "effort-short-circuit.jsonl"),
		Label:       "old",
		Sink:        tab.sink,
	})
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		tab.Ctrl.Close()
		tab.releaseSessionLease()
	})

	if err := app.SetEffortForTab(tab.ID, "max"); err != nil {
		t.Fatalf("SetEffortForTab max: %v", err)
	}
	if tab.effort == nil || *tab.effort != "max" {
		t.Fatalf("tab effort after switch = %v, want max", tab.effort)
	}
	// Let any post-rebuild async settle so the short-circuit assertion below
	// compares against a quiescent tab.
	time.Sleep(500 * time.Millisecond)
	rebuilt := tab.Ctrl
	t.Logf("before-second: ctrl=%p rebuilt=%p", tab.Ctrl, rebuilt)

	// Same level again: the short circuit must keep the running controller
	// instead of paying for a full runtime rebuild.
	if err := app.SetEffortForTab(tab.ID, "max"); err != nil {
		t.Fatalf("SetEffortForTab same level: %v", err)
	}
	t.Logf("after-second: ctrl=%p rebuilt=%p tab.effort=%v", tab.Ctrl, rebuilt, tab.effort)
	if tab.Ctrl != rebuilt {
		t.Fatal("same-level effort switch rebuilt the controller")
	}

	// A genuinely different depth takes the per-request fast path when the
	// provider varies effort per call: the running controller is kept (no
	// rebuild) and the tab's depth follows. Task 334: this assertion predates 866's fast path — "still rebuilds" described a world without it; the fallback direction (provider without a per-request vocabulary, recovery fork) is covered by the rebuild tests and agent-side decline logs.
	if err := app.SetEffortForTab(tab.ID, "low"); err != nil {
		t.Fatalf("SetEffortForTab low: %v", err)
	}
	if tab.Ctrl != rebuilt {
		t.Fatal("different-level effort switch rebuilt the controller despite a per-request-capable provider")
	}
	if tab.effort == nil || *tab.effort != "low" {
		t.Fatalf("tab effort after different-level switch = %v, want low", tab.effort)
	}
}

// Task 354: a stock MiMo entry (no supported_efforts configured) must switch
// medium/high over the per-request fast path — the probe now reports the
// canonical four-level set NormalizeEffort emits for MiMo, which the 1855 installed log showed being declined as level-not-in-vocabulary (25-26s full rebuild per switch). The third segment pins the fail-fast for a level the provider never offered: usage error, no rebuild.
func TestSetEffortForTabMiMoVocabularyFastPath(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "MIMO_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "mimo/mimo-v2.6-flash"
	cfg.Desktop.ProviderAccess = []string{"mimo"}
	cfg.Providers = []config.ProviderEntry{{
		Name:      "mimo",
		Kind:      "openai",
		BaseURL:   "https://api.xiaomimimo.com/v1",
		Model:     "mimo-v2.6-flash",
		APIKeyEnv: "MIMO_KEY",
		// no SupportedEfforts: the 1855 log entry had none, which is
		// exactly the configuration that used to decline every override.
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	exec := agent.New(nil, nil, agent.NewSession("mimo system prompt"), agent.Options{}, event.Discard)

	app := NewApp()
	app.ctx = context.Background()
	app.runtimeEvents.emit = func(context.Context, string, ...any) {}
	tab := &WorkspaceTab{
		ID:          "tab_effort_mimo_fast",
		Scope:       "global",
		Ready:       true,
		model:       "mimo/mimo-v2.6-flash",
		disabledMCP: map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	tab.Ctrl = control.New(control.Options{
		Executor:    exec,
		SessionDir:  dir,
		SessionPath: filepath.Join(dir, "effort-mimo-fast.jsonl"),
		Label:       "mimo",
		Sink:        tab.sink,
	})
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		tab.Ctrl.Close()
		tab.releaseSessionLease()
	})

	// First switch builds the real controller (boot-time provider wiring),
	// mirroring the first phase of TestSetEffortForTabSameLevelShortCircuit.
	if err := app.SetEffortForTab(tab.ID, "medium"); err != nil {
		t.Fatalf("SetEffortForTab medium: %v", err)
	}
	if tab.effort == nil || *tab.effort != "medium" {
		t.Fatalf("tab effort after medium switch = %v, want medium", tab.effort)
	}
	time.Sleep(500 * time.Millisecond)
	built := tab.Ctrl

	// Medium→high on the real MiMo client: the four-level probe admits it,
	// so the switch must not touch the controller (no runtime build).
	if err := app.SetEffortForTab(tab.ID, "high"); err != nil {
		t.Fatalf("SetEffortForTab high: %v", err)
	}
	if tab.Ctrl != built {
		t.Fatal("mimo high switch rebuilt the controller despite the four-level request vocabulary")
	}
	if tab.effort == nil || *tab.effort != "high" {
		t.Fatalf("tab effort after high switch = %v, want high", tab.effort)
	}

	// A level outside the provider's input vocabulary is a usage error: fail
	// fast with no rebuild (task 354's second acceptance leg).
	if err := app.SetEffortForTab(tab.ID, "nonexistent"); err == nil {
		t.Fatal("unsupported effort level succeeded, want usage error")
	}
	if tab.Ctrl != built {
		t.Fatal("unsupported effort level rebuilt the controller; want fail-fast with the old controller kept")
	}
	if tab.effort == nil || *tab.effort != "high" {
		t.Fatalf("tab effort after failed switch = %v, want high (unchanged)", tab.effort)
	}
}

func TestRemoveBuiltInProviderAccessRetargetsDefaultToRemainingAccess(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "MIMO_API_KEY", "sk-test")
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
default_model = "deepseek-flash/deepseek-v4-pro"

[desktop]
provider_access = ["deepseek-flash", "mimo-pro"]

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

	if err := NewApp().RemoveProviderAccess("deepseek"); err != nil {
		t.Fatalf("RemoveProviderAccess: %v", err)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	access := providerAccessSet(cfg.Desktop.ProviderAccess)
	if access["deepseek"] || !access["mimo-pro"] {
		t.Fatalf("provider_access = %+v, want only mimo-pro", cfg.Desktop.ProviderAccess)
	}
	if cfg.DefaultModel != "mimo-pro/mimo-v2.5-pro" {
		t.Fatalf("default_model = %q, want mimo-pro/mimo-v2.5-pro", cfg.DefaultModel)
	}
}

func TestModelsForTabOnlyListsProviderAccessWhenConfigured(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	setDesktopTestCredential(t, "MIMO_API_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "deepseek/deepseek-v4-flash"
	cfg.Desktop.ProviderAccess = []string{"deepseek", "mimo-pro"}
	cfg.Providers = append(cfg.Providers, config.ProviderEntry{
		Name: "deepseek", Kind: "anthropic", BaseURL: "https://api.deepseek.com/anthropic",
		Models: []string{"deepseek-v4-flash", "deepseek-v4-pro"}, Default: "deepseek-v4-flash", APIKeyEnv: "DEEPSEEK_API_KEY",
	})
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	models := NewApp().Models()
	refs := modelRefsFromView(models)
	for _, want := range []string{
		"deepseek/deepseek-v4-flash",
		"deepseek/deepseek-v4-pro",
		"mimo-pro/mimo-v2.5-pro",
		"mimo-pro/mimo-v2.5",
	} {
		if !refs[want] {
			t.Fatalf("Models() refs = %+v, missing %s", models, want)
		}
	}
	for _, hidden := range []string{
		"deepseek-pro/deepseek-v4-pro",
		"mimo-flash/mimo-v2.5",
	} {
		if refs[hidden] {
			t.Fatalf("Models() refs = %+v, should not include hidden provider %s", models, hidden)
		}
	}
	if len(models) != 5 {
		t.Fatalf("Models() len = %d, want 5: %+v", len(models), models)
	}
}

func TestModelsForTabListsNothingWhenProviderAccessExplicitlyEmpty(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")

	cfg := config.Default()
	cfg.Desktop.ProviderAccess = []string{}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	if models := NewApp().Models(); len(models) != 0 {
		t.Fatalf("Models() = %+v, want no models when provider access is explicitly empty", models)
	}
}

func TestModelsForTabListsCustomMultiModelProviderWithoutMetadata(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "LOCAL_API_KEY", "sk-test")
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
default_model = "local/model-a"

[desktop]
provider_access = ["local"]

[[providers]]
name = "local"
kind = "openai"
base_url = "http://127.0.0.1:23333/v1"
models = ["model-a", "model-b"]
default = "model-a"
api_key_env = "LOCAL_API_KEY"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	models := NewApp().Models()
	refs := modelRefsFromView(models)
	for _, want := range []string{"local/model-a", "local/model-b"} {
		if !refs[want] {
			t.Fatalf("Models() refs = %+v, missing %s", models, want)
		}
	}
	if len(models) != 2 {
		t.Fatalf("Models() len = %d, want 2: %+v", len(models), models)
	}
}

func TestModelsForTabListsKeylessCustomMultiModelProvider(t *testing.T) {
	isolateDesktopUserDirs(t)
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
default_model = "local/model-a"

[desktop]
provider_access = ["local"]

[[providers]]
name = "local"
kind = "openai"
base_url = "http://127.0.0.1:23333/v1"
models = ["model-a", "model-b"]
default = "model-a"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	models := NewApp().Models()
	refs := modelRefsFromView(models)
	for _, want := range []string{"local/model-a", "local/model-b"} {
		if !refs[want] {
			t.Fatalf("Models() refs = %+v, missing %s", models, want)
		}
	}
}

func TestModelsForTabListsLoopbackCustomProviderWithMissingKeyEnv(t *testing.T) {
	isolateDesktopUserDirs(t)
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
default_model = "local/model-a"

[desktop]
provider_access = ["local"]

[[providers]]
name = "local"
kind = "openai"
base_url = "http://127.0.0.1:23333/v1"
models = ["model-a", "model-b"]
default = "model-a"
api_key_env = "LOCAL_API_KEY"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	models := NewApp().Models()
	refs := modelRefsFromView(models)
	for _, want := range []string{"local/model-a", "local/model-b"} {
		if !refs[want] {
			t.Fatalf("Models() refs = %+v, missing %s", models, want)
		}
	}
}

func TestModelsForTabListsMimoAPIPaidAccess(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "MIMO_API_KEY", "sk-test")

	cfg := config.Default()
	preset, ok := config.CuratedProviderPreset("mimo-api")
	if !ok || len(preset.Entries) == 0 {
		t.Fatal("mimo-api preset missing")
	}
	if err := cfg.UpsertProvider(preset.Entries[0]); err != nil {
		t.Fatalf("upsert mimo-api preset: %v", err)
	}
	cfg.DefaultModel = "mimo-api/mimo-v2.5-pro"
	cfg.Desktop.ProviderAccess = []string{"mimo-api"}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	models := NewApp().Models()
	refs := modelRefsFromView(models)
	for _, want := range []string{
		"mimo-api/mimo-v2.5-pro",
		"mimo-api/mimo-v2.5",
	} {
		if !refs[want] {
			t.Fatalf("Models() refs = %+v, missing %s", models, want)
		}
	}
	if len(models) != 2 {
		t.Fatalf("Models() len = %d, want 2: %+v", len(models), models)
	}
}
