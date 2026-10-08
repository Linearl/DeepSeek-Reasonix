package desktoplauncher

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// The watchdog tests re-execute the test binary as a stand-in desktop
// (GO_LAUNCHER_WATCHDOG_HELPER): each helper invocation appends one line to
// GO_HELPER_LOG and then either exits with GO_HELPER_EXIT or parks. The
// launcher's desktop-log mirror (appendDesktopLogLine) is pointed at a temp
// file so tests never touch the real user state.

const (
	helperEnv     = "GO_LAUNCHER_WATCHDOG_HELPER"
	helperLogEnv  = "GO_HELPER_LOG"
	helperExitEnv = "GO_HELPER_EXIT"
)

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) != "" {
		helperDesktopMain()
		return
	}
	os.Exit(m.Run())
}

// helperDesktopMain plays the desktop: record the invocation, then exit with
// the requested code — or park so a future test can kill this generation.
func helperDesktopMain() {
	if path := os.Getenv(helperLogEnv); path != "" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			fmt.Fprintf(f, "pid=%d\n", os.Getpid())
			f.Close()
		}
	}
	if exit := os.Getenv(helperExitEnv); exit != "" {
		code, _ := strconv.Atoi(exit)
		os.Exit(code)
	}
	for {
		time.Sleep(time.Hour)
	}
}

func helperCommand(t *testing.T, logPath string, exitCode string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(),
		helperEnv+"=1",
		helperLogEnv+"="+logPath,
		helperExitEnv+"="+exitCode,
	)
	return cmd
}

func readHelperLog(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read helper log: %v", err)
	}
	var lines []string
	for _, line := range bytes.Split(bytes.TrimSpace(body), []byte("\n")) {
		if len(line) > 0 {
			lines = append(lines, string(line))
		}
	}
	return lines
}

func shrinkBackoff(t *testing.T) {
	t.Helper()
	old := watchdogRestartDelay
	watchdogRestartDelay = time.Millisecond
	t.Cleanup(func() { watchdogRestartDelay = old })
}

// isolateDesktopLogMirror keeps supervise's appendDesktopLogLine mirror away
// from the real desktop.log while the tests run.
func isolateDesktopLogMirror(t *testing.T) {
	t.Helper()
	old := desktopLogPath
	desktopLogPath = func() string {
		return filepath.Join(t.TempDir(), "desktop.log")
	}
	t.Cleanup(func() { desktopLogPath = old })
}

// startedHelper returns the helper command in the contract restart()
// promises: already started (production restartCommand returns post-Start).
func startedHelper(t *testing.T, logPath string, exitCode string) *exec.Cmd {
	t.Helper()
	cmd := helperCommand(t, logPath, exitCode)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	return cmd
}

// 任务 404 acceptance: an abnormal desktop death is followed by exactly one
// restart, and the restarted generation's clean exit ends the launch with 0.
func TestSuperviseRestartsOnceAfterAbnormalExit(t *testing.T) {
	shrinkBackoff(t)
	isolateDesktopLogMirror(t)
	logPath := filepath.Join(t.TempDir(), "gen.log")
	gen1 := helperCommand(t, logPath, "3")
	if err := gen1.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	restarts := 0
	got := supervise(gen1, func() (*exec.Cmd, error) {
		restarts++
		return startedHelper(t, logPath, "0"), nil
	})
	if got != 0 {
		t.Fatalf("supervise = %d, want 0 (restarted generation exits cleanly)", got)
	}
	if restarts != 1 {
		t.Fatalf("restarts = %d, want exactly 1", restarts)
	}
	if lines := readHelperLog(t, logPath); len(lines) != 2 {
		t.Fatalf("helper invocations = %d (%v), want 2 (died generation + restart)", len(lines), lines)
	}
}

