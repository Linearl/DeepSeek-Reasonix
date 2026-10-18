package main

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
)

// TestSetEffortForTabBackgroundJobsSurviveRebuild is acceptance ①③ (task 974):
// with the turn ended (running=false, no pending prompt) a background job
// alone must not block an effort switch, the job must survive every rebuild
// in the switch chain (the outgoing controller hands its manager and lease to
// the replacement, same as the task-890 model swap), and the effort tiers can
// be cycled end to end while the job keeps running.
func TestSetEffortForTabBackgroundJobsSurviveRebuild(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "REASONIX_TEST_KEY", "sk-test")
	cfg := config.Default()
	cfg.DefaultModel = "official-ds/deepseek-v4-pro"
	cfg.Providers = []config.ProviderEntry{
		{Name: "official-ds", Kind: "openai", BaseURL: "https://api.deepseek.com",
			Model: "deepseek-v4-pro", Models: []string{"deepseek-v4-pro"},
			// Declared vocabulary keeps every tier distinct (the DeepSeek
			// protocol fallback would fold low/medium onto high), so the cycle
			// below really exercises four different depths.
			SupportedEfforts: []string{"disabled", "low", "high", "max"},
			APIKeyEnv:        "REASONIX_TEST_KEY"},
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
	// The stub executor's provider exposes no per-request vocabulary, so the
	// fast path declines and the guarded build+swap path under test runs.
	tab.model = "official-ds/deepseek-v4-pro"

	if err := app.SetEffortForTab(tab.ID, "low"); err != nil {
		t.Fatalf("effort switch with a background job (running=false) refused: %v", err)
	}

	newCtrl, ok := tab.Ctrl.(*control.Controller)
	if !ok {
		t.Fatalf("tab.Ctrl = %T, want *control.Controller", tab.Ctrl)
	}
	if newCtrl == oldCtrl {
		t.Fatal("effort switch kept the outgoing controller; rebuild path was not exercised")
	}
	if newCtrl.JobsManager() != jm {
		t.Fatal("replacement did not adopt the outgoing job manager (handoff missing)")
	}
	if newCtrl.WorkspaceLeaseOwner() != lease {
		t.Fatal("replacement did not receive the outgoing write lease (handoff missing)")
	}
	select {
	case <-finished:
		t.Fatal("the effort switch killed the running background job")
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

	// Cycle the remaining effort tiers while the job keeps running; each
	// switch must land the normalized depth on the tab and keep the job —
	// and the shared manager behind it — alive across whatever path (rebuild
	// or per-request fast path) it takes.
	for _, level := range []string{"high", "max", "disabled", "low"} {
		if err := app.SetEffortForTab(tab.ID, level); err != nil {
			t.Fatalf("effort switch to %q with a background job refused: %v", level, err)
		}
		entry, err := app.currentProviderEntryForTab(tab.ID)
		if err != nil {
			t.Fatalf("resolve entry after switching to %q: %v", level, err)
		}
		want, nerr := config.NormalizeEffort(entry, level)
		if nerr != nil {
			t.Fatalf("normalize %q: %v", level, nerr)
		}
		app.mu.RLock()
		got := ""
		if tab.effort != nil {
			got = *tab.effort
		}
		app.mu.RUnlock()
		if got != want {
			t.Fatalf("tab.effort after switching to %q = %q, want %q", level, got, want)
		}
		ctrl, ok := app.controllerForTab(tab).(*control.Controller)
		if !ok {
			t.Fatalf("tab.Ctrl = %T after switching to %q, want *control.Controller", app.controllerForTab(tab), level)
		}
		if ctrl.JobsManager() != jm {
			t.Fatalf("job manager handoff lost after switching to %q", level)
		}
		if !jm.HasUnfinishedForSession(agent.BranchID(oldPath)) {
			t.Fatalf("background job died after switching to %q", level)
		}
	}

	releaseJob()
	waitForDesktop(t, "outgoing cleanup after the job exited", oldCleaned.Load)
	<-finished
}
