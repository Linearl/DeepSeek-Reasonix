package main

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// tl1366: the delivery half of the task-254 roster resume. The 包68 incident
// (2026-11-03): the roster resume fired, the submit was accepted — and the
// resumed turn died without a trace inside the fresh process's boot-time
// maintenance window (a 2m50s compaction burned, the drained turn appended
// nothing, the session sat silent until the user typed). The delivery chain
// adds two guarantees, each pinned here:
//   - submit past the boot-maintenance window (MaintenanceInFlight probe);
//   - verify delivery via transcript growth, name a fall-through (续跑落空)
//     and retry exactly once.

// resumeProbeController is a SessionAPI fake carrying both narrow probes the
// delivery chain reads: the boot-maintenance flag (MaintenanceInFlight) and
// the transcript length (HistoryLen). The real records are pinned at the
// control/agent level; this fake exists so the delivery chain's decisions can
// be asserted in isolation.
type resumeProbeController struct {
	stubSessionAPI
	mu         sync.Mutex
	path       string
	maintain   atomic.Bool
	historyLen atomic.Int32
}

func (c *resumeProbeController) SessionPath() string { return c.path }

// SetInboxPausedPassive stands in for the restore point's passive unpause —
// the embedded control.SessionAPI interface is nil on this fake, so the
// restore chain's call would otherwise dereference it.
func (c *resumeProbeController) SetInboxPausedPassive(paused bool) error { return nil }

func (c *resumeProbeController) MaintenanceInFlight() bool { return c.maintain.Load() }

func (c *resumeProbeController) HistoryLen() int { return int(c.historyLen.Load()) }

func (c *resumeProbeController) setHistory(n int) { c.historyLen.Store(int32(n)) }

// shrinkResumeDeliveryWindows pins the delivery knobs to test-scale values
// and restores them on cleanup.
func shrinkResumeDeliveryWindows(t *testing.T, waitBound, liveness time.Duration) {
	t.Helper()
	oldWait, oldWaitPoll := resumeMaintenanceWaitBound, resumeMaintenancePoll
	oldLive, oldLivePoll := resumeLivenessWindow, resumeLivenessPoll
	resumeMaintenanceWaitBound, resumeMaintenancePoll = waitBound, 10*time.Millisecond
	resumeLivenessWindow, resumeLivenessPoll = liveness, 10*time.Millisecond
	t.Cleanup(func() {
		resumeMaintenanceWaitBound, resumeMaintenancePoll = oldWait, oldWaitPoll
		resumeLivenessWindow, resumeLivenessPoll = oldLive, oldLivePoll
	})
}

// submitRecorder overrides the restartResumeSubmit seam and counts calls.
// When grow is non-nil, each submit lands the continue prompt in the fake's
// transcript (history grows by grow) — the receipt the delivery watch needs.
// A nil fake means "never grows" (the silent-death shape under test).
func submitRecorder(t *testing.T, ctrl *resumeProbeController, grow int) *atomic.Int32 {
	t.Helper()
	var submits atomic.Int32
	old := restartResumeSubmit
	restartResumeSubmit = func(a *App, tabID, prompt string) error {
		submits.Add(1)
		if ctrl != nil && grow != 0 {
			ctrl.historyLen.Add(int32(grow))
		}
		return nil
	}
	t.Cleanup(func() { restartResumeSubmit = old })
	return &submits
}

