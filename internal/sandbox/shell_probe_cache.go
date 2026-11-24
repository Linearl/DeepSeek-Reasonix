package sandbox

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"reasonix/internal/fileutil"
	fileencoding "reasonix/internal/fileutil/encoding"
)

// tl1771: bash health-probe successes used to live only in the per-process
// shellSnapshot.probeCache, so every restart paid the full three-route
// discovery again — on Windows that means re-probing Git Bash candidates whose
// msys cold start measured 2.4-3.7s (tl1763), the exact cold-start cost this
// file removes. A successful probe result is persisted as path→source→time;
// the next discovery pass verifies the persisted path first (exists + probe —
// the same acceptance gate as every candidate, so the decision semantics are
// untouched) and only falls back to the full route scan when that fails:
//   - persisted path verifies → one probe short-circuits discovery (cache hit);
//   - persisted path fails to verify (uninstalled, broken) → it is dropped and
//     the full scan decides as before — the cache can only make discovery
//     faster, never wrong;
//   - the fresh successful paths rewrite the file, so the cache self-heals.
//
// The file lives under the per-user cache root (config.CacheDir) beside the
// tool-probe snapshots and the tl1614 shell-label snapshot, and carries a
// machine+user fingerprint that must match on load: cache roots are normally
// per-machine (%LocalAppData%), but REASONIX_CACHE_HOME / REASONIX_HOME can
// repoint them at a synced directory — a mismatching fingerprint (or a corrupt
// or future-version file) discards the whole file and discovery proceeds
// without it, so a foreign machine's paths can never be trusted here.

const shellProbeCacheVersion = 1

// shellProbeCacheMaxEntries bounds the persisted path set. A healthy host
// probes one candidate per pass (the first success short-circuits), so in
// practice the file holds one or two entries; the bound only caps pathological
// growth across reinstall churn.
const shellProbeCacheMaxEntries = 8

// shellProbeCacheEntry is one verified probe success: where the executable
// lives, how discovery attributed it, and when it was probed.
type shellProbeCacheEntry struct {
	Path     string    `json:"path"`
	Source   string    `json:"source,omitempty"`
	ProbedAt time.Time `json:"probed_at"`
}

// shellProbeCacheFile is the persisted document. Host is the machine+user
// isolation fingerprint: a file written by another machine or user account is
// discarded whole (OneDrive/roaming sync guard).
type shellProbeCacheFile struct {
	Version int                    `json:"version"`
	Host    string                 `json:"host"`
	Entries []shellProbeCacheEntry `json:"entries"`
}

var (
	shellProbeCacheDirMu sync.Mutex
	// shellProbeCacheDir is the persistence root injected at boot. Empty (the
	// default, and the state in every test that does not opt in) disables
	// persistence entirely: discovery behaves exactly as before tl1771.
	shellProbeCacheDir string
)

// SetShellProbeCacheDir points shell probe persistence at dir (the per-user
// cache root). Call once during boot, before the first shell resolution.
// An empty dir disables persistence — CacheDir can be unavailable, and caching
// stays best-effort like every consumer of that root.
func SetShellProbeCacheDir(dir string) {
	shellProbeCacheDirMu.Lock()
	defer shellProbeCacheDirMu.Unlock()
	shellProbeCacheDir = strings.TrimSpace(dir)
}

func currentShellProbeCacheDir() string {
	shellProbeCacheDirMu.Lock()
	defer shellProbeCacheDirMu.Unlock()
	return shellProbeCacheDir
}

// shellProbeCachePath lives beside the tool-probe snapshots
// (probes-<hash>.json) and the tl1614 label snapshot (shell-label.json) under
// <cache>/environment/, under a fixed distinct name so the three snapshot
// domains cannot collide.
func shellProbeCachePath(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "environment", "shell-probes.json")
}

// shellProbeCacheHost is the isolation fingerprint: hostname plus user name.
// Both must match for a persisted entry to be considered, so a cache root
// shared across machines (synced profiles) can never inject foreign paths.
func shellProbeCacheHost() string {
	host, _ := os.Hostname()
	user := os.Getenv("USERNAME")
	if user == "" {
		user = os.Getenv("USER")
	}
	return strings.ToLower(strings.TrimSpace(host)) + "\x00" + strings.ToLower(strings.TrimSpace(user))
}

// loadShellProbeCache returns the persisted entries, however stale. The caller
// re-verifies every path (exists + probe) before trusting it — staleness here
// only costs one failed verification, never a wrong answer.
func loadShellProbeCache(dir string) []shellProbeCacheEntry {
	path := shellProbeCachePath(dir)
	if path == "" {
		return nil
	}
	b, err := fileencoding.ReadFileUTF8(path)
	if err != nil {
		return nil
	}
	var f shellProbeCacheFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil
	}
	if f.Version != shellProbeCacheVersion || f.Host != shellProbeCacheHost() {
		return nil
	}
	out := make([]shellProbeCacheEntry, 0, len(f.Entries))
	for _, e := range f.Entries {
		if strings.TrimSpace(e.Path) != "" {
			out = append(out, e)
		}
	}
	return out
}

