package main

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"reasonix/internal/config"
)

// Task 609: the effort read path (EffortForTab → effortForTabDirect →
// currentProviderEntryForTab) used to run a full config.LoadForRoot on every
// call. LoadForRoot is not a plain file read: it loads the credential store,
// resolves config access twice, runs the on-disk legacy-key migrations (which
// rewrite files when they fire), and merges user + project TOML. Task 421
// field telemetry measured the read at 431-1350ms normally with multi-second
// outliers whenever the disk or an antivirus scan was busy — which is exactly
// when a tab switch awaits the read, and why "effort read timed out" kept
// firing after task 148 covered only the switch path and task 421 only capped
// the read.
//
// cachedConfigForRoot gives those read paths an in-memory snapshot instead:
// the first call for a workspace root — or the first call after a tracked
// config file changes — pays one LoadForRoot, and every later call is a
// couple of os.Stat freshness checks. The tracked sources are the files an
// effort-relevant writer touches: the user config and the project
// reasonix.toml (applyConfigChange writes the former, project-owned skill
// edits the latter; both are where providers, default_model and
// provider_access live). What the loader reads beyond those — credential
// store, .env expansion, legacy fallback paths — is deliberately not tracked:
// the effort display derives from provider entries and capability tables,
// which those files do not move. LoadForRoot stays the loader, so the on-disk
// migration still runs (once per real change) instead of being skipped.
//
// The snapshot serves reads only. Every config write keeps going through
// applyConfigChange / LoadForEdit*, which reload from disk; the stamped
// mtime/size pair then invalidates the snapshot before the next read, so an
// effort or provider change shows up on the very next EffortForTab.

// configFileStamp is the os.Stat fingerprint of one tracked config source.
type configFileStamp struct {
	exists bool
	size   int64
	mtime  time.Time
}

// configSnapshot is the cached LoadForRoot result for one workspace root plus
// the fingerprints captured just before that load.
type configSnapshot struct {
	mu     sync.Mutex
	cfg    *config.Config
	loaded bool
	// reloads counts completed full loads since the snapshot was created.
	// Observability only (task 639 breakdown line distinguishes a warm hit
	// from a load the read had to pay); guarded by mu like the rest.
	reloads uint64
	// sources and stamps are captured before the load, so a file replaced
	// while it was being read no longer matches and forces a reload on the
	// next call instead of serving a snapshot assembled from torn state.
	sources []string
	stamps  []configFileStamp
}

// configLoadForRoot is the snapshot's loader indirection: tests wrap it to
// count how often the read path really hits the disk (task 609 acceptance:
// zero full loads on warm reads).
var configLoadForRoot = config.LoadForRoot

// snapshotTiming is the task-889 split of one snapshot access: wait is the
// time to acquire the per-root mutex (a reader queued behind a sibling's
// slow load), stat the freshness fingerprints (os.Stat over the tracked
// sources), load the configLoadForRoot call itself (0 when warm). The
// effort-read breakdown carries all three, so a slow snapshot_ms answers
// "disk stall on the stat, a sibling's load, or my own reload" instead of
// just "snapshot was slow".
type snapshotTiming struct {
	wait time.Duration
	stat time.Duration
	load time.Duration
}

// cachedConfigForRoot returns the config snapshot for root, reloading it when
// any tracked source changed since the cached load. Same-root callers
// serialize on the snapshot's own lock, so a slow load delays only that
// root's readers — and EffortForTab still bounds the wait with its timeout
// cap (task 421). Task 889 keeps it as the untimed wrapper for callers that
// only want the config.
func (a *App) cachedConfigForRoot(root string) (*config.Config, error) {
	cfg, _, err := a.cachedConfigForRootTimed(root, nil)
	return cfg, err
}

