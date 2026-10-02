//go:build windows

package baseproc

import "os"

// pidAlive reports whether pid names a live process on Windows: FindProcess
// opens the process and fails when it is gone (and GetExitCodeProcess rejects
// one that has already exited), so an exited pid is reported dead without
// guessing. Pid reuse can make a dead owner look alive — that only delays the
// C4 sweep to the next hello, it never drops a live client's lease.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	_, err := os.FindProcess(pid)
	return err == nil
}