// TestResumeDeliverySubmitsOnlyAfterBootMaintenanceSettles pins the maintenance
// wait: while the fresh controller's boot compaction is in flight, the resume
// submit must not fire; once maintenance settles, it must.
func TestResumeDeliverySubmitsOnlyAfterBootMaintenanceSettles(t *testing.T) {
	isolateDesktopUserDirs(t)
	shrinkResumeDeliveryWindows(t, 2*time.Second, 100*time.Millisecond)

	ctrl := &resumeProbeController{path: "s1.jsonl"}
	ctrl.maintain.Store(true) // boot compaction running
	submits := submitRecorder(t, ctrl, 3)
	tab := &WorkspaceTab{ID: "t1", SessionPath: "s1.jsonl", Ready: true, Ctrl: ctrl}
	app := &App{}

	done := make(chan struct{})
	go func() {
		app.resumeRosterSession(tab, "t1", sessionRuntimeKey("s1.jsonl"), autonomousUpdateResumePrompt)
		close(done)
	}()

	// Maintenance in flight → no submit yet.
	time.Sleep(80 * time.Millisecond)
	if got := submits.Load(); got != 0 {
		t.Fatalf("the resume must wait out the boot compaction, got %d submits", got)
	}
	// Maintenance settles → the submit fires.
	ctrl.maintain.Store(false)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the resume chain must finish once maintenance settles")
	}
	if got := submits.Load(); got != 1 {
		t.Fatalf("submits = %d, want exactly 1 after maintenance settled", got)
	}
}

// TestResumeDeliverySubmitsPastStuckMaintenance pins the 维护未清 face: a
// maintenance window that outlives the bound must not strand the resume —
// the submit goes through anyway (the task-920 park path remains the
// backstop) and the fall-through is named on the log.
func TestResumeDeliverySubmitsPastStuckMaintenance(t *testing.T) {
	isolateDesktopUserDirs(t)
	shrinkResumeDeliveryWindows(t, 30*time.Millisecond, 100*time.Millisecond)
	logs := captureSlogWarnings(t)

	ctrl := &resumeProbeController{path: "s1.jsonl"}
	ctrl.maintain.Store(true) // never settles within the bound
	submits := submitRecorder(t, ctrl, 3)
	tab := &WorkspaceTab{ID: "t1", SessionPath: "s1.jsonl", Ready: true, Ctrl: ctrl}
	app := &App{}

	app.resumeRosterSession(tab, "t1", sessionRuntimeKey("s1.jsonl"), autonomousUpdateResumePrompt)

	if got := submits.Load(); got != 1 {
		t.Fatalf("submits = %d, want 1 (the park path remains the backstop)", got)
	}
	if out := logs.String(); !strings.Contains(out, resumeMaintenanceBusyMarker) {
		t.Fatalf("the stuck-maintenance submit must be named ("+resumeMaintenanceBusyMarker+"), log=%s", out)
	}
}

// TestResumeDeliveryNamesFallThroughAndRetriesOnce pins the 续跑落空 face: a
// submit the transcript never acknowledges (the incident's silent death) is
// retried exactly once, and a second fall-through gives up named — no
// resurrection loop (task 263).
func TestResumeDeliveryNamesFallThroughAndRetriesOnce(t *testing.T) {
	isolateDesktopUserDirs(t)
	shrinkResumeDeliveryWindows(t, 30*time.Millisecond, 60*time.Millisecond)
	logs := captureSlogWarnings(t)

	ctrl := &resumeProbeController{path: "s1.jsonl"}
	ctrl.setHistory(7) // constant: no turn ever lands
	submits := submitRecorder(t, ctrl, 0)
	tab := &WorkspaceTab{ID: "t1", SessionPath: "s1.jsonl", Ready: true, Ctrl: ctrl}
	app := &App{}

	app.resumeRosterSession(tab, "t1", sessionRuntimeKey("s1.jsonl"), autonomousUpdateResumePrompt)

	if got := submits.Load(); got != 2 {
		t.Fatalf("submits = %d, want exactly 2 (initial + one bounded retry)", got)
	}
	out := logs.String()
	if strings.Count(out, resumeFallThroughMarker) != 2 {
		t.Fatalf("both fall-throughs must be named ("+resumeFallThroughMarker+"), log=%s", out)
	}
	if !strings.Contains(out, "giving up") {
		t.Fatalf("the second fall-through must give up named, log=%s", out)
	}
}

