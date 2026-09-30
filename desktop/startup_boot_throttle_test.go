package main

import (
	"sync"
	"testing"
	"time"
)

// TestOrderTabsActiveFirst: the task-405 Q2 ordering helper moves the active
// tab to the front and preserves the relative order of the rest (stable), so
// the tab the user is looking at never queues behind background restores.
func TestOrderTabsActiveFirst(t *testing.T) {
	mk := func(id string) *WorkspaceTab { return &WorkspaceTab{ID: id} }
	tabs := []*WorkspaceTab{mk("a"), mk("b"), mk("c"), mk("d")}
	got := orderTabsActiveFirst(tabs, "c")
	want := []string{"c", "a", "b", "d"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("position %d = %q, want %q", i, got[i].ID, id)
		}
	}
	// No active match: the original order is preserved untouched.
	got = orderTabsActiveFirst(tabs, "zzz")
	for i, tab := range tabs {
		if got[i].ID != tab.ID {
			t.Fatalf("no-match order changed at %d: %q vs %q", i, got[i].ID, tab.ID)
		}
	}
}

// TestStartupBootSemaphoreOrderingAndCap: the task-405 Q2 throttled startup
// build path (a) never exceeds startupBootConcurrency builds in flight and
// (b) starts the first enqueued tab before the throttled tail. Uses the
// tabBuildStartHook observation point with real WorkspaceTab entries so the
// same code path the restore loop exercises is what runs here.
func TestStartupBootSemaphoreOrderingAndCap(t *testing.T) {
	a := &App{}
	a.mu.Lock()
	a.activeTabID = "active"
	a.mu.Unlock()

	const total = 5
	var mu sync.Mutex
	inFlight, maxInFlight := 0, 0
	var startOrder []string
	started := make(chan string, total)

	a.tabBuildStartHook = func(tabID string) {
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		startOrder = append(startOrder, tabID)
		mu.Unlock()
		started <- tabID
		// Hold each build at the hook so the semaphore is visibly binding.
		time.Sleep(150 * time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()
	}

	sem := make(chan struct{}, startupBootConcurrency)
	tabs := []*WorkspaceTab{
		{ID: "bg1"}, {ID: "bg2"}, {ID: "active"}, {ID: "bg3"}, {ID: "bg4"},
	}
	for _, tab := range orderTabsActiveFirst(tabs, "active") {
		a.startTabControllerBuildThrottled(tab, sem)
	}

	deadline := time.After(10 * time.Second)
	seen := 0
	for seen < total {
		select {
		case <-started:
			seen++
		case <-deadline:
			t.Fatalf("timeout: only %d/%d builds reached the hook", seen, total)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if maxInFlight > startupBootConcurrency {
		t.Fatalf("max concurrent builds at hook = %d, want <= %d", maxInFlight, startupBootConcurrency)
	}
	if len(startOrder) == 0 || startOrder[0] != "active" {
		t.Fatalf("first build started = %v, want active tab first", startOrder)
	}
	// Starvation check: every enqueued tab eventually reached the hook.
	if len(startOrder) != total {
		t.Fatalf("hook saw %d builds, want %d (background tabs starved?)", len(startOrder), total)
	}
}

// TestBuildSlotNilSemUnthrottled: non-startup paths pass a nil semaphore and
// must behave exactly as before task 405 (acquire/release are no-ops).
func TestBuildSlotNilSemUnthrottled(t *testing.T) {
	var slot buildSlot
	done := make(chan struct{})
	go func() {
		slot.acquire()
		slot.acquire()
		slot.release()
		slot.release()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("nil-semaphore buildSlot blocked — non-startup paths would deadlock")
	}
}
