package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/control"
	"reasonix/internal/installlayout"
)

// Task 520: the restart_update tool's restart action — relaunch the CURRENT
// version without switching, and continue the calling session afterwards.
// These tests pin the "planned restart" chain: update-restart marker written
// (the gate credential), caller staged for auto-resume, busy-guard exemption,
// install untouched — the exact same 435/450/461-P2 semantics the execute
// path carries, minus the version swap.

// TestRestartOnlyRelaunchesSameVersionAndContinues is acceptance ①+②+③: the
// relaunch commits without moving the pointer, the caller is staged to
// continue, and the next launch's gate opens — with the restart-and-update
// experiment OFF (the install is not touched, so the switch is not required).
func TestRestartOnlyRelaunchesSameVersionAndContinues(t *testing.T) {
	isolateDesktopUserDirs(t)
	resetUpdateRestartGateForTest()
	t.Cleanup(resetUpdateRestartGateForTest)
	active := "v1.38.3-20260923-1010"
	root := fakeInstallRoot(t, []string{active}, active)
	addVersionTreeWithCLI(t, root, active, true)
	// Deliberately NO fakeVersionedConfig(t, true): experimental_restart_update
	// stays off, proving the pure restart needs no version-swap permission.
	fakeVersionedConfig(t, false)
	// The plain-restart core resolves the launcher from the running executable,
	// so point that seam at the fake install root too.
	oldResolve := restartResolveInstallRoot
	restartResolveInstallRoot = func(string) (string, error) { return root, nil }
	t.Cleanup(func() { restartResolveInstallRoot = oldResolve })

	shrinkRestartWindows(t, 150*time.Millisecond, 100*time.Millisecond)
	heartbeatStopped, launcherStarted, quitCalled := stubRestartSeams(t)

	caller := filepath.Join(t.TempDir(), "caller.session.jsonl")
	// A second tab stays busy past the (shrunk) window: it must be cancelled
	// and staged — 谁断谁续 through the new entry, same family semantics.
	otherCtrl := &restartProbeController{path: filepath.Join(t.TempDir(), "busy.session.jsonl"), status: control.RuntimeStatus{Running: true}}
	app := &App{tabs: map[string]*WorkspaceTab{
		"t1": {ID: "t1", SessionPath: caller, Ctrl: &restartProbeController{path: caller, status: control.RuntimeStatus{Running: true}}, autopilot: true, Ready: true},
		"t2": {ID: "t2", SessionPath: otherCtrl.path, Ctrl: otherCtrl, Ready: true},
	}}
	controller := newAutonomousUpdateController(app)

	result, err := controller.RestartOnly(context.Background(), caller)
	if err != nil {
		t.Fatalf("restart must not be refused by the caller's own turn (task 254 exemption): %v", err)
	}
	if !strings.Contains(result, "do not retry") {
		t.Fatalf("result must warn against retrying: %q", result)
	}
	if !strings.Contains(result, "CURRENT version") {
		t.Fatalf("result must state the no-switch fact (acceptance ③): %q", result)
	}
	if !strings.Contains(result, restartForcedMarker) || !strings.Contains(result, otherCtrl.path) {
		t.Fatalf("the forced pass through the busy tab must be named on the tool text: %q", result)
	}

	// The install is untouched: pointer still on the active version, and no
	// version tree was published.
	ptr, err := installlayout.ReadCurrent(root)
	if err != nil {
		t.Fatal(err)
	}
	if ptr.ActiveVersion != active {
		t.Fatalf("active version = %q, want %q (restart must not switch)", ptr.ActiveVersion, active)
	}
	if _, err := os.Stat(filepath.Join(root, installlayout.VersionsDirName, "v1.38.3-20260923-1011")); !os.IsNotExist(err) {
		t.Fatal("restart must not publish a new version tree")
	}

	// The restart is planned: marker written with the restart reason and the
	// active version, so the fresh process opens the auto-resume gate.
	data, err := os.ReadFile(updateRestartMarkerPath())
	if err != nil {
		t.Fatalf("the tool restart must write the update-restart marker: %v", err)
	}
	var marker updateRestartMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatal(err)
	}
	if marker.Reason != restartMarkerReasonToolRestart || marker.Version != active {
		t.Fatalf("marker = %+v, want reason %q version %q", marker, restartMarkerReasonToolRestart, active)
	}
	if !updateRestartResumeAllowed() {
		t.Fatal("the marker must open the next launch's auto-resume gate (acceptance ②)")
	}

	// The caller continues: exactly the caller plus the cancelled tab on the
	// roster (the cancelled one via 谁断谁续, the caller via the task-254 dial).
	roster := rosterPaths(t)
	if len(roster) != 2 {
		t.Fatalf("roster = %v, want caller + cancelled busy session", roster)
	}
	if got := otherCtrl.cancelCount(); got != 1 {
		t.Fatalf("the still-busy tab must be cancelled once, got %d", got)
	}
	if !heartbeatStopped.Load() {
		t.Fatal("the heartbeat engine must be stopped before the window opens")
	}
	if !launcherStarted.Load() {
		t.Fatal("the launcher must start — the process enters its exit flow")
	}
	// The quit fires after the 3s tool grace (const, not shrunk): drain it so
	// the late goroutine cannot leak into a later test's recorder.
	waitFor(t, 5*time.Second, quitCalled.Load)
}

