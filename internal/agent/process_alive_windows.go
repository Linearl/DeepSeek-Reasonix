//go:build windows

package agent

import "golang.org/x/sys/windows"

const processStillActiveExitCode = 259 // STILL_ACTIVE

// defaultProcessAlive mirrors desktop's desktopProcessAlive: open the process
// with query-limited rights and read its exit code. STILL_ACTIVE means the
// process object is alive; a dead or unknown pid fails OpenProcess.
func defaultProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var code uint32
	return windows.GetExitCodeProcess(handle, &code) == nil && code == processStillActiveExitCode
}
