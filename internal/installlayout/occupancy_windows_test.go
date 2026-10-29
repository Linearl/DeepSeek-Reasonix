//go:build windows

package installlayout

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Task 1259 occupancy tests use a REAL second process: the test binary is
// copied into a fake version tree and launched from there, so the probe sees
// exactly the production shape — a live image whose path is
// <root>/versions/<ver>/reasonix-desktop.exe.

// TestOccupancyHelperProcess is the child face of the occupancy tests. In the
// parent suite run it does nothing (return = structural, not a skipped
// assertion); re-executed with GO_WANT_HELPER_PROCESS=1 it sits still so the
// parent can enumerate and lock-probe it, and the parent kills it when done.
func TestOccupancyHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	time.Sleep(60 * time.Second)
	// Not reached in practice: the parent kills the process. The sleep bounds
	// leakage if the parent itself dies mid-test.
}

// spawnTreeInstance copies the test binary into <versionsDir>/<version>/
// under the desktop binary name and starts it in helper mode. The returned
// cleanup kills the child and waits, so temp-dir removal never races a live
// image lock.
func spawnTreeInstance(t *testing.T, versionsDir, version string) (pid int) {
	t.Helper()
	treeDir := filepath.Join(versionsDir, version)
	if err := os.MkdirAll(treeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exeCopy := filepath.Join(treeDir, DesktopBinaryName())
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

	cmd := exec.Command(exeCopy, "-test.run=TestOccupancyHelperProcess", "-test.v")
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

	// The image lock and the process table both exist once the child is up;
	// wait briefly so the assertions are deterministic on a loaded CI box.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if len(probeVersionTreeProcesses(versionsDir, version)) > 0 {
			return cmd.Process.Pid
		}
		if time.Now().After(deadline) {
			t.Fatalf("spawned child pid %d not attributed to %s within 10s", cmd.Process.Pid, version)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestProbeVersionTreeProcessesFindsAndAttributes(t *testing.T) {
	root := t.TempDir()
	versionsDir := filepath.Join(root, VersionsDirName)
	if err := os.MkdirAll(versionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pid := spawnTreeInstance(t, versionsDir, "v1.38.3-20261029-0900")
	// A second, empty tree must not pollute the attribution.
	if err := os.MkdirAll(filepath.Join(versionsDir, "v1.37.0"), 0o755); err != nil {
		t.Fatal(err)
	}

	all := probeVersionTreeProcesses(versionsDir, "")
	if len(all) != 1 {
		t.Fatalf("probe=%v want exactly the spawned child", all)
	}
	if all[0].PID != pid || all[0].Version != "v1.38.3-20261029-0900" {
		t.Fatalf("probe[0]=%+v want pid %d in v1.38.3-20261029-0900", all[0], pid)
	}

	only := ProbeVersionOccupancy(root, "v1.38.3-20261029-0900")
	if len(only) != 1 || only[0].PID != pid {
		t.Fatalf("targeted probe=%v want pid %d", only, pid)
	}
	if other := ProbeVersionOccupancy(root, "v1.37.0"); len(other) != 0 {
		t.Fatalf("empty tree must not be attributed: %v", other)
	}
}

func TestAttributionClearsAfterExit(t *testing.T) {
	root := t.TempDir()
	versionsDir := filepath.Join(root, VersionsDirName)
	pid := spawnTreeInstance(t, versionsDir, "v1.38.3-20261029-0900")

	if len(ProbeVersionOccupancy(root, "v1.38.3-20261029-0900")) != 1 {
		t.Fatalf("running pid %d must be attributed before exit", pid)
	}

	// Kill and wait: the attribution must drop — this is the exact sequence
	// that turns a kept tree into a prunable one. (File-open probes cannot
	// see a running image on modern Windows, so path attribution is the
	// signal under test.)
	if err := killPID(pid); err != nil {
		t.Fatalf("kill child: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if left := len(ProbeVersionOccupancy(root, "v1.38.3-20261029-0900")); left == 0 {
			return
		} else if time.Now().After(deadline) {
			t.Fatalf("after kill: probe still attributes %d processes, want clear within 10s", left)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// killPID terminates one process by id (the child is a helper we own).
func killPID(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

func TestVersionUnderTreeMatching(t *testing.T) {
	versionsDir := `C:\install\versions`
	cases := []struct {
		exePath string
		want    string
		ok      bool
	}{
		{`C:\install\versions\v1.38.3\reasonix-desktop.exe`, "v1.38.3", true},
		{`c:\INSTALL\VERSIONS\V1.38.3\sub\reasonix-cli.exe`, "v1.38.3", true}, // case-insensitive, any depth
		{`C:\install\versions\v1.38.3-20261029-0900\reasonix-desktop.exe`, "v1.38.3-20261029-0900", true},
		{`C:\install\versions\v1.38.3-sibling\reasonix-desktop.exe`, "v1.38.3-sibling", true}, // no prefix bleed
		{`C:\install\versions\.staging-abc\reasonix-desktop.exe`, "", false},                  // staging is not a version
		{`C:\install\versions\reasonix-desktop.exe`, "", false},                               // directly in versions/
		{`C:\elsewhere\versions\v1.38.3\reasonix-desktop.exe`, "", false},                     // different root
		{`C:\install\versions-v1.38.3\reasonix-desktop.exe`, "", false},                       // separator-bounded
	}
	for _, tc := range cases {
		got, ok := versionUnderTree(tc.exePath, versionsDir)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("versionUnderTree(%q) = (%q,%v), want (%q,%v)", tc.exePath, got, ok, tc.want, tc.ok)
		}
	}
}
