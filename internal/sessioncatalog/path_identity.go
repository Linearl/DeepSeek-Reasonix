package sessioncatalog

import (
	"log/slog"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	// pathIdentityResolveBudget bounds one identity walk (tl1526). The walk is
	// EvalSymlinks plus (on Windows) a per-component case probe, so a saturated
	// disk once made a reconcile pay the walk per session record; past the
	// budget the lexical key is served and the walk refreshes in background.
	pathIdentityResolveBudget = 250 * time.Millisecond
	// TTLs mirror the internal/sandbox canonical engine: resolved identities
	// are stable for the process lifetime, degraded or tail-re-appended ones
	// keep a short TTL so a later rename is picked up.
	pathIdentityNegativeTTL = 30 * time.Second
	pathIdentityPositiveTTL = 10 * time.Minute
	pathIdentityMaxEntries  = 16384
	// pathIdentityWarnCooldown rate-gates the degrade WARN: a full-directory
	// scan under a saturated disk would otherwise emit one line per record.
	pathIdentityWarnCooldown = time.Minute
)

// Test seams: production code never reassigns these; tests swap them to
// simulate slow filesystems.
var (
	pathIdentityEvalSymlinks   = filepath.EvalSymlinks
	pathIdentityResolveTimeout = pathIdentityResolveBudget
)

type pathIdentityEntry struct {
	identity  string
	expiresAt time.Time
}

var (
	pathIdentityMu    sync.Mutex
	pathIdentityCache = map[string]pathIdentityEntry{}

	pathIdentityWarnMu     sync.Mutex
	pathIdentityWarnAt     time.Time
	pathIdentitySuppressed int
)

// PathIdentityKey returns the stable comparison key for a catalog path while
// leaving the caller's access spelling untouched. Parent aliases are resolved,
// and case is folded only when the governing filesystem directory reports
// case-insensitive lookup. tl1526: the walk is bounded and memoized — on
// timeout the lexical key is served (Windows folds it lowercased, the common
// per-directory-insensitive case) with one WARN carrying the elapsed time and
// the degrade mode, and the in-flight walk lands the resolved identity in the
// cache for later callers.
func PathIdentityKey(path string) string {
	path = cleanCatalogAccessPath(path)
	if path == "" {
		return ""
	}
	key := filepath.Clean(path)
	if absolute, err := filepath.Abs(key); err == nil {
		key = filepath.Clean(absolute)
	}
	if identity, ok := lookupPathIdentity(key); ok {
		return identity
	}
	started := time.Now()
	res, ok := pathIdentityWalkBounded(key)
	if !ok {
		// Degrade-first (tl1526): the caller never waits past the budget — the
		// lexical key is served now and the walker's outcome lands in the
		// cache for later callers.
		fallback := lexicalPathIdentityKey(key)
		warnPathIdentityDegrade(key, time.Since(started))
		storePathIdentity(key, fallback, pathIdentityNegativeTTL)
		return fallback
	}
	storePathIdentity(key, res.identity, pathIdentityTTL(res.positive))
	return res.identity
}

// pathIdentityWalkResult carries the resolved identity plus whether the exact
// path resolved itself (long-TTL stable) or needed a re-appended missing tail.
type pathIdentityWalkResult struct {
	identity string
	positive bool
}

// pathIdentityWalkBounded runs the ancestor walk plus the platform case fold
// under pathIdentityResolveTimeout. ok=false means the budget was hit: the
// caller degrades and the walker's outcome is stored in the cache when it
// eventually lands, so later callers see the resolved identity.
func pathIdentityWalkBounded(key string) (pathIdentityWalkResult, bool) {
	ch := make(chan pathIdentityWalkResult, 1)
	go func() {
		resolved := resolveCatalogPathThroughExistingAncestor(key)
		ch <- pathIdentityWalkResult{
			identity: filepath.Clean(platformCatalogPathIdentity(resolved)),
			positive: resolved == key,
		}
	}()
	timer := time.NewTimer(pathIdentityResolveTimeout)
	defer timer.Stop()
	select {
	case res := <-ch:
		return res, true
	case <-timer.C:
		go func() {
			if res := <-ch; res.identity != "" {
				storePathIdentity(key, res.identity, pathIdentityTTL(res.positive))
			}
		}()
		return pathIdentityWalkResult{}, false
	}
}

