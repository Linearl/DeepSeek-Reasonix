package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"reasonix/internal/control"
)

// heartbeatLegacyWaitWindow is the window the heartbeat controller wait used to
// be pinned to: `for range 40 { ...; time.Sleep(250ms) }` = 10s. Task 197 was
// measured at 10.23s of cold boot, i.e. 0.23s past it, and the run then skipped
// the approval mode + prompt steps with no signal beyond one log line.
const heartbeatLegacyWaitWindow = 10 * time.Second

// heartbeatColdBootBeyondLegacyWindow is how long the stubbed cold boot takes in
// TestHeartbeatExecuteTaskWaitsForSlowColdBoot: just past the legacy window, so
// a wait that still gives up on a fixed deadline fails the test while a wait
// that follows the build's completion signal passes.
const heartbeatColdBootBeyondLegacyWindow = heartbeatLegacyWaitWindow + 300*time.Millisecond

// publishControllerAfterDelay watches for the heartbeat run's tab, stops its
// real build, and then publishes ctrl only after delay — faking a cold boot that
// outlives the legacy wait window. Mirrors the injection harness of
// TestHeartbeatExecuteTaskPersistsFreshConversationTopicID. The publisher stops
// at t.Cleanup, so no goroutine outlives the test.
func publishControllerAfterDelay(t *testing.T, app *App, ctrl control.SessionAPI, delay time.Duration) <-chan string {
	t.Helper()
	tabID := make(chan string, 1)
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		for {
			var (
				target *WorkspaceTab
				cancel context.CancelFunc
			)
			app.mu.Lock()
			for _, tab := range app.tabs {
				if tab == nil || tab.Ctrl != nil {
					continue
				}
				tab.removed = true
				target, cancel = tab, tab.buildCancel
				break
			}
			app.mu.Unlock()
			if target == nil {
				select {
				case <-time.After(5 * time.Millisecond):
					continue
				case <-stop:
					return
				}
			}
			if cancel != nil {
				// Stop the real build so it cannot publish a controller of its
				// own; the wait then has nothing but the stub to find.
				cancel()
			}
			select {
			case <-time.After(delay):
			case <-stop:
				return
			}
			app.mu.Lock()
			target.Ctrl = ctrl
			target.Ready = true
			target.StartupErr = ""
			app.advanceSessionRuntimeEpochLocked(target)
			app.mu.Unlock()
			tabID <- target.ID
			return
		}
	}()
	return tabID
}

// TestHeartbeatExecuteTaskWaitsForSlowColdBoot is the task-197 regression: a
// session whose controller build outlives the legacy 10s window must still get
// the task's approval mode and its prompt.
func TestHeartbeatExecuteTaskWaitsForSlowColdBoot(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	app.runtimeEvents.emit = func(context.Context, string, ...any) {}
	engine := &HeartbeatEngine{
		app:           app,
		pendingTopics: map[string]heartbeatPendingTopic{},
	}
	seed := HeartbeatTask{
		ID:           "cold-boot",
		Title:        "Cold boot",
		Prompt:       "wake up",
		ApprovalMode: "auto",
	}
	if err := engine.saveTasks([]HeartbeatTask{seed}); err != nil {
		t.Fatal(err)
	}
	engine.ReloadConfig()

	ctrl := &heartbeatExecuteTaskCtrlStub{}
	tabID := publishControllerAfterDelay(t, app, ctrl, heartbeatColdBootBeyondLegacyWindow)

	got := engine.executeTask(seed)

	if got.TopicID == "" {
		t.Fatal("task should have created a topic before waiting for the controller")
	}
	if got.LastRunAt == 0 {
		t.Fatalf("a cold boot of %s (legacy window %s) must not skip the run",
			heartbeatColdBootBeyondLegacyWindow, heartbeatLegacyWaitWindow)
	}
	if len(ctrl.submitted) != 1 || ctrl.submitted[0] != "wake up" {
		t.Fatalf("submitted prompts = %v, want [wake up]", ctrl.submitted)
	}
	if ctrl.approvalMode != "auto" {
		t.Fatalf("approval mode = %q, want the task's configured auto", ctrl.approvalMode)
	}
	if id := <-tabID; id == "" {
		t.Fatal("controller was published to an unnamed tab")
	}
}

