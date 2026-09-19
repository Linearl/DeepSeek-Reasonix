//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// procCounters is the raw, platform-provided view of this process. `Available`
// says whether the OS actually answered: a platform without these counters must
// not report zeroes as if they were measurements (task 184).
type procCounters struct {
	WorkingSetBytes uint64
	PrivateBytes    uint64
	Handles         uint32
	ReadBytes       uint64
	WriteBytes      uint64
	CPUSeconds      float64
	Available       bool
}

// The psapi/kernel32 entry points the monitor needs are not exported by
// golang.org/x/sys/windows, so they are declared here through the same lazy-DLL
// pattern the desktop's smoke tools already use. Nothing is resolved until a
// sample is actually taken, so a machine without them only loses counters.
var (
	modpsapi    = windows.NewLazySystemDLL("psapi.dll")
	modkernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procGetProcessMemoryInfo  = modpsapi.NewProc("GetProcessMemoryInfo")
	procGetProcessHandleCount = modkernel32.NewProc("GetProcessHandleCount")
	procGetProcessIoCounters  = modkernel32.NewProc("GetProcessIoCounters")
	procGetProcessTimes       = modkernel32.NewProc("GetProcessTimes")
)

// processMemoryCounters mirrors PROCESS_MEMORY_COUNTERS.
type processMemoryCounters struct {
	Cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

// ioCounters mirrors IO_COUNTERS.
type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

// readProcCounters reads this process's memory, handle and IO counters through
// the same Win32 calls Task Manager uses, so a sample can be compared with what
// the user sees in the OS tooling.
func readProcCounters() procCounters {
	var out procCounters
	handle := uintptr(windows.CurrentProcess())

	var memory processMemoryCounters
	memory.Cb = uint32(unsafe.Sizeof(memory))
	if ok, _, _ := procGetProcessMemoryInfo.Call(handle, uintptr(unsafe.Pointer(&memory)), uintptr(memory.Cb)); ok != 0 {
		out.WorkingSetBytes = uint64(memory.WorkingSetSize)
		out.PrivateBytes = uint64(memory.PagefileUsage)
		out.Available = true
	}

	var handles uint32
	if ok, _, _ := procGetProcessHandleCount.Call(handle, uintptr(unsafe.Pointer(&handles))); ok != 0 {
		out.Handles = handles
	}

	var io ioCounters
	if ok, _, _ := procGetProcessIoCounters.Call(handle, uintptr(unsafe.Pointer(&io))); ok != 0 {
		out.ReadBytes = io.ReadTransferCount
		out.WriteBytes = io.WriteTransferCount
	}

	var creation, exit, kernel, user windows.Filetime
	if ok, _, _ := procGetProcessTimes.Call(handle,
		uintptr(unsafe.Pointer(&creation)), uintptr(unsafe.Pointer(&exit)),
		uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user))); ok != 0 {
		out.CPUSeconds = float64(kernel.Nanoseconds()+user.Nanoseconds()) / 1e9
	}
	return out
}
