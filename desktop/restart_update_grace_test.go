package main

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/installlayout"
)

// restartProbeController is a SessionAPI fake for the task-450 grace-window
// tests: a mutable runtime status (so a test can simulate a turn finishing on
// its own) and a Cancel counter (so a test can prove who got interrupted).
type restartProbeController struct {
	stubSessionAPI
	mu               sync.Mutex
	status           control.RuntimeStatus
	path             string
	cancelCalls      int
	inboxPausedCalls []bool
}

func (c *restartProbeController) RuntimeStatus() control.RuntimeStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *restartProbeController) SessionPath() string { return c.path }

// Cancel records the interruption and unwinds the fake turn to idle — what a
// real controller does when its context is cancelled.
func (c *restartProbeController) Cancel() {
	c.mu.Lock()
	c.cancelCalls++
	c.status = control.RuntimeStatus{}
	c.mu.Unlock()
}

func (c *restartProbeController) setStatus(s control.RuntimeStatus) {
	c.mu.Lock()
	c.status = s
	c.mu.Unlock()
}

func (c *restartProbeController) cancelCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cancelCalls
}

// SetInboxPausedPassive is overridden because maybeResumeAutonomousUpdateTab
// clears the recovery pause through it; the embedded nil SessionAPI would
// panic on the promoted method.
func (c *restartProbeController) SetInboxPausedPassive(paused bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inboxPausedCalls = append(c.inboxPausedCalls, paused)
	return nil
}

// shrinkRestartWindows replaces the grace/settle windows with test-sized ones
// and restores the production defaults on cleanup.
func shrinkRestartWindows(t *testing.T, grace, settle time.Duration) {
	t.Helper()
	oldGrace, oldPoll := restartGraceWait, restartGracePoll
	oldSettle, oldSettlePoll := restartCancelSettle, restartCancelSettlePoll
	restartGraceWait, restartGracePoll = grace, 20*time.Millisecond
	restartCancelSettle, restartCancelSettlePoll = settle, 10*time.Millisecond
	t.Cleanup(func() {
		restartGraceWait, restartGracePoll = oldGrace, oldPoll
		restartCancelSettle, restartCancelSettlePoll = oldSettle, oldSettlePoll
	})
}

// stubRestartSeams swaps the restart-family process seams for recorders and
// restores the originals on cleanup.
func stubRestartSeams(t *testing.T) (heartbeatStopped, launcherStarted, quitCalled *atomic.Bool) {
	t.Helper()
	heartbeatStopped = &atomic.Bool{}
	launcherStarted = &atomic.Bool{}
	quitCalled = &atomic.Bool{}
	oldStop, oldStart, oldQuit := restartStopHeartbeat, restartStartLauncher, restartQuit
	restartStopHeartbeat = func(*App) { heartbeatStopped.Store(true) }
	restartStartLauncher = func(string, int) error { launcherStarted.Store(true); return nil }
	restartQuit = func(*App) { quitCalled.Store(true) }
	t.Cleanup(func() {
		restartStopHeartbeat, restartStartLauncher, restartQuit = oldStop, oldStart, oldQuit
	})
	return heartbeatStopped, launcherStarted, quitCalled
}

// captureSlogWarnings routes the default logger's warnings into a buffer so a
// test can pin the log face of the forced marker (acceptance 4).
func captureSlogWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return buf
}

// waitFor polls until cond is true or the timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

// rosterPaths reads the auto-resume roster as plain paths.
func rosterPaths(t *testing.T) []string {
	t.Helper()
	state := readAutonomousUpdateResumeFile()
	out := make([]string, 0, len(state.Sessions))
	for _, s := range state.Sessions {
		out = append(out, s.Path)
	}
	return out
}

// setupRollbackStage wires an isolated install root (active + rollback, both
// healthy) with the experiment on — the execute-rollback face of the 254 tool.
func setupRollbackStage(t *testing.T) (root, active, rollback string) {
	t.Helper()
	isolateDesktopUserDirs(t)
	active = "v1.38.3-20260923-1010"
	rollback = "v1.38.3-20260922-0900"
	root = fakeInstallRoot(t, []string{active, rollback}, active)
	addVersionTreeWithCLI(t, root, rollback, true)
	fakeVersionedConfig(t, true)
	return root, active, rollback
}

