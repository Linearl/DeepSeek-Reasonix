//go:build !windows

package servepool

import (
	"os"
	"strconv"
)

// processImagePath resolves a pid to its image path via /proc/<pid>/exe —
// the audit-M2 pid-reuse guard. Any failure (dead pid, no permission) returns
// "", which the reap treats as "identity unknown => do not kill".
func processImagePath(pid int) string {
	if pid <= 0 {
		return ""
	}
	path, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	if err != nil {
		return ""
	}
	return path
}
