package main

import (
	"context"
	"errors"
	"time"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/netclient"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// runningStatusController pins the switch fast paths' ordering (task 148,
// evolving the task-334 effort nail to cover the model seam): the wrapped
// controller reports Running forever — an active turn that never ends — and
// every RuntimeStatus read is counted. A fast path must succeed with zero
// reads; reading at all means the switch ran behind the active-work guard.
// The concrete *control.Controller is embedded (not the SessionAPI interface)
// so the wrapper keeps the SetSession*Override methods the fast paths assert.
type runningStatusController struct {
	*control.Controller
	reads int
}

func (c *runningStatusController) RuntimeStatus() control.RuntimeStatus {
	c.reads++
	return control.RuntimeStatus{Running: true}
}

// varyingStubProvider arms the executor with a per-request effort vocabulary
// so the desktop effort fast path can arm through the real controller seam.
type varyingStubProvider struct{ stubProvider }

func (varyingStubProvider) PerRequestEfforts() []string { return []string{"low", "high"} }

// provAStubProvider reports the prov-a family name, standing in for the
// prov-a-resolved provider an executor really runs (the family gate compares
// against it).
type provAStubProvider struct{ stubProvider }

func (provAStubProvider) Name() string { return "prov-a" }

// modelFastPathTestConfig isolates user dirs and saves a two-provider config:
// prov-a carries two models of one family (fast-path targets) and prov-b a
// cross-family one (rebuild fallback target).
func modelFastPathTestConfig(t *testing.T) {
	t.Helper()
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "REASONIX_TEST_KEY", "sk-test")
	cfg := config.Default()
	cfg.DefaultModel = "prov-a/model-a1"
	cfg.Providers = []config.ProviderEntry{
		{Name: "prov-a", Kind: "openai", BaseURL: "https://a.example.com",
			Model: "model-a1", Models: []string{"model-a1", "model-a2"},
			APIKeyEnv:         "REASONIX_TEST_KEY",
			ReasoningProtocol: "openai", SupportedEfforts: []string{"low", "high"}},
		{Name: "prov-b", Kind: "openai", BaseURL: "https://b.example.com",
			Model: "model-b1", APIKeyEnv: "REASONIX_TEST_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

// activeTabApp builds an app whose test tab runs a real controller over an
// executor agent, wrapped so RuntimeStatus always reports Running. The
// executor wires a resolver over the saved config when non-nil, mirroring the
// boot wiring (same resolver the tab's provider was built from).
func activeTabApp(t *testing.T, prov provider.Provider, resolver provider.Resolver) (*App, *WorkspaceTab, *runningStatusController) {
	t.Helper()
	ag := agent.New(prov, tool.NewRegistry(), agent.NewSession("system"), agent.Options{ModelResolver: resolver}, event.Discard)
	ctrl := control.New(control.Options{Executor: ag, Sink: event.Discard})
	t.Cleanup(ctrl.Close)
	wrapped := &runningStatusController{Controller: ctrl}
	app := NewApp()
	app.ctx = context.Background() // SetModelForTab exits early on a nil context.
	app.setTestCtrl(wrapped, "prov-a/model-a1")
	return app, app.tabs["test"], wrapped
}

func savedConfigResolver(t *testing.T) provider.Resolver {
	t.Helper()
	cfg := config.LoadForEdit(config.UserConfigPath())
	return boot.NewLocalProviderResolver(cfg, netclient.ProxySpec{Mode: netclient.ModeAuto})
}

func tabModelOf(app *App, tab *WorkspaceTab) string {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return tab.model
}

// TestSetModelForTabFastPathAheadOfActiveWorkGuard: with the controller
// forever Running, a same-family model switch must succeed via the per-request
// override — zero RuntimeStatus reads (ahead of the active-work guard), the
// controller stays, and no runtime build is paid.
func TestSetModelForTabFastPathAheadOfActiveWorkGuard(t *testing.T) {
	modelFastPathTestConfig(t)
	app, tab, wrapped := activeTabApp(t, provAStubProvider{}, savedConfigResolver(t))

	var timing modelSwitchTiming
	app.modelSwitchTimingHook = func(got modelSwitchTiming) { timing = got }

	started := time.Now()
	if err := app.SetModelForTab("test", "prov-a/model-a2"); err != nil {
		t.Fatalf("active turn refused a same-family model switch: %v", err)
	}
	elapsed := time.Since(started)
	t.Logf("model fast path end-to-end: %s (total_ms=%d build_ms=%d)", elapsed, timing.Total.Milliseconds(), timing.Build.Milliseconds())
	if elapsed > 3*time.Second {
		t.Fatalf("model fast path took %s; acceptance is <=3s", elapsed)
	}
	if wrapped.reads != 0 {
		t.Fatalf("RuntimeStatus read %d times on the model fast path; the switch must run ahead of the active-work guard", wrapped.reads)
	}
	if got := tabModelOf(app, tab); got != "prov-a/model-a2" {
		t.Fatalf("tab.model = %q, want prov-a/model-a2", got)
	}
	if app.controllerForTab(tab) != control.SessionAPI(wrapped) {
		t.Fatal("fast path replaced the controller; it must arm an override without a rebuild")
	}
	if timing.Outcome != "ok" || timing.Build != 0 {
		t.Fatalf("fast path timing = %+v; Build must stay zero (no runtime rebuild)", timing)
	}
}

// TestSetEffortForTabFastPathAheadOfActiveWorkGuard is the task-334 nail
// (effort must keep switching during an active turn, zero RuntimeStatus reads)
// carried into this baseline so the model fast path cannot regress it.
func TestSetEffortForTabFastPathAheadOfActiveWorkGuard(t *testing.T) {
	modelFastPathTestConfig(t)
	app, tab, wrapped := activeTabApp(t, varyingStubProvider{}, nil)

	started := time.Now()
	if err := app.SetEffortForTab("test", "high"); err != nil {
		t.Fatalf("active turn refused an effort switch on the per-request fast path: %v", err)
	}
	elapsed := time.Since(started)
	t.Logf("effort fast path end-to-end: %s (comparison reading for the model fast path)", elapsed)
	if elapsed > 3*time.Second {
		t.Fatalf("effort fast path took %s; acceptance is <=3s", elapsed)
	}
	if wrapped.reads != 0 {
		t.Fatalf("RuntimeStatus read %d times on the effort fast path; the switch must run ahead of the active-work guard", wrapped.reads)
	}
	app.mu.RLock()
	effort := tab.effort
	app.mu.RUnlock()
	if effort == nil || *effort != "high" {
		t.Fatalf("tab.effort = %v, want high", effort)
	}
	if app.controllerForTab(tab) != control.SessionAPI(wrapped) {
		t.Fatal("effort fast path replaced the controller")
	}
}

// TestSetModelForTabCrossProviderStillRefusedDuringActiveWork: a cross-family
// target declines the override (the agent's family gate) and keeps the
// rebuild path — whose active-work guard still refuses during a running turn.
// The tab stays on its current model.
func TestSetModelForTabCrossProviderStillRefusedDuringActiveWork(t *testing.T) {
	modelFastPathTestConfig(t)
	app, tab, wrapped := activeTabApp(t, provAStubProvider{}, savedConfigResolver(t))

	err := app.SetModelForTab("test", "prov-b/model-b1")
	if err == nil {
		t.Fatal("cross-family switch took the fast path; it must keep the rebuild path and its active-work guard")
	}
	var busy *rebuildBusyError
	if !errors.As(err, &busy) || busy.setting != "model" {
		t.Fatalf("cross-family switch error = %v, want rebuildBusyError(model)", err)
	}
	if wrapped.reads == 0 {
		t.Fatal("the declined fallback never consulted the active-work guard")
	}
	if got := tabModelOf(app, tab); got != "prov-a/model-a1" {
		t.Fatalf("tab.model = %q, want unchanged prov-a/model-a1", got)
	}
}
