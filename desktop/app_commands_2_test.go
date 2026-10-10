package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

type staleWorkspaceBindingFixture struct {
	app          *App
	tab          *WorkspaceTab
	oldCtrl      control.SessionAPI
	projectA     string
	sessionDirA  string
	sessionPathA string
}

func newStaleWorkspaceBindingFixture(t *testing.T, suffix string) staleWorkspaceBindingFixture {
	return newStaleWorkspaceBindingFixtureWithLayout(t, suffix, "")
}

func newStaleWorkspaceBindingFixtureWithLayout(t *testing.T, suffix, layoutStyle string) staleWorkspaceBindingFixture {
	t.Helper()
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "TEST_MODEL_KEY", "sk-test")

	// Submitted turns use a real provider, so the fixture must complete them
	// instantly instead of pointing at an unreachable host.
	providerStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(providerStub.Close)

	cfg := config.Default()
	cfg.DefaultModel = "test/test-model"
	cfg.Desktop.ProviderAccess = []string{"test"}
	cfg.Providers = []config.ProviderEntry{
		{Name: "test", Kind: "openai", BaseURL: providerStub.URL, Model: "test-model", APIKeyEnv: "TEST_MODEL_KEY"},
	}
	if strings.TrimSpace(layoutStyle) != "" {
		if err := cfg.SetDesktopLayoutStyle(layoutStyle); err != nil {
			t.Fatalf("set desktop layout style: %v", err)
		}
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	projectA := t.TempDir()
	projectB := t.TempDir()
	if err := addProject(projectA, "Project A"); err != nil {
		t.Fatalf("add project A: %v", err)
	}
	if err := addProject(projectB, "Project B"); err != nil {
		t.Fatalf("add project B: %v", err)
	}

	topicID := "topic_" + suffix
	topicTitle := "Rebuild workspace " + suffix
	sessionDirA := desktopSessionDir(projectA)
	sessionDirB := desktopSessionDir(projectB)
	if err := os.MkdirAll(sessionDirA, 0o755); err != nil {
		t.Fatalf("mkdir project A sessions: %v", err)
	}
	if err := os.MkdirAll(sessionDirB, 0o755); err != nil {
		t.Fatalf("mkdir project B sessions: %v", err)
	}
	sessionPathA := writeTopicSessionWithPrompt(t, sessionDirA, "project-a.jsonl", topicID, topicTitle, projectA, "project A prompt", time.Now())
	sessionPathB := filepath.Join(sessionDirB, "wrong.jsonl")

	oldSession := agent.NewSession("old system prompt")
	oldSession.Add(provider.Message{Role: provider.RoleUser, Content: "carry me"})
	oldExec := agent.New(nil, nil, oldSession, agent.Options{}, event.Discard)
	oldCtrl := control.New(control.Options{
		Executor:      oldExec,
		SessionDir:    sessionDirB,
		SessionPath:   sessionPathB,
		Label:         "test/test-model",
		ModelRef:      "test/test-model",
		WorkspaceRoot: projectB,
		Sink:          event.Discard,
	})

	app := NewApp()
	app.readyHook = func() {}
	tab := &WorkspaceTab{
		ID:            "tab_stale_workspace_" + suffix,
		Scope:         "project",
		WorkspaceRoot: projectB,
		TopicID:       topicID,
		TopicTitle:    topicTitle,
		SessionPath:   sessionPathA,
		Ready:         true,
		model:         "test/test-model",
		Ctrl:          oldCtrl,
		sink:          &tabEventSink{tabID: "tab_stale_workspace_" + suffix, app: app},
		disabledMCP:   map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
	})

	return staleWorkspaceBindingFixture{
		app:          app,
		tab:          tab,
		oldCtrl:      oldCtrl,
		projectA:     projectA,
		sessionDirA:  sessionDirA,
		sessionPathA: sessionPathA,
	}
}

