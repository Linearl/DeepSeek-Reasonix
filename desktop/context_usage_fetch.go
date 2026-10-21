package main

import (
	"fmt"
	"log/slog"
	"time"

	"reasonix/internal/control"
)

// contextUsageReadTimeout caps one synchronous context-usage read: the
// controller-derived gauge fields behind the ContextUsageForTab binding that
// the frontend awaits in the switch-tab ancillary batch (useController.ts
// "ancillary context"). The 2026-10-14 diagnosis (issue #51, build 41f87feb0)
// measured one switch where this leg alone took 12.8s while the sibling legs
// finished (jobs 41ms, checkpoints 55ms, effort 2.2s already capped by task
// 421) — the batch waits for its slowest leg, so the whole tab switch stalled
// on a sidebar gauge. The read recomputes the full transcript on every call
// (snapshot copy, the token estimate over canonical and projected views, the
// input fingerprint; internal/agent/context_status.go
// ContextMaintenanceSnapshot), so long sessions make it the batch tail.
//
// 2s rationale: the same cap the effort read uses (task 421) — a healthy read
// is virtually never cut off, while a bad sample is bounded at the cap
// instead of running 7-15s, keeping the ancillary batch at the effort-cap
// level (~2.2s). Deliberately not a user-facing knob - no existing context
// config surface carries timings - but a package-level constant the tests can
// bypass through contextUsageReadLimitOverride on App.
const contextUsageReadTimeout = 2 * time.Second

// contextUsageCompute is the controller-derived portion of ContextInfo: the
// prompt-vs-window gauge plus the maintenance/budget panels. The session
// telemetry fields (cost, tokens) stay on the synchronous fast path in
// ContextUsageForTab and are never cached here.
type contextUsageCompute struct {
	used          int
	window        int
	compactRatio  float64
	maintenance   *ContextMaintenanceInfo
	contextBudget *ContextBudgetInfo
}

// contextUsageCacheEntry is the last completed context-usage read for one
// tab, kept so a timed-out read can serve the previous gauge instead of
// blanking it. Value semantics only: the next successful read overwrites it,
// and an abandoned (timed-out) read still lands here when it eventually
// finishes, which keeps the fallback as fresh as the source allows.
type contextUsageCacheEntry struct {
	compute contextUsageCompute
	at      time.Time
}

// boundedContextUsageCompute runs the controller-derived gauge read bounded
// by contextUsageReadTimeout (task 882). The direct read runs on its own
// goroutine; if it exceeds the cap, the switch does not wait: the last cached
// value is served (or a defined default on the first read, before any cache
// exists - used/window zero means no data yet, which the gauge hides) and a
// warn log plus a tab notice record the fallback, so a degraded sample is
// diagnosable instead of silently slow. The stage tag matches the frontend's
// switch-tab ancillary batch name so the warn line pairs with the
// "switch-tab:ancillary context" timing breadcrumb.
func (a *App) boundedContextUsageCompute(tabID string, ctrl control.SessionAPI) contextUsageCompute {
	limit := a.contextUsageReadLimit()
	// Buffered channel: once the timeout branch has returned, the late send
	// lands in the buffer and the goroutine exits - an abandoned read never
	// leaks on the send, and its storeContextUsageCache still lands.
	done := make(chan contextUsageCompute, 1)
	go func() {
		compute := a.contextUsageComputeRun(ctrl)
		a.storeContextUsageCache(tabID, compute)
		done <- compute
	}()
	select {
	case compute := <-done:
		a.contextUsageNoticeCleared(tabID)
		return compute
	case <-time.After(limit):
		if cached, at, ok := a.cachedContextUsageForTab(tabID); ok {
			// A fallback hit is not an error for the caller - the switch must
			// go through - but it must leave a trace, or the multi-second
			// stalls that motivated task 882 would stay invisible forever.
			// 任务 1021: the notice trace is DEDUPED per tab — a session whose
			// gauge read blocks for minutes (a live compaction recomputes the
			// full transcript on every read) used to fire this notice once per
			// ancillary batch, flooding the transcript with identical lines.
			// The warn log keeps every occurrence; the notice reports the
			// FIRST timeout and a folded count once a read finally succeeds
			// (contextUsageNoticeCleared).
			slog.Warn("desktop: context usage read timed out; serving cached value",
				"tab", tabID, "stage", "switch-tab:ancillary context",
				"limit", limit, "cache_age", time.Since(at).Round(time.Millisecond))
			if first, _ := a.contextUsageNoticeMarkTimeout(tabID); first {
				a.noticeForTab(tabID, fmt.Sprintf("context read timed out (>%s); showing last known value", limit))
			}
			return cached
		}
		slog.Warn("desktop: context usage read timed out; no cached value, serving default",
			"tab", tabID, "stage", "switch-tab:ancillary context", "limit", limit)
		if first, _ := a.contextUsageNoticeMarkTimeout(tabID); first {
			a.noticeForTab(tabID, fmt.Sprintf("context read timed out (>%s); showing default", limit))
		}
		return contextUsageCompute{}
	}
}

