//go:build windows

package main

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// tl1456 资源管理器的 Windows 数据源：系统内存（GlobalMemoryStatusEx，任务
// 管理器同源）+ 本进程直接子进程枚举（Toolhelp32Snapshot 按父 PID 过滤——
// 只认我们自己派生的进程，不做全系统枚举）。
//
// 权限姿态：子进程内存读数走 OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)
// + GetProcessMemoryInfo，与 perf_monitor_windows.go 读自身用的同一组 Win32
// 调用；打不开（权限/已退出/杀软拦截）的子进程保留行但 Measured=false——
// 行在、读数诚实，绝不静默丢行。

// MEMORYSTATUSEX mirrors the Win32 structure for GlobalMemoryStatusEx.
type memoryStatusEx struct {
	DWLength                uint32
	DWMemoryLoad            uint32
	ULLTotalPhys            uint64
	ULLAvailPhys            uint64
	ULLTotalPageFile        uint64
	ULLAvailPageFile        uint64
	ULLTotalVirtual         uint64
	ULLAvailVirtual         uint64
	ULLAvailExtendedVirtual uint64
}

var procGlobalMemoryStatusEx = modkernel32.NewProc("GlobalMemoryStatusEx")

// readSystemMemory reports machine-level physical memory (total + available)
// through the same kernel32 call Task Manager's performance tab uses.
func readSystemMemory() ResourceSystemMemory {
	var status memoryStatusEx
	status.DWLength = uint32(unsafe.Sizeof(status))
	if ok, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status))); ok == 0 {
		return ResourceSystemMemory{}
	}
	return ResourceSystemMemory{
		TotalBytes:     status.ULLTotalPhys,
		AvailableBytes: status.ULLAvailPhys,
		Available:      true,
	}
}

// resourceChildSnapshot is the injectable seam the tests replace: it returns
// the (name, pid, parentPid) triple list the child filter runs on. Production
// takes one Toolhelp process snapshot.
var resourceChildSnapshot = defaultResourceChildSnapshot

type resourceChildEntry struct {
	name      string
	pid       int
	parentPID int
}

func defaultResourceChildSnapshot() []resourceChildEntry {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil
	}
	out := make([]resourceChildEntry, 0, 16)
	for {
		name := windows.UTF16ToString(entry.ExeFile[:])
		out = append(out, resourceChildEntry{name: name, pid: int(entry.ProcessID), parentPID: int(entry.ParentProcessID)})
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	return out
}

// resourceChildProbe is the injectable seam for per-pid (memory, create
// time) reads. Production opens each pid with limited query rights.
var resourceChildProbe = defaultResourceChildProbe

type resourceChildProbeResult struct {
	memoryBytes   uint64
	createTimeUTC int64 // unix seconds; 0 = unknown
	measured      bool
}

func defaultResourceChildProbe(pid int) resourceChildProbeResult {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return resourceChildProbeResult{}
	}
	defer windows.CloseHandle(handle)

	var out resourceChildProbeResult
	var memory processMemoryCounters
	memory.Cb = uint32(unsafe.Sizeof(memory))
	if ok, _, _ := procGetProcessMemoryInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&memory)), uintptr(memory.Cb)); ok != 0 {
		out.memoryBytes = uint64(memory.WorkingSetSize)
		out.measured = true
	}
	var creation, exit, kernel, user windows.Filetime
	if ok, _, _ := procGetProcessTimes.Call(uintptr(handle),
		uintptr(unsafe.Pointer(&creation)), uintptr(unsafe.Pointer(&exit)),
		uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user))); ok != 0 && creation.Nanoseconds() > 0 {
		out.createTimeUTC = creation.Nanoseconds() / 1e9
	}
	return out
}

// listChildProcesses returns the directly spawned child rows (parent = this
// process), heaviest first (sortResourceChildren applies the order + cap).
// A child that vanished between the snapshot and the detail read keeps its
// row with Measured=false: the next tick converges, and a flaky read must
// never look like the process disappeared.
func listChildProcesses() []ResourceProcessRow {
	entries := resourceChildSnapshot()
	if len(entries) == 0 {
		return nil
	}
	self := int(windows.GetCurrentProcessId())
	now := time.Now().Unix()
	children := make([]ResourceProcessRow, 0, 8)
	for _, entry := range entries {
		if entry.parentPID != self || entry.pid <= 0 || entry.pid == self {
			continue
		}
		details := resourceChildProbe(entry.pid)
		row := ResourceProcessRow{
			Name:     entry.name,
			PID:      entry.pid,
			Kind:     "child",
			Measured: details.measured,
		}
		if details.measured {
			row.MemoryBytes = details.memoryBytes
		}
		if details.createTimeUTC > 0 && now > details.createTimeUTC {
			row.UptimeSeconds = float64(now - details.createTimeUTC)
		}
		children = append(children, row)
	}
	return children
}