func assertTabRebuiltToPinnedWorkspace(t *testing.T, f staleWorkspaceBindingFixture) {
	t.Helper()
	if f.tab.Ctrl == nil {
		t.Fatal("controller was not rebuilt")
	}
	if f.tab.Ctrl == f.oldCtrl {
		t.Fatal("stale controller was reused")
	}
	if got := normalizeProjectRoot(f.tab.WorkspaceRoot); got != normalizeProjectRoot(f.projectA) {
		t.Fatalf("tab workspace root = %q, want project A %q", got, normalizeProjectRoot(f.projectA))
	}
	if got := normalizeProjectRoot(f.tab.Ctrl.WorkspaceRoot()); got != normalizeProjectRoot(f.projectA) {
		t.Fatalf("controller workspace root = %q, want project A %q", got, normalizeProjectRoot(f.projectA))
	}
	if !sameDesktopPath(f.tab.Ctrl.SessionDir(), f.sessionDirA) {
		t.Fatalf("controller session dir = %q, want %q", f.tab.Ctrl.SessionDir(), f.sessionDirA)
	}
	if !sameDesktopPath(f.tab.Ctrl.SessionPath(), f.sessionPathA) {
		t.Fatalf("controller session path = %q, want %q", f.tab.Ctrl.SessionPath(), f.sessionPathA)
	}
}

type blockingSnapshotCtrl struct {
	control.SessionAPI

	firstSnapshotStarted  chan struct{}
	secondSnapshotStarted chan struct{}
	releaseSnapshot       chan struct{}
	firstOnce             sync.Once
	secondOnce            sync.Once
	snapshotCount         atomic.Int32
	closeCount            atomic.Int32
}

func newBlockingSnapshotCtrl(ctrl control.SessionAPI) *blockingSnapshotCtrl {
	return &blockingSnapshotCtrl{
		SessionAPI:            ctrl,
		firstSnapshotStarted:  make(chan struct{}),
		secondSnapshotStarted: make(chan struct{}),
		releaseSnapshot:       make(chan struct{}),
	}
}

func (c *blockingSnapshotCtrl) Snapshot() error {
	count := c.snapshotCount.Add(1)
	switch count {
	case 1:
		c.firstOnce.Do(func() { close(c.firstSnapshotStarted) })
	case 2:
		c.secondOnce.Do(func() { close(c.secondSnapshotStarted) })
	}
	<-c.releaseSnapshot
	if c.SessionAPI == nil {
		return nil
	}
	return c.SessionAPI.Snapshot()
}

func (c *blockingSnapshotCtrl) Close() {
	c.closeCount.Add(1)
	if c.SessionAPI != nil {
		c.SessionAPI.Close()
	}
}

func (f *staleWorkspaceBindingFixture) installBlockingSnapshotController() *blockingSnapshotCtrl {
	ctrl := newBlockingSnapshotCtrl(f.tab.Ctrl)
	f.tab.Ctrl = ctrl
	f.oldCtrl = ctrl
	return ctrl
}

func TestEnsureTabControllerWorkspaceRebuildsStaleWorkspace(t *testing.T) {
	f := newStaleWorkspaceBindingFixture(t, "rebuild_workspace")

	if err := f.app.ensureTabControllerWorkspace(f.tab); err != nil {
		t.Fatalf("ensureTabControllerWorkspace: %v", err)
	}
	assertTabRebuiltToPinnedWorkspace(t, f)
}

func TestEnsureTabControllerWorkspaceWarnsWhenPinnedSessionSwitchesWorkspace(t *testing.T) {
	f := newStaleWorkspaceBindingFixture(t, "warn_workspace_switch")
	events := make(chan event.Event, 8)
	f.tab.sink.SetBotSink(event.FuncSink(func(e event.Event) {
		events <- e
	}))

	if err := f.app.ensureTabControllerWorkspace(f.tab); err != nil {
		t.Fatalf("ensureTabControllerWorkspace: %v", err)
	}
	assertTabRebuiltToPinnedWorkspace(t, f)

	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-events:
			if e.Kind == event.Notice &&
				e.Level == event.LevelWarn &&
				strings.Contains(strings.ToLower(e.Text), strings.ToLower(f.projectA)) &&
				strings.Contains(e.Text, "switched tab") {
				return
			}
		case <-deadline:
			t.Fatal("did not receive workspace switch warning notice")
		}
	}
}

func TestDescribeSessionBindingWorkspaceKeepsWindowsPathReadable(t *testing.T) {
	path := `C:\Users\Jane Doe\Reasonix`
	want := `project workspace "C:\Users\Jane Doe\Reasonix"`
	if got := describeSessionBindingWorkspace("project", path); got != want {
		t.Fatalf("describeSessionBindingWorkspace = %q, want %q", got, want)
	}
}

