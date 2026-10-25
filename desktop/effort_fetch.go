package main

import (
	"fmt"
	"log/slog"
	"time"
)

// effortReadTimeout caps one synchronous effort re-fetch: the read behind the
// EffortForTab binding that the frontend awaits in the switch-tab ancillary
// batch (useController.ts "ancillary effort"). Task 421 field telemetry (n=7,
// 2026-09-30): the fetch was normally 431-1350ms (p99 ~1.4s) but bimodal, with
// 7.6s/14.8s outliers, because every read walked a full config.LoadForRoot
// disk load (including on-disk migration) plus a session-binding reconcile.
// Task 609 moved the read onto the per-root config snapshot
// (config_snapshot.go), so warm reads no longer pay a full load; the cap
// stays as the bound on whatever disk work remains (first load per root,
// reload after a config change, the session reconcile). On Windows an
// antivirus scan can still stall those reads for seconds; without a cap the
// whole tab switch waits on a sidebar value.
//
// 2s rationale: ~1.4x the observed p99, so a healthy read is virtually never
// cut off, while a bad sample is bounded at the cap instead of running 7-15s.
// Deliberately not a user-facing knob - no existing effort config surface
// carries timings - but a package-level constant the tests can bypass through
// effortReadLimitOverride on App. Task 148 (per-request effort) removed the
// switch-path rebuild and task 609 removed the read-path full config load;
// this fetch-path cap remains the last-resort bound and does not touch the
// boot chain.
const effortReadTimeout = 2 * time.Second

// effortStaleServeMaxAge bounds how long a cache entry may serve the
// task-1161 fast path (freshCachedEffortForTab): an entry whose inputs still
// match and that is younger than this serves the caller immediately while a
// background refresh recomputes the real value. 15s rationale: the window
// only needs to cover one disk-congestion episode (the 2026-10-25 timeouts
// ran minutes, but those minutes were many separate reads each re-stalling
// on the same walk — with the task-1161 stamp fix a single walk completes
// once and the cache settles), while capping how stale a value config-file
// edits can show. Deliberately conservative: beyond the window the read
// falls back to the synchronous bounded path (task 421 semantics unchanged).
const effortStaleServeMaxAge = 15 * time.Second

// effortBreakdownLogMs is the threshold under which a task-639 effort read
// breakdown is not worth a log line, matching slowTabSwitchLogMs: tab
// switches happen constantly and the log should carry only the reads the user
// experiences as slow. After the task-639 memo a warm read is two os.Stat
// freshness checks plus in-memory resolution, so a line here means something
// regressed (snapshot reload, registry change, or disk stall).
const effortBreakdownLogMs = 150

// effortReadBreakdown is the segment split for one completed effort read,
// the task-639 answer to "what is left of the read" and the task-889 answer
// to "which step exactly". Top level: reconcile vs snapshot vs model
// resolve, as before. Task 889 adds the sub-splits so a congestion-window
// sample localizes without cross-reading the phase logs: memo/walk split
// reconcile into "memo key computation (settled path, disk stats)" vs
// "binding resolve + apply", and wait/stat/load split snapshot into "queued
// behind a sibling's load" vs "freshness stats" vs "LoadForRoot". afterTimeout
// pairs the line back to the 2s-cap warn for the same read (set when the cap
// fired before this completion), so grep finds both halves of one incident.
type effortReadBreakdown struct {
	tabID            string
	total            time.Duration
	reconcile        time.Duration
	memo             time.Duration // part of reconcile: memo key compute + lookup
	walk             time.Duration // part of reconcile: binding resolve + apply after a memo miss
	memoHit          bool          // the memo served this read (walk = 0 by construction)
	snapshot         time.Duration
	snapshotWait     time.Duration // part of snapshot: per-root mutex wait
	snapshotStat     time.Duration // part of snapshot: freshness fingerprints
	snapshotLoad     time.Duration // part of snapshot: configLoadForRoot (0 when warm)
	snapshotReloaded bool
	resolve          time.Duration
	afterTimeout     bool
}

// logEffortReadBreakdown emits the segment split for one effort read (see
// currentProviderEntryForTab). The stage tag matches the frontend's
// switch-tab ancillary batch name so a "switch-tab:ancillary effort" timing
// line pairs with the Go-side breakdown of the same read. Threshold semantics
// unchanged (task 639): under effortBreakdownLogMs the line stays silent.
func logEffortReadBreakdown(b effortReadBreakdown) {
	if b.total.Milliseconds() < effortBreakdownLogMs {
		return
	}
	slog.Info("desktop: effort read breakdown",
		"tab", b.tabID,
		"stage", "switch-tab:ancillary effort",
		"total_ms", b.total.Milliseconds(),
		"reconcile_ms", b.reconcile.Milliseconds(),
		"memo_ms", b.memo.Milliseconds(),
		"reconcile_walk_ms", b.walk.Milliseconds(),
		"memo_hit", b.memoHit,
		"snapshot_ms", b.snapshot.Milliseconds(),
		"snapshot_wait_ms", b.snapshotWait.Milliseconds(),
		"snapshot_stat_ms", b.snapshotStat.Milliseconds(),
		"snapshot_load_ms", b.snapshotLoad.Milliseconds(),
		"resolve_ms", b.resolve.Milliseconds(),
		"snapshot_reloaded", b.snapshotReloaded,
		"after_timeout", b.afterTimeout)
}

