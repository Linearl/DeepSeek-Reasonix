package main

// Task 1169 integration tests: the rank-based cross-vocabulary effort map
// (config.MapEffortAcrossVocabularies) wired into the model-switch fast path
// behind experimental_effort_cross_model_map. The 1161b fold tests
// (app_effort_fold_1161_test.go) pin the switch-off behavior — every test
// here that flips the switch on must compose with that layer, never replace
// it.

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
)

// crossMap1169Providers assembles the provider cast the rank-map acceptance
// rides on: the official DeepSeek pair (vocabulary disabled|low|high|max),
// a declared three-depth entry (max is NOT in its vocabulary — the fold
// fails and the rank map answers), a thinking-locked entry whose declared
// vocabulary is the off slot alone (every depth fold fails — the off-only
// rule answers), and the MiMo pair partner.
func crossMap1169Providers() []config.ProviderEntry {
	return []config.ProviderEntry{
		{Name: "prov-ds", Kind: "openai", BaseURL: "https://api.deepseek.com",
			Model: "deepseek-flash", APIKeyEnv: "REASONIX_TEST_KEY"},
		{Name: "prov-mimo", Kind: "openai", BaseURL: "https://api.xiaomimimo.com/v1",
			Model: "mimo-v2.5-pro", APIKeyEnv: "REASONIX_TEST_KEY"},
		{Name: "prov-x", Kind: "openai", BaseURL: "https://example.invalid/v1",
			Model: "test-model", APIKeyEnv: "REASONIX_TEST_KEY",
			SupportedEfforts: []string{"low", "medium", "high"}},
		{Name: "prov-offonly", Kind: "openai", BaseURL: "https://example.invalid/v1",
			Model: "offonly-model", APIKeyEnv: "REASONIX_TEST_KEY",
			SupportedEfforts: []string{"disabled"}},
	}
}

// crossMap1169TestConfig isolates user dirs and writes the provider cast
// above with the lab switch in the requested position.
func crossMap1169TestConfig(t *testing.T, mapOn bool) {
	t.Helper()
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "REASONIX_TEST_KEY", "sk-test")
	cfg := config.Default()
	cfg.DefaultModel = "prov-ds/deepseek-flash"
	cfg.Providers = crossMap1169Providers()
	cfg.Agent.ExperimentalEffortCrossModelMap = mapOn
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

// crossMap1169App mirrors the 1161b app fixture (same stub vocabulary, same
// notice recorder) against the config written by crossMap1169TestConfig.
func crossMap1169App(t *testing.T) (*App, *WorkspaceTab, *runningStatusController, *[]event.Event) {
	t.Helper()
	app := NewApp()
	app.ctx = context.Background()
	tab := &WorkspaceTab{
		ID:          "test",
		Scope:       "global",
		Ready:       true,
		disabledMCP: map[string]ServerView{},
	}
	notices := &[]event.Event{}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	tab.sink.SetBotSink(event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice {
			*notices = append(*notices, e)
		}
	}))
	installNoopRuntimeEvents(app, tab.sink)
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	reg := toolRegistryFor1161()
	ag := agent.New(deepSeekStubProvider{}, reg, agent.NewSession("system"), agent.Options{ModelResolver: effortFold1161Resolver(t)}, event.Discard)
	ctrl := control.New(control.Options{Executor: ag, Sink: event.Discard})
	t.Cleanup(ctrl.Close)
	wrapped := &runningStatusController{Controller: ctrl}
	app.setTestCtrl(wrapped, "prov-ds/deepseek-flash")
	return app, app.tabs["test"], wrapped, notices
}

// noticesJoined concatenates the notices recorded after index from.
func noticesJoined(notices *[]event.Event, from int) string {
	joined := ""
	for _, n := range (*notices)[from:] {
		joined += n.Text + "\n"
	}
	return joined
}

