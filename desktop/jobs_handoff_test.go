package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/jobs"
	"reasonix/internal/workspacelease"
)

// startBlockingDesktopJob registers a job scoped to sessionPath that blocks
// until released; mirrors the user scenario behind task 890 (a background
// demo server that must not veto — or die from — a model switch).
func startBlockingDesktopJob(t *testing.T, jm *jobs.Manager, sessionPath string) (release func(), finished <-chan struct{}) {
	t.Helper()
	done := make(chan struct{})
	releaseCh := make(chan struct{})
	jm.StartForSession(agent.BranchID(sessionPath), "bash", "demo server", func(jobCtx context.Context, _ io.Writer) (string, error) {
		defer close(done)
		select {
		case <-releaseCh:
			return "stopped", nil
		case <-jobCtx.Done():
			return "", jobCtx.Err()
		}
	})
	var once sync.Once
	return func() { once.Do(func() { close(releaseCh) }) }, done
}

// newHandoffDesktopJobPair builds the shared manager + write lease a task-890
// handoff travels with, plus a blocking job on sessionPath.
func newHandoffDesktopJobPair(t *testing.T, sessionPath string) (*jobs.Manager, *workspacelease.Owner, func(), <-chan struct{}) {
	t.Helper()
	jm := jobs.NewManager(event.Discard)
	lease, err := workspacelease.New(t.TempDir(), filepath.Join(t.TempDir(), "locks"), nil)
	if err != nil {
		t.Fatalf("workspace lease: %v", err)
	}
	release, finished := startBlockingDesktopJob(t, jm, sessionPath)
	return jm, lease, release, finished
}

// TestSetModelForTabBackgroundJobsSurviveRebuild is acceptance ① (task 890):
// with the turn ended (running=false, no pending prompt) a background job
// alone must not block the model switch, and the job must survive the swap —
// the outgoing controller hands its manager and lease to the replacement,
// whose runtime still counts the job, and the outgoing build-scoped cleanup
// waits for the job to exit.
func TestSetModelForTabBackgroundJobsSurviveRebuild(t *testing.T) {
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

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	oldPath := filepath.Join(dir, "old.jsonl")
	oldExec := agent.New(nil, nil, agent.NewSession("old system prompt"), agent.Options{}, event.Discard)
	jm, lease, releaseJob, finished := newHandoffDesktopJobPair(t, oldPath)
	defer jm.Close()

	var oldCleaned atomic.Bool
	oldCtrl := control.New(control.Options{
		Executor:       oldExec,
		SessionDir:     dir,
		SessionPath:    oldPath,
		Label:          "old",
		Sink:           event.Discard,
		Jobs:           jm,
		WorkspaceLease: lease,
		Cleanup:        func() { oldCleaned.Store(true) },
	})

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	tab := reloadRuntimeTab(t, app, dir, oldCtrl)
	// Persona config: crossing the official DeepSeek-V4-Pro boundary declines
	// the per-request fast path, forcing the guarded build+swap path under test.
	tab.model = "official-ds/deepseek-v4-pro"

	if err := app.SetModelForTab(tab.ID, "prov-b/model-b1"); err != nil {
		t.Fatalf("model switch with a background job (running=false) refused: %v", err)
	}

	newCtrl, ok := tab.Ctrl.(*control.Controller)
	if !ok {
		t.Fatalf("tab.Ctrl = %T, want *control.Controller", tab.Ctrl)
	}
	if newCtrl == oldCtrl {
		t.Fatal("model switch kept the outgoing controller; rebuild path was not exercised")
	}
	if newCtrl.JobsManager() != jm {
		t.Fatal("replacement did not adopt the outgoing job manager (handoff missing)")
	}
	if newCtrl.WorkspaceLeaseOwner() != lease {
		t.Fatal("replacement did not receive the outgoing write lease (handoff missing)")
	}
	select {
	case <-finished:
		t.Fatal("the model switch killed the running background job")
	default:
	}
	if !jm.HasUnfinishedForSession(agent.BranchID(oldPath)) {
		t.Fatal("background job no longer unfinished after the swap")
	}
	if got := newCtrl.RuntimeStatus().BackgroundJobs; got != 1 {
		t.Fatalf("replacement BackgroundJobs = %d, want 1 (job must stay visible)", got)
	}
	if oldCleaned.Load() {
		t.Fatal("outgoing build-scoped cleanup ran while the handed-off job was still running")
	}

	releaseJob()
	waitForDesktop(t, "outgoing cleanup after the job exited", oldCleaned.Load)
	<-finished
}