// effortServeFingerprint is the tab-state part of one effort read's inputs:
// the entry resolution in currentProviderEntryForTab reads the tab's model
// ref and effort override (and a rebuild bumps buildGeneration), so a cache
// entry may only fast-serve while this fingerprint still matches the tab.
// The config side of the inputs is covered by the age window
// (effortStaleServeMaxAge) on top of the task-609 snapshot's own file-stamp
// invalidation.
type effortServeFingerprint struct {
	modelRef  string
	effortOvr string // "" = no override (tab.effort nil)
	buildGen  uint64
}

// effortCacheEntry is the last completed effort read for one tab, kept so a
// timed-out re-fetch can serve the previous value instead of blanking the
// control, and (task 1161) so a fresh-enough entry can serve immediately.
// Value semantics only: the next successful read overwrites it, and an
// abandoned (timed-out) read still lands here when it eventually finishes,
// which keeps the fallback as fresh as the source allows.
type effortCacheEntry struct {
	info EffortInfo
	at   time.Time
	fp   effortServeFingerprint
}

// EffortForTab returns the effort control state for a tab. Task 1161 fast
// path: a cache entry whose input fingerprint (tab model/effort/build
// generation) still matches and that is younger than effortStaleServeMaxAge
// serves immediately — the switch never waits on disk — while a single-flight
// background refresh recomputes the real value for the next read. Without a
// servable entry, the direct read runs on its own goroutine bounded by
// effortReadTimeout (task 421); if it exceeds the cap, the last cached value
// is served (or a defined default on the first read, before any cache exists)
// and a warn log plus a tab notice record the fallback, so a degraded sample
// is diagnosable instead of silently slow.
func (a *App) EffortForTab(tabID string) EffortInfo {
	if cached, ok := a.freshCachedEffortForTab(tabID); ok {
		// Not logged: this is the healthy path (tab switches are constant,
		// see effortBreakdownLogMs) — the refresh keeps the cache evolving
		// and a genuinely slow recompute still shows up in its own read.
		a.spawnEffortRefresh(tabID)
		return cached
	}
	limit := a.effortReadLimit()
	// Task 889: register this attempt's in-flight phase trail; the timeout
	// branch renders it and flags it for the breakdown's after_timeout
	// pairing, and completion drops it (generation-checked).
	phaseGen := a.beginEffortReadPhase(tabID)
	// Buffered channel: once the timeout branch has returned, the late send
	// lands in the buffer and the goroutine exits - an abandoned read never
	// leaks on the send, and its storeEffortCache still lands.
	done := make(chan EffortInfo, 1)
	go func() {
		defer a.finishEffortReadPhase(tabID, phaseGen)
		fp := a.effortServeFingerprintOf(tabID)
		info := a.effortReadCompute(tabID)
		a.storeEffortCache(tabID, info, fp)
		done <- info
	}()
	select {
	case info := <-done:
		return info
	case <-time.After(limit):
		// Task 889: snapshot the trail before flagging; "" means a newer
		// attempt replaced the record — no attribution beats the wrong one.
		phases := a.effortPhaseTrailString(tabID, phaseGen)
		a.markEffortReadTimedOut(tabID, phaseGen)
		if cached, at, ok := a.cachedEffortForTab(tabID); ok {
			// A fallback hit is not an error for the caller - the switch must
			// go through - but it must leave a trace, or the bad samples that
			// motivated task 421 would stay invisible forever.
			attrs := []any{
				"tab", tabID,
				"limit", limit,
				"cache_age", time.Since(at).Round(time.Millisecond),
			}
			if phases != "" {
				attrs = append(attrs, "phases", phases)
			}
			slog.Warn("desktop: effort read timed out; serving cached value", attrs...)
			a.noticeForTab(tabID, fmt.Sprintf("effort read timed out (>%s); showing last known value", limit))
			return cached
		}
		attrs := []any{"tab", tabID, "limit", limit}
		if phases != "" {
			attrs = append(attrs, "phases", phases)
		}
		slog.Warn("desktop: effort read timed out; no cached value, serving default", attrs...)
		a.noticeForTab(tabID, fmt.Sprintf("effort read timed out (>%s); showing default", limit))
		return EffortInfo{Current: "auto", Levels: []string{}}
	}
}

