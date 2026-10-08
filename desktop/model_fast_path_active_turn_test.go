package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

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
// boot wiring (same resolver the tab's provider was built from). The executor
// agent and its registry come back too so tests can snapshot the tool surface
// the fast path must leave untouched; one fake MCP-namespaced tool rides the
// registry as the acceptance-② probe.
func activeTabApp(t *testing.T, prov provider.Provider, resolver provider.Resolver) (*App, *WorkspaceTab, *runningStatusController, *agent.Agent, *tool.Registry) {
	t.Helper()
	reg := tool.NewRegistry()
	reg.Add(fakeMCPTool{name: "mcp__probe__lookup"})
	ag := agent.New(prov, reg, agent.NewSession("system"), agent.Options{ModelResolver: resolver}, event.Discard)
	ctrl := control.New(control.Options{Executor: ag, Sink: event.Discard})
	t.Cleanup(ctrl.Close)
	wrapped := &runningStatusController{Controller: ctrl}
	app := NewApp()
	app.ctx = context.Background() // SetModelForTab exits early on a nil context.
	app.setTestCtrl(wrapped, "prov-a/model-a1")
	return app, app.tabs["test"], wrapped, ag, reg
}

// fakeMCPTool is the minimal mcp__-namespaced probe tool: the fast path must
// leave its registry entry byte-identical across a hot switch.
type fakeMCPTool struct{ name string }

func (t fakeMCPTool) Name() string                                             { return t.name }
func (t fakeMCPTool) Description() string                                      { return "acceptance probe" }
func (t fakeMCPTool) Schema() json.RawMessage                                  { return json.RawMessage(`{"type":"object"}`) }
func (t fakeMCPTool) ReadOnly() bool                                           { return true }
func (t fakeMCPTool) Execute(context.Context, json.RawMessage) (string, error) { return "ok", nil }

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
	app, tab, wrapped, _, _ := activeTabApp(t, provAStubProvider{}, savedConfigResolver(t))

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
	app, tab, wrapped, _, _ := activeTabApp(t, varyingStubProvider{}, nil)

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

// TestSetModelForTabCrossProviderFastPathDuringActiveWork is the task-602 nail
// evolving the task-148 refusal: a cross-provider target now takes the
// per-request fast path too (every destination-scoped surface follows the
// override), even with the controller forever Running — zero RuntimeStatus
// reads, the controller stays, no runtime build is paid, the tab lands the new
// ref/label, the controller identity follows, and the tool surface is
// byte-identical across the switch (MCP tools untouched by construction).
func TestSetModelForTabCrossProviderFastPathDuringActiveWork(t *testing.T) {
	modelFastPathTestConfig(t)
	app, tab, wrapped, ag, reg := activeTabApp(t, provAStubProvider{}, savedConfigResolver(t))
	_ = ag // the executor rides the controller; only its registry is snapshotted below

	var timing modelSwitchTiming
	app.modelSwitchTimingHook = func(got modelSwitchTiming) { timing = got }

	// Tool surface snapshot before the switch: the registry travels with the
	// executor (the fast path rebuilds nothing), so the provider-visible
	// schema list must be identical after a cross-provider hot switch.
	toolNamesBefore := desktopTestToolNames(reg)

	// Warm-up hop: the first switch pays this process's cold config load;
	// the acceptance scenario is a warm session, so the timed hop is the
	// second one (destination → destination, override re-arming mid-flight).
	if err := app.SetModelForTab("test", "prov-b/model-b1"); err != nil {
		t.Fatalf("warm-up cross-provider switch refused: %v", err)
	}

	started := time.Now()
	if err := app.SetModelForTab("test", "prov-a/model-a2"); err != nil {
		t.Fatalf("active turn refused a cross-provider model switch: %v", err)
	}
	elapsed := time.Since(started)
	t.Logf("cross-provider model fast path end-to-end: %s (total_ms=%d build_ms=%d)", elapsed, timing.Total.Milliseconds(), timing.Build.Milliseconds())
	if elapsed > 2*time.Second {
		t.Fatalf("cross-provider model fast path took %s; task-602 acceptance is <2s", elapsed)
	}
	if wrapped.reads != 0 {
		t.Fatalf("RuntimeStatus read %d times on the cross-provider fast path; the switch must run ahead of the active-work guard", wrapped.reads)
	}
	if got := tabModelOf(app, tab); got != "prov-a/model-a2" {
		t.Fatalf("tab.model = %q, want prov-a/model-a2 (second hop back into prov-a)", got)
	}
	if app.controllerForTab(tab) != control.SessionAPI(wrapped) {
		t.Fatal("cross-provider fast path replaced the controller; it must arm an override without a rebuild")
	}
	if timing.Outcome != "ok" || timing.Build != 0 {
		t.Fatalf("fast path timing = %+v; Build must stay zero (no runtime rebuild)", timing)
	}
	// Acceptance ②: MCP/tool list identical across the switch.
	if toolNamesAfter := desktopTestToolNames(reg); !equalStringSlices(toolNamesBefore, toolNamesAfter) {
		t.Fatalf("tool surface changed across the hot switch: before=%v after=%v", toolNamesBefore, toolNamesAfter)
	}
}

