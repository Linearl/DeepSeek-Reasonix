//go:build windows

package agent

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

const processStillActiveExitCode = 259 // STILL_ACTIVE

// processHasLiveThread reports whether the system thread snapshot lists at
// least one thread for pid. tl1258: a process object can outlive its own death
// as a kernel husk (Threads=0/Handles=0/Mem=0) that OpenProcess still opens
// with a STILL_ACTIVE exit code — the exit-code probe alone then misclassifies
// the husk as a live lease holder (the pid-reuse/husk false-busy family). A
// live process always owns at least one thread, so an empty thread list for
// the pid is a definitive death verdict. A FAILED snapshot is inconclusive and
// must not flip the verdict — the conservative pre-tl1258 answer (alive) keeps
// a probe outage from turning a real holder into a wrongly reaped zombie.
var processHasLiveThread = defaultProcessHasLiveThread

func defaultProcessHasLiveThread(pid uint32) bool {
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

// defaultProcessAlive mirrors desktop's desktopProcessAlive: open the process
// with query-limited rights and read its exit code. STILL_ACTIVE means the
// process object is alive; a dead or unknown pid fails OpenProcess. A
// STILL_ACTIVE object with zero threads is a kernel husk, not a holder
// (tl1258), so the verdict additionally requires a live thread.
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
	if windows.GetExitCodeProcess(handle, &code) != nil || code != processStillActiveExitCode {
		return false
	}
	return processHasLiveThread(uint32(pid))
}