// lexicalPathIdentityKey is the degraded identity: the cleaned absolute path,
// lowercased on Windows where per-directory insensitive lookup is the norm.
func lexicalPathIdentityKey(key string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(key)
	}
	return key
}

// pathIdentityTTL picks the memoization TTL: a fully resolved path is stable
// (long), a re-appended missing tail may still be created (short).
func pathIdentityTTL(positive bool) time.Duration {
	if positive {
		return pathIdentityPositiveTTL
	}
	return pathIdentityNegativeTTL
}

func lookupPathIdentity(key string) (string, bool) {
	pathIdentityMu.Lock()
	defer pathIdentityMu.Unlock()
	entry, ok := pathIdentityCache[key]
	if !ok || !time.Now().Before(entry.expiresAt) {
		return "", false
	}
	return entry.identity, true
}

func storePathIdentity(key, identity string, ttl time.Duration) {
	pathIdentityMu.Lock()
	defer pathIdentityMu.Unlock()
	now := time.Now()
	if len(pathIdentityCache) >= pathIdentityMaxEntries {
		for k, entry := range pathIdentityCache {
			if now.After(entry.expiresAt) {
				delete(pathIdentityCache, k)
			}
		}
	}
	for len(pathIdentityCache) >= pathIdentityMaxEntries {
		for k := range pathIdentityCache {
			delete(pathIdentityCache, k)
			break
		}
	}
	pathIdentityCache[key] = pathIdentityEntry{identity: identity, expiresAt: now.Add(ttl)}
}

// warnPathIdentityDegrade emits the tl1526 degrade WARN: first timeout per
// cooldown logs with the elapsed time and the degrade mode; suppressed
// timeouts ride the next logged line.
func warnPathIdentityDegrade(path string, elapsed time.Duration) {
	pathIdentityWarnMu.Lock()
	defer pathIdentityWarnMu.Unlock()
	now := time.Now()
	if !pathIdentityWarnAt.IsZero() && now.Sub(pathIdentityWarnAt) < pathIdentityWarnCooldown {
		pathIdentitySuppressed++
		return
	}
	fields := []any{
		"path", path,
		"elapsed_ms", elapsed.Milliseconds(),
		"budget", pathIdentityResolveTimeout.String(),
		"mode", "lexical_fallback",
	}
	if pathIdentitySuppressed > 0 {
		fields = append(fields, "suppressed", pathIdentitySuppressed)
		pathIdentitySuppressed = 0
	}
	pathIdentityWarnAt = now
	slog.Warn("sessioncatalog: canonical path identity degraded to lexical key (slow disk or unreachable root?)", fields...)
}

func cleanCatalogAccessPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = filepath.Clean(path)
	if path == "." {
		return ""
	}
	return path
}

func resolveCatalogPathThroughExistingAncestor(path string) string {
	current := filepath.Clean(path)
	missing := make([]string, 0, 4)
	for {
		if resolved, err := pathIdentityEvalSymlinks(current); err == nil {
			for _, part := range slices.Backward(missing) {
				resolved = filepath.Join(resolved, part)
			}
			return resolved
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

// UniqueDirectoryTargets keeps the first usable access spelling for each
// physical directory identity. Rebuild and every caller use the same boundary,
// so a missed Desktop or CLI call site cannot reintroduce duplicate scans.
func UniqueDirectoryTargets(targets []DirectoryTarget) []DirectoryTarget {
	return uniqueDirectoryTargetsBy(targets, PathIdentityKey)
}

func uniqueDirectoryTargetsBy(targets []DirectoryTarget, identity func(string) string) []DirectoryTarget {
	seen := make(map[string]struct{}, len(targets))
	out := make([]DirectoryTarget, 0, len(targets))
	for _, target := range targets {
		target.Path = cleanCatalogAccessPath(target.Path)
		if target.Path == "" {
			continue
		}
		key := identity(target.Path)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, target)
	}
	return out
}