// effortReadLimit resolves the timeout for one read: the production constant,
// unless a test shortened it via effortReadLimitOverride.
func (a *App) effortReadLimit() time.Duration {
	if a.effortReadLimitOverride > 0 {
		return a.effortReadLimitOverride
	}
	return effortReadTimeout
}

// effortReadCompute is the unbounded direct read; the stub exists so tests can
// simulate a slow source without real config disk I/O.
func (a *App) effortReadCompute(tabID string) EffortInfo {
	if a.effortReadStub != nil {
		return a.effortReadStub(tabID)
	}
	return a.effortForTabDirect(tabID)
}

func (a *App) cachedEffortForTab(tabID string) (EffortInfo, time.Time, bool) {
	a.effortCacheMu.Lock()
	defer a.effortCacheMu.Unlock()
	entry, ok := a.effortCache[tabID]
	if !ok {
		return EffortInfo{}, time.Time{}, false
	}
	return entry.info, entry.at, true
}

// effortStaleServeAge resolves the fast-path freshness window: the production
// constant, unless a test shortened it via effortStaleServeAgeOverride.
func (a *App) effortStaleServeAge() time.Duration {
	if a.effortStaleServeAgeOverride > 0 {
		return a.effortStaleServeAgeOverride
	}
	return effortStaleServeMaxAge
}

// effortServeFingerprintOf snapshots the tab-state inputs behind one effort
// read. Empty/unknown tabs read as the zero fingerprint consistently, so the
// active-tab form ("") behaves like any other key.
func (a *App) effortServeFingerprintOf(tabID string) effortServeFingerprint {
	a.mu.RLock()
	defer a.mu.RUnlock()
	fp := effortServeFingerprint{}
	if tab := a.tabByIDLocked(tabID); tab != nil {
		fp.modelRef = tab.model
		if tab.effort != nil {
			fp.effortOvr = *tab.effort
		}
		fp.buildGen = tab.buildGeneration
	}
	return fp
}

// freshCachedEffortForTab reports the cached effort for tab when it may serve
// the task-1161 fast path: the tab-state fingerprint still matches (an effort
// or model switch busts it, so a switched-to value can never be answered with
// the pre-switch one) and the entry is younger than the stale-serve window.
func (a *App) freshCachedEffortForTab(tabID string) (EffortInfo, bool) {
	fp := a.effortServeFingerprintOf(tabID)
	a.effortCacheMu.Lock()
	entry, ok := a.effortCache[tabID]
	a.effortCacheMu.Unlock()
	if !ok || entry.fp != fp {
		return EffortInfo{}, false
	}
	if time.Since(entry.at) > a.effortStaleServeAge() {
		return EffortInfo{}, false
	}
	return entry.info, true
}

// spawnEffortRefresh recomputes the effort for tab in the background after a
// fast-path serve, single-flighted per tab: concurrent serves join the
// in-flight refresh instead of stampeding one goroutine each. The refresh
// carries no phase trail and no timeout semantics — it is the cache's own
// upkeep, and its completion lands in storeEffortCache like any read.
func (a *App) spawnEffortRefresh(tabID string) {
	a.effortRefreshMu.Lock()
	if a.effortRefreshing == nil {
		a.effortRefreshing = map[string]bool{}
	}
	if a.effortRefreshing[tabID] {
		a.effortRefreshMu.Unlock()
		return
	}
	a.effortRefreshing[tabID] = true
	a.effortRefreshMu.Unlock()
	go func() {
		defer func() {
			a.effortRefreshMu.Lock()
			delete(a.effortRefreshing, tabID)
			a.effortRefreshMu.Unlock()
		}()
		fp := a.effortServeFingerprintOf(tabID)
		info := a.effortReadCompute(tabID)
		a.storeEffortCache(tabID, info, fp)
	}()
}

func (a *App) storeEffortCache(tabID string, info EffortInfo, fp effortServeFingerprint) {
	// The result was computed under fp; if the tab state moved mid-read (an
	// effort/model switch landed while the disk crawled), the entry would
	// advertise the new fingerprint with the old value — drop it instead and
	// let the next read recompute under the settled state.
	if now := a.effortServeFingerprintOf(tabID); now != fp {
		return
	}
	a.effortCacheMu.Lock()
	defer a.effortCacheMu.Unlock()
	if a.effortCache == nil {
		a.effortCache = make(map[string]effortCacheEntry)
	}
	a.effortCache[tabID] = effortCacheEntry{info: info, at: time.Now(), fp: fp}
}
