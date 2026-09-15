//go:build windows

package desktoplauncher

import (
	"os/exec"
	"testing"
	"time"
)

// A pid that no longer exists must pass the handoff immediately: the whole
// point is that a dead desktop has already released the gateway port.
func TestWaitForProcessExitAlreadyGone(t *testing.T) {
	start := time.Now()
	if err := waitForProcessExit(0x7FFFFFFF, time.Second); err != nil {
		t.Fatalf("wait on nonexistent pid: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("wait on nonexistent pid took %s; want immediate pass", elapsed)
	}
}

// A short-lived child must be observed exiting, proving the wait is real and
// not a no-op that always succeeds.
func TestWaitForProcessExitObservesRealExit(t *testing.T) {
	if _, err := exec.LookPath("cmd"); err != nil {
		t.Skip("cmd.exe unavailable")
	}
	cmd := exec.Command("cmd", "/c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleeper: %v", err)
	}
	go func() { _ = cmd.Wait() }()
	if err := waitForProcessExit(cmd.Process.Pid, 10*time.Second); err != nil {
		t.Fatalf("wait on exiting pid: %v", err)
	}
}
