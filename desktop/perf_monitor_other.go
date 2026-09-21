//go:build !windows

package main

// procCounters mirrors the windows-only shape so perf_monitor.go compiles on
// every platform. The portable fallback never reports OS-side measurements:
// Available stays false so a reader can tell "this platform does not report
// it" from "the counter really is zero" (task 184).
type procCounters struct {
	WorkingSetBytes uint64
	PrivateBytes    uint64
	Handles         uint32
	ReadBytes       uint64
	WriteBytes      uint64
	CPUSeconds      float64
	KernelSeconds   float64
	UserSeconds     float64
	Available       bool
}

// readProcCounters is the portable fallback: the sampler still records Go
// runtime memory (heap/sys/goroutines in the sample line itself), it just has no
// OS-side counters to add.
func readProcCounters() procCounters {
	return procCounters{}
}