// TestResumeDeliveryGrowthSatisfiesLiveness pins the happy path: transcript
// growth past the submit-time baseline is the delivery receipt — exactly one
// submit, no retry.
func TestResumeDeliveryGrowthSatisfiesLiveness(t *testing.T) {
	isolateDesktopUserDirs(t)
	shrinkResumeDeliveryWindows(t, 30*time.Millisecond, 500*time.Millisecond)
	ctrl := &resumeProbeController{path: "s1.jsonl"}
	ctrl.setHistory(7)
	submits := submitRecorder(t, ctrl, 2) // the turn lands with the submit

	tab := &WorkspaceTab{ID: "t1", SessionPath: "s1.jsonl", Ready: true, Ctrl: ctrl}
	app := &App{}

	app.resumeRosterSession(tab, "t1", sessionRuntimeKey("s1.jsonl"), autonomousUpdateResumePrompt)

	if got := submits.Load(); got != 1 {
		t.Fatalf("submits = %d, want exactly 1 (growth = delivered)", got)
	}
}

// TestResumeDeliveryRetryDelivers pins the repair path: the first submit's
// turn dies silently (no growth), the retry's turn lands — the chain ends
// with the resume delivered and no user in the loop.
func TestResumeDeliveryRetryDelivers(t *testing.T) {
	isolateDesktopUserDirs(t)
	shrinkResumeDeliveryWindows(t, 30*time.Millisecond, 200*time.Millisecond)
	logs := captureSlogWarnings(t)

	ctrl := &resumeProbeController{path: "s1.jsonl"}
	ctrl.setHistory(7)
	// Only the RETRY's turn lands: the first submit never grows the transcript
	// (the silent death), the second one does.
	var submits atomic.Int32
	old := restartResumeSubmit
	restartResumeSubmit = func(a *App, tabID, prompt string) error {
		if submits.Add(1) >= 2 {
			ctrl.setHistory(10)
		}
		return nil
	}
	t.Cleanup(func() { restartResumeSubmit = old })

	tab := &WorkspaceTab{ID: "t1", SessionPath: "s1.jsonl", Ready: true, Ctrl: ctrl}
	app := &App{}

	app.resumeRosterSession(tab, "t1", sessionRuntimeKey("s1.jsonl"), autonomousUpdateResumePrompt)

	if got := submits.Load(); got != 2 {
		t.Fatalf("submits = %d, want exactly 2 (initial fall-through + delivering retry)", got)
	}
	if out := logs.String(); !strings.Contains(out, "delivered on retry") {
		t.Fatalf("the retry's delivery must be logged, log=%s", out)
	}
}

// TestResumeDeliveryWiringThroughRestorePoint pins the end-to-end wiring: the
// task-254 restore point (maybeResumeAutonomousUpdateTab) routes its continue
// prompt through the delivery chain, so a probe-carrying controller gets the
// maintenance wait without any other change to the restore contract.
func TestResumeDeliveryWiringThroughRestorePoint(t *testing.T) {
	isolateDesktopUserDirs(t)
	session := "wired.session.jsonl"
	app := &App{}
	if !app.stageInterruptedByRestart(session) {
		t.Fatal("precondition: staging under the default dial must succeed")
	}
	shrinkResumeDeliveryWindows(t, 30*time.Millisecond, 100*time.Millisecond)

	ctrl := &resumeProbeController{path: session}
	done := make(chan struct{})
	var submits atomic.Int32
	old := restartResumeSubmit
	restartResumeSubmit = func(a *App, tabID, prompt string) error {
		n := submits.Add(1)
		if n == 1 {
			ctrl.historyLen.Add(2) // the continue prompt lands
			go func() { close(done) }()
		}
		return nil
	}
	t.Cleanup(func() { restartResumeSubmit = old })

	tab := &WorkspaceTab{ID: "t1", SessionPath: session, Ready: true, Ctrl: ctrl}

	app.maybeResumeAutonomousUpdateTab(tab)

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the restore point must submit the continue prompt through the delivery chain")
	}
	if got := submits.Load(); got != 1 {
		t.Fatalf("submits = %d, want exactly 1", got)
	}
	if left := rosterPaths(t); len(left) != 0 {
		t.Fatalf("the restore point must consume the roster entry: %v", left)
	}
}
