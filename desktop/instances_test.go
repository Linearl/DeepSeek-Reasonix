package main

// Task 1259 layer 2 tests: the pre-switch instance sweep. Pure-rule tests
// live here; the real-process end-to-end scenario (two spawned instances +
// switch + prune) lives in version_switch_scenario_test.go.

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/installlayout"
)

func TestFilterOtherInstances(t *testing.T) {
	procs := []installlayout.OccupiedProcess{
		{PID: 111, Version: "v1.37.0"},
		{PID: 222, Version: "v1.38.0"},
		{PID: 333, Version: "v1.38.3-20261029-0900"},
		{PID: 444, Version: "v1.38.1"},
	}
	got := filterOtherInstances(procs, 222 /*self*/, "v1.38.1" /*running*/, "v1.38.3-20261029-0900" /*target*/)
	var out []string
	for _, p := range got {
		out = append(out, p.Version)
	}
	// Self (222) excluded, own running tree (444) excluded, target version
	// (333) excluded — the switch leaves the target current. Oldest first.
	if strings.Join(out, ",") != "v1.37.0" {
		t.Fatalf("filterOtherInstances = %v want only v1.37.0", out)
	}

	// With no running-tree exemption (flat caller), only self and target go.
	got = filterOtherInstances(procs, 222, "", "v1.38.3-20261029-0900")
	out = out[:0]
	for _, p := range got {
		out = append(out, p.Version)
	}
	if strings.Join(out, ",") != "v1.37.0,v1.38.1" {
		t.Fatalf("filterOtherInstances (no running tree) = %v want v1.37.0,v1.38.1 (oldest first)", out)
	}
}

func TestSwitchInstancesPromptMessage(t *testing.T) {
	others := []installlayout.OccupiedProcess{
		{PID: 62960, Version: "v1.38.3-20261024-2200"},
		{PID: 71000, Version: "v1.38.3-20261026-1130"},
	}
	msg := switchInstancesPromptMessage(others, "v1.38.3-20261029-0900")
	for _, want := range []string{
		"2 other Reasonix instance(s)",
		"v1.38.3-20261024-2200 (pid 62960)",
		"v1.38.3-20261026-1130 (pid 71000)",
		"Switching to v1.38.3-20261029-0900 leaves them running an old version",
		"graceful close, no force kill",
		"manual close",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("prompt message missing %q:\n%s", want, msg)
		}
	}
}

func TestUnclosedInstancesMessage(t *testing.T) {
	survivors := []installlayout.OccupiedProcess{{PID: 62960, Version: "v1.38.3-20261024-2200"}}
	msg := unclosedInstancesMessage(survivors)
	for _, want := range []string{
		"did not close gracefully",
		"v1.38.3-20261024-2200 (pid 62960)",
		"close them manually",
		"kept from pruning",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("unclosed message missing %q:\n%s", want, msg)
		}
	}
}

// TestSwitchToVersionInstanceSweepWiring drives the switch with stubbed
// seams: two other instances detected, user confirms, the first closes, the
// second refuses — the switch must still complete and the refusal must reach
// the unclosed notice path.
func TestSwitchToVersionInstanceSweepWiring(t *testing.T) {
	t.Cleanup(func() {
		versionSwitchOtherInstances = scanOtherVersionInstances
		versionSwitchConfirmClose = confirmCloseOtherInstances
		versionSwitchCloseOtherInstance = gracefulCloseOtherInstance
	})
	fakeVersionedConfig(t, true)
	fakeInstallRoot(t, []string{"v1.38.3-20261029-0900", "v1.38.3-20261024-2200", "v1.38.3-20261026-1130"}, "v1.38.3-20261024-2200")
	launched := make(chan string, 1)
	quit := make(chan struct{})
	versionSwitchStartLauncher = func(launcherPath string, exitingPID int) error {
		launched <- filepath.Dir(launcherPath)
		return nil
	}
	versionSwitchQuit = func(a *App) { close(quit) }

	versionSwitchOtherInstances = func(installRoot, target string) []installlayout.OccupiedProcess {
		return []installlayout.OccupiedProcess{
			{PID: 62960, Version: "v1.38.3-20261024-2200"},
			{PID: 71000, Version: "v1.38.3-20261026-1130"},
		}
	}
	var confirmArgs struct {
		others []installlayout.OccupiedProcess
		target string
	}
	versionSwitchConfirmClose = func(a *App, others []installlayout.OccupiedProcess, target string) bool {
		confirmArgs.others = others
		confirmArgs.target = target
		return true
	}
	var closedPIDs []int
	versionSwitchCloseOtherInstance = func(pid int) bool {
		closedPIDs = append(closedPIDs, pid)
		return pid == 62960 // the second instance refuses
	}

	app := &App{}
	if err := app.SwitchToVersion("v1.38.3-20261029-0900"); err != nil {
		t.Fatal(err)
	}
	<-launched
	<-quit

	if len(confirmArgs.others) != 2 || confirmArgs.target != "v1.38.3-20261029-0900" {
		t.Fatalf("confirm saw others=%v target=%q", confirmArgs.others, confirmArgs.target)
	}
	if strings.Join(intStrings(closedPIDs), ",") != "62960,71000" {
		t.Fatalf("close attempts = %v want 62960,71000 (oldest version first)", closedPIDs)
	}
	// The switch itself completed despite the refusing instance.
	root := versionSwitchRootForTest()
	ptr, err := installlayout.ReadCurrent(root)
	if err != nil {
		t.Fatal(err)
	}
	if ptr.ActiveVersion != "v1.38.3-20261029-0900" {
		t.Fatalf("pointer not moved after instance sweep: %+v", ptr)
	}
}

