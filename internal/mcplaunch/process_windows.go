//go:build windows

package mcplaunch

import (
	"errors"

	"golang.org/x/sys/windows"
)

// launchLockContention reports transient Windows name/handle races while one
// owner removes the exclusive-create lock and another tries to create it.
// OpenFile can surface those races as access or sharing violations instead of
// os.ErrExist, so callers must retry them within the normal lock deadline.
func launchLockContention(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
