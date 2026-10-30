//go:build !windows

package main

// reasonixRunning is a no-op off Windows: the per-session lease check inside
// the migration API is the only guard there, so callers must still be told to
// stop the app before running apply.
func reasonixRunning() (string, bool) { return "", false }