// TestGraceWindowThreeBusyTabsStillExecute is acceptance 1: caller exempt,
// a heartbeat tab Running, an audit tab with background jobs — execute must
// NOT return a busy error, must finish well inside N+3s+margin, and the
// process must enter its exit flow (launcher started, quit scheduled).
func TestGraceWindowThreeBusyTabsStillExecute(t *testing.T) {
	root, _, rollback := setupRollbackStage(t)
	shrinkRestartWindows(t, 250*time.Millisecond, 400*time.Millisecond)
	// The rollback face carries its own seams (versionSwitch*); record through
	// them and restore via fakeInstallRoot's cleanup + our own.
	heartbeatStopped, launcherStarted, quitCalled := stubRestartSeams(t)
	oldStart, oldQuit := versionSwitchStartLauncher, versionSwitchQuit
	versionSwitchStartLauncher = func(string, int) error { launcherStarted.Store(true); return nil }
	versionSwitchQuit = func(*App) { quitCalled.Store(true) }
	t.Cleanup(func() { versionSwitchStartLauncher, versionSwitchQuit = oldStart, oldQuit })

	caller := filepath.Join(t.TempDir(), "caller.session.jsonl")
	heartbeat := filepath.Join(t.TempDir(), "heartbeat.session.jsonl")
	audit := filepath.Join(t.TempDir(), "audit.session.jsonl")
	app := &App{tabs: map[string]*WorkspaceTab{
		"t1": {ID: "t1", SessionPath: caller, Ctrl: &restartProbeController{path: caller, status: control.RuntimeStatus{Running: true}}, autopilot: true, Ready: true},
		"t2": {ID: "t2", SessionPath: heartbeat, Ctrl: &restartProbeController{path: heartbeat, status: control.RuntimeStatus{Running: true}}, Ready: true},
		"t3": {ID: "t3", SessionPath: audit, Ctrl: &restartProbeController{path: audit, status: control.RuntimeStatus{BackgroundJobs: 2}}, Ready: true},
	}}
	controller := newAutonomousUpdateController(app)
	if _, err := controller.SetTarget(context.Background(), rollback); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	result, err := controller.ExecuteTarget(context.Background(), caller)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("execute must not be refused by busy tabs (task 450): %v", err)
	}
	if !strings.Contains(result, "do not retry") {
		t.Fatalf("result must warn against retrying: %q", result)
	}
	// Real N is 10s: with the window shrunk, anything above this ceiling means
	// the code waited on the production constant instead of the knob.
	if elapsed > 5*time.Second {
		t.Fatalf("execute took %s; the grace window must honor the (shrunk) knob", elapsed)
	}
	ptr, err := installlayout.ReadCurrent(root)
	if err != nil {
		t.Fatal(err)
	}
	if ptr.ActiveVersion != rollback {
		t.Fatalf("active version = %q, want %q (swap committed)", ptr.ActiveVersion, rollback)
	}
	if !heartbeatStopped.Load() {
		t.Fatal("the heartbeat engine must be stopped before the window opens")
	}
	if !launcherStarted.Load() {
		t.Fatal("the launcher must start — the process enters its exit flow")
	}
	// The quit fires after the 3s tool grace (const, not shrunk): drain it here
	// so the late goroutine cannot leak into a later test's recorder.
	waitFor(t, 5*time.Second, quitCalled.Load)
	// Everyone the window cancelled (heartbeat + audit tabs) is staged for
	// resume, next to the caller.
	roster := rosterPaths(t)
	if len(roster) != 3 {
		t.Fatalf("roster = %v, want caller + heartbeat + audit sessions", roster)
	}
}

