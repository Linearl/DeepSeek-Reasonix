package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"reasonix/internal/config"
)

// startGatedTabsFlusher starts the task-653 tabs-save flusher parked behind a
// closed gate: once a snapshot is queued, the flusher blocks immediately
// before saveTabsWrite, so tests can assert the not-yet-written state (and
// the coalescing behaviour) deterministically — no sleeps, no scheduling
// races. The returned openGate func releases the flusher exactly once; a
// test-end cleanup releases it too if the test did not.
func startGatedTabsFlusher(t *testing.T, app *App) (openGate func(), flushCount func() int) {
	t.Helper()
	gate := make(chan struct{})
	var once sync.Once
	var probeMu sync.Mutex
	flushes := 0
	app.tabsSaveQueue.flushGate = func() { <-gate }
	app.tabsSaveQueue.flushProbe = func(version uint64, coalesced uint64) {
		probeMu.Lock()
		flushes++
		probeMu.Unlock()
	}
	app.startTabsSaveFlusher()
	t.Cleanup(func() { once.Do(func() { close(gate) }) })
	return func() { once.Do(func() { close(gate) }) }, func() int {
		probeMu.Lock()
		defer probeMu.Unlock()
		return flushes
	}
}

// newSingleTabApp wires one test tab into a fresh App (tab_profile_test
// conventions).
func newSingleTabApp(t *testing.T) (*App, *WorkspaceTab) {
	t.Helper()
	app := NewApp()
	tab := testTab("a", t.TempDir())
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	return app, tab
}

// TestSaveTabsQueueSyncFallbackBeforeFlusherStarts pins the compatibility
// contract the whole existing suite relies on: before App.startup starts the
// flusher, saveTabsLocked writes desktop-tabs.json synchronously, exactly as
// pre-653 code did. Several production-path tests read the file right after
// a save; the async mode must never leak into them.
func TestSaveTabsQueueSyncFallbackBeforeFlusherStarts(t *testing.T) {
	isolateDesktopUserDirs(t)
	app, tab := newSingleTabApp(t)

	app.mu.Lock()
	app.saveTabsLocked()
	app.mu.Unlock()

	got := loadTabsFile()
	if len(got.Tabs) != 1 || got.Tabs[0].ID != tab.ID {
		t.Fatalf("sync fallback tabs = %+v, want exactly %q", got.Tabs, tab.ID)
	}
}

// TestSaveTabsQueueDefersWriteOffLockUntilDrain is the task-653 core
// assertion: with the flusher running, saveTabsLocked under App.mu leaves no
// file behind — the write moved out of the lock-held caller — and
// flushQueuedTabsSave drains the newest snapshot to disk with the collected
// version.
func TestSaveTabsQueueDefersWriteOffLockUntilDrain(t *testing.T) {
	isolateDesktopUserDirs(t)
	app, tab := newSingleTabApp(t)
	openGate, flushCount := startGatedTabsFlusher(t, app)

	app.mu.Lock()
	app.saveTabsLocked()
	savedVersion := app.tabsSaveVersion
	app.mu.Unlock()

	// The flusher is parked before saveTabsWrite, so the file cannot exist
	// yet — deterministic, independent of scheduling.
	if _, err := os.Stat(filepath.Join(config.ReasonixHomeDir(), tabsFileName)); !os.IsNotExist(err) {
		t.Fatalf("desktop-tabs.json written while flusher gated (err=%v) — write did not leave the lock-held caller", err)
	}
	openGate()
	if !app.flushQueuedTabsSave(2 * time.Second) {
		t.Fatal("queue did not drain after gate opened")
	}
	got := loadTabsFile()
	if len(got.Tabs) != 1 || got.Tabs[0].ID != tab.ID {
		t.Fatalf("drained tabs = %+v, want exactly %q", got.Tabs, tab.ID)
	}
	app.tabsSaveMu.Lock()
	written := app.tabsLastWrittenVersion
	app.tabsSaveMu.Unlock()
	if written != savedVersion {
		t.Fatalf("tabsLastWrittenVersion = %d, want collected version %d", written, savedVersion)
	}
	if n := flushCount(); n != 1 {
		t.Fatalf("flush count = %d, want 1", n)
	}
}

