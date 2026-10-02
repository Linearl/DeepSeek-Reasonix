package sandbox

import (
	"log/slog"
	"path/filepath"
	"sync"
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
	// user-supplied, so the key set is open-ended. Far above any real
	// working set of write roots.
	canonicalCacheMaxEntries = 1024
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

	canonicalMu.Lock()
	if entry, ok := canonicalCache[abs]; ok && time.Now().Before(entry.expiresAt) {
		canonicalMu.Unlock()
		return entry.resolved, nil
	}
	if flight, ok := canonicalFlights[abs]; ok {
		canonicalMu.Unlock()
		<-flight.done
		return flight.resolved, nil
	}
	flight := &canonicalFlight{done: make(chan struct{})}
	canonicalFlights[abs] = flight
	canonicalMu.Unlock()

	resolved := canonicalWalkBounded(abs)

	canonicalMu.Lock()
	delete(canonicalFlights, abs)
	canonicalCacheStore(abs, resolved)
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
	return canonicalWalkBounded(filepath.Clean(abs)), nil
}

// canonicalCacheStore inserts an entry, evicting expired entries first and
// then arbitrary ones (memoization-grade eviction is enough) when the key
// set outgrows canonicalCacheMaxEntries. canonicalMu must be held.
func canonicalCacheStore(key, resolved string) {
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
	canonicalCache[key] = canonicalEntry{resolved: resolved, expiresAt: now.Add(canonicalCacheTTL)}
}

// canonicalWalkBounded resolves the deepest existing ancestor of abs with
// EvalSymlinks under canonicalResolveTimeout and re-appends the missing
// tail. On timeout it returns abs itself — the same fallback the walk uses
// when nothing along the path exists — after one warning; the result is
// cached, so the warning cannot repeat within the TTL.
func canonicalWalkBounded(abs string) string {
	type walkResult struct{ path string }
	// Buffered: a walker that outlives the budget (e.g. an SMB reconnect)
	// must never block on delivery.
	ch := make(chan walkResult, 1)
	go func() {
		tail := ""
		cur := abs
		for {
			if real, err := canonicalEvalSymlinks(cur); err == nil {
				ch <- walkResult{filepath.Join(real, tail)}
				return
			}
			parent := filepath.Dir(cur)
			if parent == cur {
				ch <- walkResult{abs} // nothing along the path exists; use the cleaned abs
				return
			}
			tail = filepath.Join(filepath.Base(cur), tail)
			cur = parent
		}
	}()
	timer := time.NewTimer(canonicalResolveTimeout)
	defer timer.Stop()
	select {
	case res := <-ch:
		return res.path
	case <-timer.C:
		slog.Warn("canonical path resolution timed out; keeping cleaned path (network drive offline?)",
			"path", abs, "budget", canonicalResolveTimeout.String())
		return abs
	}
}