func TestSteerForTabReconcilesStaleWorkspaceBeforeRejectingIdleGuidance(t *testing.T) {
	f := newStaleWorkspaceBindingFixture(t, "steer_idle_fallback")

	err := f.app.SteerForTab(f.tab.ID, "steer guidance")
	if err == nil || !strings.Contains(err.Error(), "remain queued") {
		t.Fatalf("SteerForTab error = %v, want explicit rejected-guidance result", err)
	}
	assertTabRebuiltToPinnedWorkspace(t, f)
}

func TestCompactReconcilesStaleWorkspaceBeforeCompaction(t *testing.T) {
	f := newStaleWorkspaceBindingFixture(t, "compact")

	if err := f.app.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	assertTabRebuiltToPinnedWorkspace(t, f)
}

func TestEffortCommandUsesPinnedSessionOwnerBeforeStaleWorkspaceRoot(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OWNER_MODEL_KEY", "sk-test")
	setDesktopTestCredential(t, "STALE_MODEL_KEY", "sk-test")

	projectA := t.TempDir()
	projectB := t.TempDir()
	if err := addProject(projectA, "Project A"); err != nil {
		t.Fatalf("add project A: %v", err)
	}
	if err := addProject(projectB, "Project B"); err != nil {
		t.Fatalf("add project B: %v", err)
	}
	ownerConfig := `default_model = "owner/owner-model"
[[providers]]
name = "owner"
kind = "openai"
base_url = "https://owner.example.invalid/v1"
model = "owner-model"
api_key_env = "OWNER_MODEL_KEY"
supported_efforts = ["max"]
default_effort = "max"
`
	if err := os.WriteFile(filepath.Join(projectA, "reasonix.toml"), []byte(ownerConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	staleConfig := `default_model = "stale/stale-model"
[[providers]]
name = "stale"
kind = "openai"
base_url = "https://stale.example.invalid/v1"
model = "stale-model"
api_key_env = "STALE_MODEL_KEY"
reasoning_protocol = "none"
`
	if err := os.WriteFile(filepath.Join(projectB, "reasonix.toml"), []byte(staleConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	topicID := "topic_effort_owner"
	topicTitle := "Effort owner"
	sessionDirA := desktopSessionDir(projectA)
	sessionDirB := desktopSessionDir(projectB)
	if err := os.MkdirAll(sessionDirA, 0o755); err != nil {
		t.Fatalf("mkdir project A sessions: %v", err)
	}
	if err := os.MkdirAll(sessionDirB, 0o755); err != nil {
		t.Fatalf("mkdir project B sessions: %v", err)
	}
	sessionPathA := writeTopicSessionWithPrompt(t, sessionDirA, "project-a.jsonl", topicID, topicTitle, projectA, "project A prompt", time.Now())
	oldCtrl := control.New(control.Options{
		SessionDir:    sessionDirB,
		SessionPath:   filepath.Join(sessionDirB, "wrong.jsonl"),
		WorkspaceRoot: projectB,
		Sink:          event.Discard,
	})

	app := NewApp()
	app.readyHook = func() {}
	tab := &WorkspaceTab{
		ID:            "tab_stale_effort",
		Scope:         "project",
		WorkspaceRoot: projectB,
		TopicID:       topicID,
		TopicTitle:    topicTitle,
		SessionPath:   sessionPathA,
		Ready:         true,
		Ctrl:          oldCtrl,
		sink:          &tabEventSink{tabID: "tab_stale_effort", app: app},
		disabledMCP:   map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
	})

	if err := app.SubmitToTab(tab.ID, "/effort max"); err != nil {
		t.Fatalf("SubmitToTab(/effort max): %v", err)
	}
	waitNotRunning(t, tab.Ctrl)
	if tab.effort == nil || *tab.effort != "max" {
		t.Fatalf("tab effort = %#v, want max from pinned project A provider", tab.effort)
	}
	if got := normalizeProjectRoot(tab.WorkspaceRoot); got != normalizeProjectRoot(projectA) {
		t.Fatalf("tab workspace root = %q, want project A %q", got, normalizeProjectRoot(projectA))
	}
	if got := normalizeProjectRoot(tab.Ctrl.WorkspaceRoot()); got != normalizeProjectRoot(projectA) {
		t.Fatalf("controller workspace root = %q, want project A %q", got, normalizeProjectRoot(projectA))
	}
}

func TestLegacyClassicLayoutQuickClicksSerializeWorkspaceRebuild(t *testing.T) {
	runQuickClickWorkspaceReconcileTest(t, "classic")
}

func TestWorkbenchLayoutQuickClicksSerializeWorkspaceRebuild(t *testing.T) {
	runQuickClickWorkspaceReconcileTest(t, "workbench")
}

func TestCreationLayoutQuickClicksSerializeWorkspaceRebuild(t *testing.T) {
	runQuickClickWorkspaceReconcileTest(t, "creation")
}

func runQuickClickWorkspaceReconcileTest(t *testing.T, layoutStyle string) {
	t.Helper()
	f := newStaleWorkspaceBindingFixtureWithLayout(t, "quick_click_"+layoutStyle, layoutStyle)
	if got, want := f.app.singleSurfaceLayoutEnabled(), singleSurfaceLayoutStyle(layoutStyle); got != want {
		t.Fatalf("singleSurfaceLayoutEnabled(%q) = %v, want %v", layoutStyle, got, want)
	}
	blockingCtrl := f.installBlockingSnapshotController()

	type quickAction struct {
		name string
		run  func() error
	}
	actions := []quickAction{
		{name: "submit", run: func() error { return f.app.SubmitToTab(f.tab.ID, "/unknown-command") }},
		{name: "steer", run: func() error { return f.app.SteerForTab(f.tab.ID, "steer guidance") }},
		{name: "compact", run: func() error { return f.app.Compact() }},
		{name: "submit-display", run: func() error { return f.app.SubmitDisplayToTab(f.tab.ID, "/unknown display", "/unknown-command") }},
	}

	start := make(chan struct{})
	ready := make(chan struct{}, len(actions))
	errs := make(chan error, len(actions))
	var wg sync.WaitGroup
	for _, action := range actions {
		wg.Go(func() {
			ready <- struct{}{}
			<-start
			if err := action.run(); err != nil {
				errs <- fmt.Errorf("%s: %w", action.name, err)
			}
		})
	}
	for range actions {
		<-ready
	}
	close(start)

	select {
	case <-blockingCtrl.firstSnapshotStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first stale controller snapshot")
	}
	select {
	case <-blockingCtrl.secondSnapshotStarted:
		t.Fatal("workspace rebuild was not serialized: second stale snapshot started before the first rebuild finished")
	case <-time.After(75 * time.Millisecond):
	}
	close(blockingCtrl.releaseSnapshot)
	wg.Wait()
	close(errs)
	for err := range errs {
		// Racing quick clicks may legitimately observe a busy controller or an
		// already-ended steer target. This test asserts workspace-rebuild
		// serialization, not that every concurrent action wins admission.
		if strings.Contains(err.Error(), "turn already running") ||
			strings.Contains(err.Error(), "cannot compact while a turn is running") ||
			strings.Contains(err.Error(), "remain queued") {
			continue
		}
		t.Error(err)
	}
	if t.Failed() {
		return
	}
	if got := blockingCtrl.snapshotCount.Load(); got != 1 {
		t.Fatalf("stale snapshot count = %d, want 1", got)
	}
	if got := blockingCtrl.closeCount.Load(); got != 1 {
		t.Fatalf("stale close count = %d, want 1", got)
	}
	waitNotRunning(t, f.tab.Ctrl)
	assertTabRebuiltToPinnedWorkspace(t, f)
}

func TestListSessionsUsesPinnedSessionOwnerBeforeStaleRuntimeDir(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectA := t.TempDir()
	projectB := t.TempDir()
	if err := addProject(projectA, "Project A"); err != nil {
		t.Fatalf("add project A: %v", err)
	}
	if err := addProject(projectB, "Project B"); err != nil {
		t.Fatalf("add project B: %v", err)
	}
	sessionDirA := desktopSessionDir(projectA)
	sessionDirB := desktopSessionDir(projectB)
	if err := os.MkdirAll(sessionDirA, 0o755); err != nil {
		t.Fatalf("mkdir project A sessions: %v", err)
	}
	if err := os.MkdirAll(sessionDirB, 0o755); err != nil {
		t.Fatalf("mkdir project B sessions: %v", err)
	}
	sessionPathA := writeTopicSessionWithPrompt(t, sessionDirA, "project-a.jsonl", "topic_project_a", "Project A topic", projectA, "project A prompt", time.Now())
	sessionPathB := writeTopicSessionWithPrompt(t, sessionDirB, "project-b.jsonl", "topic_project_b", "Project B topic", projectB, "project B prompt", time.Now().Add(time.Minute))

	app := NewApp()
	oldCtrl := control.New(control.Options{
		SessionDir:    sessionDirB,
		SessionPath:   sessionPathB,
		WorkspaceRoot: projectB,
		Sink:          event.Discard,
	})
	tab := &WorkspaceTab{
		ID:            "tab_stale_runtime_dir",
		Scope:         "project",
		WorkspaceRoot: projectB,
		TopicID:       "topic_project_a",
		TopicTitle:    "Project A topic",
		SessionPath:   sessionPathA,
		Ready:         true,
		Ctrl:          oldCtrl,
		disabledMCP:   map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	installSessionCatalogForTest(t, app, sessionDirA, "project", projectA)
	t.Cleanup(oldCtrl.Close)
	sessions := listSessionsAfterPinnedOwnerReconcile(t, app, sessionDirA, projectA)
	if len(sessions) == 0 {
		t.Fatal("ListSessions() returned no sessions")
	}
	if filepath.Clean(sessions[0].Path) != filepath.Clean(sessionPathA) {
		t.Fatalf("ListSessions()[0].Path = %q, want pinned project A session %q", sessions[0].Path, sessionPathA)
	}
	for _, item := range sessions {
		if filepath.Clean(item.Path) == filepath.Clean(sessionPathB) {
			t.Fatalf("ListSessions() included stale project B runtime session: %+v", sessions)
		}
	}
	if got := normalizeProjectRoot(tab.WorkspaceRoot); got != normalizeProjectRoot(projectA) {
		t.Fatalf("tab workspace root = %q, want project A %q", got, normalizeProjectRoot(projectA))
	}
}

func TestSetDefaultModelRejectsProviderWithoutKey(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("MIMO_API_KEY", "")

	cfg := config.Default()
	cfg.Desktop.ProviderAccess = []string{"mimo-api"}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	tab := &WorkspaceTab{ID: "tab_a", Scope: "global", Ready: true, model: "deepseek-flash/deepseek-v4-flash"}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	err := app.SetDefaultModel("mimo-api/mimo-v2.5-pro")
	if err == nil || !strings.Contains(err.Error(), "has no key") {
		t.Fatalf("SetDefaultModel no-key error = %v, want has no key", err)
	}
	if tab.model != "deepseek-flash/deepseek-v4-flash" {
		t.Fatalf("tab model after failed default change = %q, want previous", tab.model)
	}
}

func TestSaveProviderPersistsReasoningProtocol(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	if err := app.SaveProvider(ProviderView{
		Name:              "deepseek-proxy",
		Kind:              "openai",
		BaseURL:           "https://proxy.example.com/v1",
		Models:            []string{"deepseek-v4-flash"},
		Default:           "deepseek-v4-flash",
		APIKeyEnv:         "DEEPSEEK_PROXY_KEY",
		ReasoningProtocol: "none",
		SupportedEfforts:  []string{"high", "max"},
		DefaultEffort:     "max",
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	cfg := config.LoadForEdit(config.UserConfigPath())
	got, ok := cfg.Provider("deepseek-proxy")
	if !ok {
		t.Fatal("saved provider not found")
	}
	if got.ReasoningProtocol != "none" || got.DefaultEffort != "max" {
		t.Fatalf("saved provider = %+v, want reasoning_protocol none and default_effort max", got)
	}

	view := app.Settings()
	for _, p := range view.Providers {
		if p.Name == "deepseek-proxy" {
			if p.ReasoningProtocol != "none" {
				t.Fatalf("settings reasoningProtocol = %q, want none", p.ReasoningProtocol)
			}
			return
		}
	}
	t.Fatalf("Settings() missing saved provider: %+v", view.Providers)
}

func TestDeleteProviderMigratesConfigAndPreservesOpenTabs(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "REASONIX_TEST_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "prov-a/model-a2"
	cfg.Providers = []config.ProviderEntry{
		{Name: "prov-a", Kind: "openai", BaseURL: "https://a.example.com", Model: "model-a1", Models: []string{"model-a1", "model-a2"}, APIKeyEnv: "REASONIX_TEST_KEY"},
		{Name: "prov-b", Kind: "openai", BaseURL: "https://b.example.com", Model: "model-b1", APIKeyEnv: "REASONIX_TEST_KEY"},
	}
	cfg.Agent.PlannerModel = "prov-a"
	cfg.Desktop.ProviderAccess = []string{"prov-a", "prov-b"}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	ctrl := control.New(control.Options{Label: "old"})
	defer ctrl.Close()
	app := NewApp()
	tab := &WorkspaceTab{ID: "tab_a", Scope: "global", Ctrl: ctrl, Label: "prov-a/model-a1", Ready: true, model: "prov-a/model-a1"}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	if err := app.DeleteProvider("prov-a"); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}

	got := config.LoadForEdit(config.UserConfigPath())
	if _, ok := got.Provider("prov-a"); ok {
		t.Fatal("prov-a should be removed")
	}
	if got.DefaultModel != "prov-b" || got.Agent.PlannerModel != "prov-b" {
		t.Fatalf("model refs after delete = default:%q planner:%q, want prov-b", got.DefaultModel, got.Agent.PlannerModel)
	}
	if providerAccessSet(got.Desktop.ProviderAccess)["prov-a"] {
		t.Fatalf("provider access still contains prov-a: %+v", got.Desktop.ProviderAccess)
	}
	if tab.model != "prov-a/model-a1" || tab.Label != "prov-a/model-a1" {
		t.Fatalf("saving deletion changed current identity: model:%q label:%q", tab.model, tab.Label)
	}
	if tab.Ctrl != ctrl {
		t.Fatal("saving deletion closed the current controller")
	}
}

// assertTabBuildSuperseded checks that the startup build registered before the
// mutation (generation) can no longer install its controller and that its
// build context was cancelled.
func assertTabBuildSuperseded(t *testing.T, app *App, tab *WorkspaceTab, generation uint64, buildCtx context.Context) {
	t.Helper()
	app.mu.Lock()
	superseded := app.tabBuildSupersededLocked(tab, generation)
	app.mu.Unlock()
	if !superseded {
		t.Fatal("in-flight startup build was not superseded; finishing it would reinstall a stale controller")
	}
	select {
	case <-buildCtx.Done():
	default:
		t.Fatal("in-flight startup build context was not cancelled")
	}
	if tab.buildCancel != nil {
		t.Fatal("build cancel was not cleared")
	}
}

func TestDeleteProviderLeavesStartupPublicationToVersionFence(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "REASONIX_TEST_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "prov-b/model-b1"
	cfg.Providers = []config.ProviderEntry{
		{Name: "prov-a", Kind: "openai", BaseURL: "https://a.example.com", Model: "model-a1", APIKeyEnv: "REASONIX_TEST_KEY"},
		{Name: "prov-b", Kind: "openai", BaseURL: "https://b.example.com", Model: "model-b1", APIKeyEnv: "REASONIX_TEST_KEY"},
	}
	cfg.Desktop.ProviderAccess = []string{"prov-a", "prov-b"}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	// Model the async startup build still being in flight for the affected
	// tab: no controller yet, a live generation, a cancellable build context.
	buildCtx, buildCancel := context.WithCancel(context.Background())
	tab := &WorkspaceTab{
		ID:              "tab_a",
		Scope:           "global",
		model:           "prov-a/model-a1",
		buildGeneration: 1,
		buildCancel:     buildCancel,
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	if err := app.DeleteProvider("prov-a"); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}
	if tab.buildGeneration != 1 || buildCtx.Err() != nil || tab.model != "prov-a/model-a1" {
		t.Fatal("saving deletion changed an in-flight startup before its publication fence")
	}
	buildCancel()
}

func TestRemoveBuiltInProviderAccessLeavesStartupPublicationToVersionFence(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "REASONIX_TEST_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "prov-b/model-b1"
	cfg.Providers = []config.ProviderEntry{
		{Name: "deepseek", Kind: "openai", BaseURL: "https://api.deepseek.com", Model: "deepseek-chat", APIKeyEnv: "REASONIX_TEST_KEY"},
		{Name: "prov-b", Kind: "openai", BaseURL: "https://b.example.com", Model: "model-b1", APIKeyEnv: "REASONIX_TEST_KEY"},
	}
	cfg.Desktop.ProviderAccess = []string{"deepseek", "prov-b"}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	buildCtx, buildCancel := context.WithCancel(context.Background())
	tab := &WorkspaceTab{
		ID:              "tab_ds",
		Scope:           "global",
		model:           "deepseek/deepseek-chat",
		buildGeneration: 1,
		buildCancel:     buildCancel,
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	if err := app.RemoveProviderAccess("deepseek"); err != nil {
		t.Fatalf("RemoveProviderAccess: %v", err)
	}
	if tab.buildGeneration != 1 || buildCtx.Err() != nil || tab.model != "deepseek/deepseek-chat" {
		t.Fatal("saving access removal changed an in-flight startup before its publication fence")
	}
	buildCancel()
}
