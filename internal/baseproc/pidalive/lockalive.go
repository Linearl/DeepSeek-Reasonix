package pidalive

// StillActiveExitCode is the Windows STILL_ACTIVE exit code (259): a process
// object answering it through GetExitCodeProcess has not exited yet. It is
// defined here once as the single point for the pid-probe family — the
// lock-scenario probes (homemigrate / mcplaunch / cli web instance) used to
// re-declare it per package (audit 20261116 pid探测重复族 §F7).
const StillActiveExitCode = 259

// lockHolderAlive is the per-platform probe behind LockHolderAlive. It is a
// package variable only as the test seam (same var-injection precedent as
// agent's ProcessAlive and servepool's processAlive).
var lockHolderAlive = defaultLockHolderAlive

// LockHolderAlive reports whether pid names a live lock holder, using the
// lock-scenario conservative verdicts: on Windows an ACCESS_DENIED
// OpenProcess still proves existence (a less-probeable live writer must not
// have its lock stolen by a waiter), and on POSIX a zero signal answering
// EPERM means a live cross-user holder. It backs the three lock-scenario
// probes that used to be line-identical copies in homemigrate (pidAlive),
// mcplaunch (launchLockProcessAlive) and cli (webInstanceProcessAlive) —
// audit 20261116 pid探测重复族 §F1 (tl1623) merged them here. Unsupported
// platforms fail closed (a syntactically valid pid counts as alive) so a
// stale lock needs manual cleanup instead of being stolen.
func LockHolderAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return lockHolderAlive(pid)
}
