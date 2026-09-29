package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// Task 382 acceptance: 「保存成功即 UI 成功」— once the config landed on disk,
// a post-save rebuild failure must downgrade to a visible warning (busy →
// deferred replay, hard failure → restart hint) instead of bouncing the form
// back as if the write failed. The pre-save guard is unchanged: a streaming
// turn still refuses the edit BEFORE anything is written.

type settingsSaveFixture struct {
	app *App
	tab *WorkspaceTab
}

// newSettingsSaveFixture builds an App with one active tab carrying a real
// controller. withProviders=false leaves the config with no resolvable model,
// so every boot build fails (the hard-failure injection used by 385b).
func newSettingsSaveFixture(t *testing.T, withProviders bool) *settingsSaveFixture {
	t.Helper()
	isolateDesktopUserDirs(t)
	cfg := config.Default()
	if withProviders {
		setDesktopTestCredential(t, "SAVE_TEST_KEY", "sk-test")
		cfg.DefaultModel = "old/old-model"
		cfg.Desktop.ProviderAccess = []string{"old"}
		cfg.Providers = []config.ProviderEntry{
			{Name: "old", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "old-model", APIKeyEnv: "SAVE_TEST_KEY"},
		}
	} else {
		cfg.DefaultModel = ""
		cfg.Providers = nil
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	path := filepath.Join(dir, "settings-save-resilience.jsonl")
	ctrl := control.New(control.Options{Executor: exec, SessionDir: dir, SessionPath: path, Label: "old", Sink: event.Discard})

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	tab := &WorkspaceTab{
		ID:            "tab_settings_save",
		Scope:         "global",
		WorkspaceRoot: globalTabWorkspaceRoot(),
		Ready:         true,
		model:         "old/old-model",
		Ctrl:          ctrl,
		sink:          &tabEventSink{tabID: "tab_settings_save", app: app, ctx: context.Background()},
		disabledMCP:   map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() { ctrl.Close() })
	return &settingsSaveFixture{app: app, tab: tab}
}

func (f *settingsSaveFixture) persisted(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.LoadForEditReadOnlyStrict(config.UserConfigPath())
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	return cfg
}

// TestPostSaveHardFailureWarnsWithRestartHint pins the restart-hint channel:
// the save lands, the rebuild fails (no resolvable model), the caller gets a
// warning — never an error — and the warning names the restart path.
func TestPostSaveHardFailureWarnsWithRestartHint(t *testing.T) {
	f := newSettingsSaveFixture(t, false)

	warning, err := f.app.applyConfigChangeWithWarning("context compaction threshold", func(c *config.Config) error {
		return c.SetCompactRatio(0.42)
	})
	if err != nil {
		t.Fatalf("a post-save rebuild failure must not bounce back: %v", err)
	}
	if warning == "" {
		t.Fatal("the downgrade must produce a visible warning")
	}
	if !contains(warning, "restart the desktop to apply") {
		t.Fatalf("warning must carry the restart hint, got %q", warning)
	}
	if !contains(warning, "context compaction threshold") {
		t.Fatalf("warning must name the setting, got %q", warning)
	}
	if got := f.persisted(t).Agent.CompactRatio; got != 0.42 {
		t.Fatalf("the save must stand: CompactRatio = %v, want 0.42", got)
	}
}

// TestPostSaveBusyDefersReplay pins the busy downgrade (the mid-save race
// window): the queue takes the request and the warning promises the end of the
// turn — no error reaches the form.
func TestPostSaveBusyDefersReplay(t *testing.T) {
	f := newSettingsSaveFixture(t, true)

	busyErr := &rebuildBusyError{setting: "sandbox", work: activeRuntimeWork{running: true}}
	warning, downgraded := f.app.downgradePostSaveRebuildFailure("sandbox", busyErr)
	if !downgraded {
		t.Fatal("a busy rebuild after save must downgrade")
	}
	if !contains(warning, "current turn") {
		t.Fatalf("busy warning must defer to the turn, got %q", warning)
	}
	if !f.app.deferredRebuildPending(f.tab.ID) {
		t.Fatal("the refresh must be queued for replay after the turn")
	}
}

// TestPreSaveGuardStillRefusesDuringStreaming pins the unchanged pre-save
// guard: during a running turn the edit is refused BEFORE anything is written,
// so the error there is honest (nothing bounced, nothing silently dropped).
func TestPreSaveGuardStillRefusesDuringStreaming(t *testing.T) {
	f := newSettingsSaveFixture(t, true)
	streaming := &retargetRuntimeController{
		path:   filepath.Join(config.SessionDir(), "settings-save-streaming.jsonl"),
		status: control.RuntimeStatus{Running: true},
	}
	f.tab.Ctrl = streaming

	err := f.app.SetPermissionMode("allow")
	if err == nil {
		t.Fatal("a streaming turn must still refuse the edit up front")
	}
	if !contains(err.Error(), "active work") {
		t.Fatalf("the refusal must say the turn is in the way, got %q", err.Error())
	}
	if got := f.persisted(t).Permissions.Mode; got == "allow" {
		t.Fatalf("nothing may be written when the pre-save guard refuses (mode=%q)", got)
	}
}

// TestMutateFailureStillErrors pins the non-downgraded path: a config mutation
// that fails is a REAL save failure — no save landed, so it must surface as an
// error (the downgrade only covers post-save rebuild failures).
func TestMutateFailureStillErrors(t *testing.T) {
	f := newSettingsSaveFixture(t, true)

	boom := errors.New("mutation boom")
	_, err := f.app.applyConfigChangeWithWarning("settings", func(*config.Config) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("mutate failures must surface unchanged, got %v", err)
	}
}

// TestHealthySaveStillAppliesInstantly pins the no-regression path: with a
// buildable config the round trip stays silent success — save, rebuild, read
// back — exactly as before the downgrade work.
func TestHealthySaveStillAppliesInstantly(t *testing.T) {
	f := newSettingsSaveFixture(t, true)

	warning, err := f.app.applyConfigChangeWithWarning("context compaction threshold", func(c *config.Config) error {
		return c.SetCompactRatio(0.33)
	})
	if err != nil || warning != "" {
		t.Fatalf("healthy save must be silent success, got warning=%q err=%v", warning, err)
	}
	if got := f.persisted(t).Agent.CompactRatio; got != 0.33 {
		t.Fatalf("CompactRatio = %v, want 0.33", got)
	}
	// The view the settings form reloads must read the new value back.
	if view := f.app.Settings(); view.Agent.CompactRatio != 0.33 {
		t.Fatalf("Settings() CompactRatio = %v, want 0.33", view.Agent.CompactRatio)
	}
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
