package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/jobs"
	"reasonix/internal/provider"
)

func assertDeprecatedExecutionModeNoop(t *testing.T, app *App, tab *WorkspaceTab, old control.SessionAPI, notices []string) {
	t.Helper()
	if tab == nil {
		t.Fatal("tab missing")
	}
	if tab.Ctrl == nil || tab.Ctrl != old {
		t.Fatalf("controller identity changed: got %p want %p", tab.Ctrl, old)
	}
	if got := old.AgentPreset(); got != boot.AgentPresetStandard {
		t.Fatalf("controller AgentPreset = %q, want standard (light folds)", got)
	}
	if got := currentTabTokenMode(tab); got != boot.TokenModeFull {
		t.Fatalf("token mode = %q, want full", got)
	}
	meta := app.MetaForTab(tab.ID)
	if meta.TokenMode != boot.TokenModeFull || meta.AgentPreset != boot.AgentPresetStandard {
		t.Fatalf("meta token/preset = %q/%q, want full/standard", meta.TokenMode, meta.AgentPreset)
	}
}

func assertSetTokenModeDidNotPersistLiveModes(t *testing.T) {
	t.Helper()
	for _, entry := range loadTabsFile().Tabs {
		if entry.TokenMode == "economy" || entry.TokenMode == "light" {
			t.Fatalf("SetTokenMode persisted folded mode %q", entry.TokenMode)
		}
		if entry.AgentPreset == "light" || entry.AgentPreset == "balanced" {
			t.Fatalf("SetTokenMode persisted non-floor preset %q", entry.AgentPreset)
		}
	}
}

func assertPinnedCompatPersisted(t *testing.T, app *App, tab *WorkspaceTab) {
	t.Helper()
	app.persistTabTokenMode(tab)
	saved := loadTabsFile()
	if len(saved.Tabs) != 1 {
		t.Fatalf("saved tabs = %+v, want 1", saved.Tabs)
	}
	if saved.Tabs[0].TokenMode != boot.TokenModeFull {
		t.Fatalf("saved compat token = %q, want full", saved.Tabs[0].TokenMode)
	}
}

func TestSetTokenModeRebuildsController(t *testing.T) {
	// Name kept for history; SetTokenMode is a deprecated no-op wrapper.
	isolateDesktopUserDirs(t)

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	old := control.New(control.Options{Label: "old-controller"})
	app.setTestCtrl(old, "deepseek-flash/deepseek-v4-flash")
	defer func() {
		if c := app.activeCtrl(); c != nil {
			c.Close()
		}
	}()
	tab := app.activeTab()
	notices := captureTabNotices(app, tab)

	if err := app.SetTokenMode("economy"); err != nil {
		t.Fatalf("SetTokenMode(economy): %v", err)
	}
	assertDeprecatedExecutionModeNoop(t, app, tab, old, *notices)
	assertSetTokenModeDidNotPersistLiveModes(t)
	assertPinnedCompatPersisted(t, app, tab)
}

func TestSetTokenModeDeliveryRebuildsAndPersistsProfile(t *testing.T) {
	// SetTokenMode(delivery) now writes the session quality floor in place.
	isolateDesktopUserDirs(t)

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	old := control.New(control.Options{Label: "old-controller"})
	app.setTestCtrl(old, "deepseek-flash/deepseek-v4-flash")
	defer func() {
		if c := app.activeCtrl(); c != nil {
			c.Close()
		}
	}()
	tab := app.activeTab()
	notices := captureTabNotices(app, tab)

	if err := app.SetTokenMode(boot.TokenModeDelivery); err != nil {
		t.Fatalf("SetTokenMode(delivery): %v", err)
	}
	if tab.Ctrl == nil || tab.Ctrl != old {
		t.Fatalf("controller identity changed: got %p want %p", tab.Ctrl, old)
	}
	if got := old.QualityFloor(); got != control.QualityFloorDelivery {
		t.Fatalf("controller QualityFloor = %q, want delivery", got)
	}
	if got := tab.qualityFloor; got != control.QualityFloorDelivery {
		t.Fatalf("tab qualityFloor = %q, want delivery", got)
	}

	if err := app.SetTokenMode(boot.TokenModeFull); err != nil {
		t.Fatalf("SetTokenMode(full): %v", err)
	}
	assertDeprecatedExecutionModeNoop(t, app, tab, old, *notices)
	assertPinnedCompatPersisted(t, app, tab)
}

