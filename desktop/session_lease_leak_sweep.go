package main

// Task 485 (P1): self-held orphan session lease sweeper.
//
// Background: every automatic lease cleanup path (TryReclaim,
// ClearStaleSessionLeaseInfo, SessionLeaseHeldByOtherRuntime) takes the OS
// lease lock as its first step, so all three are structurally blind to a lock
// held by a leaked handle INSIDE this process (LockFileEx is per-handle; a
// second handle in the same process gets ERROR_LOCK_VIOLATION). The 2026-10-05
// incident: releaseDetachedSession (task 308-O4) released a detached runtime
// but not its tab's lease — the session stayed "refusing session access"
// forever, 568 WARN lines in one morning.
//
// P0 fixes that release chain. P1 is the backstop for ANY future leak path:
// the desktop layer owns the one fact the lock cannot express — whether any
// in-process runtime still owns the session. A lease that (a) is registered
// active for this process, (b) names THIS process as holder, (c) has no live
// tab/detached runtime behind it, and (d) has sat in that state past a
// two-tick age threshold (absorbing the acquire middle window between
// TryAcquire and tab registration) is provably a leak: release it through the
// tracked lease object, so the real OS handle unlocks and the blessed Release
// path cleans registry, info file and lock file in the usual order.
//
// Deliberately NOT a TTL: only leases with no live in-process owner qualify.
// A long turn behind a visible tab is never a candidate, so long-turn
// double-write risk (the reason TTL was rejected) does not exist here.
//
// The tracker only records leases a desktop TAB bound through the standard
// helpers (every mutation funnels through storeSessionLeaseRuntimeKey).
// Serve-side or transient holders (cleanup, catalog persistence) never enter
// the tracker, so the sweeper cannot touch them.

import (
	"log/slog"
	"os"
	"sync"
	"time"

	"reasonix/internal/agent"
)

// sessionLeaseLeakSweepInterval is the sweeper cadence; reusing the detached
// idle release tick keeps the two backstops phase-aligned.
const sessionLeaseLeakSweepInterval = detachedIdleReleaseTick

// sessionLeaseLeakMinAge is how long a lease must sit orphaned before the
// sweeper acts: two release ticks, so an acquire still inside its
// registration middle window (acquired, not yet bound to a tab) can never be
// mistaken for a leak. Var only so tests can shrink the window; production
// keeps the two-tick buffer.
var sessionLeaseLeakMinAge = 2 * detachedIdleReleaseTick

// trackedSessionLease is one tab-bound lease. Holding the lease object is the
// whole point: without it the leaked OS handle is unreachable and the lock
// could only be broken by deleting the file out from under it.
type trackedSessionLease struct {
	tab   *WorkspaceTab
	lease *agent.SessionLease
}

var (
	sessionLeaseTrackerMu sync.Mutex
	sessionLeaseTracker   = map[string]trackedSessionLease{}
)

// syncSessionLeaseTracker mirrors every tab lease mutation into the tracker.
// Callers are the storeSessionLeaseRuntimeKey sites, all of which hold
// t.sessionLeaseMu; reading t.sessionLease here is safe under that same lock.
// A tab owns at most one tracked entry: rebinding to a new key retires the
// old entry, clearing the key (release/take) drops the tab's entries.
func syncSessionLeaseTracker(t *WorkspaceTab, key string) {
	sessionLeaseTrackerMu.Lock()
	defer sessionLeaseTrackerMu.Unlock()
	for k, entry := range sessionLeaseTracker {
		if entry.tab == t && (key == "" || k != key) {
			delete(sessionLeaseTracker, k)
		}
	}
	if key == "" || t == nil || t.sessionLease == nil {
		return
	}
	sessionLeaseTracker[key] = trackedSessionLease{tab: t, lease: t.sessionLease}
}

func trackedSessionLeaseEntry(key string) (trackedSessionLease, bool) {
	sessionLeaseTrackerMu.Lock()
	defer sessionLeaseTrackerMu.Unlock()
	entry, ok := sessionLeaseTracker[key]
	return entry, ok
}

