package main

import (
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

// Task 691: resolveSessionBinding walked the full session-directory list on
// EVERY call — per-dir session path validation (Lstat + two EvalSymlinks
// directory walks per hit candidate) plus per-hit branch-meta sidecar reads
// and project-registry re-reads (legacyMigrationTargetForDir). Field
// telemetry (desktop.log 2026-10-09 20:43, 18:35) shows one tab switch firing
// 30-70 of these walks concurrently (scan_ms 100-170 each under disk
// pressure, 1.4s spikes when an antivirus scan was active) — the storm behind
// the "switch-tab:ancillary context" 0.9-2.0s leg and part of the meta leg.
// The task-639 memo only gated the effort read; every other reconcile caller
// paid the walk again.
//
// This cache memoizes the walk per session path. Freshness follows the
// task-609/639 stamp convention: an entry is served only while every input
// that can move the outcome still fingerprints unchanged —
//   - the project registry + organization sidecar (knownSessionDirs's dir
//     list, legacyMigrationTargetForDir's answers),
//   - the in-memory tab-derived directory set (open/detached tabs contribute
//     directories the registry does not know),
//   - the exact file paths whose existence/content decided the last walk
//     (candidate probes per directory, the matched session file, its
//     branch-meta sidecar).
// Any mismatch re-runs the walk once and re-stamps, so a session that moved,
// grew a sidecar, or changed project membership still heals on the next
// resolve. Concurrent callers of the same path join one walk (single flight)
// instead of stampeding — that is the storm half of the fix.
//
// Deliberately not tracked (same reasoning as task 609): directory mtimes,
// unrelated session files, and the branch-meta contents of candidate paths
// that never validated — none of them can move a resolved binding without
// also moving a stamped input.

// bindingResolveKey fingerprints every input behind one cached resolve. The
// stamps use the configFileStamp (exists/size/mtime) convention from
// config_snapshot.go, so an absent file and a present file never compare
// equal.
type bindingResolveKey struct {
	projects configFileStamp
	org      configFileStamp
	tabDirs  string
}

// bindingFileStamp pins one file path the walk consulted to its then-current
// fingerprint.
type bindingFileStamp struct {
	path  string
	stamp configFileStamp
}

// bindingResolveEntry is the cached outcome of one walk plus the fingerprints
// that were true when it ran.
type bindingResolveEntry struct {
	key     bindingResolveKey
	ok      bool
	binding sessionBinding
	probed  []bindingFileStamp
}

// bindingResolveFlight is the single-flight slot concurrent callers join
// while one walk is running.
type bindingResolveFlight struct {
	done    chan struct{}
	binding sessionBinding
	ok      bool
}

// bindingResolveCache is the App-level memo. Its mutex is its own (never
// a.mu): the warm path takes it for map access only, and walks run entirely
// outside it so one slow disk never blocks unrelated resolves.
type bindingResolveCache struct {
	mu       sync.Mutex
	entries  map[string]*bindingResolveEntry
	flights  map[string]*bindingResolveFlight
	walks    uint64
	walkHook func() // test-only, fires at the top of every real walk
}

// bindingResolveCacheCapacity bounds the memo. The working set is the number
// of distinct session paths the process resolves (open tabs, history panels,
// recovery flows) — tens, not thousands. On overflow the map resets: entries
// rebuild on the next resolve at one walk each, which is the pre-cache cost.
const bindingResolveCacheCapacity = 256

// bindingResolveKeyPath canonicalizes the map key: same file spelled with
// different separators or case (Windows) must share one entry.
func bindingResolveKeyPath(sessionPath string) string {
	path := filepath.Clean(sessionPath)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

// bindingTabDirsFingerprint captures the tab-derived part of
// knownSessionDirs: open and detached tabs contribute directories the
// registry does not list. Purely in-memory (tab fields + controller state
// read under a.mu.RLock, the same lock knownSessionDirs holds).
func (a *App) bindingTabDirsFingerprint() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var b strings.Builder
	for _, tab := range a.tabs {
		b.WriteString(tabSessionDir(tab))
		b.WriteByte(0)
	}
	for _, tab := range a.detachedSessions {
		b.WriteString(tabSessionDir(tab))
		b.WriteByte(0)
	}
	return b.String()
}

// bindingResolveKeyOf snapshots the key inputs for one session path. Callers
// must not hold a.mu or the cache mutex.
func (a *App) bindingResolveKeyOf(sessionPath string) bindingResolveKey {
	return bindingResolveKey{
		projects: configFileStampOf(filepath.Join(desktopConfigDir(), desktopProjectsFile)),
		org:      configFileStampOf(filepath.Join(desktopConfigDir(), desktopProjectOrganizationFile)),
		tabDirs:  a.bindingTabDirsFingerprint(),
	}
}

// bindingResolveLookup serves the cached walk while the entry's key matches
// and every probed file still fingerprints unchanged. fresh=false means the
// caller must run the walk.
func (a *App) bindingResolveLookup(key bindingResolveKey, cachePath string) (sessionBinding, bool, bool) {
	a.bindingResolve.mu.Lock()
	entry := a.bindingResolve.entries[cachePath]
	var probed []bindingFileStamp
	var binding sessionBinding
	var ok, known bool
	if entry != nil && entry.key == key {
		probed = entry.probed
		binding, ok, known = entry.binding, entry.ok, true
	}
	a.bindingResolve.mu.Unlock()
	if !known {
		return sessionBinding{}, false, false
	}
	for _, f := range probed {
		if configFileStampOf(f.path) != f.stamp {
			return sessionBinding{}, false, false
		}
	}
	return binding, ok, true
}

// bindingResolveJoinFlight returns the in-flight walk for cachePath to wait
// on, or creates one and reports created=true (the caller then runs the walk
// and completes the flight).
func (a *App) bindingResolveJoinFlight(cachePath string) (flight *bindingResolveFlight, created bool) {
	a.bindingResolve.mu.Lock()
	defer a.bindingResolve.mu.Unlock()
	if a.bindingResolve.flights == nil {
		a.bindingResolve.flights = map[string]*bindingResolveFlight{}
	}
	if f, ok := a.bindingResolve.flights[cachePath]; ok {
		return f, false
	}
	f := &bindingResolveFlight{done: make(chan struct{})}
	a.bindingResolve.flights[cachePath] = f
	return f, true
}

// bindingResolveCompleteFlight publishes the walk result to joiners and
// removes the flight slot.
func (a *App) bindingResolveCompleteFlight(cachePath string, binding sessionBinding, ok bool) {
	a.bindingResolve.mu.Lock()
	f := a.bindingResolve.flights[cachePath]
	delete(a.bindingResolve.flights, cachePath)
	a.bindingResolve.mu.Unlock()
	if f != nil {
		f.binding, f.ok = binding, ok
		close(f.done)
	}
}

// bindingResolveStore records one completed walk under its key. The walk's
// probe list is the entry's freshness contract, so it must be non-nil even
// for walks that consulted nothing unusual.
func (a *App) bindingResolveStore(key bindingResolveKey, cachePath string, binding sessionBinding, ok bool, probed []bindingFileStamp) {
	a.bindingResolve.mu.Lock()
	defer a.bindingResolve.mu.Unlock()
	if a.bindingResolve.entries == nil {
		a.bindingResolve.entries = map[string]*bindingResolveEntry{}
	}
	if len(a.bindingResolve.entries) >= bindingResolveCacheCapacity {
		a.bindingResolve.entries = map[string]*bindingResolveEntry{}
	}
	a.bindingResolve.entries[cachePath] = &bindingResolveEntry{key: key, ok: ok, binding: binding, probed: probed}
}

// resolveSessionBinding resolves a session path to its saved binding,
// serving repeat and concurrent calls from the task-691 cache (see the file
// comment). The walk itself lives in resolveSessionBindingWalk unchanged.
func (a *App) resolveSessionBinding(sessionPath string) (sessionBinding, bool) {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return sessionBinding{}, false
	}
	cachePath := bindingResolveKeyPath(sessionPath)
	key := a.bindingResolveKeyOf(sessionPath)
	if binding, ok, fresh := a.bindingResolveLookup(key, cachePath); fresh {
		return binding, ok
	}
	flight, created := a.bindingResolveJoinFlight(cachePath)
	if !created {
		<-flight.done
		return flight.binding, flight.ok
	}
	// The walk is the cache miss cost; a concurrent registry change between
	// our key snapshot and the walk only costs key freshness, not
	// correctness — the stored entry revalidates its own key on lookup. The
	// flight is completed even if the walk panics, so joiners never hang.
	defer func() {
		a.bindingResolveCompleteFlight(cachePath, flight.binding, flight.ok)
	}()
	a.bindingResolve.mu.Lock()
	a.bindingResolve.walks++
	hook := a.bindingResolve.walkHook
	a.bindingResolve.mu.Unlock()
	if hook != nil {
		hook()
	}
	binding, ok, probed := a.resolveSessionBindingWalk(sessionPath)
	a.bindingResolveStore(key, cachePath, binding, ok, probed)
	flight.binding, flight.ok = binding, ok
	return binding, ok
}