// TestSwitchToVersionDeclineKeepsInstances runs the sweep with the user
// declining: no close attempt, switch proceeds.
func TestSwitchToVersionDeclineKeepsInstances(t *testing.T) {
	t.Cleanup(func() {
		versionSwitchOtherInstances = scanOtherVersionInstances
		versionSwitchConfirmClose = confirmCloseOtherInstances
		versionSwitchCloseOtherInstance = gracefulCloseOtherInstance
	})
	fakeVersionedConfig(t, true)
	fakeInstallRoot(t, []string{"v1.38.3-20261029-0900", "v1.38.3-20261024-2200"}, "v1.38.3-20261024-2200")
	quit := make(chan struct{})
	versionSwitchStartLauncher = func(launcherPath string, exitingPID int) error { return nil }
	versionSwitchQuit = func(a *App) { close(quit) }
	versionSwitchOtherInstances = func(installRoot, target string) []installlayout.OccupiedProcess {
		return []installlayout.OccupiedProcess{{PID: 62960, Version: "v1.38.3-20261024-2200"}}
	}
	versionSwitchConfirmClose = func(a *App, others []installlayout.OccupiedProcess, target string) bool { return false }
	closesAttempted := 0
	versionSwitchCloseOtherInstance = func(pid int) bool {
		closesAttempted++
		return true
	}
	app := &App{}
	if err := app.SwitchToVersion("v1.38.3-20261029-0900"); err != nil {
		t.Fatal(err)
	}
	<-quit
	if closesAttempted != 0 {
		t.Fatalf("decline must not attempt closes, got %d", closesAttempted)
	}
}

// TestSwitchToVersionNoInstancesSkipsConfirm guards the common path: no other
// instances, no dialog, no close attempts.
func TestSwitchToVersionNoInstancesSkipsConfirm(t *testing.T) {
	t.Cleanup(func() {
		versionSwitchOtherInstances = scanOtherVersionInstances
		versionSwitchConfirmClose = confirmCloseOtherInstances
		versionSwitchCloseOtherInstance = gracefulCloseOtherInstance
	})
	fakeVersionedConfig(t, true)
	fakeInstallRoot(t, []string{"v1.38.3-20261029-0900", "v1.38.3-20261024-2200"}, "v1.38.3-20261024-2200")
	quit := make(chan struct{})
	versionSwitchStartLauncher = func(launcherPath string, exitingPID int) error { return nil }
	versionSwitchQuit = func(a *App) { close(quit) }
	versionSwitchOtherInstances = func(installRoot, target string) []installlayout.OccupiedProcess { return nil }
	confirmAsked := false
	versionSwitchConfirmClose = func(a *App, others []installlayout.OccupiedProcess, target string) bool {
		confirmAsked = true
		return true
	}
	app := &App{}
	if err := app.SwitchToVersion("v1.38.3-20261029-0900"); err != nil {
		t.Fatal(err)
	}
	<-quit
	if confirmAsked {
		t.Fatalf("no instances must skip the confirm dialog")
	}
}

func intStrings(in []int) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		out = append(out, fmt.Sprintf("%d", v))
	}
	return out
}
