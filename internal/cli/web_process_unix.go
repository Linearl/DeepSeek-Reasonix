//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package cli

import (
	"errors"
	"syscall"
)

func webAddressInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}
