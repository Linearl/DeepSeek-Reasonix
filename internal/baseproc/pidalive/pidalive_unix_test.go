//go:build !windows

package pidalive

import (
	"os"
	"syscall"
	"testing"
)

// TestZeroSignalAliveVerdicts pins the tl1622 EPERM alignment: the verdict
// table must equal audit M1 (servepool/reap_orphan.go zeroSignalAlive: "no
// error or EPERM both mean the process EXISTS") and agent's Unix side
// (process_alive_other.go: err == nil || err == syscall.EPERM). agent
// compares the errno directly because its raw syscall.Kill returns it
// unwrapped, while Alive probes via os.Process.Signal whose failure is
// wrapped in *os.SyscallError — hence errors.Is here, same table. Treating
// EPERM as dead would let collabinbox/sessioncollab stale-holder recycle
// clear a live cross-user holder's sidecar (audit 20261116 pid探测重复族
// §F2, P0).
func TestZeroSignalAliveVerdicts(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"no error means alive", nil, true},
		{"EPERM means alive (audit M1 / tl1622)", syscall.EPERM, true},
		{"wrapped EPERM means alive (os.Signal wraps in SyscallError)",
			os.NewSyscallError("signal", syscall.EPERM), true},
		{"ESRCH means dead", syscall.ESRCH, false},
		{"wrapped ESRCH means dead",
			os.NewSyscallError("signal", syscall.ESRCH), false},
		{"unrelated errno fails closed", syscall.EINVAL, false},
	}
	for _, tc := range cases {
		if got := zeroSignalAlive(tc.err); got != tc.want {
			t.Errorf("%s: zeroSignalAlive(%v) = %v, want %v", tc.name, tc.err, got, tc.want)
		}
	}
}

// TestAliveEPERMThroughMockProbe pins the branch through the real Alive entry
// point with the zeroSignalProbe seam (same var-injection precedent as
// agent's ProcessAlive and servepool's processAlive): a probe that answers
// EPERM — the cross-user live holder — must read ALIVE; only ESRCH (and the
// pid<=0 short-circuit) may read DEAD.
func TestAliveEPERMThroughMockProbe(t *testing.T) {
	original := zeroSignalProbe
	t.Cleanup(func() { zeroSignalProbe = original })

	cases := []struct {
		name       string
		pid        int
		probeErr   error
		want       bool
		probeWants bool // whether Alive should reach the probe at all
	}{
		{"EPERM probe means alive (tl1622 pin)", 4321, syscall.EPERM, true, true},
		{"ESRCH probe means dead", 4321, syscall.ESRCH, false, true},
		{"nil probe means alive", 4321, nil, true, true},
		{"pid<=0 dies before probing", 0, nil, false, false},
		{"negative pid dies before probing", -7, syscall.EPERM, false, false},
	}
	for _, tc := range cases {
		probed := false
		zeroSignalProbe = func(pid int) error {
			probed = true
			if pid != tc.pid {
				t.Errorf("%s: probe got pid %d, want %d", tc.name, pid, tc.pid)
			}
			return tc.probeErr
		}
		if got := Alive(tc.pid); got != tc.want {
			t.Errorf("%s: Alive(%d) = %v, want %v", tc.name, tc.pid, got, tc.want)
		}
		if probed != tc.probeWants {
			t.Errorf("%s: probe reached = %v, want %v", tc.name, probed, tc.probeWants)
		}
	}
}

// TestAliveRealProcesses is the no-mock sanity: the running test process
// answers its own probe with success (alive), while a pid far above every
// platform's pid_max answers ESRCH (dead) — the two outcomes the stale-holder
// callers rely on. The dead pid mirrors the 999999999 fixture the
// collabinbox/sessioncollab stale-holder tests already use.
func TestAliveRealProcesses(t *testing.T) {
	if !Alive(os.Getpid()) {
		t.Fatalf("Alive(self pid %d) = false, want true", os.Getpid())
	}
	if Alive(999999999) {
		t.Fatalf("Alive(999999999) = true, want false (pid beyond every pid_max)")
	}
}
