//go:build windows

package main

import (
	"os"
	"testing"
)

// tl1258: desktopProcessAlive shares the agent-side liveness contract (lease
// reconcile, takeover, crash-fatal all consume it) — a STILL_ACTIVE kernel husk
// with zero threads must be classified dead, a real process must never be
// misjudged. The thread snapshot is the injected seam; the husk itself cannot
// be manufactured in a test.

func TestDesktopProcessAliveHuskWithZeroThreadsIsDead(t *testing.T) {
	previous := desktopProcessHasLiveThread
	desktopProcessHasLiveThread = func(uint32) bool { return false }
	t.Cleanup(func() { desktopProcessHasLiveThread = previous })
	if desktopProcessAlive(os.Getpid()) {
		t.Fatal("STILL_ACTIVE pid with zero reported threads must be classified dead (kernel husk)")
	}
}

func TestDesktopProcessAliveLiveThreadKeepsHolderAlive(t *testing.T) {
	previous := desktopProcessHasLiveThread
	desktopProcessHasLiveThread = func(uint32) bool { return true }
	t.Cleanup(func() { desktopProcessHasLiveThread = previous })
	if !desktopProcessAlive(os.Getpid()) {
		t.Fatal("STILL_ACTIVE pid with a live thread must stay classified alive")
	}
}

func TestDesktopProcessAliveRealSelfProcessNotMisjudged(t *testing.T) {
	// Real snapshot path: the current process owns threads and the probe must
	// keep reporting it alive (no false kill of a genuine holder).
	if !desktopProcessAlive(os.Getpid()) {
		t.Fatal("live current process must stay classified alive (real snapshot path)")
	}
	if !defaultDesktopProcessHasLiveThread(uint32(os.Getpid())) {
		t.Fatal("thread snapshot must list threads for the current process")
	}
}

func TestDesktopProcessAliveRejectsInvalidAndAbsentPids(t *testing.T) {
	if desktopProcessAlive(0) || desktopProcessAlive(-3) {
		t.Fatal("non-positive pids must be classified dead")
	}
	if desktopProcessAlive(0xDEADBEEF) {
		t.Fatal("absent pid must be classified dead")
	}
}