// TestGraceWindowNaturalSettlingCancelsNothing is acceptance 2: work that
// finishes inside the window passes with zero interruptions — no Cancel on
// either busy tab, and only the caller lands in the roster.
func TestGraceWindowNaturalSettlingCancelsNothing(t *testing.T) {
	_, _, rollback := setupRollbackStage(t)
	shrinkRestartWindows(t, 1500*time.Millisecond, 300*time.Millisecond)
	_, _, quitCalled := stubRestartSeams(t)
	oldStart, oldQuit := versionSwitchStartLauncher, versionSwitchQuit
	versionSwitchStartLauncher = func(string, int) error { return nil }
	versionSwitchQuit = func(*App) { quitCalled.Store(true) }
	t.Cleanup(func() { versionSwitchStartLauncher, versionSwitchQuit = oldStart, oldQuit })

	caller := filepath.Join(t.TempDir(), "caller.session.jsonl")
	heartbeatCtrl := &restartProbeController{path: filepath.Join(t.TempDir(), "heartbeat.session.jsonl"), status: control.RuntimeStatus{Running: true}}
	auditCtrl := &restartProbeController{path: filepath.Join(t.TempDir(), "audit.session.jsonl"), status: control.RuntimeStatus{BackgroundJobs: 1}}
	app := &App{tabs: map[string]*WorkspaceTab{
		"t1": {ID: "t1", SessionPath: caller, Ctrl: &restartProbeController{path: caller, status: control.RuntimeStatus{Running: true}}, autopilot: true, Ready: true},
		"t2": {ID: "t2", SessionPath: heartbeatCtrl.path, Ctrl: heartbeatCtrl, Ready: true},
		"t3": {ID: "t3", SessionPath: auditCtrl.path, Ctrl: auditCtrl, Ready: true},
	}}
	// Both tabs settle on their own, well inside the window.
	time.AfterFunc(60*time.Millisecond, func() { heartbeatCtrl.setStatus(control.RuntimeStatus{}) })
	time.AfterFunc(60*time.Millisecond, func() { auditCtrl.setStatus(control.RuntimeStatus{}) })

	controller := newAutonomousUpdateController(app)
	if _, err := controller.SetTarget(context.Background(), rollback); err != nil {
		t.Fatal(err)
	}
	result, err := controller.ExecuteTarget(context.Background(), caller)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, restartForcedMarker) {
		t.Fatalf("natural settling must not carry the forced marker: %q", result)
	}
	if got := heartbeatCtrl.cancelCount(); got != 0 {
		t.Fatalf("heartbeat tab cancelled %d times; natural settling must interrupt nobody", got)
	}
	if got := auditCtrl.cancelCount(); got != 0 {
		t.Fatalf("audit tab cancelled %d times; natural settling must interrupt nobody", got)
	}
	roster := rosterPaths(t)
	if len(roster) != 1 || roster[0] != caller {
		t.Fatalf("roster = %v, want exactly the caller", roster)
	}
}

// TestGraceWindowExpiryCancelsAndStagesInterruptedSession is acceptance 3:
// work still busy past the window is cancelled, and its session lands in the
// resume roster even under the goal_autopilot dial while running attended —
// "whoever we interrupted, we resume" is not scoped by the autopilot check.
func TestGraceWindowExpiryCancelsAndStagesInterruptedSession(t *testing.T) {
	_, _, rollback := setupRollbackStage(t)
	shrinkRestartWindows(t, 200*time.Millisecond, 300*time.Millisecond)
	_, _, quitCalled := stubRestartSeams(t)
	oldStart, oldQuit := versionSwitchStartLauncher, versionSwitchQuit
	versionSwitchStartLauncher = func(string, int) error { return nil }
	versionSwitchQuit = func(*App) { quitCalled.Store(true) }
	t.Cleanup(func() { versionSwitchStartLauncher, versionSwitchQuit = oldStart, oldQuit })

	caller := filepath.Join(t.TempDir(), "caller.session.jsonl")
	// Attended tab (autopilot off) — the dial would never stage it for resume;
	// the cancellation path must stage it anyway.
	otherCtrl := &restartProbeController{path: filepath.Join(t.TempDir(), "attended-busy.session.jsonl"), status: control.RuntimeStatus{Running: true}}
	app := &App{tabs: map[string]*WorkspaceTab{
		"t1": {ID: "t1", SessionPath: caller, Ctrl: &restartProbeController{path: caller, status: control.RuntimeStatus{Running: true}}, autopilot: true, Ready: true},
		"t2": {ID: "t2", SessionPath: otherCtrl.path, Ctrl: otherCtrl, Ready: true},
	}}
	if err := app.applyConfigOnly(func(c *config.Config) error { return c.SetAutonomousUpdateResume("goal_autopilot") }); err != nil {
		t.Fatal(err)
	}

	controller := newAutonomousUpdateController(app)
	if _, err := controller.SetTarget(context.Background(), rollback); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.ExecuteTarget(context.Background(), caller); err != nil {
		t.Fatal(err)
	}
	if got := otherCtrl.cancelCount(); got != 1 {
		t.Fatalf("the still-busy tab must be cancelled exactly once, got %d", got)
	}
	found := false
	for _, p := range rosterPaths(t) {
		if p == otherCtrl.path {
			found = true
		}
	}
	if !found {
		t.Fatalf("cancelled session %s must be in the roster despite the goal_autopilot dial (谁断谁续): %v", otherCtrl.path, rosterPaths(t))
	}
}

