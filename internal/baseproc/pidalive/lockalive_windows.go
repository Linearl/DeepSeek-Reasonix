//go:build windows

package pidalive

import (
	"errors"

	"golang.org/x/sys/windows"
)

// defaultLockHolderAlive probes pid the lock-scenario way: open with
// query-limited rights and read the exit code; STILL_ACTIVE means the owner
// has not exited. ACCESS_DENIED still proves the process exists — treating it
// as dead would let a less-privileged waiter steal a live writer's lock (the
// verdict all three merged copies already carried). The leaf stays free of
// in-repo imports; golang.org/x/sys is an external dependency.
func defaultLockHolderAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return accessDeniedAlive(err)
	}
	defer windows.CloseHandle(handle)
	var code uint32
	return windows.GetExitCodeProcess(handle, &code) == nil && code == StillActiveExitCode
}

// accessDeniedAlive is the conservative ACCESS_DENIED verdict, split out as
// the table-testable predicate: an access-denied open proves existence (the
// process is there, we just may not probe it deeper), any other open error
// means there is nothing whose lock could be alive.
func accessDeniedAlive(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
