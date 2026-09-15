//go:build windows

package desktoplauncher

import (
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

// waitForProcessExit blocks until the process exits, mirroring the update
// helper's instance handoff. A pid that is already gone (or cannot be opened)
// passes immediately: the caller only needs the single-instance resources
// (servepool gateway port, session locks) to be released, and a dead pid has
// released them.
func waitForProcessExit(pid int, timeout time.Duration) error {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return nil
		}
		return err
	}
	defer windows.CloseHandle(h)
	result, err := windows.WaitForSingleObject(h, uint32(timeout/time.Millisecond))
	if err != nil {
		return err
	}
	switch result {
	case windows.WAIT_OBJECT_0:
		return nil
	case uint32(windows.WAIT_TIMEOUT):
		return fmt.Errorf("previous desktop (pid %d) still running after %s", pid, timeout)
	default:
		return fmt.Errorf("unexpected wait result %d", result)
	}
}
