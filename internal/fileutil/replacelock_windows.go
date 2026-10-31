//go:build windows

package fileutil

import (
	"errors"
	"syscall"
)

// errnoSharingViolation is ERROR_SHARING_VIOLATION (32): the Go standard
// syscall package does not define this constant on windows/amd64, so it is
// spelled by value here (stable Win32 error number, documented in
// winerror.h).
const errnoSharingViolation = syscall.Errno(32)

// renameSharingLocked reports whether a rename failed because dest is held
// open without FILE_SHARE_DELETE — which is Go's os.Open default on Windows,
// and also how antivirus and search-indexer scanners hold files. A concurrent
// reader scanning a large session log keeps that lock for seconds, so this
// class of transient failure is worth a stretched retry window (tl1292).
func renameSharingLocked(err error) bool {
	return errors.Is(err, syscall.ERROR_ACCESS_DENIED) ||
		errors.Is(err, errnoSharingViolation)
}
