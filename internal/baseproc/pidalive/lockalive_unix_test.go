//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package pidalive

import (
	"os"
	"syscall"
	"testing"
)

// TestKillErrAliveVerdicts pins the POSIX lock-scenario verdict table: nil and
// EPERM both mean the holder EXISTS (EPERM is the live cross-user holder a
// conservative lock probe must respect, same ruling as audit M1); ESRCH is
// the only death verdict a zero signal can deliver.
func TestKillErrAliveVerdicts(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"no error means alive", nil, true},
		{"EPERM means alive (cross-user live holder)", syscall.EPERM, true},
		{"ESRCH means dead", syscall.ESRCH, false},
		{"unrelated errno fails closed", syscall.EINVAL, false},
	}
	for _, tc := range cases {
		if got := killErrAlive(tc.err); got != tc.want {
			t.Errorf("%s: killErrAlive(%v) = %v, want %v", tc.name, tc.err, got, tc.want)
		}
	}
}

// TestLockHolderAliveEPERMThroughMockProbe feeds a mocked probe result through
// the real LockHolderAlive entry point (same var-injection precedent as
// zeroSignalProbe): a probe answering EPERM — the cross-user live holder —
// must read ALIVE; only ESRCH (and the pid<=0 short-circuit) may read DEAD.
func TestLockHolderAliveEPERMThroughMockProbe(t *testing.T) {
	original := zeroKillProbe
	t.Cleanup(func() { zeroKillProbe = original })

	cases := []struct {
		name       string
		pid        int
		probeErr   error
		want       bool
		probeWants bool // whether LockHolderAlive should reach the probe at all
	}{
		{"EPERM probe means alive (cross-user holder)", 4321, syscall.EPERM, true, true},
		{"ESRCH probe means dead", 4321, syscall.ESRCH, false, true},
		{"nil probe means alive", 4321, nil, true, true},
		{"pid<=0 dies before probing", 0, nil, false, false},
	}
	for _, tc := range cases {
		probed := false
		zeroKillProbe = func(pid int) error {
			probed = true
			if pid != tc.pid {
				t.Errorf("%s: probe got pid %d, want %d", tc.name, pid, tc.pid)
			}
			return tc.probeErr
		}
		if got := LockHolderAlive(tc.pid); got != tc.want {
			t.Errorf("%s: LockHolderAlive(%d) = %v, want %v", tc.name, tc.pid, got, tc.want)
		}
		if probed != tc.probeWants {
			t.Errorf("%s: probe reached = %v, want %v", tc.name, probed, tc.probeWants)
		}
	}
}

// TestLockHolderAliveRealProcesses is the no-mock sanity: the running test
// process answers its own probe with success (alive), while a pid far above
// every platform's pid_max answers ESRCH (dead) — the two outcomes the lock
// callers rely on.
func TestLockHolderAliveRealProcesses(t *testing.T) {
	if !LockHolderAlive(os.Getpid()) {
		t.Fatalf("LockHolderAlive(self pid %d) = false, want true", os.Getpid())
	}
	if LockHolderAlive(999999999) {
		t.Fatalf("LockHolderAlive(999999999) = true, want false (pid beyond every pid_max)")
	}
}
