package main

// meminfo_windows.go —— Windows 进程内存读数（WorkingSet / Pagefile/commit）。
// 口径：ONNX Runtime 是 C 层分配，runtime.MemStats 看不到，必须走 OS 级读数。
// WorkingSetSize=物理常驻，PagefileUsage=commit 量。报告以 WorkingSet 为主读数。

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

type ProcMem struct {
	WorkingSetBytes uint64
	PagefileBytes   uint64
}

func (m ProcMem) String() string {
	return fmt.Sprintf("WorkingSet=%.1fMiB Pagefile=%.1fMiB",
		float64(m.WorkingSetBytes)/(1024*1024),
		float64(m.PagefileBytes)/(1024*1024))
}

func GetProcMem() (ProcMem, error) {
	var pmc struct {
		CB                         uint32
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
	pmc.CB = uint32(unsafe.Sizeof(pmc))
	psapi := windows.NewLazySystemDLL("psapi.dll")
	proc := psapi.NewProc("GetProcessMemoryInfo")
	h := windows.CurrentProcess()
	r1, _, lastErr := proc.Call(uintptr(h), uintptr(unsafe.Pointer(&pmc)), uintptr(pmc.CB))
	if r1 == 0 {
		return ProcMem{}, fmt.Errorf("GetProcessMemoryInfo failed: %v", lastErr)
	}
	return ProcMem{
		WorkingSetBytes: uint64(pmc.WorkingSetSize),
		PagefileBytes:   uint64(pmc.PagefileUsage),
	}, nil
}
