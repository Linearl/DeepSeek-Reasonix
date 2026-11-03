//go:build !windows

package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// defaultProcessImageName resolves pid's executable through the /proc symlink
// where available. Platforms without /proc return empty values; callers
// degrade to the pid-only wording.
func defaultProcessImageName(pid int) (string, string) {
	if pid <= 0 {
		return "", ""
	}
	path, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	if err != nil {
		return "", ""
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return "", ""
	}
	return filepath.Base(path), path
}
