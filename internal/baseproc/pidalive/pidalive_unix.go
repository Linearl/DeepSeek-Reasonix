//go:build !windows

package pidalive

import (
	"os"
	"syscall"
)

// Alive reports whether pid names a live process on POSIX: a zero signal
// only checks permission, so ESRCH means "no such process". A zombie child
// still answers (it exists until reaped) — conservative, same as Windows.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
