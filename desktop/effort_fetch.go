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

// effortCacheEntry is the last completed effort read for one tab, kept so a
// timed-out re-fetch can serve the previous value instead of blanking the
// control. Value semantics only: the next successful read overwrites it, and
// an abandoned (timed-out) read still lands here when it eventually finishes,
// which keeps the fallback as fresh as the source allows.
type effortCacheEntry struct {
	info EffortInfo
	at   time.Time
}

// EffortForTab returns the effort control state for a tab, bounded by
// effortReadTimeout (task 421). The direct read runs on its own goroutine; if
// it exceeds the cap, the switch does not wait: the last cached value is
// served (or a defined default on the first read, before any cache exists)
// and a warn log plus a tab notice record the fallback, so a degraded sample
// is diagnosable instead of silently slow.
func (a *App) EffortForTab(tabID string) EffortInfo {
	limit := a.effortReadLimit()
	// Buffered channel: once the timeout branch has returned, the late send
	// lands in the buffer and the goroutine exits - an abandoned read never
	// leaks on the send, and its storeEffortCache still lands.
	done := make(chan EffortInfo, 1)
	go func() {
		info := a.effortReadCompute(tabID)
		a.storeEffortCache(tabID, info)
		done <- info
	}()
	select {
	case info := <-done:
		return info
	case <-time.After(limit):
		if cached, at, ok := a.cachedEffortForTab(tabID); ok {
			// A fallback hit is not an error for the caller - the switch must
			// go through - but it must leave a trace, or the bad samples that
			// motivated task 421 would stay invisible forever.
			slog.Warn("desktop: effort read timed out; serving cached value",
				"tab", tabID, "limit", limit, "cache_age", time.Since(at).Round(time.Millisecond))
			a.noticeForTab(tabID, fmt.Sprintf("effort read timed out (>%s); showing last known value", limit))
			return cached
		}
		slog.Warn("desktop: effort read timed out; no cached value, serving default",
			"tab", tabID, "limit", limit)
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

func (a *App) storeEffortCache(tabID string, info EffortInfo) {
	a.effortCacheMu.Lock()
	defer a.effortCacheMu.Unlock()
	if a.effortCache == nil {
		a.effortCache = make(map[string]effortCacheEntry)
	}
	a.effortCache[tabID] = effortCacheEntry{info: info, at: time.Now()}
}
