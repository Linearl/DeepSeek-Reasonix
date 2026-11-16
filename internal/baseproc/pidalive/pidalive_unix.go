//go:build !windows

package pidalive

import (
	"errors"
	"os"
	"syscall"
)

// Alive reports whether pid names a live process on POSIX: a zero signal
// probes existence without delivering anything, so ESRCH means "no such
// process", and EPERM means the process exists but is owned by someone we
// cannot signal — which still counts as alive. A zombie child still answers
// (it exists until reaped) — conservative, same as Windows.
//
// The EPERM→alive ruling is audit M1 (tl1622, 20261116 pid探测重复族审计
// §F2): servepool's zeroSignalAlive carries it because treating EPERM as dead
// misclassifies a live peer desktop's serve as an orphan and kills it, and
// agent's process_alive_other.go carries the same ruling for lease-holder
// classification. Here the same misjudgment would make collabinbox /
// sessioncollab stale-holder recycle clear a live cross-user holder's
// sidecar — breaking the "never clear on a guess" contract these callers
// document.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return zeroSignalAlive(zeroSignalProbe(pid))
}

// zeroSignalProbe performs the POSIX existence probe for Alive. It is a
// package variable only as the test seam (same var-injection precedent as
// agent's ProcessAlive and servepool's processAlive) so tests can feed a
// mocked EPERM result through the real Alive entry point.
var zeroSignalProbe = func(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(syscall.Signal(0))
}

// zeroSignalAlive interprets a Signal(0) probe result (audit M1): no error or
// EPERM both mean the process EXISTS — ESRCH is the only death verdict a zero
// signal can deliver.
func zeroSignalAlive(err error) bool {
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}
