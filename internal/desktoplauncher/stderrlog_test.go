package desktoplauncher

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// restoreStdlog keeps the process-global stderr/log redirection inside the
// test that caused it.
func restoreStdlog(t *testing.T) {
	t.Helper()
	origStderr := os.Stderr
	origLogWriter := log.Default().Writer()
	t.Cleanup(func() {
		os.Stderr = origStderr
		log.SetOutput(origLogWriter)
	})
}

// 任务 404 acceptance (stderr 落盘): the launcher's stderr lands in
// logs/launcher/launcher.log — including slog lines written through the std
// log sink.
func TestInstallLauncherLogRedirectsWhenDetached(t *testing.T) {
	restoreStdlog(t)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "launcher", "launcher.log")
	oldPath, oldDetach := launcherLogPath, detachByDefault
	launcherLogPath = func() string { return logPath }
	detachByDefault = func() bool { return true }
	t.Cleanup(func() { launcherLogPath, detachByDefault = oldPath, oldDetach })

	f := installLauncherLog()
	if f == nil {
		t.Fatal("installLauncherLog = nil, want the rolling log file in detach mode")
	}
	defer f.Close()
	fmt.Fprintln(os.Stderr, "launcher: stderr redirection probe")
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read launcher log: %v", err)
	}
	if !strings.Contains(string(body), "stderr redirection probe") {
		t.Fatalf("launcher log missing the stderr line, got %q", string(body))
	}
}

// A console-launched launcher keeps talking to its terminal: no redirection,
// no log file, no error.
func TestInstallLauncherLogConsolePassthrough(t *testing.T) {
	restoreStdlog(t)
	oldPath, oldDetach := launcherLogPath, detachByDefault
	launcherLogPath = func() string { return filepath.Join(t.TempDir(), "unused.log") }
	detachByDefault = func() bool { return false }
	t.Cleanup(func() { launcherLogPath, detachByDefault = oldPath, oldDetach })

	stderrBefore := os.Stderr
	if f := installLauncherLog(); f != nil {
		t.Fatal("installLauncherLog != nil in console mode")
	}
	if os.Stderr != stderrBefore {
		t.Fatal("console mode must not touch os.Stderr")
	}
}

// Rotation is the baseproc family pattern: an oversized generation moves to
// <path>.1 at open, and the fresh file keeps appending. A rotation failure
// (open handle on Windows) degrades to append-forever, never an error.
func TestOpenLauncherLogRotatesOversizedGeneration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "launcher.log")
	oversized := strings.Repeat("x", 4096)
	if err := os.WriteFile(path, []byte(oversized), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := openLauncherLogAt(path, 2048)
	if err != nil {
		t.Fatalf("openLauncherLogAt: %v", err)
	}
	defer f.Close()
	rotated, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("rotated generation missing: %v", err)
	}
	if string(rotated) != oversized {
		t.Fatalf("rotated generation content mismatch: %d bytes", len(rotated))
	}
	if st, err := os.Stat(path); err != nil || st.Size() != 0 {
		t.Fatalf("fresh generation not empty (size=%v err=%v)", st, err)
	}
}

// The 4 MiB cap is the task-404 decision; pin it so a casual edit is a
// deliberate one.
func TestLauncherLogCapIs4MiB(t *testing.T) {
	if launcherLogMaxBytes != 4<<20 {
		t.Fatalf("launcherLogMaxBytes = %d, want 4 MiB", launcherLogMaxBytes)
	}
}