// Every deliberate exit path (normal quit, update handoff, superseded
// relaunch, second-instance yield) leaves code 0 — and must not be re-armed.
func TestSuperviseNormalExitNeverRestarts(t *testing.T) {
	shrinkBackoff(t)
	isolateDesktopLogMirror(t)
	logPath := filepath.Join(t.TempDir(), "gen.log")
	gen1 := helperCommand(t, logPath, "0")
	if err := gen1.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	restarts := 0
	got := supervise(gen1, func() (*exec.Cmd, error) {
		restarts++
		return helperCommand(t, logPath, "0"), nil
	})
	if got != 0 {
		t.Fatalf("supervise = %d, want 0", got)
	}
	if restarts != 0 {
		t.Fatalf("restarts = %d, want 0: a deliberate exit must not be re-armed", restarts)
	}
	if lines := readHelperLog(t, logPath); len(lines) != 1 {
		t.Fatalf("helper invocations = %d, want 1", len(lines))
	}
}

// A crash loop stops after the one restart: the restarted generation dying
// abnormally is recorded (its exit code propagates) and left alone.
func TestSuperviseSecondAbnormalExitSpendsBudget(t *testing.T) {
	shrinkBackoff(t)
	isolateDesktopLogMirror(t)
	logPath := filepath.Join(t.TempDir(), "gen.log")
	gen1 := helperCommand(t, logPath, "3")
	if err := gen1.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	restarts := 0
	got := supervise(gen1, func() (*exec.Cmd, error) {
		restarts++
		return startedHelper(t, logPath, "2"), nil
	})
	if got != 2 {
		t.Fatalf("supervise = %d, want 2 (restarted generation's exit code)", got)
	}
	if restarts != 1 {
		t.Fatalf("restarts = %d, want exactly 1", restarts)
	}
	if lines := readHelperLog(t, logPath); len(lines) != 2 {
		t.Fatalf("helper invocations = %d, want 2 — the budget must stop at one restart", len(lines))
	}
}

func TestWatchdogDisabledKillSwitch(t *testing.T) {
	t.Setenv("REASONIX_LAUNCHER_WATCHDOG", "off")
	if !watchdogDisabled() {
		t.Fatal("kill switch not honored")
	}
	t.Setenv("REASONIX_LAUNCHER_WATCHDOG", "on")
	if watchdogDisabled() {
		t.Fatal("any value other than off must keep the watchdog on")
	}
	os.Unsetenv("REASONIX_LAUNCHER_WATCHDOG")
	if watchdogDisabled() {
		t.Fatal("default must keep the watchdog on")
	}
}

func TestExitCodeOf(t *testing.T) {
	cmd := helperCommand(t, filepath.Join(t.TempDir(), "x.log"), "7")
	if err := cmd.Run(); err == nil {
		t.Fatal("helper with exit 7 must not exit cleanly")
	}
	if got := exitCodeOf(cmd); got != 7 {
		t.Fatalf("exitCodeOf = %d, want 7", got)
	}
	cmd = helperCommand(t, filepath.Join(t.TempDir(), "x.log"), "0")
	if err := cmd.Run(); err != nil {
		t.Fatalf("clean helper exit: %v", err)
	}
	if got := exitCodeOf(cmd); got != 0 {
		t.Fatalf("exitCodeOf = %d, want 0", got)
	}
}

// restartCommand must prefer a fresh current.json resolution but fall back to
// the previously running desktop when the pointer is unreadable (corrupt
// pointer must not convert a restartable crash into an abandoned desktop).
func TestBuildRestartCommandResolution(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, "desktop-stale.exe")
	if err := os.WriteFile(stale, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "current.json"), []byte(`{"schemaVersion":99}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := buildRestartCommand(root, stale, nil)
	if got := cmd.Path; got != stale {
		t.Fatalf("restart path = %q, want the previously running %q", got, stale)
	}
	if cmd.Dir != root {
		t.Fatalf("restart dir = %q, want %q", cmd.Dir, root)
	}
}
