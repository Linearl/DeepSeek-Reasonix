package pidalive

import "testing"

// TestStillActiveExitCodeConstant pins the family-wide constant: every
// lock-scenario probe used to re-declare 259 locally (audit 20261116
// pid探测重复族 §F7); the value is the Windows STILL_ACTIVE exit code the
// merged probe compares GetExitCodeProcess against.
func TestStillActiveExitCodeConstant(t *testing.T) {
	if StillActiveExitCode != 259 {
		t.Fatalf("StillActiveExitCode = %d, want 259 (STILL_ACTIVE)", StillActiveExitCode)
	}
}

// TestLockHolderAliveRejectsNonPositivePids pins the entry-point short
// circuit: an identity-less holder (pid <= 0) must read dead without ever
// reaching the platform probe — callers treat 0 as "cannot judge" and the
// platform layer must not be asked to classify it.
func TestLockHolderAliveRejectsNonPositivePids(t *testing.T) {
	original := lockHolderAlive
	t.Cleanup(func() { lockHolderAlive = original })

	probed := false
	lockHolderAlive = func(pid int) bool {
		probed = true
		return true
	}

	cases := []struct {
		name string
		pid  int
	}{
		{"zero pid means cannot judge", 0},
		{"negative pid means cannot judge", -1},
		{"large negative pid means cannot judge", -421944},
	}
	for _, tc := range cases {
		probed = false
		if got := LockHolderAlive(tc.pid); got {
			t.Errorf("%s: LockHolderAlive(%d) = true, want false", tc.name, tc.pid)
		}
		if probed {
			t.Errorf("%s: platform probe was reached for pid %d, want short-circuit", tc.name, tc.pid)
		}
	}
}

// TestLockHolderAliveDelegatesToPlatformProbe pins the seam forwarding: the
// exported entry point must reach the platform probe with the caller's pid
// and report its verdict unchanged (this is the tl1623 merged route
// homemigrate / mcplaunch / cli take).
func TestLockHolderAliveDelegatesToPlatformProbe(t *testing.T) {
	original := lockHolderAlive
	t.Cleanup(func() { lockHolderAlive = original })

	cases := []struct {
		name       string
		probeReply bool
	}{
		{"platform probe says alive", true},
		{"platform probe says dead", false},
	}
	for _, tc := range cases {
		gotPid := 0
		lockHolderAlive = func(pid int) bool {
			gotPid = pid
			return tc.probeReply
		}
		if got := LockHolderAlive(4321); got != tc.probeReply {
			t.Errorf("%s: LockHolderAlive(4321) = %v, want %v", tc.name, got, tc.probeReply)
		}
		if gotPid != 4321 {
			t.Errorf("%s: platform probe got pid %d, want 4321", tc.name, gotPid)
		}
	}
}