// TestSaveTabsQueueCoalescesBurstIntoSingleFlush pins the coalescing
// behaviour: a burst of saves (three mutations of the same tab in one turn,
// the contention shape 639 measured) queues only the newest snapshot and
// costs exactly one disk flush.
func TestSaveTabsQueueCoalescesBurstIntoSingleFlush(t *testing.T) {
	isolateDesktopUserDirs(t)
	app, tab := newSingleTabApp(t)
	openGate, flushCount := startGatedTabsFlusher(t, app)

	app.mu.Lock()
	tab.model = "burst-1"
	app.saveTabsLocked()
	tab.model = "burst-2"
	app.saveTabsLocked()
	tab.model = "burst-3"
	app.saveTabsLocked()
	v3 := app.tabsSaveVersion
	app.tabsSaveQueue.mu.Lock()
	snapVersion, hasSnap := app.tabsSaveQueue.version, app.tabsSaveQueue.hasSnap
	app.tabsSaveQueue.mu.Unlock()
	app.mu.Unlock()
	if !hasSnap || snapVersion != v3 {
		t.Fatalf("queued snapshot version = %d (hasSnap=%v), want newest %d", snapVersion, hasSnap, v3)
	}

	openGate()
	if !app.flushQueuedTabsSave(2 * time.Second) {
		t.Fatal("queue did not drain after gate opened")
	}
	got := loadTabsFile()
	if len(got.Tabs) != 1 {
		t.Fatalf("drained tabs = %+v, want one entry", got.Tabs)
	}
	if got.Tabs[0].Model != "burst-3" {
		t.Fatalf("drained model = %q, want burst-3 (newest snapshot must win)", got.Tabs[0].Model)
	}
	// Two legal interleavings, both correct: the flusher woke before the
	// burst and handed off burst-1 (parked on the gate) — then the burst
	// coalesces into one more flush (total 2); or it woke after all three
	// saves and the whole burst collapses into one flush of burst-3. What
	// must never happen is a flush per stale snapshot or a stale winner.
	if n := flushCount(); n < 1 || n > 2 {
		t.Fatalf("flush count = %d, want 1..2 — three saves must never cost three disk writes", n)
	}
}

// TestFlushQueuedTabsSaveTimesOutWhileGated pins the bounded-drain contract
// shutdown relies on: a gated flusher makes flushQueuedTabsSave return false
// after the timeout, and a real drain after release.
func TestFlushQueuedTabsSaveTimesOutWhileGated(t *testing.T) {
	isolateDesktopUserDirs(t)
	app, _ := newSingleTabApp(t)
	openGate, _ := startGatedTabsFlusher(t, app)

	app.mu.Lock()
	app.saveTabsLocked()
	app.mu.Unlock()

	if app.flushQueuedTabsSave(50 * time.Millisecond) {
		t.Fatal("drain reported success while the flusher is gated on an unwritten snapshot")
	}
	openGate()
	if !app.flushQueuedTabsSave(2 * time.Second) {
		t.Fatal("queue did not drain after gate opened")
	}
}

// TestSlowTabsSaveFlushLogMsMatchesSlowLogFamily keeps the 653 flush
// threshold in the 639 slow-log family and typed as time.Duration — the
// 639 threshold regression pinned the Duration-vs-untyped-constant trap.
func TestSlowTabsSaveFlushLogMsMatchesSlowLogFamily(t *testing.T) {
	if slowTabsSaveFlushLogMs != 150*time.Millisecond {
		t.Fatalf("slowTabsSaveFlushLogMs = %s, want 150ms", slowTabsSaveFlushLogMs)
	}
	if slowTabsSaveFlushLogMs.Milliseconds() != slowSessionReconcileLogMs {
		t.Fatalf("slowTabsSaveFlushLogMs = %s does not match the 639 slow-log family threshold (%dms)",
			slowTabsSaveFlushLogMs, slowSessionReconcileLogMs)
	}
}
