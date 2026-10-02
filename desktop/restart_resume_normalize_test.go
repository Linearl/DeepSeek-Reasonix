package main

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Task 450 note2 (audit-2): the task-254 roster is staged from the tab's
// Ctrl.SessionPath() spelling, and consumed through tab.currentSessionPath() —
// which inside the two-phase recovery-handoff window legitimately returns the
// lease-backed tab form, a different spelling of the SAME session. Raw ==
// comparisons split that one session into a silently missed resume, so both
// sides now compare through sessionRuntimeKey. These tests pin the narrow
// window: the two spellings differ as raw strings yet must hit as one identity
// (谁断谁续 must not lose a session to path spelling).

// narrowWindowPathForms returns two DIFFERENT spellings of one session path
// that sessionRuntimeKey folds to the same identity: on Windows the lease key
// form is case-folded (#5999: C:\Users\... vs the lease's lowercased
// c:\users\...), elsewhere a redundant "." segment is cleaned away. Both
// spellings stay inside the test's temp base, so the fold is deterministic.
func narrowWindowPathForms(t *testing.T) (staged, consumed string) {
	t.Helper()
	base := t.TempDir()
	name := "interrupted.session.jsonl"
	staged = filepath.Join(base, "Interrupted.Session.jsonl")
	if runtime.GOOS == "windows" {
		consumed = filepath.Join(base, name)
	} else {
		consumed = base + string(filepath.Separator) + "." + string(filepath.Separator) + name
	}
	if staged == consumed {
		t.Fatalf("precondition: the two spellings must differ as raw strings, got %q", staged)
	}
	key := sessionRuntimeKey(staged)
	if key == "" || key != sessionRuntimeKey(consumed) {
		t.Fatalf("precondition: sessionRuntimeKey must fold %q and %q into one identity (%q vs %q)",
			staged, consumed, key, sessionRuntimeKey(consumed))
	}
	return staged, consumed
}

// TestResumeRosterMatchesAcrossPathForms is the narrow-window consumption half
// of 450 note2: the roster holds the registration-side spelling (what
// restartTabSessionPath recorded from Ctrl.SessionPath()), while at the restore
// point the lease-backed tab form is what currentSessionPath() returns. The
// normalized comparison must hit: entry consumed, recovery pause cleared
// passively, continue prompt submitted — exactly the same-chain assertions the
// same-form test (TestMaybeResumeAutonomousUpdateTabConsumesMarkerOnce) pins.
func TestResumeRosterMatchesAcrossPathForms(t *testing.T) {
	isolateDesktopUserDirs(t)
	staged, consumed := narrowWindowPathForms(t)

	app := &App{}
	if !app.stageInterruptedByRestart(staged) {
		t.Fatal("precondition: staging under the default dial must succeed")
	}
	// Precondition face: with the old raw == comparison this roster entry could
	// never match the consumed spelling at all.
	for _, p := range rosterPaths(t) {
		if p == consumed {
			t.Fatalf("precondition broken: roster already carries the consumed spelling %q", p)
		}
	}

	// Lease-backed tab: the lock-free mirror makes currentSessionPath() return
	// the tab spelling instead of the controller spelling — the exact branch
	// the recovery-handoff window rides on. The mirror is stored directly so
	// the test stays hermetic (no takeover-request watcher goroutine).
	ctrl := &retargetRuntimeController{path: staged}
	tab := &WorkspaceTab{ID: "t1", SessionPath: consumed, Ready: true, Ctrl: ctrl}
	leaseKey := sessionRuntimeKey(consumed)
	tab.sessionLeaseKey.Store(&leaseKey)
	if got := tab.currentSessionPath(); got != consumed {
		t.Fatalf("precondition: currentSessionPath must return the lease-backed tab form %q, got %q", consumed, got)
	}

	submits := make(chan string, 1)
	oldSubmit := restartResumeSubmit
	restartResumeSubmit = func(a *App, tabID, prompt string) error {
		submits <- tabID + "|" + prompt
		return nil
	}
	t.Cleanup(func() { restartResumeSubmit = oldSubmit })

	app.maybeResumeAutonomousUpdateTab(tab)

	if left := rosterPaths(t); len(left) != 0 {
		t.Fatalf("the entry must be consumed once the normalized comparison hits: %v", left)
	}
	if len(ctrl.inboxPausedCalls) != 1 || ctrl.inboxPausedCalls[0] {
		t.Fatalf("the resume must clear the recovery pause exactly once, passively: %+v", ctrl.inboxPausedCalls)
	}
	select {
	case got := <-submits:
		if got != "t1|"+autonomousUpdateResumePrompt {
			t.Fatalf("resume submit = %q, want the continue prompt on the restored tab", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the narrow-window match must submit the continue prompt (续跑提交)")
	}
}

// TestStageInterruptedByRestartDedupesAcrossPathForms is the staging half of
// the same unification: the same session staged twice under different
// spellings (the 254 path wrote one form, the 450 grace window's report
// carried another) must refresh the single existing row instead of adding a
// second one — one identity, one roster entry.
func TestStageInterruptedByRestartDedupesAcrossPathForms(t *testing.T) {
	isolateDesktopUserDirs(t)
	staged, consumed := narrowWindowPathForms(t)
	app := &App{}

	if !app.stageInterruptedByRestart(staged) {
		t.Fatal("first staging must succeed")
	}
	if !app.stageInterruptedByRestart(consumed) {
		t.Fatal("second staging (different spelling, same identity) must succeed")
	}
	paths := rosterPaths(t)
	if len(paths) != 1 {
		t.Fatalf("one session under two spellings must hold a single roster row: %v", paths)
	}
	state := readAutonomousUpdateResumeFile()
	if key := sessionRuntimeKey(state.Sessions[0].Path); key != sessionRuntimeKey(consumed) {
		t.Fatalf("the surviving row must keep the staged identity %q: %+v", key, state.Sessions)
	}
}