func TestSetTokenModeReusesCurrentSessionLease(t *testing.T) {
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
	session := agent.NewSession("old system prompt")
	session.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	exec := agent.New(nil, nil, session, agent.Options{}, event.Discard)
	path := filepath.Join(dir, "leased-token-mode-switch.jsonl")
	oldCtrl := control.New(control.Options{Executor: exec, SessionDir: dir, SessionPath: path, Label: "old", Sink: event.Discard})

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

	if err := tab.ensureSessionLease(path); err != nil {
		t.Fatalf("ensureSessionLease: %v", err)
	}
	notices := captureTabNotices(app, tab)
	if err := app.SetTokenModeForTab(tab.ID, "economy"); err != nil {
		t.Fatalf("SetTokenModeForTab: %v", err)
	}
	assertDeprecatedExecutionModeNoop(t, app, tab, oldCtrl, *notices)
	if tab.sessionLease == nil || sessionRuntimeKey(tab.sessionLease.Path()) != sessionRuntimeKey(path) {
		t.Fatalf("session lease path = %q, want %q", tab.currentSessionPath(), path)
	}
	history := tab.Ctrl.History()
	if len(history) < 2 || history[1].Role != provider.RoleUser || history[1].Content != "hello" {
		t.Fatalf("carried history = %+v, want original user message", history)
	}
}

func TestSetTokenModeLeaseHeldKeepsCurrentController(t *testing.T) {
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
	path := filepath.Join(dir, "externally-leased-token-mode-switch.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write placeholder session: %v", err)
	}
	externalLease, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}
	defer externalLease.Release()

	session := agent.NewSession("old system prompt")
	session.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	exec := agent.New(nil, nil, session, agent.Options{}, event.Discard)
	oldCtrl := control.New(control.Options{Executor: exec, SessionDir: dir, SessionPath: path, Label: "old", Sink: event.Discard})
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

	// Deprecated wrapper must not re-acquire the session lease, so an
	// externally held lease does not block the call or replace the controller.
	notices := captureTabNotices(app, tab)
	if err := app.SetTokenModeForTab(tab.ID, "economy"); err != nil {
		t.Fatalf("SetTokenModeForTab: %v", err)
	}
	assertDeprecatedExecutionModeNoop(t, app, tab, oldCtrl, *notices)
	meta := app.MetaForTab(tab.ID)
	if !meta.Ready || meta.Runtime.Phase != sessionRuntimeReady {
		t.Fatalf("deprecated mode call disabled current runtime: ready=%v phase=%q", meta.Ready, meta.Runtime.Phase)
	}
}

