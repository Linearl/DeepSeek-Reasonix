package main

import (
	"log/slog"
	"sync"
	"time"
)

// slowTabsSaveFlushLogMs is the task-653 flush slow-log threshold (task-639
// slow-log family: slowTabSwitchLogMs / slowSessionReconcileLogMs). A flush
// slower than this is exactly the disk stall the queue exists to absorb, so
// it is the one desktop.log must name. Deliberately typed time.Duration —
// the 639 threshold regression pinned the Duration-vs-untyped-constant trap.
const slowTabsSaveFlushLogMs = 150 * time.Millisecond

// tabsSaveQueue defers the desktop-tabs.json write out of the App.mu critical
// section (task 653).
//
// Before 653, saveTabsLocked — called under App.mu at ~50 sites (SetModel/
// SetEffort/SetGoal per-tab setters, open/close/switch tab, controller build
// publication, session recovery/takeover, turn admission) — performed the
// disk write inline: MkdirAll + remote-tab registry read + marshal + tmp-file
// replace, all while holding the app-wide tab lock. Any disk stall (a
// concurrent controller build's IO pressure, antivirus scans) held App.mu for
// the write's full duration and stalled every other tab's state reads behind
// it — the root cause 639 fixed at one call site by hand. Draining the
// remaining sites one by one would have meant ~50 hand splits of the same
// shape, so 653 fixes them all by mechanism instead: under the lock we only
// collect the snapshot (saveTabsCollectLocked) and enqueue it; one flusher
// goroutine coalesces queued snapshots and writes the newest one through the
// pre-existing saveTabsWrite, whose tabsSaveMu serialization +
// tabsLastWrittenVersion guard already make out-of-lock writes safe and stale
// writes harmless (same idiom remote_single_surface.go and 639 use).
//
// Until startTabsSaveFlusher runs, enqueue keeps the exact pre-653
// synchronous write. That is what every existing test observes (several read
// desktop-tabs.json right after a production-path save), and it covers the
// pre-startup boot window; the flusher is started once from the desktop boot
// path (App.startup) and shutdown drains whatever is still queued
// (flushQueuedTabsSave, bounded like the stats.Flush precedent).
//
// Lock order: a.mu → (enqueue) → tabsSaveQueue.mu; flusher:
// tabsSaveQueue.mu released → tabsSaveMu → remoteTabMu — never touches a.mu,
// so a slow flush can no longer hold the tab lock.
type tabsSaveQueue struct {
	mu   sync.Mutex
	kick chan struct{} // buffered 1; created by startTabsSaveFlusher

	running bool
	// latest queued snapshot (guarded by mu); enqueues overwrite, so a burst
	// of saves coalesces into one disk write.
	hasSnap  bool
	dir      string
	entries  []desktopTabEntry
	activeID string
	version  uint64
	// inFlight is true while the flusher is inside saveTabsWrite for a
	// handed-off snapshot; flushQueuedTabsSave waits for quiescence
	// (!hasSnap && !inFlight).
	inFlight   bool
	sinceFlush uint64 // enqueues coalesced into the current/next flush
	drained    chan struct{} // created per waiter, closed at quiescence

	// Test-only hooks (catalogReconcileHook convention: set before the
	// flusher starts, nil in production). flushGate parks the flusher right
	// before saveTabsWrite so tests can assert the not-yet-written state
	// deterministically; flushProbe observes each completed flush.
	flushGate  func()
	flushProbe func(version uint64, coalesced uint64)
}

// startTabsSaveFlusher switches the queue to deferred writes and starts the
// single flusher goroutine. Idempotent; called once from App.startup on the
// process that owns local tabs (the remote web-window child returns earlier
// and never starts one).
func (a *App) startTabsSaveFlusher() {
	q := &a.tabsSaveQueue
	q.mu.Lock()
	if q.running {
		q.mu.Unlock()
		return
	}
	q.kick = make(chan struct{}, 1)
	q.running = true
	q.mu.Unlock()
	go q.flusherLoop(a)
}

// enqueue stores the newest snapshot and wakes the flusher. Called under
// App.mu from saveTabsLocked. With no flusher running it writes synchronously
// — the pre-653 behavior every existing caller and test observes.
func (q *tabsSaveQueue) enqueue(a *App, dir string, entries []desktopTabEntry, activeID string, version uint64) {
	q.mu.Lock()
	if !q.running {
		q.mu.Unlock()
		a.saveTabsWrite(dir, entries, activeID, version)
		return
	}
	q.dir, q.entries, q.activeID, q.version = dir, entries, activeID, version
	q.hasSnap = true
	q.sinceFlush++
	if q.kick != nil {
		select {
		case q.kick <- struct{}{}:
		default:
		}
	}
	q.mu.Unlock()
}

// flusherLoop coalesces queued snapshots and writes the newest one. It never
// acquires App.mu — a slow disk write cannot hold the tab lock.
func (q *tabsSaveQueue) flusherLoop(a *App) {
	for range q.kick {
		for {
			q.mu.Lock()
			if !q.hasSnap {
				if !q.inFlight {
					q.closeDrainedLocked()
				}
				q.mu.Unlock()
				break
			}
			dir, entries, activeID, version := q.dir, q.entries, q.activeID, q.version
			coalesced := q.sinceFlush
			q.hasSnap = false
			q.sinceFlush = 0
			q.inFlight = true
			q.mu.Unlock()

			if q.flushGate != nil {
				q.flushGate()
			}
			start := time.Now()
			a.saveTabsWrite(dir, entries, activeID, version)
			if took := time.Since(start); took >= slowTabsSaveFlushLogMs {
				slog.Info("desktop: tabs save flush slow",
					"ms", took.Milliseconds(),
					"version", version,
					"coalesced", coalesced,
					"entries", len(entries))
			}
			if q.flushProbe != nil {
				q.flushProbe(version, coalesced)
			}

			q.mu.Lock()
			q.inFlight = false
			q.mu.Unlock()
		}
	}
}

// flushQueuedTabsSave waits up to timeout for everything enqueued so far to
// reach disk. Best-effort and bounded (the shutdownBody stats.Flush
// precedent) — the shutdown watchdog bounds the whole teardown either way.
// Returns false if the queue did not drain in time.
func (a *App) flushQueuedTabsSave(timeout time.Duration) bool {
	q := &a.tabsSaveQueue
	q.mu.Lock()
	if !q.running || (!q.hasSnap && !q.inFlight) {
		q.mu.Unlock()
		return true
	}
	if q.drained == nil {
		q.drained = make(chan struct{})
	}
	wait := q.drained
	q.mu.Unlock()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-wait:
		return true
	case <-timer.C:
		return false
	}
}

func (q *tabsSaveQueue) closeDrainedLocked() {
	if q.drained != nil {
		close(q.drained)
		q.drained = nil
	}
}
