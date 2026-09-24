package main

import (
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
)

// TestShutdownWatchdogForcesExitWhenBodyWedges pins task 272 L1: a body()
// that never returns must still end the process (the launcher's 90s wait and
// the single-instance lock make an old lingering desktop fatal), and the
// forced exit must be observable here instead of killing the test binary.
func TestShutdownWatchdogForcesExitWhenBodyWedges(t *testing.T) {
	oldExit, oldTimeout := shutdownExit, shutdownWatchdogTimeout
	t.Cleanup(func() { shutdownExit, shutdownWatchdogTimeout = oldExit, oldTimeout })
	shutdownWatchdogTimeout = 30 * time.Millisecond
	exited := make(chan int, 1)
	shutdownExit = func(code int) { exited <- code }

	tracker := newDesktopLifecycleTracker(t.TempDir(), "v0-test", "test")
	completeDesktopShutdown(tracker, func() {
		// The body wedges exactly like a stuck rebuild lock; only the
		// watchdog can unblock it.
		select {
		case code := <-exited:
			if code != 1 {
				t.Errorf("watchdog exit code = %d, want 1", code)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("watchdog never fired: shutdown would hang forever (incident ①)")
		}
	})

	// The watchdog fired exactly once and the shutdown completed after it.
	select {
	case code := <-exited:
		t.Fatalf("watchdog fired a second time after body finished: code=%d", code)
	default:
	}
}

// TestShutdownWatchdogAbsentForFastBody pins the non-regression: a fast
// body() must complete with NO forced exit at all.
func TestShutdownWatchdogAbsentForFastBody(t *testing.T) {
	oldExit, oldTimeout := shutdownExit, shutdownWatchdogTimeout
	t.Cleanup(func() { shutdownExit, shutdownWatchdogTimeout = oldExit, oldTimeout })
	shutdownWatchdogTimeout = time.Hour
	shutdownExit = func(code int) { t.Fatalf("watchdog fired on a fast body: code=%d", code) }

	tracker := newDesktopLifecycleTracker(t.TempDir(), "v0-test", "test")
	completeDesktopShutdown(tracker, func() {})
}

// TestSessionLeaseBusyErrorNamesLeftoverPID pins task 272 L3: when the lease
// holder is a concrete foreign pid, the message hands over an EXISTING next
// step (restart reaps it; taskkill PID is directly runnable) instead of the
// dead-end "close the other window" that had no window to close.
func TestSessionLeaseBusyErrorNamesLeftoverPID(t *testing.T) {
	const leftoverPID = 987654
	wrapped := &sessionLeaseBusyError{
		err: &agent.SessionLeaseError{
			Path: "/sessions/demo.jsonl",
			Info: &agent.SessionLeaseInfo{PID: leftoverPID, WriterID: "host-987654-w1"},
		},
	}
	msg := wrapped.Error()
	for _, want := range []string{
		"leftover background process (pid 987654)",
		"taskkill /PID 987654",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message missing %q:\n%s", want, msg)
		}
	}
	// A wrapper with no underlying lease info keeps the original wording: no
	// pid means no leftover-process claim can be made.
	plain := &sessionLeaseBusyError{}
	if plain.Error() == "" {
		t.Fatal("empty-error wrapper must still render the base advice")
	}
	if strings.Contains(plain.Error(), "taskkill") {
		t.Fatalf("no pid => no taskkill advice:\n%s", plain.Error())
	}
}
