package sandbox

import (
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Shared canonical path resolution (task 455fix). The walk used to exist
// twice — internal/tool/builtin confine.go realPath and write_path.go
// ResolveAbsPath — and every boot walked each configured write root with an
// unbounded filepath.EvalSymlinks cascade. One dead network drive in
// allow_write made each boot eat a full SMB reconnect budget (~21s observed).
// This file is now the single implementation: one bounded walk per path,
// results cached for a short TTL (failures included), concurrent callers
// joining the in-flight walk instead of starting their own.

const (
	// canonicalResolveTimeoutBudget bounds one canonicalization walk. On
	// timeout the cleaned absolute path is kept (the same fallback the walk
	// uses when nothing along the path exists) and boot continues — a dead
	// network root may cost the budget, never an SMB reconnect.
	canonicalResolveTimeoutBudget = 250 * time.Millisecond

	// canonicalCacheMaxEntries caps memoization growth: write targets are
	// user-supplied, so the key set is open-ended. Task 928 raised the cap
	// from 1024: the desktop funnels every historical session path through
	// this cache (sessionRuntimeKey), and one project alone carries >1400
	// session records — below the old cap the store's arbitrary eviction
	// re-walked the same paths inside the TTL, the walk storm behind
	// desktop.log's canonical probe lines. Entries are one small string plus
	// a deadline, so 8192 caps worst-case memory well under a megabyte.
	canonicalCacheMaxEntries = 8192
)

// Test seams: production code never reassigns these; tests swap them to
// simulate slow filesystems and count walks.
//
// canonicalCacheTTL keeps every caller — both boot canonicalizers, repeated
// guards, and concurrent tabs booting in parallel — off the filesystem for
// this long per path. Failed lookups are cached too, so a dead drive is
// probed at most once per TTL (and the timeout warning stays low-frequency).
var (
	canonicalEvalSymlinks   = filepath.EvalSymlinks
	canonicalResolveTimeout = canonicalResolveTimeoutBudget
	canonicalCacheTTL       = 30 * time.Second

	// Task 928 (residual ①): the TTL treadmill. Paths that are continuously
	// re-canonicalized (every open tab's session file on the save/load/lock
	// funnel) were re-walked once per 30s forever — harmless at 2ms on a
	// healthy disk, but each walk under disk pressure cost 100-500ms and each
	// timed-out walk leaked an abandoned walker goroutine still hammering
	// EvalSymlinks. A positive first-level resolution (the exact path exists
	// and resolved itself) now gets a much longer TTL: the identity of an
	// existing file is stable, and the security-sensitive staleness question
	// is owned by ResolveAbsPathFresh (EnsureWriteDir's approval check),
	// which is untouched. Missing paths and timeouts keep the short TTL —
	// a not-yet-created write target must be re-probed (it may be created as
	// a symlink), and a dead drive must be retried.
	canonicalCachePositiveTTL = 10 * time.Minute
)

type canonicalEntry struct {
	resolved  string
	expiresAt time.Time
}

// canonicalFlight lets concurrent callers share one walk. resolved is
// written before done is closed, so readers after <-done see it.
type canonicalFlight struct {
	done     chan struct{}
	resolved string
}

var (
	canonicalMu      sync.Mutex
	canonicalCache   = map[string]canonicalEntry{}
	canonicalFlights = map[string]*canonicalFlight{}
)

// Task 883 probe (read-only instrumentation, zero behavior change): counters
// for sizing the 639 root fix. They answer, from desktop.log alone, how often
// canonicalization is entered, what share is served by the 30s TTL cache or
// an in-flight walk, how many real walks run and how long they take, and how
// many hit the 250ms budget. All atomic; the only log is one line per
// completed walk — walks are already the slow path (at least one
// EvalSymlinks syscall chain, at most one per path per TTL), and each line
// doubles as the running counter dump, so no timer or goroutine is added.
// Outcome values: "resolved" (walk found an existing ancestor), "missing"
// (nothing along the path existed; cleaned input kept), "timeout" (250ms
// budget hit; cleaned input kept). The phrase "timed out" is deliberately
// avoided so existing log assertions keep counting only the original warning.
var (
	probeCalls atomic.Uint64 // resolveCanonicalPath entries
	probeHits  atomic.Uint64 // served from canonicalCache within TTL
	probeJoins atomic.Uint64 // joined an in-flight walk
	probeWalks atomic.Uint64 // canonicalWalkBounded runs (cached + fresh)
	probeFresh atomic.Uint64 // ResolveAbsPathFresh uncached walks
	probeTime  atomic.Uint64 // walks that hit the 250ms budget
)

// canonicalProbeWalkLog emits the per-walk line with the running totals.
func canonicalProbeWalkLog(abs string, started time.Time, outcome string) {
	probeWalks.Add(1)
	slog.Info("canonical probe: walk",
		"path", abs,
		"elapsed_ms", time.Since(started).Milliseconds(),
		"outcome", outcome,
		"calls_total", probeCalls.Load(),
		"cache_hits", probeHits.Load(),
		"flight_joins", probeJoins.Load(),
		"walks_total", probeWalks.Load(),
		"fresh_walks_total", probeFresh.Load(),
		"timeouts_total", probeTime.Load())
}

// canonicalCacheKey folds the memoization key. On Windows the filesystem is
// case-insensitive, but callers reach this cache with both the on-disk
// spelling (catalog records keep directory-listing case, e.g.
// C--Users-...) and the lowercased identity form (pathidentity.Canonical
// output fed back in). Task 928: without the fold each physical file held
// two cache entries and paid two walks, doubling the working set — enough,
// with the old 1024 cap, to make the store evict paths that were about to be
// asked for again. The walk itself still runs on the caller's spelling; only
// the cache/flight key is folded, and on Windows both spellings resolve to
// the same volume-canonical case anyway, so the shared entry is the same
// value the second walk would have produced.
func canonicalCacheKey(abs string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(abs)
	}
	return abs
}

