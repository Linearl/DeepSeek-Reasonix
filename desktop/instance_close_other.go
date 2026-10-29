//go:build !windows

package main

import (
	"os"
	"syscall"
	"time"
)

// Non-Windows graceful close (task 1259 layer 2): SIGTERM is the canonical
// graceful termination request on POSIX — the app can intercept it, flush,
// and exit. Like the Windows face this never escalates to SIGKILL; survivors
// are reported for manual handling.

// instanceCloseGrace mirrors the Windows seam (test-overridable wait).
var instanceCloseGrace = 10 * time.Second

const instanceClosePollInterval = 250 * time.Millisecond

func gracefulCloseOtherInstance(pid int) bool {
	if pid <= 0 || pid == os.Getpid() {
		return false
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return false
	}
	deadline := time.Now().Add(instanceCloseGrace)
	for {
		if !desktopProcessAlive(pid) {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(instanceClosePollInterval)
	}
}
