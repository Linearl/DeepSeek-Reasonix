package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/billing"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/jobs"
	"reasonix/internal/provider"
)

func TestClearActiveSessionRuntimeSupersedesInFlightStartupBuild(t *testing.T) {
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
	sessionPath := filepath.Join(dir, "clear-runtime-in-flight.jsonl")
	if err := os.WriteFile(sessionPath, nil, 0o644); err != nil {
		t.Fatalf("write placeholder session: %v", err)
	}

	oldSession := agent.NewSession("old system prompt")
	oldExec := agent.New(nil, nil, oldSession, agent.Options{}, event.Discard)
	oldCtrl := control.New(control.Options{Executor: oldExec, SessionDir: dir, SessionPath: sessionPath, Label: "old", Sink: event.Discard})

	app := NewApp()
	// A runtime is attached while an older async build is still in flight
	// (e.g. attached via topic activation); destroying the session must
	// invalidate that build so it cannot resurrect the destroyed session.
	buildCtx, buildCancel := context.WithCancel(context.Background())
	tab := &WorkspaceTab{
		ID:              "tab_clear",
		Scope:           "global",
		SessionPath:     sessionPath,
		model:           "old/old-model",
		Ready:           true,
		Ctrl:            oldCtrl,
		buildGeneration: 1,
		buildCancel:     buildCancel,
		disabledMCP:     map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(tab.releaseSessionLease)

	if _, err := app.clearActiveSessionRuntime(tab, oldCtrl); err != nil {
		t.Fatalf("clearActiveSessionRuntime: %v", err)
	}
	if tab.Ctrl == nil || tab.Ctrl == oldCtrl {
		t.Fatalf("clear did not install a fresh controller (ctrl=%v)", tab.Ctrl)
	}
	defer tab.Ctrl.Close()
	assertTabBuildSuperseded(t, app, tab, 1, buildCtx)
}

func TestClearActiveSessionRuntimeReleasesResourcesWhenTabReplaced(t *testing.T) {
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
	sessionPath := filepath.Join(dir, "clear-runtime-replaced-tab.jsonl")
	if err := os.WriteFile(sessionPath, nil, 0o644); err != nil {
		t.Fatalf("write placeholder session: %v", err)
	}

	oldSession := agent.NewSession("old system prompt")
	oldExec := agent.New(nil, nil, oldSession, agent.Options{}, event.Discard)
	oldCtrl := control.New(control.Options{Executor: oldExec, SessionDir: dir, SessionPath: sessionPath, Label: "old", Sink: event.Discard})

	app := NewApp()
	tab := &WorkspaceTab{
		ID:          "tab_replaced",
		Scope:       "global",
		SessionPath: sessionPath,
		model:       "old/old-model",
		Ready:       true,
		Ctrl:        oldCtrl,
		disabledMCP: map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	// The tab entry now points at a replacement struct (the tab was closed and
	// reopened while the clear ran off-lock), so the swap must not apply.
	replacement := &WorkspaceTab{ID: tab.ID, Scope: "global"}
	app.tabs = map[string]*WorkspaceTab{tab.ID: replacement}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(tab.releaseSessionLease)

	_, err := app.clearActiveSessionRuntime(tab, oldCtrl)
	if err == nil || !strings.Contains(err.Error(), "changed while clearing") {
		t.Fatalf("clearActiveSessionRuntime error = %v, want tab-changed error", err)
	}
	if replacement.Ctrl != nil {
		t.Fatalf("replacement tab controller = %v, want untouched nil", replacement.Ctrl)
	}
	if tab.Ctrl != oldCtrl {
		t.Fatalf("replaced tab controller = %v, want left on the destroyed runtime", tab.Ctrl)
	}
	if key := tab.sessionLeaseRuntimeKey(); key != "" {
		t.Fatalf("replaced tab still holds a session lease for %q; the fresh lease leaked", key)
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("old session artifacts were not destroyed (stat err=%v)", err)
	}
}

func TestDeleteProviderPreservesRunningAffectedTab(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "REASONIX_TEST_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "prov-a/model-a1"
	cfg.Providers = []config.ProviderEntry{
		{Name: "prov-a", Kind: "openai", BaseURL: "https://a.example.com", Model: "model-a1", APIKeyEnv: "REASONIX_TEST_KEY"},
		{Name: "prov-b", Kind: "openai", BaseURL: "https://b.example.com", Model: "model-b1", APIKeyEnv: "REASONIX_TEST_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Runner: runner}), "prov-a/model-a1")
	ctrl := app.activeCtrl()
	ctrl.Submit("work")
	<-runner.started

	err := app.DeleteProvider("prov-a")
	if err != nil || app.activeCtrl() != ctrl || !ctrl.RuntimeStatus().Running {
		t.Fatalf("DeleteProvider interrupted accepted work: %v", err)
	}
	if _, ok := config.LoadForEdit(config.UserConfigPath()).Provider("prov-a"); ok {
		t.Fatal("provider deletion was not persisted during the run")
	}

	close(runner.release)
	waitNotRunning(t, ctrl)
	ctrl.Close()
}

func TestDeleteProviderDoesNotWaitForRuntimeReconstruction(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "REASONIX_TEST_KEY", "sk-test")
	cfg := config.Default()
	cfg.DefaultModel = "prov-a/model-a1"
	cfg.Providers = []config.ProviderEntry{
		{Name: "prov-a", Kind: "openai", BaseURL: "https://a.example.com", Model: "model-a1", APIKeyEnv: "REASONIX_TEST_KEY"},
		{Name: "prov-b", Kind: "openai", BaseURL: "https://b.example.com", Model: "model-b1", APIKeyEnv: "REASONIX_TEST_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Runner: runner}), "prov-a/model-a1")
	ctrl := app.activeCtrl()
	app.runtimeRebuildMu.Lock()
	defer app.runtimeRebuildMu.Unlock()
	ctrl.Submit("work")
	<-runner.started
	done := make(chan error, 1)
	go func() { done <- app.DeleteProvider("prov-a") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("saving provider deletion waited for runtime reconstruction")
	}
	if app.activeCtrl() != ctrl || !ctrl.RuntimeStatus().Running {
		t.Fatal("saving deletion interrupted accepted work")
	}
	if _, ok := config.LoadForEdit(config.UserConfigPath()).Provider("prov-a"); ok {
		t.Fatal("deletion was not committed")
	}
	close(runner.release)
	waitNotRunning(t, ctrl)
	ctrl.Close()
}

func TestDeleteProviderPreservesAffectedTabSharedHostReference(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "REASONIX_TEST_KEY", "sk-test")
	cfg := config.Default()
	cfg.DefaultModel = "prov-a/model-a1"
	cfg.Providers = []config.ProviderEntry{
		{Name: "prov-a", Kind: "openai", BaseURL: "https://a.example.com", Model: "model-a1", APIKeyEnv: "REASONIX_TEST_KEY"},
		{Name: "prov-b", Kind: "openai", BaseURL: "https://b.example.com", Model: "model-b1", APIKeyEnv: "REASONIX_TEST_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	hostKey := "provider-shared-host"
	host := app.acquireSharedHost(hostKey)
	ctrl := control.New(control.Options{Host: host})
	tab := &WorkspaceTab{
		ID: "affected", Scope: "global", Ready: true, Ctrl: ctrl,
		model: "prov-a/model-a1", SharedHostKey: hostKey, disabledMCP: map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	if err := app.DeleteProvider("prov-a"); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}
	if tab.SharedHostKey != hostKey || tab.Ctrl != ctrl {
		t.Fatal("saving deletion released the current runtime's shared host")
	}
	app.sharedHostsMu.Lock()
	_, retained := app.sharedHosts[hostKey]
	app.sharedHostsMu.Unlock()
	if !retained {
		t.Fatal("provider deletion released an owned shared host reference")
	}
	ctrl.Close()
	app.releaseSharedHost(hostKey)
}

func TestRemoveBuiltInProviderAccessPreservesAffectedTabSharedHostReference(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "REASONIX_TEST_KEY", "sk-test")
	cfg := config.Default()
	cfg.DefaultModel = "deepseek/deepseek-chat"
	cfg.Providers = []config.ProviderEntry{
		{Name: "deepseek", Kind: "openai", BaseURL: "https://api.deepseek.com", Model: "deepseek-chat", APIKeyEnv: "REASONIX_TEST_KEY"},
		{Name: "prov-b", Kind: "openai", BaseURL: "https://b.example.com", Model: "model-b1", APIKeyEnv: "REASONIX_TEST_KEY"},
	}
	cfg.Desktop.ProviderAccess = []string{"deepseek", "prov-b"}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	hostKey := "provider-access-shared-host"
	host := app.acquireSharedHost(hostKey)
	ctrl := control.New(control.Options{Host: host})
	tab := &WorkspaceTab{
		ID: "affected", Scope: "global", Ready: true, Ctrl: ctrl,
		model: "deepseek/deepseek-chat", SharedHostKey: hostKey, disabledMCP: map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	if err := app.RemoveProviderAccess("deepseek"); err != nil {
		t.Fatalf("RemoveProviderAccess: %v", err)
	}
	if tab.SharedHostKey != hostKey || tab.Ctrl != ctrl {
		t.Fatal("saving access removal released the current runtime's shared host")
	}
	app.sharedHostsMu.Lock()
	_, retained := app.sharedHosts[hostKey]
	app.sharedHostsMu.Unlock()
	if !retained {
		t.Fatal("provider access removal released an owned shared host reference")
	}
	ctrl.Close()
	app.releaseSharedHost(hostKey)
}

func TestDeleteProviderPreservesAffectedBackgroundJobs(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "REASONIX_TEST_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "prov-a/model-a1"
	cfg.Providers = []config.ProviderEntry{
		{Name: "prov-a", Kind: "openai", BaseURL: "https://a.example.com", Model: "model-a1", APIKeyEnv: "REASONIX_TEST_KEY"},
		{Name: "prov-b", Kind: "openai", BaseURL: "https://b.example.com", Model: "model-b1", APIKeyEnv: "REASONIX_TEST_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, "provider-job.jsonl")
	jm := jobs.NewManager(event.Discard)
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: path, Label: "test", Jobs: jm})
	defer ctrl.Close()
	app := NewApp()
	app.setTestCtrl(ctrl, "prov-a/model-a1")
	jm.StartForSession(agent.BranchID(path), "bash", "provider job", func(ctx context.Context, _ io.Writer) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})

	err := app.DeleteProvider("prov-a")
	if err != nil || !controllerHasActiveRuntimeWork(ctrl) {
		t.Fatalf("DeleteProvider interrupted background work: %v", err)
	}
	if _, ok := config.LoadForEdit(config.UserConfigPath()).Provider("prov-a"); ok {
		t.Fatal("provider deletion was not persisted")
	}
}

func TestDeleteProviderPreservesUnaffectedBackgroundJobsWhenSavingConfig(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "REASONIX_TEST_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "prov-b/model-b1"
	cfg.Providers = []config.ProviderEntry{
		{Name: "prov-a", Kind: "openai", BaseURL: "https://a.example.com", Model: "model-a1", APIKeyEnv: "REASONIX_TEST_KEY"},
		{Name: "prov-b", Kind: "openai", BaseURL: "https://b.example.com", Model: "model-b1", APIKeyEnv: "REASONIX_TEST_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()
	app.setTestCtrl(newBackgroundJobController(t, "provider-unaffected-job"), "prov-b/model-b1")

	err := app.DeleteProvider("prov-a")
	if err != nil || !controllerHasActiveRuntimeWork(app.activeCtrl()) {
		t.Fatalf("DeleteProvider interrupted unrelated background work: %v", err)
	}
	if _, ok := config.LoadForEdit(config.UserConfigPath()).Provider("prov-a"); ok {
		t.Fatal("provider deletion was not persisted")
	}
}

func TestRemoveBuiltInProviderAccessPreservesBackgroundJobsWhenSavingConfig(t *testing.T) {
	isolateDesktopUserDirs(t)
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
default_model = "mimo-pro/mimo-v2.5-pro"

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

	app := NewApp()
	app.ctx = context.Background()
	app.setTestCtrl(newBackgroundJobController(t, "provider-access-unaffected-job"), "mimo-token-plan/mimo-v2.5-pro")

	err := app.RemoveProviderAccess("deepseek")
	if err != nil || !controllerHasActiveRuntimeWork(app.activeCtrl()) {
		t.Fatalf("RemoveProviderAccess interrupted background work: %v", err)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	access := providerAccessSet(cfg.Desktop.ProviderAccess)
	if access["deepseek"] || access["deepseek-flash"] {
		t.Fatalf("provider access removal was not persisted: %+v", cfg.Desktop.ProviderAccess)
	}
}

func TestConnectKeySavesWhileBackgroundJobKeepsItsController(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	os.Unsetenv("DEEPSEEK_API_KEY")
	oldFetch := connectKeyBalanceFetch
	connectKeyBalanceFetch = func(context.Context, *http.Client, string, string) (*billing.Balance, error) {
		return &billing.Balance{Available: true}, nil
	}
	t.Cleanup(func() { connectKeyBalanceFetch = oldFetch })

	app := NewApp()
	app.ctx = context.Background()
	app.setTestCtrl(newBackgroundJobController(t, "connect-key-job"), "deepseek-flash/deepseek-v4-flash")
	oldCtrl := app.activeCtrl()

	_, err := app.ConnectKey("sk-test")
	if err != nil {
		t.Fatalf("ConnectKey with background job: %v", err)
	}
	p, ok := config.LoadForEdit(config.UserConfigPath()).Provider("deepseek")
	if !ok || !p.Configured() || app.activeCtrl() != oldCtrl {
		t.Fatal("key save must configure the connection and preserve the background runtime")
	}
}

func TestConnectKeyRestoresDeepSeekProviderAccess(t *testing.T) {
	isolateDesktopUserDirs(t)
	cfg := config.Default()
	cfg.DefaultModel = "custom/custom-model"
	cfg.Desktop.ProviderAccess = []string{"custom"}
	cfg.Providers = []config.ProviderEntry{{
		Name: "custom", Kind: "openai", BaseURL: "https://models.example.invalid/v1",
		Model: "custom-model", APIKeyEnv: "CUSTOM_API_KEY",
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save custom provider config: %v", err)
	}

	oldFetch := connectKeyBalanceFetch
	connectKeyBalanceFetch = func(context.Context, *http.Client, string, string) (*billing.Balance, error) {
		return &billing.Balance{Available: true}, nil
	}
	t.Cleanup(func() { connectKeyBalanceFetch = oldFetch })

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	app.setTestCtrl(control.New(control.Options{Label: "custom"}), "custom/custom-model")
	defer func() {
		if ctrl := app.activeCtrl(); ctrl != nil {
			ctrl.Close()
		}
	}()
	if _, err := app.ConnectKey("sk-test"); err != nil {
		t.Fatalf("ConnectKey: %v", err)
	}

	got := config.LoadForEditWithoutCredentials(config.UserConfigPath())
	if !providerAccessSet(got.Desktop.ProviderAccess)["deepseek"] {
		t.Fatalf("provider_access = %v, want DeepSeek restored", got.Desktop.ProviderAccess)
	}
	if _, ok := got.Provider("deepseek"); !ok {
		t.Fatal("DeepSeek provider template should be restored")
	}
	if app.NeedsOnboarding() {
		t.Fatal("restored DeepSeek access and saved key should satisfy onboarding")
	}
}

func TestConnectKeyFreshInstallUsesDeepSeekChatAndIndependentSearchDefaults(t *testing.T) {
	isolateDesktopUserDirs(t)
	oldFetch := connectKeyBalanceFetch
	connectKeyBalanceFetch = func(context.Context, *http.Client, string, string) (*billing.Balance, error) {
		return &billing.Balance{Available: true}, nil
	}
	t.Cleanup(func() { connectKeyBalanceFetch = oldFetch })

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	app.setTestCtrl(control.New(control.Options{Label: "fresh-install"}), "deepseek-flash/deepseek-v4-flash")
	workspace := t.TempDir()
	app.tabs["test"].WorkspaceRoot = workspace
	defer func() {
		if ctrl := app.activeCtrl(); ctrl != nil {
			ctrl.Close()
		}
	}()

	if _, err := app.ConnectKey("sk-test"); err != nil {
		t.Fatalf("ConnectKey: %v", err)
	}
	cfg, err := config.LoadForRootReadOnly(workspace)
	if err != nil {
		t.Fatalf("load fresh-install config: %v", err)
	}
	entry, ok := cfg.ResolveModel(cfg.DefaultModel)
	if !ok {
		t.Fatalf("default model %q did not resolve", cfg.DefaultModel)
	}
	if entry.Kind != "openai" || entry.BaseURL != "https://api.deepseek.com" ||
		entry.Thinking != "enabled" || !config.EffectiveIndependentWebSearch(entry) || !config.EffectiveVision(entry) {
		t.Fatalf("fresh-install DeepSeek entry = %+v; want Chat Completions, thinking, independent search, and the vendor's image-capable Flash", entry)
	}
	if app.NeedsOnboarding() {
		t.Fatal("fresh-install onboarding should close after the validated DeepSeek key is stored")
	}
}

func TestBalanceForTabUsesDesktopPricingCurrency(t *testing.T) {
	isolateDesktopUserDirs(t)
	cfg := config.Default()
	cfg.Desktop.Currency = "USD"
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save USD desktop currency: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"70.16"},{"currency":"USD","total_balance":"9.82"}]}`)
	}))
	defer srv.Close()

	app := NewApp()
	app.ctx = context.Background()
	ctrl := control.New(control.Options{BalanceURL: srv.URL, BalanceClient: srv.Client()})
	t.Cleanup(ctrl.Close)
	app.setTestCtrl(ctrl, "deepseek/deepseek-v4-flash")

	got := app.BalanceForTab("test")
	// Prefer the matching USD wallet exactly; no FX approximation is used.
	if !got.Available || got.Err != "" {
		t.Fatalf("USD desktop balance = %+v, want available", got)
	}
	if !strings.Contains(got.Display, "9.82") && !strings.Contains(got.Display, "$9.82") {
		t.Fatalf("USD desktop balance display = %q, want USD 9.82", got.Display)
	}
}

func TestConnectKeyRebuildLeaseHeldKeepsCurrentController(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv(onboardingKeyEnv, "")
	os.Unsetenv(onboardingKeyEnv)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")

	oldFetch := connectKeyBalanceFetch
	connectKeyBalanceFetch = func(context.Context, *http.Client, string, string) (*billing.Balance, error) {
		return &billing.Balance{Available: true}, nil
	}
	t.Cleanup(func() { connectKeyBalanceFetch = oldFetch })

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
	sessionPath := filepath.Join(dir, "externally-leased-connect-key.jsonl")
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
		ID:          "tab_connect",
		Scope:       "global",
		SessionPath: sessionPath,
		Ready:       true,
		model:       "old/old-model",
		Ctrl:        oldCtrl,
		sink:        &tabEventSink{tabID: "tab_connect", app: app},
		disabledMCP: map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	warning, err := app.ConnectKey("sk-test")
	if err != nil {
		t.Fatalf("ConnectKey: %v", err)
	}
	if warning != "" {
		t.Fatalf("ConnectKey warning = %q; saving does not acquire a runtime lease", warning)
	}
	if tab.Ctrl != oldCtrl {
		t.Fatalf("tab controller changed after failed connect-key rebuild")
	}
	if tab.StartupErr != "" {
		t.Fatalf("tab startup error = %q, want unchanged current session", tab.StartupErr)
	}
	p, ok := config.LoadForEdit(config.UserConfigPath()).Provider("deepseek")
	if !ok || !p.Configured() {
		t.Fatal("onboarding key should be persisted under the new connection reference")
	}
}

func TestMigrateDesktopPreferencesDoesNotOverwriteExistingConfig(t *testing.T) {
	isolateDesktopUserDirs(t)

	userCfg := config.LoadForEdit(config.UserConfigPath())
	if err := userCfg.SetDesktopLanguage("en"); err != nil {
		t.Fatalf("set desktop language: %v", err)
	}
	if err := userCfg.SetDesktopLayoutStyle("workbench"); err != nil {
		t.Fatalf("set desktop layout style: %v", err)
	}
	if err := userCfg.SetDesktopAppearance("dark", "graphite"); err != nil {
		t.Fatalf("set desktop appearance: %v", err)
	}
	if err := userCfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save user config: %v", err)
	}

	if err := NewApp().MigrateDesktopPreferences("zh", "light", "glacier"); err != nil {
		t.Fatalf("migrate desktop preferences: %v", err)
	}

	got := config.LoadForEdit(config.UserConfigPath())
	if got.DesktopLanguage() != "en" || got.DesktopLayoutStyle() != "workbench" || got.DesktopTheme() != "dark" || got.DesktopThemeStyle() != "graphite" {
		t.Fatalf("desktop prefs after migration = lang:%q layout:%q theme:%q style:%q, want existing config preserved", got.DesktopLanguage(), got.DesktopLayoutStyle(), got.DesktopTheme(), got.DesktopThemeStyle())
	}
}

func TestSetEffortRebuildsController(t *testing.T) {
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

	if err := app.SetEffort("max"); err != nil {
		t.Fatalf("SetEffort(max): %v", err)
	}
	if c := app.activeCtrl(); c == nil {
		t.Fatal("SetEffort should leave a rebuilt controller")
	}
	if c := app.activeCtrl(); c == old {
		t.Fatal("SetEffort should rebuild the active controller so the provider sees the new effort")
	}
	// Task 611: deterministic oracle — the 2s bounded read flaked here under
	// load (cap fired, fallback served "auto").
	if got := app.effortForTabDirect("").Current; got != "max" {
		t.Fatalf("Effort current = %q, want max", got)
	}
}

func TestSetEffortMigratesStaleOfficialDeepSeekTabModel(t *testing.T) {
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

	if err := app.SetEffort("max"); err != nil {
		t.Fatalf("SetEffort(max): %v", err)
	}
	tab := app.activeTab()
	if tab == nil {
		t.Fatal("active tab missing")
	}
	if tab.model != "deepseek/deepseek-v4-flash" {
		t.Fatalf("tab model = %q, want migrated official ref", tab.model)
	}
}

func captureTabNotices(app *App, tab *WorkspaceTab) *[]string {
	var notices []string
	if tab.sink == nil {
		tab.sink = &tabEventSink{tabID: tab.ID, app: app, ctx: context.Background()}
	}
	tab.sink.SetBotSink(event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && strings.TrimSpace(e.Text) != "" {
			notices = append(notices, e.Text)
		}
	}))
	return &notices
}
