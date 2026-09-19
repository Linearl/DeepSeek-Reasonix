//go:build !windows

package main

// readProcCounters is the portable fallback: the sampler still records Go
// runtime memory (heap/sys/goroutines in the sample line itself), it just has no
// OS-side counters to add. Available stays false so a reader can tell "this
// platform does not report it" from "the counter really is zero" (task 184).
func readProcCounters() procCounters {
	return procCounters{}
}