func removeSessionLeaseTrackerEntry(key string, entry trackedSessionLease) {
	sessionLeaseTrackerMu.Lock()
	defer sessionLeaseTrackerMu.Unlock()
	if current, ok := sessionLeaseTracker[key]; ok && current == entry {
		delete(sessionLeaseTracker, key)
	}
}

// sessionLeaseLeakDecision is the pure half of the sweeper (same family as
// leaseReclaimDecision): given the published holder info, is this old enough
// and free of live-turn markers to be treated as a leak? Liveness and holder
// identity checks stay with the caller because they need desktop state.
func sessionLeaseLeakDecision(info *agent.SessionLeaseInfo, now time.Time, minAge time.Duration) bool {
	if info == nil {
		return false
	}
	// A registered turn means a writer claims to be working; the sweeper never
	// second-guesses that, however stale the registration looks.
	if info.Turn != nil {
		return false
	}
	return now.Sub(info.AcquiredAt) >= minAge
}

// sessionLeaseHasLiveInProcessOwner reports whether any visible tab or
// detached runtime is bound to key — the desktop-state check the lock cannot
// make. Mirrors the owner scan of canReclaimCurrentProcessSessionLease so
// "live" means the same thing in both decisions.
func (a *App) sessionLeaseHasLiveInProcessOwner(key string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, candidate := range a.runtimeTabsLocked() {
		if candidate == nil {
			continue
		}
		if candidate.sessionLeaseRuntimeKey() == key {
			return true
		}
		if candidate.Ctrl != nil && sessionRuntimeKey(candidate.currentSessionPath()) == key {
			return true
		}
	}
	return a.detachedSessions[key] != nil
}

// sweepOrphanedSessionLeases is one sweeper pass: release every tracked lease
// that is provably a self-held orphan. Each condition is checked in
// cheapest-first order and the whole pass is read-only for anything that does
// not match all of them.
func (a *App) sweepOrphanedSessionLeases(now time.Time) {
	for _, key := range agent.SessionLeaseActiveOwnerKeys() {
		entry, ok := trackedSessionLeaseEntry(key)
		if !ok {
			continue
		}
		if entry.lease.Released() {
			// Properly retired elsewhere; drop the stale tracker entry.
			removeSessionLeaseTrackerEntry(key, entry)
			continue
		}
		if a.sessionLeaseHasLiveInProcessOwner(key) {
			continue
		}
		info, err := agent.LoadSessionLeaseInfo(key)
		if err != nil || info == nil {
			continue
		}
		if classifyLeaseHolder(info, os.Getpid(), agent.SessionWriterID(), leaseReconcileProcessAlive) != leaseHolderSelf {
			continue
		}
		if !sessionLeaseLeakDecision(info, now, sessionLeaseLeakMinAge) {
			continue
		}
		// The lock guarantees nobody re-acquired under the orphan: while the
		// orphan's handle holds the OS lock, TryAcquire/Reclaim cannot succeed,
		// so the active registration still names this lease's generation.
		if !agent.SessionLeaseHeldByCurrentRuntime(key) {
			continue
		}
		removeSessionLeaseTrackerEntry(key, entry)
		idle := now.Sub(info.AcquiredAt)
		slog.Warn("desktop: orphan in-process session lease released",
			"path", key,
			"acquired_at", info.AcquiredAt.UTC().Format(time.RFC3339Nano),
			"idleMinutes", int(idle.Minutes()))
		entry.lease.Release()
	}
}

// startSessionLeaseLeakSweeper boots the backstop loop. No gate: it only ever
// fires on a state that is a bug by definition (a lease nothing in this
// process owns anymore), so an untouched install pays one map check per
// active key per 5 minutes and changes nothing else.
func (a *App) startSessionLeaseLeakSweeper() {
	a.goSafe("sessionLeaseLeakSweeper", func() {
		ticker := time.NewTicker(sessionLeaseLeakSweepInterval)
		defer ticker.Stop()
		for now := range ticker.C {
			a.sweepOrphanedSessionLeases(now)
		}
	})
}