// TestSetModelForTabCrossProviderRebindsControllerIdentity pins the task-602
// controller identity rebind: after a cross-provider fast path, ModelRef,
// Label, and the frozen image gate reflect the destination, exactly as a
// rebuild would derive them from the new entry.
func TestSetModelForTabCrossProviderRebindsControllerIdentity(t *testing.T) {
	modelFastPathTestConfig(t)
	app, _, wrapped, _, _ := activeTabApp(t, provAStubProvider{}, savedConfigResolver(t))

	if err := app.SetModelForTab("test", "prov-b/model-b1"); err != nil {
		t.Fatalf("cross-provider fast path refused: %v", err)
	}
	// The harness wraps the concrete controller; identity reads go through the
	// embedded real one (SetModelIdentity lands there via the same embedding).
	ctrl := wrapped.Controller
	if got := ctrl.ModelRef(); got != "prov-b/model-b1" {
		t.Fatalf("controller ModelRef = %q, want prov-b/model-b1 after the hot switch", got)
	}
	if got := ctrl.Label(); got != "model-b1" {
		t.Fatalf("controller Label = %q, want model-b1 (boot's entry.Model derivation)", got)
	}
	// prov-b carries no vision catalog entry: the rebound gate must be false,
	// matching what a rebuild of prov-b would freeze.
	if enabled := ctrl.ImageInputEnabled(); enabled {
		t.Fatal("image gate stayed on the construction provider's vision capability")
	}
}

// TestSetModelForTabPersonaBoundaryKeepsRebuildFallback pins the one
// documented task-602 decline: crossing the official DeepSeek-V4-Pro persona
// boundary declines the fast path (the persona is baked into the cache-stable
// system prompt), and the build+swap fallback keeps its active-work guard —
// a running turn still refuses, the tab stays on its model.
func TestSetModelForTabPersonaBoundaryKeepsRebuildFallback(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "REASONIX_TEST_KEY", "sk-test")
	cfg := config.Default()
	cfg.DefaultModel = "official-ds/deepseek-v4-pro"
	cfg.Providers = []config.ProviderEntry{
		{Name: "official-ds", Kind: "deepseek", BaseURL: "https://api.deepseek.com",
			Model: "deepseek-v4-pro", Models: []string{"deepseek-v4-pro"},
			APIKeyEnv: "REASONIX_TEST_KEY"},
		{Name: "prov-b", Kind: "openai", BaseURL: "https://b.example.com",
			Model: "model-b1", APIKeyEnv: "REASONIX_TEST_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}
	app, tab, wrapped, _, _ := activeTabApp(t, provAStubProvider{}, savedConfigResolver(t))
	// The tab runs the official DeepSeek-V4-Pro entry: the harness default
	// points at prov-a, which this config does not carry.
	app.mu.Lock()
	tab.model = "official-ds/deepseek-v4-pro"
	app.mu.Unlock()

	err := app.SetModelForTab("test", "prov-b/model-b1")
	if err == nil {
		t.Fatal("persona-boundary switch took the fast path; it must keep the rebuild path and its active-work guard")
	}
	var busy *rebuildBusyError
	if !errors.As(err, &busy) || busy.setting != "model" {
		t.Fatalf("persona-boundary switch error = %v, want rebuildBusyError(model)", err)
	}
	if wrapped.reads == 0 {
		t.Fatal("the declined fallback never consulted the active-work guard")
	}
	if got := tabModelOf(app, tab); got != "official-ds/deepseek-v4-pro" {
		t.Fatalf("tab.model = %q, want unchanged official-ds/deepseek-v4-pro", got)
	}
}

// desktopTestToolNames snapshots the provider-visible tool names of the
// executor's registry — the acceptance-② surface (MCP tools unchanged across
// a hot switch). The registry travels with the executor; the fast path never
// rebuilds it, so its name set must be identical before and after.
func desktopTestToolNames(reg *tool.Registry) []string {
	return reg.AllNames()
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