// TestStageInterruptedByRestartDial pins the staging rule directly: the
// interruption bypasses the autopilot check, and the dial's explicit "off"
// opt-out still wins (at off the restore gate skips every resume path, so
// staging would only defer a surprise resume to a later restart). The boolean
// return (task 435) feeds the 1545 unstaged face, so both branches pin it.
func TestStageInterruptedByRestartDial(t *testing.T) {
	isolateDesktopUserDirs(t)
	session := filepath.Join(t.TempDir(), "interrupted.session.jsonl")
	app := &App{}

	if !app.stageInterruptedByRestart(session) {
		t.Fatal("staging under the default dial must report staged=true")
	}
	found := false
	for _, p := range rosterPaths(t) {
		if p == session {
			found = true
		}
	}
	if !found {
		t.Fatalf("interrupted session must be staged under the default dial: %v", rosterPaths(t))
	}

	if err := app.applyConfigOnly(func(c *config.Config) error { return c.SetAutonomousUpdateResume("off") }); err != nil {
		t.Fatal(err)
	}
	if app.stageInterruptedByRestart(filepath.Join(t.TempDir(), "second.session.jsonl")) {
		t.Fatal("dial off must report staged=false")
	}
	for _, p := range rosterPaths(t) {
		if strings.HasSuffix(p, "second.session.jsonl") {
			t.Fatalf("dial off must stage nothing new: %v", rosterPaths(t))
		}
	}
}

// TestGraceWindowNeverCancelsAPendingPrompt is acceptance 4: a tab holding an
// unanswered prompt is never Cancelled (the prompt is the user's decision),
// the forced pass is visible in the result text AND the log under the
// 超时强制 marker, and the tab is not staged (it was not cancelled).
func TestGraceWindowNeverCancelsAPendingPrompt(t *testing.T) {
	_, _, rollback := setupRollbackStage(t)
	shrinkRestartWindows(t, 200*time.Millisecond, 300*time.Millisecond)
	_, _, quitCalled := stubRestartSeams(t)
	oldStart, oldQuit := versionSwitchStartLauncher, versionSwitchQuit
	versionSwitchStartLauncher = func(string, int) error { return nil }
	versionSwitchQuit = func(*App) { quitCalled.Store(true) }
	t.Cleanup(func() { versionSwitchStartLauncher, versionSwitchQuit = oldStart, oldQuit })
	logs := captureSlogWarnings(t)

	caller := filepath.Join(t.TempDir(), "caller.session.jsonl")
	promptCtrl := &restartProbeController{path: filepath.Join(t.TempDir(), "prompt.session.jsonl"), status: control.RuntimeStatus{Running: true, PendingPrompt: true}}
	app := &App{tabs: map[string]*WorkspaceTab{
		"t1": {ID: "t1", SessionPath: caller, Ctrl: &restartProbeController{path: caller, status: control.RuntimeStatus{Running: true}}, autopilot: true, Ready: true},
		"t2": {ID: "t2", SessionPath: promptCtrl.path, Ctrl: promptCtrl, Ready: true},
	}}

	controller := newAutonomousUpdateController(app)
	if _, err := controller.SetTarget(context.Background(), rollback); err != nil {
		t.Fatal(err)
	}
	result, err := controller.ExecuteTarget(context.Background(), caller)
	if err != nil {
		t.Fatal(err)
	}
	if got := promptCtrl.cancelCount(); got != 0 {
		t.Fatalf("a pending prompt must never be Cancelled away (不替用户否决), got %d cancels", got)
	}
	if !strings.Contains(result, restartForcedMarker) {
		t.Fatalf("the forced pass must surface in the tool text under %q: %q", restartForcedMarker, result)
	}
	if !strings.Contains(result, promptCtrl.path) {
		t.Fatalf("the forced note must name the pushed-through session: %q", result)
	}
	if !strings.Contains(logs.String(), restartForcedMarker) || !strings.Contains(logs.String(), "pending prompt") {
		t.Fatalf("the forced pass must surface in the log under %q: %s", restartForcedMarker, logs.String())
	}
	for _, p := range rosterPaths(t) {
		if p == promptCtrl.path {
			t.Fatal("a prompt-holding tab was not cancelled, so it must not be staged")
		}
	}
}

