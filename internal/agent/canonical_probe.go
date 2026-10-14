package agent

import (
	"log/slog"
	"sync/atomic"
	"time"
)

// Task 883 probe (read-only instrumentation, zero behavior change): times
// canonicalSessionSavePath — the save-path lock key funnel (pathidentity.
// Canonical → sandbox bounded walk) — so desktop.log can answer how much of
// a tab switch the save-path canonicalization costs, ahead of the 639 root
// fix.
//
// Slow-calls-only logging: the funnel runs on every session save, load and
// lock acquisition, far too hot for a line per call; warm calls are a TTL
// cache hit in well under 1ms. A call is logged only when it costs
// canonicalProbeSlowMs or more (i.e. it left the cache and walked); running
// totals ride every such line so call frequency stays measurable. Overhead
// on the silent path is three atomic adds plus one clock read.
const canonicalProbeSlowMs = 5 * time.Millisecond

var (
	probeSaveCalls atomic.Uint64 // canonicalSessionSavePath calls
	probeSaveSlow  atomic.Uint64 // calls at or above canonicalProbeSlowMs
)

// canonicalProbeSaveLockKey records one lock-key canonicalization and logs it
// when slow. The key itself is returned unchanged by the caller either way.
func canonicalProbeSaveLockKey(path, key string, started time.Time) {
	elapsed := time.Since(started)
	probeSaveCalls.Add(1)
	if elapsed < canonicalProbeSlowMs {
		return
	}
	probeSaveSlow.Add(1)
	attrs := []any{
		"site", "save-lock-key",
		"path", path,
		"key", key,
		"elapsed_ms", elapsed.Milliseconds(),
		"calls_total", probeSaveCalls.Load(),
		"slow_total", probeSaveSlow.Load(),
	}
	slog.Info("session: canonical probe", attrs...)
}