// TestModelSwitchRankMapsDepthOntoDestination is the 1169 acceptance: with
// the lab switch on, a carried depth the destination cannot spell (deepseek
// max onto a declared low|medium|high vocabulary) lands on the SAME RANK
// slot (high — the destination ceiling) instead of auto, the arm rides the
// fast path, and the automatic step-down is user-visible.
func TestModelSwitchRankMapsDepthOntoDestination(t *testing.T) {
	crossMap1169TestConfig(t, true)
	buf := captureSlog(t)
	app, tab, wrapped, notices := crossMap1169App(t)

	if err := app.SetEffortForTab("test", "max"); err != nil {
		t.Fatalf("seed effort max: %v", err)
	}
	notices0 := len(*notices)

	if err := app.SetModelForTab("test", "prov-x/test-model"); err != nil {
		t.Fatalf("cross-vocabulary switch failed: %v", err)
	}
	if got := tabModelOf(app, tab); got != "prov-x/test-model" {
		t.Fatalf("tab.model = %q, want the prov-x ref", got)
	}
	if got := tabEffortOf1161(app, tab); got == nil || *got != "high" {
		t.Fatalf("tab.effort after the rank map = %q, want high (max keeps its ceiling rank)", *got)
	}
	if app.controllerForTab(tab) != control.SessionAPI(wrapped) {
		t.Fatal("rank-mapped switch replaced the controller; it must arm the override without a rebuild")
	}
	log := buf.String()
	if !strings.Contains(log, "effort_fold") || (!strings.Contains(log, `"path":"fast-per-request"`) && !strings.Contains(log, "path=fast-per-request")) {
		t.Fatalf("rank-mapped switch must ride the fast path and log the fold:\n%s", log)
	}
	if strings.Contains(log, "path=fallback") || strings.Contains(log, "effort-not-in-vocabulary") {
		t.Fatalf("rank-mapped switch fell back or declined:\n%s", log)
	}
	joined := noticesJoined(notices, notices0)
	if !strings.Contains(joined, "max") || !strings.Contains(joined, "high") {
		t.Fatalf("rank-map notice must name the outgoing and the landed level, got:\n%s", joined)
	}
}

// TestModelSwitchRankMapOffKeepsAuto pins the rollback: with the lab switch
// off (the default), the same switch keeps the 1161b behavior — the
// unmappable depth lands on auto and the fold notice says so.
func TestModelSwitchRankMapOffKeepsAuto(t *testing.T) {
	crossMap1169TestConfig(t, false)
	app, tab, _, notices := crossMap1169App(t)

	if err := app.SetEffortForTab("test", "max"); err != nil {
		t.Fatalf("seed effort max: %v", err)
	}
	notices0 := len(*notices)
	if err := app.SetModelForTab("test", "prov-x/test-model"); err != nil {
		t.Fatalf("cross-vocabulary switch failed: %v", err)
	}
	if got := tabEffortOf1161(app, tab); got == nil || *got != "" {
		t.Fatalf("switch-off tab.effort = %v, want auto (the status-quo fold)", got)
	}
	joined := noticesJoined(notices, notices0)
	if !strings.Contains(joined, "auto (provider default)") {
		t.Fatalf("switch-off fold notice must name the auto landing, got:\n%s", joined)
	}
}

// TestModelSwitchRankCarriesMaxOntoMimoCeiling is the user-scenario end to
// end with the switch on: deepseek max → MiMo still arms at MiMo's ceiling
// (high, the 1161b direct fold) — the rank map composes without disturbing
// the direct-hit path.
func TestModelSwitchRankCarriesMaxOntoMimoCeiling(t *testing.T) {
	crossMap1169TestConfig(t, true)
	buf := captureSlog(t)
	app, tab, wrapped, notices := crossMap1169App(t)

	if err := app.SetEffortForTab("test", "max"); err != nil {
		t.Fatalf("seed effort max: %v", err)
	}
	notices0 := len(*notices)
	if err := app.SetModelForTab("test", "prov-mimo/mimo-v2.5-pro"); err != nil {
		t.Fatalf("deepseek → mimo switch failed: %v", err)
	}
	if got := tabEffortOf1161(app, tab); got == nil || *got != "high" {
		t.Fatalf("tab.effort = %v, want high (MiMo's ceiling)", got)
	}
	if app.controllerForTab(tab) != control.SessionAPI(wrapped) {
		t.Fatal("the mimo switch replaced the controller")
	}
	joined := noticesJoined(notices, notices0)
	if !strings.Contains(joined, "max") || !strings.Contains(joined, "high") {
		t.Fatalf("fold notice must name both levels, got:\n%s", joined)
	}
	if strings.Contains(buf.String(), "path=fallback") {
		t.Fatalf("mimo switch fell back:\n%s", buf.String())
	}
}

