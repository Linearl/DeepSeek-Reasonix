//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package homemigrate

// Unsupported platforms fail closed: a syntactically valid PID counts as
// alive, so a stale lock needs manual cleanup instead of being stolen.
func pidAlive(pid int) bool { return pid > 0 }
