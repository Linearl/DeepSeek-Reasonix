//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package pidalive

import "syscall"

// defaultLockHolderAlive probes pid the POSIX way: signal 0 checks existence
// without delivering anything. nil and EPERM both mean the holder exists —
// EPERM is the live cross-user holder a conservative lock probe must respect,
// not a death verdict (same ruling as audit M1 in pidalive_unix.go); ESRCH is
// the only death verdict a zero signal can deliver.
func defaultLockHolderAlive(pid int) bool {
	return killErrAlive(zeroKillProbe(pid))
}

// zeroKillProbe performs the POSIX existence probe for LockHolderAlive. It is
// a package variable only as the test seam (same var-injection precedent as
// zeroSignalProbe above and agent's ProcessAlive) so tests can feed a mocked
// EPERM result — the cross-user live holder — through the real entry point.
var zeroKillProbe = func(pid int) error {
	return syscall.Kill(pid, 0)
}

// killErrAlive interprets a Kill(0) result: no error or EPERM both mean the
// lock holder EXISTS — a zero signal cannot deliver any death verdict other
// than ESRCH.
func killErrAlive(err error) bool {
	return err == nil || err == syscall.EPERM
}
