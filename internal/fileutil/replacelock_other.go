//go:build !windows

package fileutil

// renameSharingLocked has no work to do on platforms whose rename-over is not
// blocked by open handles: POSIX unlink/rename semantics never fail with a
// sharing denial, so the default short retry envelope applies everywhere.
func renameSharingLocked(err error) bool {
	return false
}