// TestHeartbeatExecuteTaskSkipsWhenTheBuildNeverFinishes pins the other half of
// task 197: a build that really does not finish must skip the run without
// touching LastRunAt (so the next tick retries), must give up on the configured
// bound instead of hanging, and must leave a reason an operator can act on.
func TestHeartbeatExecuteTaskSkipsWhenTheBuildNeverFinishes(t *testing.T) {
	isolateDesktopUserDirs(t)
	original := heartbeatControllerWaitTimeout
	heartbeatControllerWaitTimeout = 500 * time.Millisecond
	t.Cleanup(func() { heartbeatControllerWaitTimeout = original })

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	app.runtimeEvents.emit = func(context.Context, string, ...any) {}
	engine := &HeartbeatEngine{
		app:           app,
		pendingTopics: map[string]heartbeatPendingTopic{},
	}
	seed := HeartbeatTask{
		ID:           "stuck-boot",
		Title:        "Stuck boot",
		Prompt:       "never delivered",
		ApprovalMode: "auto",
	}
	if err := engine.saveTasks([]HeartbeatTask{seed}); err != nil {
		t.Fatal(err)
	}
	engine.ReloadConfig()

	ctrl := &heartbeatExecuteTaskCtrlStub{}
	// Published only after an hour: the build never finishes within the run.
	tabID := publishControllerAfterDelay(t, app, ctrl, time.Hour)

	start := time.Now()
	got := engine.executeTask(seed)
	elapsed := time.Since(start)

	if len(ctrl.submitted) != 0 {
		t.Fatalf("submitted prompts = %v, want none while the controller is missing", ctrl.submitted)
	}
	if got.LastRunAt != 0 {
		t.Fatal("a skipped run must leave LastRunAt untouched so the next tick retries")
	}
	if elapsed > 30*time.Second {
		t.Fatalf("wait took %s, want it bounded by the configured %s", elapsed, heartbeatControllerWaitTimeout)
	}

	// The skipped run has to be diagnosable: the tab it waited on must still be
	// there and explain itself (the panel alone would keep showing "never ran").
	var waited string
	app.mu.RLock()
	for id, tab := range app.tabs {
		if tab != nil && tab.Ctrl == nil {
			waited = id
			break
		}
	}
	app.mu.RUnlock()
	if waited == "" {
		t.Fatal("the skipped run left no controller-less tab to diagnose")
	}
	if reason := app.tabControllerWaitReason(waited); reason == "" || reason == "tab is gone" {
		t.Fatalf("skipped run reason for %s = %q, want an actionable reason", waited, reason)
	}
	select {
	case <-tabID:
		t.Fatal("the stub controller was published before the wait gave up")
	default:
	}
}

// TestAwaitTabControllerReturnsOnBuildSignal drives the happy arm of the wait in
// isolation: the build's completion channel wins, and the wait returns the
// controller that was published with it.
func TestAwaitTabControllerReturnsOnBuildSignal(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	engine := &HeartbeatEngine{app: app}

	tab := &WorkspaceTab{ID: "tab-signal", buildDone: make(chan struct{})}
	app.mu.Lock()
	app.tabs[tab.ID] = tab
	app.mu.Unlock()

	ctrl := &heartbeatExecuteTaskCtrlStub{}
	go func() {
		time.Sleep(100 * time.Millisecond)
		app.mu.Lock()
		tab.Ctrl = ctrl
		tab.Ready = true
		app.advanceSessionRuntimeEpochLocked(tab)
		close(tab.buildDone)
		tab.buildDone = nil
		app.mu.Unlock()
	}()

	start := time.Now()
	got := engine.awaitTabController(tab.ID)
	if got == nil {
		t.Fatal("awaitTabController returned nil although the build signalled completion")
	}
	if got != heartbeatRuntimeStatus(ctrl) {
		t.Fatalf("awaitTabController returned %#v, want the published stub", got)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("wait took %s, want it to end with the build signal", elapsed)
	}
}

// TestAwaitTabControllerTimesOutWithoutAController drives the timeout arm in
// isolation: no build channel and no controller means the wait gives up on the
// bound instead of blocking forever.
func TestAwaitTabControllerTimesOutWithoutAController(t *testing.T) {
	isolateDesktopUserDirs(t)
	original := heartbeatControllerWaitTimeout
	heartbeatControllerWaitTimeout = 150 * time.Millisecond
	t.Cleanup(func() { heartbeatControllerWaitTimeout = original })

	app := NewApp()
	engine := &HeartbeatEngine{app: app}
	app.mu.Lock()
	app.tabs["tab-idle"] = &WorkspaceTab{ID: "tab-idle"}
	app.mu.Unlock()

	start := time.Now()
	if got := engine.awaitTabController("tab-idle"); got != nil {
		t.Fatalf("awaitTabController returned %#v, want nil without a controller", got)
	}
	if elapsed := time.Since(start); elapsed < heartbeatControllerWaitTimeout {
		t.Fatalf("wait ended after %s, want it to hold for the %s bound", elapsed, heartbeatControllerWaitTimeout)
	}
	if got := engine.awaitTabController("missing-tab"); got != nil {
		t.Fatalf("awaitTabController returned %#v for a missing tab, want nil", got)
	}
}

// TestTabControllerWaitReasonDescribesEveryTimeoutState keeps the skipped-run
// diagnosis honest: an operator reading the WARN must be able to tell a slow
// build from a failed one.
func TestTabControllerWaitReasonDescribesEveryTimeoutState(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()

	if reason := app.tabControllerWaitReason("missing-tab"); reason != "tab is gone" {
		t.Fatalf("missing tab reason = %q", reason)
	}

	app.mu.Lock()
	app.tabs["tab-failed"] = &WorkspaceTab{ID: "tab-failed", StartupErr: "build exploded"}
	app.tabs["tab-removed"] = &WorkspaceTab{ID: "tab-removed", removed: true}
	app.tabs["tab-building"] = &WorkspaceTab{ID: "tab-building", buildDone: make(chan struct{})}
	app.tabs["tab-idle"] = &WorkspaceTab{ID: "tab-idle"}
	app.mu.Unlock()

	for tabID, want := range map[string]string{
		"tab-failed":   "controller build failed:",
		"tab-removed":  "removed while waiting",
		"tab-building": "build still running",
		"tab-idle":     "no controller and no build in flight",
	} {
		if reason := app.tabControllerWaitReason(tabID); !strings.Contains(reason, want) {
			t.Fatalf("reason for %s = %q, want it to contain %q", tabID, reason, want)
		}
	}
}
