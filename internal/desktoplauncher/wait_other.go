//go:build !windows

package desktoplauncher

import "time"

// waitForProcessExit is a Windows-only handoff detail: the detach-and-exit
// relaunch elsewhere never races an exiting desktop, so there is nothing to
// wait for.
func waitForProcessExit(pid int, timeout time.Duration) error {
	_ = pid
	_ = timeout
	return nil
}