// TestGraceWindowStopsTheHeartbeat is acceptance 5: the heartbeat engine is
// stopped before the window opens — the hook fires on every entry, and the
// real hook leaves the engine not-running.
func TestGraceWindowStopsTheHeartbeat(t *testing.T) {
	isolateDesktopUserDirs(t)
	heartbeatStopped, _, _ := stubRestartSeams(t)
	busyCtrl := &restartProbeController{path: filepath.Join(t.TempDir(), "busy.session.jsonl"), status: control.RuntimeStatus{Running: true}}
	app := &App{tabs: map[string]*WorkspaceTab{
		"t1": {ID: "t1", SessionPath: busyCtrl.path, Ctrl: busyCtrl, Ready: true},
	}}
	shrinkRestartWindows(t, 60*time.Millisecond, 30*time.Millisecond)
	app.clearRestartPath("")
	if !heartbeatStopped.Load() {
		t.Fatal("the heartbeat stop hook must fire when the window opens")
	}

	// The real hook on a real engine: Start flips running, Stop must clear it.
	// stopHeartbeatEngine is called directly because the var itself is stubbed
	// above.
	engineApp := &App{}
	engineApp.heartbeat = newHeartbeatEngine(engineApp)
	engineApp.heartbeat.Start()
	if !engineApp.heartbeat.running {
		t.Fatal("precondition: the engine should be running after Start")
	}
	stopHeartbeatEngine(engineApp)
	if engineApp.heartbeat.running {
		t.Fatal("the real hook must leave the heartbeat engine stopped")
	}
}

// TestRestartDesktopSharesTheGraceWindow pins the user ruling (2026-10-02):
// the plain settings-page restart runs the same window — busy tabs are
// cancelled with a resume marker instead of refusing the restart. Audit-2
// fix: the window runs AFTER the launchability checks, so this uses the
// install-root seam to pass them and proves the window still fires on the
// success path (the refusal faces are pinned by
// TestClearRestartPathHoldsUntilValidationPasses).
func TestRestartDesktopSharesTheGraceWindow(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := fakeInstallRoot(t, nil, "")
	oldResolve := restartResolveInstallRoot
	restartResolveInstallRoot = func(string) (string, error) { return root, nil }
	t.Cleanup(func() { restartResolveInstallRoot = oldResolve })
	shrinkRestartWindows(t, 100*time.Millisecond, 100*time.Millisecond)
	heartbeatStopped, launcherStarted, _ := stubRestartSeams(t)
	busyCtrl := &restartProbeController{path: filepath.Join(t.TempDir(), "busy.session.jsonl"), status: control.RuntimeStatus{Running: true}}
	app := &App{tabs: map[string]*WorkspaceTab{
		"t1": {ID: "t1", SessionPath: busyCtrl.path, Ctrl: busyCtrl, Ready: true},
	}}

	if err := app.RestartDesktop(); err != nil {
		t.Fatalf("a launchable plain restart must go through: %v", err)
	}
	if got := busyCtrl.cancelCount(); got != 1 {
		t.Fatalf("the plain restart must cancel the busy tab (no busy refusal anymore), got %d", got)
	}
	if !heartbeatStopped.Load() {
		t.Fatal("the plain restart must stop the heartbeat too")
	}
	if !launcherStarted.Load() {
		t.Fatal("the launcher seam must have been reached")
	}
	found := false
	for _, p := range rosterPaths(t) {
		if p == busyCtrl.path {
			found = true
		}
	}
	if !found {
		t.Fatalf("the cancelled session must be staged: %v", rosterPaths(t))
	}
}

