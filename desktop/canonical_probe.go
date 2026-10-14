package main

import (
	"log/slog"
	"sync/atomic"
	"time"
)

// Task 883 probe (read-only instrumentation, zero behavior change): times the
// workspacelease.CanonicalWorkspace calls behind the lease-overlap decision
// (WorkspaceConflictForTab) so desktop.log can answer how much of a tab
// switch the canonicalization costs and which shape the slow calls take
// (junction alias vs network drive vs nonexistent root), ahead of the 639
// root fix.
//
// Why slow-calls-only logging: the frontend polls WorkspaceConflictForTab
// every 500ms per running tab (useRuntimeStatus), and the candidate loop
// canonicalizes every other tab root before checking lease ownership — with
// N tabs that is N² calls per 500ms, almost all served by the sandbox TTL
// cache in well under 1ms. Logging every call would flood desktop.log, so a
// call is logged only when it costs canonicalProbeSlowMs or more (i.e. it
// left the cache and walked); the running totals ride every such line, so
// call frequency and slow-call share stay measurable from the log alone.
// Overhead on the silent path is three atomic adds plus one clock read.
const canonicalProbeSlowMs = 5 * time.Millisecond

var (
	probeLeaseCalls  atomic.Uint64 // CanonicalWorkspace calls at the two lease-overlap sites
	probeLeaseSlow   atomic.Uint64 // calls at or above canonicalProbeSlowMs
	probeLeaseTotal  atomic.Uint64 // cumulative nanoseconds across all calls
	probeLeaseSlowNs atomic.Uint64 // cumulative nanoseconds across slow calls
)

// canonicalProbeLease records one CanonicalWorkspace call and logs it when
// slow. site distinguishes the target root from candidate roots so the log
// can separate the one mandatory call per poll from the per-candidate loop.
func canonicalProbeLease(site, root string, started time.Time, resolved string, err error) {
	elapsed := time.Since(started)
	probeLeaseCalls.Add(1)
	probeLeaseTotal.Add(uint64(elapsed))
	if elapsed < canonicalProbeSlowMs {
		return
	}
	probeLeaseSlow.Add(1)
	probeLeaseSlowNs.Add(uint64(elapsed))
	attrs := []any{
		"site", site,
		"root", root,
		"resolved", resolved,
		"elapsed_ms", elapsed.Milliseconds(),
		"calls_total", probeLeaseCalls.Load(),
		"slow_total", probeLeaseSlow.Load(),
		"slow_total_ms", probeLeaseSlowNs.Load() / uint64(time.Millisecond),
		"all_calls_total_ms", probeLeaseTotal.Load() / uint64(time.Millisecond),
	}
	if err != nil {
		attrs = append(attrs, "err", err.Error())
	}
	slog.Info("desktop: canonical probe", attrs...)
}
