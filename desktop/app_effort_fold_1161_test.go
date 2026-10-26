package main

// Task 1161 追加（1161a/1161b）：
//   1161a — composer 的 effort 菜单只渲染 EffortMenuDomain（会到达 wire 的档位）；
//   1161b — 跨词表切模型自动把 effort 折叠到目标可表达的档位（deepseek max → MiMo high），
//           且切档/切模型全程走快路径，不再出现 path=fallback 的 set-effort 重建。
//
// 根因（2026-10-25 装机日志）：官方 DeepSeek 的 per-request 探针词表为空，
// 每次 /effort 切档都 declined 走全量重建（连续 2~3.8s）；composer 菜单又未接
// EffortMenuDomain，落差档位照样可选。

import (
	"context"
	"slices"
	"strings"
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

// deepSeekStubProvider arms the executor agent with the official DeepSeek
// per-request vocabulary — the probe face the tl1161b-1 fix wired into the
// openai adapter — so the effort fast path arms through the real controller
// seam exactly as it does against the real client.
type deepSeekStubProvider struct{ stubProvider }

func (deepSeekStubProvider) PerRequestEfforts() []string {
	return []string{"disabled", "low", "high", "max"}
}
func (deepSeekStubProvider) Name() string { return "prov-ds" }

// effortFold1161TestConfig isolates user dirs and saves an official-shape
// DeepSeek provider plus a MiMo one — the cross-vocabulary pair the fold
// acceptance rides on.
func effortFold1161TestConfig(t *testing.T) {
	t.Helper()
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "REASONIX_TEST_KEY", "sk-test")
	cfg := config.Default()
	cfg.DefaultModel = "prov-ds/deepseek-flash"
	cfg.Providers = []config.ProviderEntry{
		{Name: "prov-ds", Kind: "openai", BaseURL: "https://api.deepseek.com",
			Model: "deepseek-flash", APIKeyEnv: "REASONIX_TEST_KEY"},
		{Name: "prov-mimo", Kind: "openai", BaseURL: "https://api.xiaomimimo.com/v1",
			Model: "mimo-v2.5-pro", APIKeyEnv: "REASONIX_TEST_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

func effortFold1161Resolver(t *testing.T) provider.Resolver {
	t.Helper()
	cfg := config.LoadForEdit(config.UserConfigPath())
	return boot.NewLocalProviderResolver(cfg, netclient.ProxySpec{Mode: netclient.ModeAuto})
}

// noticeRecorder wires the test tab's sink so noticeForTab output is
// observable — the fold notice is part of the acceptance (the user must see
// the automatic step-down).
func effortFold1161App(t *testing.T) (*App, *WorkspaceTab, *runningStatusController, *[]event.Event) {
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

func tabEffortOf1161(app *App, tab *WorkspaceTab) *string {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return cloneStringPtr(tab.effort)
}

// TestEffortSwitchPerRequestOnOfficialDeepSeek is the root-cause regression
// nail: before tl1161b-1 the official DeepSeek probe vocabulary was empty, so
// every depth switch declined as level-not-in-vocabulary and paid the full
// runtime rebuild. Now each switch arms the per-request override — no
// rebuild, no declined line, depth lands on the tab.
func TestEffortSwitchPerRequestOnOfficialDeepSeek(t *testing.T) {
	effortFold1161TestConfig(t)
	buf := captureSlog(t)
	app, tab, wrapped, _ := effortFold1161App(t)

	for _, level := range []string{"high", "max", "disabled"} {
		started := time.Now()
		if err := app.SetEffortForTab("test", level); err != nil {
			t.Fatalf("effort switch to %s failed: %v", level, err)
		}
		if elapsed := time.Since(started); elapsed > 3*time.Second {
			t.Fatalf("effort switch to %s took %s; the per-request path must not rebuild", level, elapsed)
		}
		if got := tabEffortOf1161(app, tab); got == nil || *got != level {
			t.Fatalf("after switching to %s tab.effort = %v, want %s", level, got, level)
		}
		if app.controllerForTab(tab) != control.SessionAPI(wrapped) {
			t.Fatalf("switch to %s replaced the controller; it must arm the override without a rebuild", level)
		}
	}
	if wrapped.reads != 0 {
		t.Fatalf("RuntimeStatus read %d times across effort switches; fast path must stay ahead of the active-work guard", wrapped.reads)
	}
	log := buf.String()
	if strings.Count(log, `"path":"fast-per-request"`) < 3 && strings.Count(log, "path=fast-per-request") < 3 {
		t.Fatalf("expected three fast-per-request effort switch lines, got:\n%s", log)
	}
	if strings.Contains(log, "level-not-in-vocabulary") {
		t.Fatalf("effort override declined on the official DeepSeek vocabulary:\n%s", log)
	}
	if strings.Contains(log, "path=fallback") {
		t.Fatalf("set-effort fell back to a runtime build:\n%s", log)
	}
}

// TestModelSwitchFoldsEffortOntoDestination is the 1161b acceptance: carrying
// deepseek max onto MiMo folds it to MiMo's ceiling (high) inside the fast
// path — the destination arms, the tab records the folded depth, and the user
// sees a notice naming the step-down. No rebuild, no fallback.
func TestModelSwitchFoldsEffortOntoDestination(t *testing.T) {
	effortFold1161TestConfig(t)
	buf := captureSlog(t)
	app, tab, wrapped, notices := effortFold1161App(t)

	if err := app.SetEffortForTab("test", "max"); err != nil {
		t.Fatalf("seed effort max: %v", err)
	}
	notices0 := len(*notices)

	if err := app.SetModelForTab("test", "prov-mimo/mimo-v2.5-pro"); err != nil {
		t.Fatalf("cross-vocabulary model switch failed: %v", err)
	}
	if got := tabModelOf(app, tab); got != "prov-mimo/mimo-v2.5-pro" {
		t.Fatalf("tab.model = %q, want the mimo ref", got)
	}
	if got := tabEffortOf1161(app, tab); got == nil || *got != "high" {
		t.Fatalf("tab.effort after the fold = %v, want high (deepseek max folds onto MiMo's ceiling)", got)
	}
	if app.controllerForTab(tab) != control.SessionAPI(wrapped) {
		t.Fatal("fold switch replaced the controller; it must arm the override without a rebuild")
	}
	log := buf.String()
	if !strings.Contains(log, "effort_fold") || (!strings.Contains(log, `"path":"fast-per-request"`) && !strings.Contains(log, "path=fast-per-request")) {
		t.Fatalf("model switch line must carry the fast path and the fold:\n%s", log)
	}
	if strings.Contains(log, "path=fallback") || strings.Contains(log, "resolve-failed") {
		t.Fatalf("fold switch fell back or declined:\n%s", log)
	}
	if len(*notices) == notices0 {
		t.Fatal("no fold notice emitted; the automatic step-down must be user-visible")
	}
	joined := ""
	for _, n := range (*notices)[notices0:] {
		joined += n.Text + "\n"
	}
	if !strings.Contains(joined, "max") || !strings.Contains(joined, "high") {
		t.Fatalf("fold notice must name both the outgoing and the folded level, got:\n%s", joined)
	}
}

// TestModelSwitchRoundTripsKeepFastPath: folding is reversible across
// vocabularies — MiMo's high rides back to deepseek unchanged (no notice, no
// fold), and a second hop to MiMo folds nothing new. Every hop stays on the
// per-request path.
func TestModelSwitchRoundTripsKeepFastPath(t *testing.T) {
	effortFold1161TestConfig(t)
	buf := captureSlog(t)
	app, tab, wrapped, notices := effortFold1161App(t)

	if err := app.SetEffortForTab("test", "max"); err != nil {
		t.Fatalf("seed effort max: %v", err)
	}
	if err := app.SetModelForTab("test", "prov-mimo/mimo-v2.5-pro"); err != nil {
		t.Fatalf("hop 1 (ds→mimo) failed: %v", err)
	}
	foldNotices := len(*notices)
	if err := app.SetModelForTab("test", "prov-ds/deepseek-flash"); err != nil {
		t.Fatalf("hop 2 (mimo→ds) failed: %v", err)
	}
	if got := tabEffortOf1161(app, tab); got == nil || *got != "high" {
		t.Fatalf("tab.effort after the return hop = %v, want high (unchanged — high is legal on deepseek)", got)
	}
	if len(*notices) != foldNotices {
		t.Fatalf("return hop emitted a fold notice; an unchanged depth must stay silent:\n%v", *notices)
	}
	if err := app.SetModelForTab("test", "prov-mimo/mimo-v2.5-pro"); err != nil {
		t.Fatalf("hop 3 (ds→mimo) failed: %v", err)
	}
	if app.controllerForTab(tab) != control.SessionAPI(wrapped) {
		t.Fatal("a round-trip hop replaced the controller")
	}
	log := buf.String()
	if got := strings.Count(log, "desktop: model switch"); got < 3 {
		t.Fatalf("expected three model switch lines, got %d:\n%s", got, log)
	}
	if strings.Contains(log, "path=fallback") {
		t.Fatalf("round-trip hops fell back to a runtime build:\n%s", log)
	}
}

// TestEffortMenuDomainFiltersComposerLevels is the 1161a nail: the composer
// reads the EffortMenuDomain both surfaces share, so a thinking-locked
// DeepSeek entry (whose runtime drops every non-default depth with a boot
// warning) no longer offers the depths it would never honour, while a healthy
// entry keeps its full vocabulary.
func TestEffortMenuDomainFiltersComposerLevels(t *testing.T) {
	effortFold1161TestConfig(t)
	cfg := config.LoadForEdit(config.UserConfigPath())
	for i := range cfg.Providers {
		if cfg.Providers[i].Name == "prov-ds" {
			cfg.Providers[i].Thinking = "disabled"
		}
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save locked config: %v", err)
	}
	app := NewApp()
	app.ctx = context.Background()
	app.setTestCtrl(nil, "prov-ds/deepseek-flash")

	info := app.effortForTabDirect("")
	if !info.Supported {
		t.Fatal("thinking-locked deepseek still supports effort (disabled stays)")
	}
	want := []string{"auto", "disabled"}
	if !slices.Equal(info.Levels, want) {
		t.Fatalf("locked Levels = %v, want %v (low/high/max never reach the wire)", info.Levels, want)
	}
	for _, option := range info.Options {
		if option.ID != "disabled" && option.ID != "auto" {
			t.Fatalf("locked Options carry %q; only the wire-legal depths may render", option.ID)
		}
	}
}

// TestEffortAndModelSwitchesStayOffFallback is the acceptance-evidence gate:
// three effort switches plus three model switches, all logged on fast paths,
// with no set-effort runtime-build line anywhere — the log fragment a
// field check can be diffed against.
func TestEffortAndModelSwitchesStayOffFallback(t *testing.T) {
	effortFold1161TestConfig(t)
	buf := captureSlog(t)
	app, tab, wrapped, _ := effortFold1161App(t)

	started := time.Now()
	for _, level := range []string{"high", "max", "disabled"} {
		if err := app.SetEffortForTab("test", level); err != nil {
			t.Fatalf("effort %s: %v", level, err)
		}
	}
	for _, ref := range []string{"prov-mimo/mimo-v2.5-pro", "prov-ds/deepseek-flash", "prov-mimo/mimo-v2.5-pro"} {
		if err := app.SetModelForTab("test", ref); err != nil {
			t.Fatalf("model %s: %v", ref, err)
		}
	}
	elapsed := time.Since(started)
	t.Logf("3 effort + 3 model switches end-to-end: %s", elapsed)
	if elapsed > 3*time.Second {
		t.Fatalf("six switches took %s; any rebuild would blow this budget", elapsed)
	}
	if app.controllerForTab(tab) != control.SessionAPI(wrapped) {
		t.Fatal("a switch in the acceptance loop replaced the controller")
	}
	log := buf.String()
	effortFast := strings.Count(log, `"path":"fast-per-request"`) + strings.Count(log, "path=fast-per-request") + strings.Count(log, "path=fast-same-level") + strings.Count(log, `"path":"fast-same-level"`)
	if effortFast < 3 {
		t.Fatalf("expected at least three fast effort-switch lines, got %d:\n%s", effortFast, log)
	}
	modelFast := strings.Count(log, "desktop: model switch")
	if modelFast < 3 {
		t.Fatalf("expected at least three model switch lines, got %d:\n%s", modelFast, log)
	}
	if strings.Contains(log, "path=fallback") {
		t.Fatalf("acceptance loop produced a path=fallback line:\n%s", log)
	}
	if strings.Contains(log, "level-not-in-vocabulary") {
		t.Fatalf("acceptance loop declined an effort override:\n%s", log)
	}
}

// toolRegistryFor1161 builds the minimal registry the executor agent needs
// (same shape activeTabApp uses, minus the MCP probe).
func toolRegistryFor1161() *tool.Registry { return tool.NewRegistry() }
