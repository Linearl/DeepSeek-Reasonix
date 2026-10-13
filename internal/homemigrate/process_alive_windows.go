//go:build windows

package homemigrate

import (
	"errors"

	"golang.org/x/sys/windows"
)

const stillActiveExitCode = 259

// pidAlive reports whether the lock owner's process still exists. Access
// denied still proves existence (mcplaunch precedent): treating a
// less-probeable live writer as dead would let a waiter steal its lock.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer windows.CloseHandle(handle)
	var code uint32
	return windows.GetExitCodeProcess(handle, &code) == nil && code == stillActiveExitCode
}
