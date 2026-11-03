//go:build windows

package agent

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// processImageNameFlagWin32 selects the Win32-format image path (drive-letter
// style, "C:\...\Reasonix.exe") from QueryFullProcessImageName. Empirically
// verified on Windows: 0 returns the drive-letter form, PROCESS_NAME_NATIVE
// (1, the only constant x/sys defines) returns the \Device\HarddiskVolumeX
// form that no message should print.
const processImageNameFlagWin32 = 0

// defaultProcessImageName resolves pid's executable through
// QueryFullProcessImageName with query-limited rights — the same access level
// defaultProcessAlive already needs, so a holder that is alive enough for the
// liveness probe is also nameable here.
func defaultProcessImageName(pid int) (string, string) {
	if pid <= 0 {
		return "", ""
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", ""
	}
	defer windows.CloseHandle(handle)
	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(handle, processImageNameFlagWin32, &buf[0], &size); err != nil {
		return "", ""
	}
	path := windows.UTF16ToString(buf[:size])
	if path == "" {
		return "", ""
	}
	return filepath.Base(path), path
}
