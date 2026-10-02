package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/provider"
)

// Task 435: the fence half of 谁断谁续. A session staged in the task-254
// roster was interrupted by OUR planned restart; when its restore point
// resumes it, the leftover unknown-outcome effect records are settled
// host-side so the 「中断的工具需要核实」 review panel never lights up. A
// session NOT in the roster — a genuine crash interruption — keeps the panel:
// that contrast is the 两者区分 the 435 ruling demands.

// fenceProbeController is a SessionAPI fake carrying its own pending-effect
// mirror: PendingToolRecovery's desktop face (ToolRecoverySnapshot) reads the
// same records, so "pending empty" here is what the review panel polls. The
// real records machinery is pinned at the agent level
// (TestResolveInterruptedByRestartSettlesPendingEffects); this fake exists so
// the restore-point chain can be asserted end to end.
type fenceProbeController struct {
	stubSessionAPI
	mu               sync.Mutex
	path             string
	pending          []provider.ToolCallRecord
	inboxPausedCalls []bool
	settleCalls      int
}

func (c *fenceProbeController) SessionPath() string { return c.path }

func (c *fenceProbeController) SetInboxPausedPassive(paused bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inboxPausedCalls = append(c.inboxPausedCalls, paused)
	return nil
}

func (c *fenceProbeController) SettleRestartInterruptedEffects() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(c.pending)
	c.pending = nil
	c.settleCalls++
	return n
}

func (c *fenceProbeController) pendingNow() []provider.ToolCallRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pending
}

func (c *fenceProbeController) settleCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.settleCalls
}

func interruptedWriteRecord() provider.ToolCallRecord {
	return provider.ToolCallRecord{
		Identity:  provider.ActionIdentity{AttemptID: "attempt-1", CallID: "call-w", CanonicalTool: "write_file"},
		State:     provider.ToolRunUnknown,
		ReadOnly:  false,
		Arguments: []byte(`{"path":"x"}`),
	}
}