// TestRestartOnlyAttendedCallerNamedUnstaged pins the 1545 face: an attended
// caller under the goal_autopilot dial is NOT staged for auto-resume, the
// tool text names it under 未入册, and nothing silently pretends otherwise.
func TestRestartOnlyAttendedCallerNamedUnstaged(t *testing.T) {
	isolateDesktopUserDirs(t)
	active := "v1.38.3-20260923-1010"
	root := fakeInstallRoot(t, []string{active}, active)
	fakeVersionedConfig(t, false)
	oldResolve := restartResolveInstallRoot
	restartResolveInstallRoot = func(string) (string, error) { return root, nil }
	t.Cleanup(func() { restartResolveInstallRoot = oldResolve })
	shrinkRestartWindows(t, 60*time.Millisecond, 40*time.Millisecond)
	_, _, quitCalled := stubRestartSeams(t)

	caller := filepath.Join(t.TempDir(), "attended.session.jsonl")
	app := &App{tabs: map[string]*WorkspaceTab{
		"t1": {ID: "t1", SessionPath: caller, Ctrl: &restartProbeController{path: caller, status: control.RuntimeStatus{Running: true}}, autopilot: false, Ready: true},
	}}
	controller := newAutonomousUpdateController(app)

	result, err := controller.RestartOnly(context.Background(), caller)
	if err != nil {
		t.Fatal(err)
	}
	// Same face as execute: the unstaged fact is named under the marker (the
	// note itself does not repeat the session path — the model knows its own
	// session), so the silent-loss can never happen.
	if !strings.Contains(result, restartUnstagedMarker) {
		t.Fatalf("the unstaged caller must be named on the tool text: %q", result)
	}
	if roster := rosterPaths(t); len(roster) != 0 {
		t.Fatalf("an attended caller must not be staged under goal_autopilot: %v", roster)
	}
	if _, err := os.Stat(updateRestartMarkerPath()); err != nil {
		t.Fatalf("the marker is written regardless of the staging outcome: %v", err)
	}
	waitFor(t, 5*time.Second, quitCalled.Load)
}

// TestRestartOnlyRefusesPortableBuildWithoutSideEffects pins the audit-2
// ordering on the new entry: every fallible check runs BEFORE the side-effect
// grace window, so a misdirected restart on a portable build refuses without
// stopping the heartbeat, cancelling anyone, or writing a marker.
func TestRestartOnlyRefusesPortableBuildWithoutSideEffects(t *testing.T) {
	isolateDesktopUserDirs(t)
	fakeInstallRoot(t, []string{"v1.38.3-20260923-1010"}, "v1.38.3-20260923-1010")
	shrinkRestartWindows(t, 60*time.Millisecond, 40*time.Millisecond)
	heartbeatStopped, launcherStarted, _ := stubRestartSeams(t)
	oldResolve := restartResolveInstallRoot
	restartResolveInstallRoot = func(string) (string, error) { return "", nil }
	t.Cleanup(func() { restartResolveInstallRoot = oldResolve })

	caller := filepath.Join(t.TempDir(), "caller.session.jsonl")
	app := &App{tabs: map[string]*WorkspaceTab{
		"t1": {ID: "t1", SessionPath: caller, Ctrl: &restartProbeController{path: caller, status: control.RuntimeStatus{Running: true}}, autopilot: true, Ready: true},
	}}
	controller := newAutonomousUpdateController(app)

	if _, err := controller.RestartOnly(context.Background(), caller); err == nil {
		t.Fatal("a portable build has no launcher to hand off to; must refuse")
	} else if !strings.Contains(err.Error(), "not a versioned install") {
		t.Fatalf("expected the not-a-versioned-install refusal, got: %v", err)
	}
	if heartbeatStopped.Load() || launcherStarted.Load() {
		t.Fatal("a refused restart must pay no side-effect costs")
	}
	if got := len(app.busyRestartTabs(caller)); got != 0 {
		t.Fatalf("the caller must stay exempt even on the refusal path, busy tabs = %d", got)
	}
	if roster := rosterPaths(t); len(roster) != 0 {
		t.Fatalf("a refused restart must stage nobody: %v", roster)
	}
	if _, err := os.Stat(updateRestartMarkerPath()); !os.IsNotExist(err) {
		t.Fatalf("a refused restart must not write the marker: %v", err)
	}
}

// TestRestartDesktopWritesNoMarker pins the contrast (acceptance ③'s other
// half): the settings-page restart goes through the same shared core but with
// an empty marker reason — a manual restart never opens the auto-resume gate
// (task 461-P2), and its canceled-work semantics are unchanged.
func TestRestartDesktopWritesNoMarker(t *testing.T) {
	isolateDesktopUserDirs(t)
	resetUpdateRestartGateForTest()
	t.Cleanup(resetUpdateRestartGateForTest)
	root := fakeInstallRoot(t, nil, "")
	shrinkRestartWindows(t, 60*time.Millisecond, 40*time.Millisecond)
	_, _, quitCalled := stubRestartSeams(t)
	oldResolve := restartResolveInstallRoot
	restartResolveInstallRoot = func(string) (string, error) { return root, nil }
	t.Cleanup(func() { restartResolveInstallRoot = oldResolve })

	app := &App{}
	if err := app.RestartDesktop(); err != nil {
		t.Fatalf("a launchable plain restart must go through: %v", err)
	}
	if _, err := os.Stat(updateRestartMarkerPath()); !os.IsNotExist(err) {
		t.Fatalf("the settings-page restart must not write the marker (task 461-P2): %v", err)
	}
	if updateRestartResumeAllowed() {
		t.Fatal("the next launch after a manual restart must keep the gate closed")
	}
	waitFor(t, 3*time.Second, quitCalled.Load)
}
