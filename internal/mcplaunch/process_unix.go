//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package mcplaunch

func launchLockContention(error) bool { return false }
