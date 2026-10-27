//go:build !windows

package agent

import "syscall"

// defaultProcessAlive mirrors desktop's desktopProcessAlive for Unix: signal
// 0 probes for existence without delivering anything; EPERM means the process
// exists but is owned by another user, which still counts as alive.
func defaultProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
