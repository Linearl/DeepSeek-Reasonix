//go:build windows

package proc

import (
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestJobHandleCloseReapsChildWithoutExplicitKill pins task 272 L2's core
// promise: the serve dies with the desktop EVEN WHEN nothing ever calls
// KillTracked — taskkill/crash/watchdog exit just drops the Job Object handle,
// and KILL_ON_JOB_CLOSE reaps the tree. This is the semantic the bare
// proc.Command spawn (incident ②) lacked: with it, an orphan `reasonix-cli
// serve` can no longer outlive the desktop holding a session lease.
func TestJobHandleCloseReapsChildWithoutExplicitKill(t *testing.T) {
	cmd := Command("cmd", "/c", "ping -n 60 127.0.0.1 >NUL")
	job, err := StartTrackedRequired(cmd)
	if err != nil {
		t.Fatalf("StartTrackedRequired: %v", err)
	}
	if cmd.Process == nil {
		t.Fatal("child did not start")
	}
	pid := cmd.Process.Pid
	// Simulate an abrupt desktop death: close the job handle the way process
	// teardown does, with NO KillTracked call.
	if err := windows.CloseHandle(windows.Handle(job)); err != nil {
		t.Fatalf("close job handle: %v", err)
	}
	// cmd.Wait is the same liveness probe the sibling KillTracked test uses:
	// it unblocks only when the child tree has actually been reaped.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		return // reaped by KILL_ON_JOB_CLOSE
	case <-time.After(8 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("cmd.Wait blocked after job handle close: child %d survived (KILL_ON_JOB_CLOSE not in effect — orphan-serve bug, task 272 ②)", pid)
	}
}