// contextUsageNotice state (任务 1021): per-tab "a timeout notice is already
// showing" marker plus a suppressed-repeat counter. Guarded by
// contextUsageCacheMu — the same lock the gauge cache uses, so the marker's
// lifetime is shared with the cache entries that caused the timeouts.
type contextUsageNoticeState struct {
	armed    bool // a timeout notice was emitted and not yet cleared
	suppress int  // timeouts folded into that notice since it was emitted
}

// contextUsageNoticeMarkTimeout records one timeout for tabID and reports
// whether the caller should EMIT the user-facing notice: true only for the
// first timeout of an armed window (subsequent timeouts are folded into the
// same notice — their count surfaces via the clear message below).
func (a *App) contextUsageNoticeMarkTimeout(tabID string) (emit bool, suppressed int) {
	a.contextUsageCacheMu.Lock()
	defer a.contextUsageCacheMu.Unlock()
	if a.contextUsageNotices == nil {
		a.contextUsageNotices = make(map[string]contextUsageNoticeState)
	}
	st := a.contextUsageNotices[tabID]
	if st.armed {
		st.suppress++
		a.contextUsageNotices[tabID] = st
		return false, st.suppress
	}
	a.contextUsageNotices[tabID] = contextUsageNoticeState{armed: true}
	return true, 0
}

// contextUsageNoticeCleared runs on a successful (non-timeout) read: when
// timeouts were folded into an already-showing notice, ONE follow-up notice
// tells the user how many repeats it absorbed; then the marker disarms so the
// next degraded window announces itself again. The first-success call
// (suppressed == 0) is silent — the normal path must stay quiet (task 882).
func (a *App) contextUsageNoticeCleared(tabID string) {
	a.contextUsageCacheMu.Lock()
	st := a.contextUsageNotices[tabID]
	delete(a.contextUsageNotices, tabID)
	a.contextUsageCacheMu.Unlock()
	if st.suppress > 0 {
		a.noticeForTab(tabID, fmt.Sprintf("context read recovered; %d repeat timeout(s) folded into the previous notice", st.suppress))
	}
}

// contextUsageReadLimit resolves the timeout for one read: the production
// constant, unless a test shortened it via contextUsageReadLimitOverride.
func (a *App) contextUsageReadLimit() time.Duration {
	if a.contextUsageReadLimitOverride > 0 {
		return a.contextUsageReadLimitOverride
	}
	return contextUsageReadTimeout
}

// contextUsageComputeRun is the unbounded direct read; the stub exists so
// tests can simulate a slow source without a real transcript.
func (a *App) contextUsageComputeRun(ctrl control.SessionAPI) contextUsageCompute {
	if a.contextUsageReadStub != nil {
		return a.contextUsageReadStub(ctrl)
	}
	return contextUsageComputeFromController(ctrl)
}

// contextUsageComputeFromController is the extraction of the previously
// inline controller reads in ContextUsageForTab - behaviorally unchanged.
func contextUsageComputeFromController(ctrl control.SessionAPI) contextUsageCompute {
	used, window := ctrl.ContextSnapshot()
	compute := contextUsageCompute{used: used, window: window, compactRatio: ctrl.CompactRatio()}
	snapshot := ctrl.ContextMaintenanceSnapshot()
	compute.maintenance = contextMaintenanceInfo(snapshot)
	if snapshot.ContextBudget != nil {
		compute.contextBudget = contextBudgetInfo(snapshot.ContextBudget)
	}
	return compute
}

func (a *App) cachedContextUsageForTab(tabID string) (contextUsageCompute, time.Time, bool) {
	a.contextUsageCacheMu.Lock()
	defer a.contextUsageCacheMu.Unlock()
	entry, ok := a.contextUsageCache[tabID]
	if !ok {
		return contextUsageCompute{}, time.Time{}, false
	}
	return entry.compute, entry.at, true
}

func (a *App) storeContextUsageCache(tabID string, compute contextUsageCompute) {
	a.contextUsageCacheMu.Lock()
	defer a.contextUsageCacheMu.Unlock()
	if a.contextUsageCache == nil {
		a.contextUsageCache = make(map[string]contextUsageCacheEntry)
	}
	a.contextUsageCache[tabID] = contextUsageCacheEntry{compute: compute, at: time.Now()}
}
