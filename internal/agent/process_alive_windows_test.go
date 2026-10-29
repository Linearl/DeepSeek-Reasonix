//go:build windows

package agent

import (
	"os"
	"testing"
)

// tl1258: the pid-liveness primitive must not classify a kernel husk (a dead
// process whose object survives with Threads=0/Handles=0 and a STILL_ACTIVE
// exit code) as a live lease holder. The husk cannot be manufactured in a test,
// so the thread probe is the injected seam: the current process provides the
// real STILL_ACTIVE exit-code path, and the stub decides what the thread
// snapshot would report.

func TestProcessAliveHuskWithZeroThreadsIsDead(t *testing.T) {
	restore := swapProcessHasLiveThread(t, func(uint32) bool { return false })
	pid := os.Getpid()
	if defaultProcessAlive(pid) {
		t.Fatal("STILL_ACTIVE pid with zero reported threads must be classified dead (kernel husk)")
	}
	_ = restore
}

func TestProcessAliveLiveThreadKeepsHolderAlive(t *testing.T) {
	swapProcessHasLiveThread(t, func(uint32) bool { return true })
	if !defaultProcessAlive(os.Getpid()) {
		t.Fatal("STILL_ACTIVE pid with a live thread must stay classified alive")
	}
}

func TestProcessAliveRealSelfProcessNotMisjudged(t *testing.T) {
	// The unstubbed default probe runs the real thread snapshot: the current
	// test process owns many threads and must never be reported dead.
	if !defaultProcessAlive(os.Getpid()) {
		t.Fatal("live current process must stay classified alive (real snapshot path)")
	}
	if !defaultProcessHasLiveThread(uint32(os.Getpid())) {
		t.Fatal("thread snapshot must list threads for the current process")
	}
}

func TestProcessAliveRejectsInvalidAndAbsentPids(t *testing.T) {
	if defaultProcessAlive(0) || defaultProcessAlive(-7) {
		t.Fatal("non-positive pids must be classified dead")
	}
	// Absent pid: OpenProcess fails before any thread probing.
	if defaultProcessAlive(0xDEADBEEF) {
		t.Fatal("absent pid must be classified dead")
	}
}

// swapProcessHasLiveThread replaces the thread-probe seam for one test and
// restores it via t.Cleanup — production reads stay on the real probe.
func swapProcessHasLiveThread(t *testing.T, probe func(uint32) bool) func() {
	t.Helper()
	previous := processHasLiveThread
	processHasLiveThread = probe
	t.Cleanup(func() { processHasLiveThread = previous })
	return func() { processHasLiveThread = previous }
}