// resolveCanonicalPath returns the absolute, symlink-free form of path,
// resolving the deepest existing ancestor and re-appending the
// not-yet-existing tail (write targets need not exist yet). The walk is
// bounded by canonicalResolveTimeout: on timeout the cleaned absolute input
// is returned (never an error) after one low-frequency warning. Results are
// cached per cleaned absolute path for canonicalCacheTTL, timeouts included;
// concurrent callers share a single in-flight walk per path.
func resolveCanonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	key := canonicalCacheKey(abs)

	canonicalMu.Lock()
	if entry, ok := canonicalCache[key]; ok && time.Now().Before(entry.expiresAt) {
		canonicalMu.Unlock()
		probeCalls.Add(1)
		probeHits.Add(1)
		return entry.resolved, nil
	}
	if flight, ok := canonicalFlights[key]; ok {
		canonicalMu.Unlock()
		probeCalls.Add(1)
		probeJoins.Add(1)
		<-flight.done
		return flight.resolved, nil
	}
	flight := &canonicalFlight{done: make(chan struct{})}
	canonicalFlights[key] = flight
	canonicalMu.Unlock()
	probeCalls.Add(1)

	resolved, positive := canonicalWalkBounded(abs)

	canonicalMu.Lock()
	delete(canonicalFlights, key)
	ttl := canonicalCacheTTL
	if positive {
		ttl = canonicalCachePositiveTTL
	}
	canonicalCacheStore(key, resolved, ttl)
	canonicalMu.Unlock()

	flight.resolved = resolved
	close(flight.done)
	return resolved, nil
}

// ResolveAbsPathFresh is the uncached form of ResolveAbsPath: same bounded
// walk, but it always touches the filesystem so it observes identity changes
// a 30s TTL would hide. Approval-identity verification (EnsureWriteDir) must
// use this form — its contract is to catch a root retargeted between the
// approval prompt and the grant.
func ResolveAbsPathFresh(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	probeFresh.Add(1)
	resolved, _ := canonicalWalkBounded(filepath.Clean(abs))
	return resolved, nil
}

// canonicalCacheStore inserts an entry, evicting expired entries first and
// then arbitrary ones (memoization-grade eviction is enough) when the key
// set outgrows canonicalCacheMaxEntries. canonicalMu must be held.
func canonicalCacheStore(key, resolved string, ttl time.Duration) {
	now := time.Now()
	if len(canonicalCache) >= canonicalCacheMaxEntries {
		for k, entry := range canonicalCache {
			if now.After(entry.expiresAt) {
				delete(canonicalCache, k)
			}
		}
	}
	for len(canonicalCache) >= canonicalCacheMaxEntries {
		for k := range canonicalCache {
			delete(canonicalCache, k)
			break
		}
	}
	canonicalCache[key] = canonicalEntry{resolved: resolved, expiresAt: now.Add(ttl)}
}

// canonicalWalkBounded resolves the deepest existing ancestor of abs with
// EvalSymlinks under canonicalResolveTimeout and re-appends the missing
// tail. On timeout it returns abs itself — the same fallback the walk uses
// when nothing along the path exists — after one warning; the result is
// cached, so the warning cannot repeat within the TTL.
//
// The second result reports a positive first-level resolution: the exact
// path existed and eval'd itself, so its identity is stable and the caller
// may cache it under the long positive TTL (task 928 residual ①). Anything
// else — a re-appended missing tail, nothing-existed fallback, timeout —
// keeps the short TTL, because the path may still be created (possibly as a
// symlink) or the dead root may come back with a different identity.
func canonicalWalkBounded(abs string) (string, bool) {
	type walkResult struct {
		path     string
		positive bool
	}
	// Buffered: a walker that outlives the budget (e.g. an SMB reconnect)
	// must never block on delivery.
	ch := make(chan walkResult, 1)
	// Capture the seam into a local before spawning: a walker abandoned past
	// the timeout keeps running after a later test swapped/restored the
	// package variable, and must never re-read the shared seam (the -race
	// report on task 762 was exactly that cross-test race).
	eval := canonicalEvalSymlinks
	go func() {
		tail := ""
		cur := abs
		for {
			if real, err := eval(cur); err == nil {
				ch <- walkResult{filepath.Join(real, tail), tail == ""}
				return
			}
			parent := filepath.Dir(cur)
			if parent == cur {
				ch <- walkResult{abs, false} // nothing along the path exists; use the cleaned abs
				return
			}
			tail = filepath.Join(filepath.Base(cur), tail)
			cur = parent
		}
	}()
	timer := time.NewTimer(canonicalResolveTimeout)
	defer timer.Stop()
	// Task 883 probe: measure the walk only; no branch, value or error path
	// depends on anything below.
	walkStarted := time.Now()
	select {
	case res := <-ch:
		outcome := "resolved"
		if res.path == abs {
			outcome = "missing"
		}
		canonicalProbeWalkLog(abs, walkStarted, outcome)
		return res.path, res.positive
	case <-timer.C:
		probeTime.Add(1)
		canonicalProbeWalkLog(abs, walkStarted, "timeout")
		// Task 928 residual ④: the old text blamed the network drive even
		// for local C:\ paths — the observed cause was a saturated disk
		// pushing EvalSymlinks past the budget. Name both candidates and
		// drop the parenthetical verdict; the path attr lets the operator
		// tell which one it actually was.
		slog.Warn("canonical path resolution timed out; keeping cleaned path (slow disk or unreachable network drive?)",
			"path", abs, "budget", canonicalResolveTimeout.String())
		return abs, false
	}
}