func TestSetTokenModeMigratesStaleOfficialDeepSeekTabModel(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "deepseek/deepseek-v4-flash"
	cfg.Desktop.ProviderAccess = []string{"deepseek"}
	cfg.Providers = []config.ProviderEntry{{
		Name:      "deepseek",
		Kind:      "openai",
		BaseURL:   "https://api.deepseek.com",
		Model:     "glm-5",
		APIKeyEnv: "DEEPSEEK_API_KEY",
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	old := control.New(control.Options{Label: "old-controller"})
	app.setTestCtrl(old, "deepseek-flash/deepseek-v4-flash")
	defer func() {
		if c := app.activeCtrl(); c != nil {
			c.Close()
		}
	}()

	tab := app.activeTab()
	notices := captureTabNotices(app, tab)
	if err := app.SetTokenMode("economy"); err != nil {
		t.Fatalf("SetTokenMode(economy): %v", err)
	}
	if tab == nil {
		t.Fatal("active tab missing")
	}
	// SetTokenMode does not rebuild, so stale model aliases stay put
	// (migration still runs on model/effort rebuilds).
	if tab.model != "deepseek-flash/deepseek-v4-flash" {
		t.Fatalf("tab model = %q, want unchanged stale ref without rebuild", tab.model)
	}
	assertDeprecatedExecutionModeNoop(t, app, tab, old, *notices)
}

func TestMetaForTabReportsImageInputCapability(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "CUSTOM_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "custom/text-only"
	cfg.Desktop.ProviderAccess = []string{"custom"}
	cfg.Providers = []config.ProviderEntry{{
		Name:         "custom",
		Kind:         "openai",
		BaseURL:      "https://example.invalid/v1",
		APIKeyEnv:    "CUSTOM_KEY",
		Models:       []string{"text-only", "vision-pro"},
		VisionModels: []string{"vision-pro"},
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	app.setTestCtrl(control.New(control.Options{Label: "custom/text-only"}), "custom/text-only")
	defer func() {
		if c := app.activeCtrl(); c != nil {
			c.Close()
		}
	}()

	if got := app.Meta().ImageInputEnabled; got {
		t.Fatal("text-only meta should disable image input")
	}
	if err := app.SetModel("custom/vision-pro"); err != nil {
		t.Fatalf("SetModel(custom/vision-pro): %v", err)
	}
	// ImageInputEnabled is served from the per-tab cache; the model change
	// invalidates it and a background refresh repopulates it (tab:meta).
	waitForMetaImageInput(t, app, true)
}

// waitForMetaImageInput polls until the cached image-input capability reaches
// the expected value. MetaForTab serves the background-refreshed cache, so the
// value flips asynchronously after a model/settings change.
func waitForMetaImageInput(t *testing.T, app *App, want bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if app.Meta().ImageInputEnabled == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Meta().ImageInputEnabled did not become %v", want)
}

func TestMetaForTabImageInputCapabilityUsesCurrentRef(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "CUSTOM_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "custom/vision-pro"
	cfg.Desktop.ProviderAccess = []string{"custom"}
	cfg.Providers = []config.ProviderEntry{{
		Name:         "custom",
		Kind:         "openai",
		BaseURL:      "https://example.invalid/v1",
		APIKeyEnv:    "CUSTOM_KEY",
		Models:       []string{"text-only", "vision-pro"},
		VisionModels: []string{"vision-pro"},
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	app.setTestCtrl(control.New(control.Options{Label: "deleted/model"}), "deleted/model")
	defer func() {
		if c := app.activeCtrl(); c != nil {
			c.Close()
		}
	}()

	if got := app.Meta().ImageInputEnabled; got {
		t.Fatal("unknown model ref should not inherit image input from the default fallback model")
	}
}

func TestSetTokenModeKeepsControllerWhenRebuildFails(t *testing.T) {
	// Name kept for history; an unknown model must not block the deprecated no-op.
	isolateDesktopUserDirs(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("MIMO_API_KEY", "")

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	old := control.New(control.Options{Label: "old-controller"})
	app.setTestCtrl(old, "missing-token-mode-model")
	defer func() {
		if c := app.activeCtrl(); c != nil {
			c.Close()
		}
	}()
	tab := app.activeTab()
	notices := captureTabNotices(app, tab)

	if err := app.SetTokenMode("economy"); err != nil {
		t.Fatalf("SetTokenMode(economy): %v", err)
	}
	assertDeprecatedExecutionModeNoop(t, app, tab, old, *notices)
}

func TestSetEffortRejectsRunningTurn(t *testing.T) {
	isolateDesktopUserDirs(t)

	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Runner: runner}), "")
	app.activeCtrl().Submit("work")
	<-runner.started

	err := app.SetEffort("max")
	if err == nil || !strings.Contains(err.Error(), "finish or cancel") {
		t.Fatalf("SetEffort while running error = %v, want finish/cancel guard", err)
	}

	close(runner.release)
	waitNotRunning(t, app.activeCtrl())
}

func TestSetTokenModeRejectsRunningTurn(t *testing.T) {
	// Name kept for history; the deprecated wrapper does not require an idle tab.
	isolateDesktopUserDirs(t)

	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	app := NewApp()
	old := control.New(control.Options{Runner: runner})
	app.setTestCtrl(old, "")
	tab := app.activeTab()
	notices := captureTabNotices(app, tab)
	old.Submit("work")
	<-runner.started

	if err := app.SetTokenMode("economy"); err != nil {
		t.Fatalf("SetTokenMode while running: %v", err)
	}
	assertDeprecatedExecutionModeNoop(t, app, tab, old, *notices)

	close(runner.release)
	waitNotRunning(t, app.activeCtrl())
}

func TestSetTokenModeRejectsBackgroundJobs(t *testing.T) {
	// Name kept for history; background jobs must not block the deprecated wrapper.
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
	path := filepath.Join(dir, "jobs.jsonl")
	jm := jobs.NewManager(event.Discard)
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: path, Label: "test", Jobs: jm})
	app := NewApp()
	app.ctx = context.Background()
	app.setTestCtrl(ctrl, "old/old-model")
	t.Cleanup(func() {
		if current := app.activeCtrl(); current != nil {
			current.Close()
		}
	})
	tab := app.activeTab()
	notices := captureTabNotices(app, tab)

	release := make(chan struct{})
	job := jm.StartForSession(agent.BranchID(path), "bash", "long job", func(ctx context.Context, _ io.Writer) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-release:
			return "", nil
		}
	})
	t.Cleanup(func() { close(release) })

	if err := app.SetTokenMode("economy"); err != nil {
		t.Fatalf("SetTokenMode with background job: %v", err)
	}
	assertDeprecatedExecutionModeNoop(t, app, tab, ctrl, *notices)
	cancelled, err := app.CancelJobForTab("", job.ID)
	if err != nil || !cancelled {
		t.Fatalf("CancelJobForTab = %v, %v, want true, nil", cancelled, err)
	}
	if result := jm.WaitForSession(context.Background(), agent.BranchID(path), []string{job.ID}, 5); len(result) != 1 || result[0].Status != jobs.Killed {
		t.Fatalf("stopped background job = %+v, want one killed result", result)
	}
}

