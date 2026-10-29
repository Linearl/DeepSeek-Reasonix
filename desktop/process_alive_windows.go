//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

const desktopStillActiveExitCode = 259

// desktopProcessHasLiveThread mirrors agent.processHasLiveThread: at least one
// thread for pid in the system thread snapshot. tl1258: a STILL_ACTIVE process
// object with zero threads is a kernel husk (crashed process whose object was
// kept alive by leaked references), not a holder — the exit-code probe alone
// misclassifies it as live and keeps leases busy forever. A failed snapshot is
// inconclusive and conservatively reports "has threads" so an outage never
// turns a real holder into a wrongly reaped zombie.
var desktopProcessHasLiveThread = defaultDesktopProcessHasLiveThread

func defaultDesktopProcessHasLiveThread(pid uint32) bool {
	if pid == 0 {
		return false
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return true
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		// Empty snapshot: no threads exist anywhere, so pid owns none.
		return false
	}
	for {
		if entry.OwnerProcessID == pid {
			return true
		}
		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			return false
		}
	}
}

func desktopProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var code uint32
	if windows.GetExitCodeProcess(handle, &code) != nil || code != desktopStillActiveExitCode {
		return false
	}
	return desktopProcessHasLiveThread(uint32(pid))
}
