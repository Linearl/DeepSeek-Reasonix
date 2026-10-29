//go:build windows

package main

// Task 1259 acceptance scenario (任务书验收三条), replayed with REAL second
// processes: spawn two instances from old version trees, switch the version,
// run the prune — assert (1) the switch prompt fired with the real instance
// list, (2) no ghost is created (every live instance's tree and exe survive
// pruning), (3) the occupied tree is retained.

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/installlayout"
)

// TestDesktopOccupancyHelperProcess is the child face of the scenario (the
// parent-run return is structural, not a skipped assertion).
func TestDesktopOccupancyHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	time.Sleep(60 * time.Second)
}

// spawnDesktopTreeInstance copies the test binary into the tree and starts it
// in helper mode, so the probes see the production shape
// (<root>/versions/<ver>/reasonix-desktop.exe with a live image).
func spawnDesktopTreeInstance(t *testing.T, installRoot, version string) (pid int) {
	t.Helper()
	treeDir := filepath.Join(installRoot, installlayout.VersionDirRelative(version))
	if err := os.MkdirAll(treeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exeCopy := filepath.Join(treeDir, installlayout.DesktopBinaryName())
	srcFile, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer srcFile.Close()
	dstFile, err := os.OpenFile(exeCopy, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dstFile, srcFile); err != nil {
		dstFile.Close()
		t.Fatal(err)
	}
	if err := dstFile.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exeCopy, "-test.run=TestDesktopOccupancyHelperProcess")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		if len(installlayout.ProbeVersionOccupancy(installRoot, version)) > 0 {
			return cmd.Process.Pid
		}
		if time.Now().After(deadline) {
			t.Fatalf("spawned instance pid %d not attributed to %s within 10s", cmd.Process.Pid, version)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestVersionSwitchAndPruneScenario(t *testing.T) {
	t.Cleanup(func() {
		versionSwitchOtherInstances = scanOtherVersionInstances
		versionSwitchConfirmClose = confirmCloseOtherInstances
		versionSwitchCloseOtherInstance = gracefulCloseOtherInstance
		instanceCloseGrace = 10 * time.Second
	})
	instanceCloseGrace = 700 * time.Millisecond // survivors must not stall the scenario

	// Two old instances (the 2026-10-29 incident shape) plus the target tree.
	oldA := "v1.38.3-20261024-2200"
	oldB := "v1.38.3-20261026-1130"
	target := "v1.38.3-20261029-0900"
	fakeVersionedConfig(t, true)
	root := fakeInstallRoot(t, []string{target, oldB, oldA}, oldA)
	versionsDir := filepath.Join(root, installlayout.VersionsDirName)
	pidA := spawnDesktopTreeInstance(t, root, oldA)
	pidB := spawnDesktopTreeInstance(t, root, oldB)

	// (1) 切换提示出现: the REAL scanner must attribute both real instances;
	// the confirm seam records exactly what the user is asked.
	var promptInstances []installlayout.OccupiedProcess
	promptTarget := ""
	versionSwitchConfirmClose = func(a *App, others []installlayout.OccupiedProcess, target string) bool {
		promptInstances = others
		promptTarget = target
		return true // user confirms closing
	}
	// The real graceful close: the headless helper has no windows, so it
	// cannot answer WM_CLOSE — the honest survivor path of a close-refusing
	// instance. Keep the real seam for pid B only.
	versionSwitchCloseOtherInstance = func(pid int) bool {
		if pid == pidA {
			return true // pretend A closed cleanly
		}
		return gracefulCloseOtherInstance(pid) // B: no windows -> survivor
	}

	quit := make(chan struct{})
	versionSwitchStartLauncher = func(launcherPath string, exitingPID int) error { return nil }
	versionSwitchQuit = func(a *App) { close(quit) }

	app := &App{}
	if err := app.SwitchToVersion(target); err != nil {
		t.Fatal(err)
	}
	<-quit

	// (1) the prompt listed both real instances with version labels, targeting
	// the new version, and the switch committed.
	if len(promptInstances) != 2 {
		t.Fatalf("switch prompt saw %d instances, want the 2 real ones: %v", len(promptInstances), promptInstances)
	}
	gotVersions := map[string]int{}
	for _, p := range promptInstances {
		gotVersions[p.Version] = p.PID
	}
	if gotVersions[oldA] != pidA || gotVersions[oldB] != pidB {
		t.Fatalf("prompt instances mismatched: want %s->%d %s->%d, got %v", oldA, pidA, oldB, pidB, gotVersions)
	}
	if promptTarget != target {
		t.Fatalf("prompt target %q want %q", promptTarget, target)
	}
	ptr, err := installlayout.ReadCurrent(root)
	if err != nil {
		t.Fatal(err)
	}
	if ptr.ActiveVersion != target {
		t.Fatalf("switch did not commit: %+v", ptr)
	}

	// (2)+(3) the prune runs (keep=1 keeps only the newest = the target tree;
	// both old trees are beyond it and occupied). No ghost may be created:
	// both live instances keep their trees and exes.
	pruned, retained, err := installlayout.PruneVersionTrees(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned) != 0 {
		t.Fatalf("occupied trees must survive pruning, got pruned=%v", pruned)
	}
	if len(retained) != 2 {
		t.Fatalf("retained=%v want both old trees", retained)
	}
	retainedNames := map[string]bool{}
	for _, occ := range retained {
		retainedNames[occ.Version] = true
	}
	if !retainedNames[oldA] || !retainedNames[oldB] {
		t.Fatalf("retained=%v want %s and %s", retained, oldA, oldB)
	}
	for _, v := range []string{oldA, oldB} {
		exePath := filepath.Join(versionsDir, v, installlayout.DesktopBinaryName())
		if _, err := os.Stat(exePath); err != nil {
			t.Fatalf("anti-ghost: %s exe must survive pruning while its instance runs: %v", v, err)
		}
	}
	// And the instances themselves are still alive (nothing killed them).
	if !desktopProcessAlive(pidA) || !desktopProcessAlive(pidB) {
		t.Fatalf("instances must survive the switch+prune scenario (pid %d alive=%v, pid %d alive=%v)",
			pidA, desktopProcessAlive(pidA), pidB, desktopProcessAlive(pidB))
	}

	// The picker annotates the occupied trees for the user.
	list, err := app.ListInstalledVersions()
	if err != nil {
		t.Fatal(err)
	}
	occupiedSeen := map[string]bool{}
	for _, item := range list {
		if item.Occupied {
			occupiedSeen[item.Version] = true
			if len(item.OccupiedPIDs) == 0 {
				t.Fatalf("occupied %s must carry pids", item.Version)
			}
		}
	}
	if !occupiedSeen[oldA] || !occupiedSeen[oldB] {
		t.Fatalf("picker must annotate occupied trees, got %v (list %v)", occupiedSeen, list)
	}

	// After both instances die, the next prune reclaims the trees and the
	// ledger disappears — the escalation loop closes.
	for _, pid := range []int{pidA, pidB} {
		p, err := os.FindProcess(pid)
		if err != nil {
			t.Fatal(err)
		}
		_ = p.Kill()
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(installlayout.ProbeVersionOccupancy(root, "")) > 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	pruned, retained, err = installlayout.PruneVersionTrees(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(retained) != 0 || len(pruned) != 2 {
		t.Fatalf("after instances exit: pruned=%v retained=%v want both old trees pruned", pruned, retained)
	}
	if _, err := os.Stat(filepath.Join(versionsDir, ".occupied-ledger.json")); !os.IsNotExist(err) {
		t.Fatalf("ledger must be gone once nothing is occupied, got %v", err)
	}
}

// TestDeleteInstalledVersionRefusesLiveTree extends task 411's guard with the
// task-1259 occupancy face: a tree a REAL second instance executes from must
// refuse with the pid, and the tree must survive.
func TestDeleteInstalledVersionRefusesLiveTree(t *testing.T) {
	victim := "v1.38.3-20261024-2200"
	root := deleteFixture(t, true, "")
	// deleteFixture built three trees; give the victim a live instance.
	pid := spawnDesktopTreeInstance(t, root, victim)
	app := &App{}
	err := app.DeleteInstalledVersion(victim)
	if err == nil || !strings.Contains(err.Error(), "still running") || !strings.Contains(err.Error(), fmt.Sprintf("%d", pid)) {
		t.Fatalf("deleting a live tree must refuse with the pid, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, installlayout.VersionsDirName, victim)); err != nil {
		t.Fatalf("live tree must survive the refused delete: %v", err)
	}
	// A non-occupied tree still deletes cleanly with the guard in place.
	if err := app.DeleteInstalledVersion("v1.38.3"); err != nil {
		t.Fatalf("non-occupied tree must delete cleanly: %v", err)
	}
}