// cachedConfigForRootTimed is the task-889 instrumented form: same snapshot
// semantics plus the wait/stat/load timing for the effort-read breakdown and
// an optional mark callback announcing each sub-step to the tab's
// effort-read phase trail (nil for callers without a trail, e.g. tests and
// non-effort reads — a nil mark costs nothing).
func (a *App) cachedConfigForRootTimed(root string, mark func(segment string)) (*config.Config, snapshotTiming, error) {
	key := filepath.Clean(root)
	a.cfgSnapshotsMu.Lock()
	if a.cfgSnapshots == nil {
		a.cfgSnapshots = make(map[string]*configSnapshot)
	}
	snap := a.cfgSnapshots[key]
	if snap == nil {
		snap = &configSnapshot{}
		a.cfgSnapshots[key] = snap
	}
	a.cfgSnapshotsMu.Unlock()
	return snap.getTimed(root, mark)
}

// get serves the cached config while every tracked source fingerprint still
// matches, and reloads exactly once otherwise.
func (s *configSnapshot) get(root string) (*config.Config, error) {
	cfg, _, err := s.getTimed(root, nil)
	return cfg, err
}

// getTimed is the task-889 instrumented get: identical freshness/reload
// semantics, with the mutex-wait / fingerprint-stat / load durations split
// out (snapshotTiming) and each sub-step announced through mark when the
// caller has one (the effort read's phase trail, so a 2s timeout can say
// snapshot.load instead of just snapshot).
func (s *configSnapshot) getTimed(root string, mark func(segment string)) (*config.Config, snapshotTiming, error) {
	var timing snapshotTiming
	markSeg := func(segment string) {
		if mark != nil {
			mark(segment)
		}
	}
	waitStart := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	timing.wait = time.Since(waitStart)
	markSeg("stat")
	statStart := time.Now()
	sources, stamps := configSnapshotSources(root)
	timing.stat = time.Since(statStart)
	if s.loaded && configStampsUnchanged(s.sources, s.stamps, sources, stamps) {
		return s.cfg, timing, nil
	}
	markSeg("load")
	loadStart := time.Now()
	cfg, err := configLoadForRoot(root)
	timing.load = time.Since(loadStart)
	if err != nil {
		// Preserve today's read-path behavior: surface the load error and
		// keep the stale stamps so the next read retries the reload.
		return nil, timing, err
	}
	s.sources, s.stamps, s.cfg, s.loaded = sources, stamps, cfg, true
	s.reloads++
	return cfg, timing, nil
}

// cfgSnapshotReloadCount reports how many full loads the root's snapshot has
// performed (0 before the snapshot exists). Task 639: lets the effort read
// breakdown distinguish a warm snapshot hit from a load paid on this read.
func (a *App) cfgSnapshotReloadCount(root string) uint64 {
	a.cfgSnapshotsMu.Lock()
	snap := a.cfgSnapshots[filepath.Clean(root)]
	a.cfgSnapshotsMu.Unlock()
	if snap == nil {
		return 0
	}
	snap.mu.Lock()
	defer snap.mu.Unlock()
	return snap.reloads
}

// configSnapshotSources mirrors loadForRoot's file selection for the sources
// that can move effort-relevant config, fingerprinting each one. Mirrors, not
// reuses: the config package keeps its path resolution unexported, and this
// list must stay limited to what a snapshot reader may wait on.
func configSnapshotSources(root string) ([]string, []configFileStamp) {
	projectTOML := "reasonix.toml"
	if cleaned := filepath.Clean(root); cleaned != "" && cleaned != "." {
		projectTOML = filepath.Join(cleaned, "reasonix.toml")
	}
	paths := []string{config.UserConfigPath(), projectTOML}
	stamps := make([]configFileStamp, len(paths))
	for i, path := range paths {
		stamps[i] = configFileStampOf(path)
	}
	return paths, stamps
}

func configFileStampOf(path string) configFileStamp {
	info, err := os.Stat(path)
	if err != nil {
		return configFileStamp{}
	}
	return configFileStamp{exists: true, size: info.Size(), mtime: info.ModTime()}
}

func configStampsUnchanged(oldSources []string, oldStamps []configFileStamp, sources []string, stamps []configFileStamp) bool {
	if len(oldSources) != len(sources) || len(oldStamps) != len(stamps) {
		return false
	}
	for i := range sources {
		if oldSources[i] != sources[i] || oldStamps[i] != stamps[i] {
			return false
		}
	}
	return true
}