// saveShellProbeCache publishes entries atomically (temp file plus rename,
// mirroring the tool-probe snapshot write), so a crash mid-write can never
// leave a torn file behind and concurrent processes on the same machine see
// either the old or the complete new document.
func saveShellProbeCache(dir string, entries []shellProbeCacheEntry) {
	path := shellProbeCachePath(dir)
	if path == "" {
		return
	}
	b, err := json.Marshal(shellProbeCacheFile{
		Version: shellProbeCacheVersion,
		Host:    shellProbeCacheHost(),
		Entries: entries,
	})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".shell-probes.*.tmp")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return
	}
	if err := fileutil.ReplaceFile(tmpPath, path); err != nil {
		os.Remove(tmpPath)
	}
}

// primeShellProbeCache moves the persisted probe successes to the front of the
// bash candidate list so this pass verifies them first. The acceptance gate is
// unchanged — every primed path still goes through the same exists + probe
// checks as the route-derived candidates — so a stale entry costs one failed
// verification and then the full scan runs exactly as before. Only the Windows
// branch calls this: probeBash is a no-op passthrough elsewhere and there is
// no slow probe to save.
func (s *shellSnapshot) primeShellProbeCache() {
	dir := currentShellProbeCacheDir()
	if dir == "" {
		return
	}
	entries := loadShellProbeCache(dir)
	if len(entries) == 0 {
		return
	}
	// Most recently verified first, capped so a churned file cannot flood the
	// candidate list ahead of fresh route-derived candidates.
	sort.Slice(entries, func(i, j int) bool { return entries[i].ProbedAt.After(entries[j].ProbedAt) })
	if len(entries) > shellProbeCacheMaxEntries {
		entries = entries[:shellProbeCacheMaxEntries]
	}

	// Paths already route-derived this pass move to the front unchanged (their
	// recorded source already matches); entries outside the list join the front
	// with their persisted source. The untouched route-derived tail keeps its
	// own order, so a failed verification falls through to a scan identical to
	// the pre-tl1771 one.
	byLower := make(map[string]string, len(s.bashCands))
	for _, p := range s.bashCands {
		byLower[strings.ToLower(p)] = p
	}
	primedSet := make(map[string]bool, len(entries))
	front := make([]string, 0, len(entries))
	for _, e := range entries {
		key := strings.ToLower(e.Path)
		if primedSet[key] {
			continue
		}
		primedSet[key] = true
		if orig, ok := byLower[key]; ok {
			front = append(front, orig)
			continue
		}
		source := e.Source
		if source == "" {
			source = ShellSourcePath
		}
		s.sources[key] = source
		front = append(front, e.Path)
	}

	ordered := make([]string, 0, len(s.bashCands)+len(front))
	seen := make(map[string]bool, len(s.bashCands)+len(front))
	for _, p := range front {
		key := strings.ToLower(p)
		if seen[key] {
			continue
		}
		seen[key] = true
		ordered = append(ordered, p)
	}
	for _, p := range s.bashCands {
		key := strings.ToLower(p)
		if seen[key] {
			continue
		}
		seen[key] = true
		ordered = append(ordered, p)
	}
	s.bashCands = ordered

	if s.cachePrimed == nil {
		s.cachePrimed = map[string]bool{}
	}
	for key := range primedSet {
		s.cachePrimed[key] = true
	}
}

// refreshShellProbeCache rewrites the persisted file at the end of a discovery
// pass: every path this pass verified successfully becomes an entry (fresh
// successes included, so first detection persists immediately), while primed
// entries that failed verification are dropped — the natural eviction that
// keeps the cache from outliving the software it points at. A pass that found
// nothing writes an empty entry list, which the next start treats as no cache.
func (s *shellSnapshot) refreshShellProbeCache() {
	dir := currentShellProbeCacheDir()
	if dir == "" {
		return
	}
	entries := make([]shellProbeCacheEntry, 0, len(s.probeCache))
	for path, ok := range s.probeCache {
		if !ok {
			continue
		}
		key := strings.ToLower(path)
		source := s.sources[key]
		if source == "" {
			// Only paths outside the route-derived candidate list land here —
			// bash resolved from PATH by the decision table / capability walk.
			source = ShellSourcePath
		}
		entries = append(entries, shellProbeCacheEntry{
			Path:     path,
			Source:   source,
			ProbedAt: s.builtAt,
		})
	}
	// A primed path that never entered probeCache was rejected before probing
	// (the exists check) — verified dead this pass, so it is evicted by not
	// being carried over. No surviving-but-unchecked carryover exists: every
	// primed path is exists-checked by the capability walk, and an exists-hit
	// always probes.
	sort.Slice(entries, func(i, j int) bool { return entries[i].ProbedAt.After(entries[j].ProbedAt) })
	if len(entries) > shellProbeCacheMaxEntries {
		entries = entries[:shellProbeCacheMaxEntries]
	}
	saveShellProbeCache(dir, entries)
}

// noteProbeCacheHit logs the persistence payoff (验收：日志可见 cache-hit) —
// once per process per path, on the first successful probe of a primed path —
// and records the hit on the snapshot as the programmatic evidence.
func (s *shellSnapshot) noteProbeCacheHit(path string) {
	key := strings.ToLower(path)
	if !s.cachePrimed[key] {
		return
	}
	delete(s.cachePrimed, key) // log once per path per process
	s.cacheHits = append(s.cacheHits, path)
	slog.Info("sandbox: shell probe cache hit",
		"path", path,
		"source", s.sources[key],
		"hint", "discovery verified the persisted path first; full route scan skipped")
}