// TestClearRestartPathHoldsUntilValidationPasses pins the audit-2 major fix:
// the grace window is side-effectful (heartbeat stop with no restart path, and
// cancels whose resume markers only get consumed after a restart), so every
// fallible exit BEFORE the first irreversible step must refuse without paying
// those costs — heartbeat still running, roster empty, nobody cancelled.
func TestClearRestartPathHoldsUntilValidationPasses(t *testing.T) {
	isolateDesktopUserDirs(t)
	fakeInstallRoot(t, []string{"v1.38.3-20260923-1010"}, "v1.38.3-20260923-1010")
	fakeVersionedConfig(t, true)
	shrinkRestartWindows(t, 80*time.Millisecond, 40*time.Millisecond)
	heartbeatStopped, _, _ := stubRestartSeams(t)
	busyCtrl := &restartProbeController{path: filepath.Join(t.TempDir(), "busy.session.jsonl"), status: control.RuntimeStatus{Running: true}}
	app := &App{tabs: map[string]*WorkspaceTab{
		"t1": {ID: "t1", SessionPath: busyCtrl.path, Ctrl: busyCtrl, Ready: true},
	}}
	expectNoSideEffects := func(stage string) {
		t.Helper()
		if heartbeatStopped.Load() {
			t.Fatalf("%s: the heartbeat must not be stopped by a refused call", stage)
		}
		if got := busyCtrl.cancelCount(); got != 0 {
			t.Fatalf("%s: a refused call must not cancel anyone, got %d cancels", stage, got)
		}
		if roster := rosterPaths(t); len(roster) != 0 {
			t.Fatalf("%s: a refused call must stage nobody, roster = %v", stage, roster)
		}
	}

	// (a) Publish face: the staged build is incomplete — a fallible exit that
	// used to sit behind the window.
	if _, err := app.restartAndUpdateExempt("", "v1.38.3-20260923-1010", ""); err == nil {
		t.Fatal("publishing an incomplete staging must be refused")
	} else if !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected the staged-members refusal, got: %v", err)
	}
	expectNoSideEffects("publish with incomplete staging")

	// (b) Rollback face: the common misuse — switching to the ACTIVE version.
	if _, err := app.switchToVersionExempt("v1.38.3-20260923-1010", ""); err == nil {
		t.Fatal("switching to the active version must be refused")
	} else if !strings.Contains(err.Error(), "already the active version") {
		t.Fatalf("expected the no-op-switch refusal, got: %v", err)
	}
	expectNoSideEffects("rollback to the active version")

	// (c) Plain restart on a portable build: not a versioned install, the
	// launcher hand-off cannot work.
	portable := restartResolveInstallRoot
	restartResolveInstallRoot = func(string) (string, error) { return "", nil }
	t.Cleanup(func() { restartResolveInstallRoot = portable })
	if err := app.RestartDesktop(); err == nil {
		t.Fatal("a portable plain restart must be refused")
	} else if !strings.Contains(err.Error(), "not a versioned install") {
		t.Fatalf("expected the not-a-versioned-install refusal, got: %v", err)
	}
	expectNoSideEffects("plain restart on a portable build")
}

