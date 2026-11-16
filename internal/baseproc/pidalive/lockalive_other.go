//go:build !windows && !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package pidalive

// defaultLockHolderAlive fails closed for syntactically valid owners (pid <= 0
// was already rejected by LockHolderAlive): the OS offers no portable
// process-liveness probe here, so a stale lock may require manual cleanup but
// a waiter must never steal a live writer's lock.
func defaultLockHolderAlive(pid int) bool {
	return true
}
