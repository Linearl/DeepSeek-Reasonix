//go:build windows

package pidalive

import (
	"fmt"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// TestAccessDeniedAliveVerdicts pins the lock-scenario conservative verdict on
// Windows: ACCESS_DENIED on OpenProcess proves the process exists (a
// less-probeable live writer must not have its lock stolen by a waiter — the
// verdict all three merged copies carried), any other open error means dead.
func TestAccessDeniedAliveVerdicts(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"bare access denied means alive", windows.ERROR_ACCESS_DENIED, true},
		{"wrapped access denied means alive",
			fmt.Errorf("open process: %w", windows.ERROR_ACCESS_DENIED), true},
		{"file not found means dead", windows.ERROR_FILE_NOT_FOUND, false},
		{"wrapped other error means dead",
			fmt.Errorf("open process: %w", windows.ERROR_INVALID_PARAMETER), false},
		{"nil is not an open error", nil, false},
	}
	for _, tc := range cases {
		if got := accessDeniedAlive(tc.err); got != tc.want {
			t.Errorf("%s: accessDeniedAlive(%v) = %v, want %v", tc.name, tc.err, got, tc.want)
		}
	}
}

// TestAccessDeniedAliveViaSeam feeds a mocked open error through the real
// LockHolderAlive entry point: the ACCESS_DENIED verdict must survive the
// merged route unchanged.
func TestAccessDeniedAliveViaSeam(t *testing.T) {
	original := lockHolderAlive
	t.Cleanup(func() { lockHolderAlive = original })

	cases := []struct {
		name       string
		probeReply bool
		want       bool
	}{
		{"access-denied style probe verdict (alive) passes through", true, true},
		{"unprobeable probe verdict (dead) passes through", false, false},
	}
	for _, tc := range cases {
		lockHolderAlive = func(pid int) bool { return tc.probeReply }
		if got := LockHolderAlive(4321); got != tc.want {
			t.Errorf("%s: LockHolderAlive(4321) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestDefaultLockHolderAliveRealProcesses is the no-mock sanity on the real
// Windows probe: the running test process opens and reads STILL_ACTIVE
// (alive), while a pid far above every pid_max fails to open with anything
// but ACCESS_DENIED (dead) — the two outcomes the lock callers rely on.
func TestDefaultLockHolderAliveRealProcesses(t *testing.T) {
	if !LockHolderAlive(os.Getpid()) {
		t.Fatalf("LockHolderAlive(self pid %d) = false, want true", os.Getpid())
	}
	if LockHolderAlive(999999999) {
		t.Fatalf("LockHolderAlive(999999999) = true, want false (pid beyond every pid_max)")
	}
}