// bindingWalkProbe appends a probed path once.
func bindingWalkProbe(probed []bindingFileStamp, path string) []bindingFileStamp {
	for _, f := range probed {
		if sameDesktopPath(f.path, path) {
			return probed
		}
	}
	return append(probed, bindingFileStamp{path: filepath.Clean(path), stamp: configFileStampOf(path)})
}

// bindingProbeCandidate reports the file validateSessionPath would Lstat for
// (dir, sessionPath) when its cheap pre-checks pass — the path whose
// existence decides this directory's answer. best effort: a false ok just
// means the directory rejects the path before any stat, nothing to record.
func bindingProbeCandidate(dir, sessionPath string) (string, bool) {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" || dir == "" {
		return "", false
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	path := sessionPath
	if !filepath.IsAbs(path) {
		path = filepath.Join(absDir, path)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	if !store.IsSessionTranscriptName(filepath.Base(absPath)) {
		return "", false
	}
	rel, err := filepath.Rel(absDir, absPath)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", false
	}
	return absPath, true
}

// bindingRecordMeta stamps the branch-meta sidecar a resolved binding was
// read from, so a sidecar rewrite (topic rename, scope change) invalidates
// the entry.
func bindingRecordMeta(probed []bindingFileStamp, sessionFile string) []bindingFileStamp {
	if metaPath := agent.BranchMetaPath(sessionFile); metaPath != "" {
		probed = bindingWalkProbe(probed, metaPath)
	}
	return probed
}

// bindingResolveInDir is sessionBindingInDir with probe recording threaded
// through: the walk's outcome for this directory depends on the candidate
// file's fingerprint (validated or absent) and, on a hit, its sidecar.
func sessionBindingInDirTracked(dir, sessionPath string, record func(string)) (sessionBinding, bool) {
	if candidate, ok := bindingProbeCandidate(dir, sessionPath); ok {
		record(candidate)
	}
	binding, ok := sessionBindingInDir(dir, sessionPath)
	if ok {
		record(binding.path)
	}
	return binding, ok
}