// TestModelSwitchRankCarriesOffThinkingSlot: with the switch on, an
// off-thinking depth survives the round trip as the off slot — deepseek
// disabled arms on MiMo as "none" (its folded off slot, the 1161b direct
// fold), and the return hop maps "none" back onto deepseek's "disabled"
// instead of collapsing to auto. It must never surface as a thinking-on
// depth.
func TestModelSwitchRankCarriesOffThinkingSlot(t *testing.T) {
	crossMap1169TestConfig(t, true)
	buf := captureSlog(t)
	app, tab, wrapped, _ := crossMap1169App(t)

	if err := app.SetEffortForTab("test", "disabled"); err != nil {
		t.Fatalf("seed effort disabled: %v", err)
	}
	if err := app.SetModelForTab("test", "prov-mimo/mimo-v2.5-pro"); err != nil {
		t.Fatalf("hop 1 (ds→mimo) failed: %v", err)
	}
	if got := tabEffortOf1161(app, tab); got == nil || *got != "none" {
		t.Fatalf("hop 1 tab.effort = %v, want none (MiMo's off slot)", got)
	}
	if err := app.SetModelForTab("test", "prov-ds/deepseek-flash"); err != nil {
		t.Fatalf("hop 2 (mimo→ds) failed: %v", err)
	}
	got := tabEffortOf1161(app, tab)
	if got == nil || *got != "disabled" {
		t.Fatalf("hop 2 tab.effort = %q, want disabled (the off slot carries back)", *got)
	}
	if got != nil && (*got == "low" || *got == "high" || *got == "max") {
		t.Fatalf("off-thinking must never map onto a thinking-on depth, got %q", *got)
	}
	if app.controllerForTab(tab) != control.SessionAPI(wrapped) {
		t.Fatal("an off-slot hop replaced the controller")
	}
	if strings.Contains(buf.String(), "path=fallback") || strings.Contains(buf.String(), "effort-not-in-vocabulary") {
		t.Fatalf("off-slot hops fell back or declined:\n%s", buf.String())
	}
}

// TestModelSwitchRankOffOnlyDestinationGetsVisibleNote: with the switch on,
// a carried depth onto a thinking-locked destination (its only reachable
// level is the off slot) lands on auto WITH the explanatory note — never
// silently, and never onto a thinking-on depth.
func TestModelSwitchRankOffOnlyDestinationGetsVisibleNote(t *testing.T) {
	crossMap1169TestConfig(t, true)
	app, tab, _, notices := crossMap1169App(t)

	if err := app.SetEffortForTab("test", "high"); err != nil {
		t.Fatalf("seed effort high: %v", err)
	}
	notices0 := len(*notices)
	if err := app.SetModelForTab("test", "prov-offonly/offonly-model"); err != nil {
		t.Fatalf("switch to the locked destination failed: %v", err)
	}
	if got := tabEffortOf1161(app, tab); got == nil || *got != "" {
		t.Fatalf("tab.effort = %q, want auto (the locked destination has no depth to rank onto)", *got)
	}
	joined := noticesJoined(notices, notices0)
	if !strings.Contains(joined, "thinking off") {
		t.Fatalf("locked-destination notice must explain the auto landing, got:\n%s", joined)
	}
}