func TestSetTokenModeUnknownTabErrors(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	err := app.SetTokenModeForTab("missing-tab", "economy")
	if err == nil || !strings.Contains(err.Error(), `tab "missing-tab" not found`) {
		t.Fatalf("SetTokenModeForTab(unknown) = %v, want tab not found", err)
	}
	err = app.SetAgentPresetForTab("missing-tab", "light")
	if err == nil || !strings.Contains(err.Error(), `tab "missing-tab" not found`) {
		t.Fatalf("SetAgentPresetForTab(unknown) = %v, want tab not found", err)
	}
}

func TestSettingsRebuildRejectsBackgroundJobs(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, "settings-job.jsonl")
	jm := jobs.NewManager(event.Discard)
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: path, Label: "test", Jobs: jm})
	defer ctrl.Close()
	app := NewApp()
	app.ctx = context.Background()
	app.setTestCtrl(ctrl, "deepseek-flash/deepseek-v4-flash")

	jm.StartForSession(agent.BranchID(path), "bash", "settings job", func(ctx context.Context, _ io.Writer) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})

	err := app.SetSandbox("enforce", true, "", nil, "")
	if err == nil || !strings.Contains(err.Error(), "stop background jobs") {
		t.Fatalf("SetSandbox with background job error = %v, want background-job guard", err)
	}
}

func TestClearSessionCancelsRunningRuntimeAndKeepsTopic(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, "clear-running.jsonl")
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"old"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	oldCtrl := control.New(control.Options{Runner: runner, SessionDir: dir, SessionPath: path, Label: "test"})
	app := NewApp()
	app.projectTreeChangedHook = func() {}
	app.setTestCtrl(oldCtrl, "deepseek-flash/deepseek-v4-flash")
	app.tabs["test"].TopicID = "topic_clear"
	app.tabs["test"].TopicTitle = "Clear topic"
	defer func() {
		if c := app.activeCtrl(); c != nil {
			c.Close()
		}
	}()

	oldCtrl.Submit("work")
	<-runner.started
	if _, err := app.ClearSession(); err != nil {
		t.Fatalf("ClearSession: %v", err)
	}
	waitNotRunning(t, oldCtrl)
	tab := app.activeTab()
	if tab == nil || tab.Ctrl == nil {
		t.Fatalf("active tab/controller missing after clear")
	}
	if tab.Ctrl == oldCtrl {
		t.Fatalf("clear should replace the active controller after cancelling old work")
	}
	if tab.TopicID != "topic_clear" || tab.TopicTitle != "Clear topic" {
		t.Fatalf("clear changed topic identity: %+v", tab)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("old cleared session artifacts should be removed, stat err = %v", err)
	}
	if got := tab.currentSessionPath(); got == "" || got == path {
		t.Fatalf("new session path = %q, want fresh path", got)
	}
}