// TestInterruptedRosterFeedsTheResumeRestorePoint is the 450→254 handoff
// anchor (acceptance 7's mechanism half): an entry staged by the cancellation
// path is consumed by the same restore point that submits the continue prompt
// and clears the recovery pause — the anti-fence handoff the 435 package
// builds its end-to-end assertion on.
func TestInterruptedRosterFeedsTheResumeRestorePoint(t *testing.T) {
	isolateDesktopUserDirs(t)
	session := filepath.Join(t.TempDir(), "interrupted.session.jsonl")
	app := &App{}
	app.stageInterruptedByRestart(session)

	tab := &WorkspaceTab{ID: "t1", SessionPath: session, Ready: true, Ctrl: &restartProbeController{path: session}}
	app.maybeResumeAutonomousUpdateTab(tab)
	if left := rosterPaths(t); len(left) != 0 {
		t.Fatalf("the restore point must consume the interrupted-session entry: %v", left)
	}
}

// TestGraceWindowIdleTabsSkipTheWait pins the cheap path: nothing busy means
// no waiting and no cancellations (and the heartbeat hook still fires first,
// shrinking the TOCTOU window for heartbeat ignitions).
func TestGraceWindowIdleTabsSkipTheWait(t *testing.T) {
	isolateDesktopUserDirs(t)
	shrinkRestartWindows(t, 2*time.Second, 100*time.Millisecond)
	heartbeatStopped, _, _ := stubRestartSeams(t)
	app := &App{tabs: map[string]*WorkspaceTab{
		"t1": {ID: "t1", Ctrl: &restartProbeController{path: filepath.Join(t.TempDir(), "idle.session.jsonl")}, Ready: true},
	}}
	start := time.Now()
	report := app.clearRestartPath("")
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("an idle workspace must not wait out the window, took %s", elapsed)
	}
	if !report.natural || len(report.cancelled) != 0 || len(report.forcedPrompt) != 0 {
		t.Fatalf("report = %+v, want a natural pass with nothing forced", report)
	}
	if report.forcedNote() != "" {
		t.Fatalf("natural pass must carry no note, got %q", report.forcedNote())
	}
	if !heartbeatStopped.Load() {
		t.Fatal("the heartbeat hook fires on every entry, idle or not")
	}
}

// TestRestartBusyReasonQuerySemanticsUnchanged guards acceptance 6's anchor:
// the pure query keeps its task-254 exemption semantics byte-for-byte (the
// autonomous_update_test.go assertions at :174-184 lean on it).
func TestRestartBusyReasonQuerySemanticsUnchanged(t *testing.T) {
	isolateDesktopUserDirs(t)
	caller := filepath.Join(t.TempDir(), "caller.session.jsonl")
	app := &App{tabs: map[string]*WorkspaceTab{
		"t1": {ID: "t1", SessionPath: caller, Ctrl: &restartProbeController{path: caller, status: control.RuntimeStatus{Running: true}}, Ready: true},
	}}
	if busy := app.restartBusyReason(caller); busy != "" {
		t.Fatalf("caller session must be exempt: %s", busy)
	}
	if busy := app.restartBusyReason(""); busy == "" {
		t.Fatal("an empty caller session must stay busy-blocked at the query level")
	}
	other := filepath.Join(t.TempDir(), "other.session.jsonl")
	app.tabs["t2"] = &WorkspaceTab{ID: "t2", SessionPath: other, Ctrl: &restartProbeController{path: other, status: control.RuntimeStatus{Running: true}}, Ready: true}
	if busy := app.restartBusyReason(caller); busy == "" {
		t.Fatal("another tab's running turn must still be reported")
	}
	// The three-component busy set: a pending prompt alone blocks the query.
	delete(app.tabs, "t2")
	app.tabs["t2"] = &WorkspaceTab{ID: "t2", SessionPath: other, Ctrl: &restartProbeController{path: other, status: control.RuntimeStatus{PendingPrompt: true}}, Ready: true}
	if busy := app.restartBusyReason(caller); busy == "" {
		t.Fatal("a pending prompt must still be reported")
	}
	delete(app.tabs, "t2")
	app.tabs["t2"] = &WorkspaceTab{ID: "t2", SessionPath: other, Ctrl: &restartProbeController{path: other, status: control.RuntimeStatus{BackgroundJobs: 3}}, Ready: true}
	if busy := app.restartBusyReason(caller); busy == "" {
		t.Fatal("background jobs must still be reported")
	}
}