// TestRefreshTabModelSettingsAppliesDespiteBackgroundJobs is acceptance ③
// (task 890): the settings-apply rebuild turn admission drives after a model
// settings save must proceed with a background job alone — no more
// "saved but this session could not apply them" half-commit — and the applied
// state must settle so the next admission does not re-fail.
func TestRefreshTabModelSettingsAppliesDespiteBackgroundJobs(t *testing.T) {
	dir := reloadRuntimeFixture(t)

	oldPath := filepath.Join(dir, "old.jsonl")
	oldExec := agent.New(nil, nil, agent.NewSession("old system prompt"), agent.Options{}, event.Discard)
	jm, lease, releaseJob, finished := newHandoffDesktopJobPair(t, oldPath)
	defer jm.Close()

	var oldCleaned atomic.Bool
	oldCtrl := control.New(control.Options{
		Executor:       oldExec,
		SessionDir:     dir,
		SessionPath:    oldPath,
		Label:          "old",
		Sink:           event.Discard,
		Jobs:           jm,
		WorkspaceLease: lease,
		Cleanup:        func() { oldCleaned.Store(true) },
		// A live settings bundle whose applied revision no longer matches the
		// desired one — the exact stale state turn admission acts on.
		ModelSettingsRevision: "applied-rev",
		ModelSettingsCurrent:  func() (string, error) { return "desired-rev", nil },
	})

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	tab := reloadRuntimeTab(t, app, dir, oldCtrl)

	if err := app.refreshTabModelSettings(tab); err != nil {
		t.Fatalf("settings apply with a background job refused: %v", err)
	}

	newCtrl, ok := tab.Ctrl.(*control.Controller)
	if !ok {
		t.Fatalf("tab.Ctrl = %T, want *control.Controller", tab.Ctrl)
	}
	if newCtrl == oldCtrl {
		t.Fatal("settings apply did not rebuild; stale state was never exercised")
	}
	if newCtrl.JobsManager() != jm {
		t.Fatal("settings rebuild did not hand the job manager to the replacement")
	}
	app.mu.RLock()
	failure := tab.modelApplication.failure
	app.mu.RUnlock()
	if failure != nil {
		t.Fatalf("half-commit residue: modelApplication.failure = %+v", failure)
	}
	if needed, err := modelSettingsNeedApply(newCtrl); err != nil || needed {
		t.Fatalf("ModelSettingsState after apply: needed=%t err=%v, want settled (admission must not re-fail)", needed, err)
	}
	select {
	case <-finished:
		t.Fatal("the settings apply killed the running background job")
	default:
	}
	if !jm.HasUnfinishedForSession(agent.BranchID(oldPath)) {
		t.Fatal("background job no longer unfinished after the settings rebuild")
	}
	if oldCleaned.Load() {
		t.Fatal("outgoing build-scoped cleanup ran while the handed-off job was still running")
	}

	releaseJob()
	waitForDesktop(t, "outgoing cleanup after the job exited", oldCleaned.Load)
	<-finished
}

// waitForDesktop polls cond until it holds or the deadline passes.
func waitForDesktop(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestRebuildModelSwitchGuardOnlyIgnoresBackgroundJobs is acceptance ② (task
// 890, extended by task 974): the demotion is scoped to the jobs-handoff
// settings (model, saved model settings, effort) — background jobs alone
// never block them, while a running turn or pending prompt still blocks them
// exactly as before, and every other setting keeps the full check.
func TestRebuildModelSwitchGuardOnlyIgnoresBackgroundJobs(t *testing.T) {
	cases := []struct {
		name     string
		setting  string
		status   control.RuntimeStatus
		wantBusy bool
	}{
		{"model ignores background jobs", "model", control.RuntimeStatus{BackgroundJobs: 1}, false},
		{"model still blocks on a running turn", "model", control.RuntimeStatus{Running: true, BackgroundJobs: 1}, true},
		{"model still blocks on a pending prompt", "model", control.RuntimeStatus{PendingPrompt: true}, true},
		{"saved model settings ignore background jobs", "saved model settings", control.RuntimeStatus{BackgroundJobs: 2}, false},
		{"saved model settings still block on a running turn", "saved model settings", control.RuntimeStatus{Running: true}, true},
		// Task 974 demotes effort onto the same jobs-handoff admission.
		{"effort ignores background jobs", "effort", control.RuntimeStatus{BackgroundJobs: 1}, false},
		{"effort still blocks on a running turn", "effort", control.RuntimeStatus{Running: true, BackgroundJobs: 1}, true},
		{"effort still blocks on a pending prompt", "effort", control.RuntimeStatus{PendingPrompt: true}, true},
		{"sandbox still blocks on background jobs", "sandbox", control.RuntimeStatus{BackgroundJobs: 1}, true},
		{"MCP server still blocks on background jobs", "MCP server", control.RuntimeStatus{BackgroundJobs: 1}, true},
		{"runtime reload still blocks on background jobs", "runtime reload", control.RuntimeStatus{BackgroundJobs: 1}, true},
		{"settings still block on background jobs", "settings", control.RuntimeStatus{BackgroundJobs: 1}, true},
		{"idle never blocks", "model", control.RuntimeStatus{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := &backgroundRuntimeController{status: tc.status}
			err := rebuildControllerActiveWorkErrorFor(ctrl, tc.setting)
			var busy *rebuildBusyError
			gotBusy := errors.As(err, &busy)
			if gotBusy != tc.wantBusy {
				t.Fatalf("rebuildControllerActiveWorkErrorFor(%q, %+v) err = %v, wantBusy = %t",
					tc.setting, tc.status, err, tc.wantBusy)
			}
			if gotBusy && busy.setting != tc.setting {
				t.Fatalf("busy.setting = %q, want %q", busy.setting, tc.setting)
			}
		})
	}
}

// TestJobsHandoffFromFallsBackWithoutConcretePair pins the no-handoff
// fallbacks (task 890): anything that is not a concrete controller holding
// BOTH the manager and the lease keeps the pre-890 fresh-pair behavior.
func TestJobsHandoffFromFallsBackWithoutConcretePair(t *testing.T) {
	if jm, lease := jobsHandoffFrom(nil); jm != nil || lease != nil {
		t.Fatalf("nil controller handed off (%v, %v), want (nil, nil)", jm, lease)
	}
	if jm, lease := jobsHandoffFrom(&backgroundRuntimeController{}); jm != nil || lease != nil {
		t.Fatalf("stub controller handed off (%v, %v), want (nil, nil)", jm, lease)
	}
	plain := control.New(control.Options{Sink: event.Discard, SessionDir: t.TempDir()})
	defer plain.Close()
	if jm, lease := jobsHandoffFrom(plain); jm != nil || lease != nil {
		t.Fatalf("controller without the pair handed off (%v, %v), want (nil, nil)", jm, lease)
	}
}
