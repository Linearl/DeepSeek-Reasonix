//go:build windows

package servepool

import (
	"golang.org/x/sys/windows"
)

// processImagePath resolves a pid to its full image path via
// QueryFullProcessImageName — the audit-M2 pid-reuse guard. Any failure
// (dead pid, no permission) returns "", which the reap treats as "identity
// unknown => do not kill".
func processImagePath(pid int) string {
	if pid <= 0 {
		return ""
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	buf := make([]uint16, 32768)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}