func TestClearSessionRemovesRunningJobArtifacts(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, "clear-running-job.jsonl")
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"old"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	jm := jobs.NewManager(event.Discard)
	oldCtrl := control.New(control.Options{SessionDir: dir, SessionPath: path, Label: "test", Jobs: jm})
	app := NewApp()
	app.projectTreeChangedHook = func() {}
	app.setTestCtrl(oldCtrl, "deepseek-flash/deepseek-v4-flash")
	defer func() {
		if c := app.activeCtrl(); c != nil {
			c.Close()
		}
	}()

	started := make(chan struct{})
	jm.StartForSession(agent.BranchID(path), "bash", "clear artifact", func(ctx context.Context, _ io.Writer) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	})
	<-started
	jobsDir := jobs.ArtifactDir(path)
	if _, err := os.Stat(jobsDir); err != nil {
		t.Fatalf("job sidecar should exist before clear: %v", err)
	}

	if _, err := app.ClearSession(); err != nil {
		t.Fatalf("ClearSession: %v", err)
	}
	if _, err := os.Stat(jobsDir); !os.IsNotExist(err) {
		t.Fatalf("old job sidecar should be removed after clear, stat err = %v", err)
	}
}

func TestSearchFileRefsFindsNestedBasename(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := robustTempDir(t)
	if err := os.MkdirAll(filepath.Join(dir, "frontend", "wailsjs", "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "frontend", "wailsjs", "runtime", "runtime.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "frontend", "Thumbs.db"), []byte("noise"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "frontend", ".DS_Store"), []byte("noise"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "runtime.js"), []byte("noise"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, noise := range []string{".codex", ".npm", ".pnpm-store", "bin", "dist", "stage", "tmp"} {
		if err := os.MkdirAll(filepath.Join(dir, noise), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, noise, "runtime.js"), []byte("noise"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "desktop", "frontend", "wailsjs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "desktop", "frontend", "wailsjs", "runtime.js"), []byte("generated"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "product", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "product", "bin", "runtime.js"), []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	app := &App{}
	listed := app.ListDir("")
	for _, hidden := range []string{".codex", ".npm", ".pnpm-store", "dist"} {
		if hasDirEntry(listed, hidden) {
			t.Fatalf("ListDir should hide local noise %q, got %+v", hidden, listed)
		}
	}
	// #10006 / task 37: the panel reflects the real disk layout, so generic
	// top-level names stay visible even though @-search still skips them.
	for _, visible := range []string{"bin", "stage", "tmp"} {
		if !hasDirEntry(listed, visible) {
			t.Fatalf("ListDir must show %q after #10006, got %+v", visible, listed)
		}
	}
	desktopFrontend := app.ListDir("desktop/frontend")
	if hasDirEntry(desktopFrontend, "wailsjs") {
		t.Fatalf("ListDir should hide generated Wails bindings, got %+v", desktopFrontend)
	}
	frontendEntries := app.ListDir("frontend")
	for _, hidden := range []string{".DS_Store", "Thumbs.db"} {
		if hasDirEntry(frontendEntries, hidden) {
			t.Fatalf("ListDir should hide local noise file %q, got %+v", hidden, frontendEntries)
		}
	}

	got := app.SearchFileRefs("runtime.js")
	if !hasDirEntry(got, "frontend/wailsjs/runtime/runtime.js") {
		t.Fatalf("SearchFileRefs(runtime.js) should find nested workspace file, got %+v", got)
	}
	if !hasDirEntry(got, "product/bin/runtime.js") {
		t.Fatalf("SearchFileRefs should keep non-root bin directories searchable, got %+v", got)
	}
	if hasDirEntry(got, "node_modules/pkg/runtime.js") {
		t.Fatalf("SearchFileRefs should skip node_modules noise, got %+v", got)
	}
	for _, hidden := range []string{
		".codex/runtime.js",
		".npm/runtime.js",
		".pnpm-store/runtime.js",
		"bin/runtime.js",
		"desktop/frontend/wailsjs/runtime.js",
		"dist/runtime.js",
		"stage/runtime.js",
		"tmp/runtime.js",
	} {
		if hasDirEntry(got, hidden) {
			t.Fatalf("SearchFileRefs should skip local noise %q, got %+v", hidden, got)
		}
	}
	if noise := app.SearchFileRefs("Thumbs"); hasDirEntry(noise, "frontend/Thumbs.db") {
		t.Fatalf("SearchFileRefs should skip Thumbs.db noise, got %+v", noise)
	}
	if noise := app.SearchFileRefs(".DS"); hasDirEntry(noise, "frontend/.DS_Store") {
		t.Fatalf("SearchFileRefs should skip .DS_Store noise even for dot-prefixed search, got %+v", noise)
	}
}