// TestRestartInterruptedSessionResumesWithoutFence is 450 acceptance 7's
// end-to-end half (task 435): staged-by-the-cancel-path session + restore
// point → roster consumed, recovery pause cleared passively, pending effects
// settled (the panel's first probe sees an empty set), and the continue prompt
// submitted. One chain, no human in the loop, no fence.
func TestRestartInterruptedSessionResumesWithoutFence(t *testing.T) {
	isolateDesktopUserDirs(t)
	session := filepath.Join(t.TempDir(), "interrupted.session.jsonl")
	app := &App{}
	// The 450 cancellation path staged this session before the restart.
	if !app.stageInterruptedByRestart(session) {
		t.Fatal("precondition: staging under the default dial must succeed")
	}

	ctrl := &fenceProbeController{path: session, pending: []provider.ToolCallRecord{interruptedWriteRecord()}}
	tab := &WorkspaceTab{ID: "t1", SessionPath: session, Ready: true, Ctrl: ctrl}

	submits := make(chan string, 1)
	oldSubmit := restartResumeSubmit
	restartResumeSubmit = func(a *App, tabID, prompt string) error {
		submits <- tabID + "|" + prompt
		return nil
	}
	t.Cleanup(func() { restartResumeSubmit = oldSubmit })

	app.maybeResumeAutonomousUpdateTab(tab)

	if left := rosterPaths(t); len(left) != 0 {
		t.Fatalf("the restore point must consume the roster entry: %v", left)
	}
	if len(ctrl.inboxPausedCalls) != 1 || ctrl.inboxPausedCalls[0] {
		t.Fatalf("recovery pause must be cleared exactly once, passively: %+v", ctrl.inboxPausedCalls)
	}
	if got := ctrl.settleCount(); got != 1 {
		t.Fatalf("the fence settle must fire exactly once at the restore point, got %d", got)
	}
	if left := ctrl.pendingNow(); len(left) != 0 {
		t.Fatalf("pending effects must be settled — the review panel face must be empty (不弹 fence): %+v", left)
	}
	select {
	case got := <-submits:
		if got != "t1|"+autonomousUpdateResumePrompt {
			t.Fatalf("resume submit = %q, want the continue prompt on the restored tab", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the restore point must submit the continue prompt (续跑提交)")
	}
}

// TestCrashInterruptedSessionKeepsTheFence is the 两者区分 contrast: a session
// interrupted by a genuine crash has no roster entry, so the restore point
// must not settle its fence, must not unpause its inbox, and must not submit
// anything — the manual review stays exactly as it was.
func TestCrashInterruptedSessionKeepsTheFence(t *testing.T) {
	isolateDesktopUserDirs(t)
	session := filepath.Join(t.TempDir(), "crashed.session.jsonl")
	app := &App{}

	ctrl := &fenceProbeController{path: session, pending: []provider.ToolCallRecord{interruptedWriteRecord()}}
	tab := &WorkspaceTab{ID: "t1", SessionPath: session, Ready: true, Ctrl: ctrl}

	oldSubmit := restartResumeSubmit
	restartResumeSubmit = func(a *App, tabID, prompt string) error {
		t.Errorf("a crash-interrupted session must not be auto-resumed")
		return nil
	}
	t.Cleanup(func() { restartResumeSubmit = oldSubmit })

	app.maybeResumeAutonomousUpdateTab(tab)

	if got := ctrl.settleCount(); got != 0 {
		t.Fatalf("a crash-interrupted session's fence must not be settled, got %d settles", got)
	}
	if left := ctrl.pendingNow(); len(left) != 1 {
		t.Fatalf("the pending effect must stay for manual review: %+v", left)
	}
	if len(ctrl.inboxPausedCalls) != 0 {
		t.Fatalf("a crash-interrupted session's recovery pause must be untouched: %+v", ctrl.inboxPausedCalls)
	}
}

// TestClearRestartPathReportsUnstagedInterruptedSessions is the 1545
// anti-silent-loss face: at dial off the cancellation still interrupts the
// session, but staging declines — the session must be named on the report, the
// forced note, and the log under the 未入册 marker, so the post-relaunch fence
// is never unexplained. Under the default dial the same session IS staged and
// the unstaged face stays empty.
func TestClearRestartPathReportsUnstagedInterruptedSessions(t *testing.T) {
	isolateDesktopUserDirs(t)
	logs := captureSlogWarnings(t)
	busy := filepath.Join(t.TempDir(), "busy.session.jsonl")
	busyCtrl := &restartProbeController{path: busy, status: control.RuntimeStatus{Running: true}}
	app := &App{tabs: map[string]*WorkspaceTab{
		"t1": {ID: "t1", SessionPath: busy, Ctrl: busyCtrl, Ready: true},
	}}
	shrinkRestartWindows(t, 60*time.Millisecond, 30*time.Millisecond)
	if err := app.applyConfigOnly(func(c *config.Config) error { return c.SetAutonomousUpdateResume("off") }); err != nil {
		t.Fatal(err)
	}

	report := app.clearRestartPath("")
	if report.natural || len(report.cancelled) != 1 || report.cancelled[0] != busy {
		t.Fatalf("the busy tab must be cancelled exactly once: %+v", report)
	}
	if len(report.unstaged) != 1 || report.unstaged[0] != busy {
		t.Fatalf("the dial-off decline must surface as unstaged: %+v", report.unstaged)
	}
	note := report.forcedNote()
	if !strings.Contains(note, restartUnstagedMarker) || !strings.Contains(note, busy) {
		t.Fatalf("the forced note must name the unstaged session under %q: %q", restartUnstagedMarker, note)
	}
	if !strings.Contains(logs.String(), restartUnstagedMarker) || !strings.Contains(logs.String(), "NOT staged") {
		t.Fatalf("the unstaged session must be named in the log under %q: %s", restartUnstagedMarker, logs.String())
	}

	// Contrast: the default dial stages the same interruption — no unstaged
	// face, the roster carries the resume marker.
	if err := app.applyConfigOnly(func(c *config.Config) error { return c.SetAutonomousUpdateResume("goal_autopilot") }); err != nil {
		t.Fatal(err)
	}
	busyCtrl.setStatus(control.RuntimeStatus{Running: true})
	report = app.clearRestartPath("")
	if len(report.unstaged) != 0 {
		t.Fatalf("a staged interruption must not surface as unstaged: %+v", report.unstaged)
	}
	found := false
	for _, p := range rosterPaths(t) {
		if p == busy {
			found = true
		}
	}
	if !found {
		t.Fatalf("the cancelled session must be staged under the default dial: %v", rosterPaths(t))
	}
}

// TestExecuteTargetNamesAnUnstagedCaller is the caller half of the 1545 face:
// an attended caller under the goal_autopilot dial is not staged, yet the
// restart ends its very turn — the tool text must carry that fact back instead
// of letting the model assume an automatic continuation.
func TestExecuteTargetNamesAnUnstagedCaller(t *testing.T) {
	_, _, rollback := setupRollbackStage(t)
	shrinkRestartWindows(t, 100*time.Millisecond, 100*time.Millisecond)
	_, _, quitCalled := stubRestartSeams(t)
	oldStart, oldQuit := versionSwitchStartLauncher, versionSwitchQuit
	versionSwitchStartLauncher = func(string, int) error { return nil }
	versionSwitchQuit = func(*App) { quitCalled.Store(true) }
	t.Cleanup(func() { versionSwitchStartLauncher, versionSwitchQuit = oldStart, oldQuit })

	// Attended caller (autopilot off): the goal_autopilot dial never stages it.
	caller := filepath.Join(t.TempDir(), "attended-caller.session.jsonl")
	app := &App{tabs: map[string]*WorkspaceTab{
		"t1": {ID: "t1", SessionPath: caller, Ctrl: &restartProbeController{path: caller, status: control.RuntimeStatus{Running: true}}, Ready: true},
	}}
	if err := app.applyConfigOnly(func(c *config.Config) error { return c.SetAutonomousUpdateResume("goal_autopilot") }); err != nil {
		t.Fatal(err)
	}

	controller := newAutonomousUpdateController(app)
	if _, err := controller.SetTarget(context.Background(), rollback); err != nil {
		t.Fatal(err)
	}
	result, err := controller.ExecuteTarget(context.Background(), caller)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, restartUnstagedMarker) || !strings.Contains(result, "NOT staged for auto-resume") {
		t.Fatalf("an unstaged caller must be named on the tool text under %q: %q", restartUnstagedMarker, result)
	}
	for _, p := range rosterPaths(t) {
		if p == caller {
			t.Fatal("an attended caller under goal_autopilot must not be staged")
		}
	}
}

// syncBuffer is a mutex-guarded log sink: the resume submit runs in a
// goroutine, so the refusal's Warn line races an unguarded buffer read.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// TestResumeSubmitRefusalWarnsUnderTheUnresumedMarker is task 435 note 3's
// audit-2 判词③ face: when the rostered session's resume submit is REFUSED
// (the restore point racing an already-running turn), the session idles with
// no fence and no resume — the refusal must surface on the log at Warn under
// the 未续跑 marker with a redacted error, and must NOT retry (task 263: a
// refused resume does not resurrect).
func TestResumeSubmitRefusalWarnsUnderTheUnresumedMarker(t *testing.T) {
	isolateDesktopUserDirs(t)
	sink := &syncBuffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(sink, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })

	session := filepath.Join(t.TempDir(), "refused.session.jsonl")
	app := &App{}
	if !app.stageInterruptedByRestart(session) {
		t.Fatal("precondition: staging under the default dial must succeed")
	}

	ctrl := &fenceProbeController{path: session, pending: []provider.ToolCallRecord{interruptedWriteRecord()}}
	tab := &WorkspaceTab{ID: "t-refused", SessionPath: session, Ready: true, Ctrl: ctrl}

	submits := make(chan string, 8)
	oldSubmit := restartResumeSubmit
	restartResumeSubmit = func(a *App, tabID, prompt string) error {
		submits <- tabID + "|" + prompt
		return errors.New("admission refused: provider api_key = sk-plantedinsecretkey1")
	}
	t.Cleanup(func() { restartResumeSubmit = oldSubmit })

	app.maybeResumeAutonomousUpdateTab(tab)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(sink.String(), restartResumeSkippedMarker) {
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case got := <-submits:
		if got != "t-refused|"+autonomousUpdateResumePrompt {
			t.Fatalf("resume submit = %q, want the continue prompt on the refused tab", got)
		}
	default:
		t.Fatal("the restore point must attempt the resume submit")
	}
	// No retry: the entry is consumed and the refusal is final (263 失败不复活).
	time.Sleep(50 * time.Millisecond)
	if len(submits) != 0 {
		t.Fatalf("a refused resume must not retry, saw %d extra submits", len(submits))
	}
	if !strings.Contains(sink.String(), restartResumeSkippedMarker) || !strings.Contains(sink.String(), "t-refused") {
		t.Fatalf("the refused resume must surface at Warn under %q with the tab named: %s", restartResumeSkippedMarker, sink.String())
	}
	if !strings.Contains(sink.String(), "admission refused") {
		t.Fatalf("the refusal log must carry the redacted error text: %s", sink.String())
	}
	if strings.Contains(sink.String(), "sk-plantedinsecretkey1") {
		t.Fatalf("the refusal log must not carry raw provider key text: %s", sink.String())
	}
	// The fence was settled BEFORE the submit (435 ordering), which is exactly
	// why the Warn matters: the idle state is "no fence, no resume".
	if left := ctrl.pendingNow(); len(left) != 0 {
		t.Fatalf("the settled fence must stay settled after a refused submit: %+v", left)
	}
}
