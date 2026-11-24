package sandbox

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// tl1771 regression coverage: bash probe successes persist across restarts.
// The acceptance matrix (tl1771 任务书): a primed path verifies first and
// short-circuits discovery (cache hit, logged); a stale primed path is evicted
// naturally and the full three-route scan decides exactly as before; the
// persisted file is machine+user isolated; tl1763's 10s budget and pwsh Store
// alias candidate are untouched. All tests are deterministic — probe outcomes
// come from the pinned hook, candidate paths from temp trees or injected
// lookups.

// primeCacheDir points shell probe persistence at a fresh temp dir and
// registers cleanup that also drops the process-wide inventory snapshot, so a
// poisoned snapshot cannot leak into later tests in this process.
func primeCacheDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	SetShellProbeCacheDir(dir)
	t.Cleanup(func() {
		SetShellProbeCacheDir("")
		InvalidateShellInventory()
	})
	return dir
}

// writeShellProbeCacheFile marshals entries into the cache document the way
// saveShellProbeCache would, with an explicit host fingerprint.
func writeShellProbeCacheFile(t *testing.T, dir, host string, entries []shellProbeCacheEntry) {
	t.Helper()
	b, err := json.Marshal(shellProbeCacheFile{
		Version: shellProbeCacheVersion,
		Host:    host,
		Entries: entries,
	})
	if err != nil {
		t.Fatalf("marshal cache file: %v", err)
	}
	path := shellProbeCachePath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write cache file: %v", err)
	}
}

// readShellProbeCacheFile reads the persisted document back for assertions.
func readShellProbeCacheFile(t *testing.T, dir string) shellProbeCacheFile {
	t.Helper()
	b, err := os.ReadFile(shellProbeCachePath(dir))
	if err != nil {
		t.Fatalf("read cache file: %v", err)
	}
	var f shellProbeCacheFile
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("unmarshal cache file: %v", err)
	}
	return f
}

// captureSlog replaces the default logger with one writing into buf and
// returns a restore func.
func captureSlog(t *testing.T) (buf *bytes.Buffer, restore func()) {
	t.Helper()
	buf = &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	return buf, func() { slog.SetDefault(prev) }
}

// tl1771FakeSnapshot builds a shellSnapshot with injected lookups against a
// synthetic candidate list, so prime/refresh logic is testable without the
// machine's real PATH or registry.
func tl1771FakeSnapshot(lookPath map[string]string, exists func(string) bool, probe func(string) bool, bashCands []string) *shellSnapshot {
	return &shellSnapshot{
		key:      "tl1771-test",
		builtAt:  time.Now(),
		goos:     "windows",
		lookPath: fakeLookPath(lookPath),
		exists:   exists,
		isWSL:    func(string) bool { return false },
		probeFunc: func(p string) bool {
			if probe != nil {
				return probe(p)
			}
			return true
		},
		sources:    map[string]string{},
		probeCache: map[string]bool{},
		bashCands:  bashCands,
	}
}

// TestTL1771PrimedPathVerifiesFirst walks the unit-level lifecycle: a
// persisted success primes the front of the candidate list (reordering the
// route-derived list without growing or losing entries), the unchanged
// exists+probe gate verifies it, the capability binds it with the persisted
// source, and the refresh pass rewrites it with its source intact.
func TestTL1771PrimedPathVerifiesFirst(t *testing.T) {
	dir := primeCacheDir(t)
	root := t.TempDir()
	writeTree(t, filepath.Join(root, "Git"), filepath.Join("bin", "bash.exe"))
	live := filepath.Join(root, "Git", "bin", "bash.exe")

	// Route-derived candidates contain live mid-list; the persisted entry (the
	// same path, previously attributed git-derived) must be verified first.
	routeCands, routeSources := windowsBashCandidateSources("auto", "", []string{}, fakeLookPath(map[string]string{
		"git.exe": filepath.Join(root, "Git", "cmd", "git.exe"),
	}), fileExists)
	found := false
	for _, p := range routeCands {
		if strings.EqualFold(p, live) {
			found = true
		}
	}
	if !found {
		t.Fatalf("route candidates %v missing the live path %q", routeCands, live)
	}

	writeShellProbeCacheFile(t, dir, shellProbeCacheHost(), []shellProbeCacheEntry{
		{Path: live, Source: ShellSourceGitDerived, ProbedAt: time.Now().Add(-time.Hour)},
	})

	snap := tl1771FakeSnapshot(map[string]string{
		"git.exe": filepath.Join(root, "Git", "cmd", "git.exe"),
	}, fileExists, nil, append([]string(nil), routeCands...))
	snap.sources = routeSources

	snap.primeShellProbeCache()
	if len(snap.bashCands) == 0 || !strings.EqualFold(snap.bashCands[0], live) {
		t.Fatalf("after prime bashCands = %v, want the persisted path %q first", snap.bashCands, live)
	}
	if len(snap.bashCands) != len(routeCands) {
		t.Fatalf("primed list = %d entries, want the route-derived %d unchanged in size", len(snap.bashCands), len(routeCands))
	}
	if !snap.cachePrimed[strings.ToLower(live)] {
		t.Fatalf("primed path %q not marked in cachePrimed: %v", live, snap.cachePrimed)
	}

	caps := windowsShellCapabilities(snap)
	if len(caps) == 0 || !caps[0].Available || !strings.EqualFold(caps[0].Path, live) {
		t.Fatalf("git-bash capability = %+v, want available at the persisted path %q", caps, live)
	}
	if len(snap.cacheHits) != 1 || !strings.EqualFold(snap.cacheHits[0], live) {
		t.Fatalf("cacheHits = %v, want exactly the verified persisted path %q", snap.cacheHits, live)
	}
	if got := snap.sources[strings.ToLower(live)]; got != ShellSourceGitDerived {
		t.Fatalf("persisted source attribution = %q, want %q", got, ShellSourceGitDerived)
	}

	snap.refreshShellProbeCache()
	f := readShellProbeCacheFile(t, dir)
	if f.Host != shellProbeCacheHost() {
		t.Fatalf("refreshed file host = %q, want the local fingerprint", f.Host)
	}
	if len(f.Entries) != 1 || !strings.EqualFold(f.Entries[0].Path, live) || f.Entries[0].Source != ShellSourceGitDerived {
		t.Fatalf("refreshed entries = %+v, want exactly the live path via %s", f.Entries, ShellSourceGitDerived)
	}
}

// TestTL1771CacheHitLogAndProbeCount pins the observable cache hit (验收 1):
// the primed path is probed exactly once (short-circuit — the untouched tail
// is never probed), one structured "cache hit" log line names the path, and a
// memoized second probe of the same path does not log again.
func TestTL1771CacheHitLogAndProbeCount(t *testing.T) {
	dir := primeCacheDir(t)
	buf, restoreLog := captureSlog(t)
	defer restoreLog()

	live := filepath.Join(`C:\tl1771-hit`, "bin", "bash.exe")
	writeShellProbeCacheFile(t, dir, shellProbeCacheHost(), []shellProbeCacheEntry{
		{Path: live, Source: ShellSourceRegistry, ProbedAt: time.Now().Add(-time.Hour)},
	})

	var probes atomic.Int64
	snap := tl1771FakeSnapshot(map[string]string{}, func(p string) bool { return strings.EqualFold(p, live) },
		func(string) bool {
			probes.Add(1)
			return true
		},
		[]string{
			filepath.Join(`C:\tl1771-a`, "bash.exe"),
			live,
			filepath.Join(`C:\tl1771-b`, "bash.exe"),
			filepath.Join(`C:\tl1771-c`, "bash.exe"),
		})

	snap.primeShellProbeCache()
	caps := windowsShellCapabilities(snap)
	if len(caps) == 0 || !caps[0].Available || !strings.EqualFold(caps[0].Path, live) {
		t.Fatalf("capability = %+v, want the primed path %q", caps, live)
	}
	if got := probes.Load(); got != 1 {
		t.Fatalf("probe calls = %d, want exactly 1 (primed path short-circuits the walk)", got)
	}
	logged := buf.String()
	if !strings.Contains(logged, "cache hit") || !strings.Contains(logged, live) {
		t.Fatalf("slog output missing a cache-hit line for %q:\n%s", live, logged)
	}
	if n := strings.Count(logged, "cache hit"); n != 1 {
		t.Fatalf("cache-hit log lines = %d, want 1:\n%s", n, logged)
	}
	// Memoized re-probe: same snapshot, no new probe call, no new log line.
	if !snap.probe(live) {
		t.Fatal("memoized probe of the verified path returned false")
	}
	if got := probes.Load(); got != 1 {
		t.Fatalf("probe calls after memoized re-probe = %d, want still 1", got)
	}
	if n := strings.Count(buf.String(), "cache hit"); n != 1 {
		t.Fatalf("cache-hit log lines after memoized re-probe = %d, want still 1", n)
	}
}

// TestTL1771StaleCacheEvictsAndFallsBackToFullScan is the eviction half of the
// contract (验收 2): a primed path that no longer exists (uninstalled Git
// Bash) fails verification, the full route scan runs untouched, the report
// stays truthful (no false positive), and the refresh pass drops the dead
// entry.
func TestTL1771StaleCacheEvictsAndFallsBackToFullScan(t *testing.T) {
	dir := primeCacheDir(t)
	root := t.TempDir()
	dead := filepath.Join(root, "gone", "Git", "bin", "bash.exe")
	writeShellProbeCacheFile(t, dir, shellProbeCacheHost(), []shellProbeCacheEntry{
		{Path: dead, Source: ShellSourceStandard, ProbedAt: time.Now().Add(-time.Hour)},
	})

	var probes atomic.Int64
	restore := PinShellProbeForTest(func(string) bool {
		probes.Add(1)
		return false // every route fails: the all-routes-not-installed world
	})
	defer restore()
	InvalidateShellInventory()

	snap := buildShellSnapshot("windows", "", "")
	if !snap.cachePrimed[strings.ToLower(dead)] {
		t.Fatalf("dead path %q was not primed (cache load failed)", dead)
	}
	if len(snap.caps) == 0 || snap.caps[0].Available || snap.caps[0].Reason != "not-installed" {
		t.Fatalf("git-bash capability = %+v, want unavailable with reason not-installed (no false positive)", snap.caps)
	}
	if got := probes.Load(); got == 0 {
		t.Fatal("probe calls = 0, want the full route scan to have run")
	}
	f := readShellProbeCacheFile(t, dir)
	for _, e := range f.Entries {
		if strings.EqualFold(e.Path, dead) {
			t.Fatalf("dead path %q survived the refresh pass: %+v", dead, f.Entries)
		}
	}
}

// TestTL1771StaleCacheEvictedFreshPathRefreshed pins the self-healing write:
// among primed entries the dead one fails verification while the live one
// verifies and carries the capability — the refresh pass keeps exactly the
// verified set, so the next boot reads the healed file.
func TestTL1771StaleCacheEvictedFreshPathRefreshed(t *testing.T) {
	dir := primeCacheDir(t)
	root := t.TempDir()
	writeTree(t, filepath.Join(root, "Git"), filepath.Join("bin", "bash.exe"))
	live := filepath.Join(root, "Git", "bin", "bash.exe")
	dead := filepath.Join(root, "gone", "bash.exe")
	writeShellProbeCacheFile(t, dir, shellProbeCacheHost(), []shellProbeCacheEntry{
		{Path: dead, Source: ShellSourceStandard, ProbedAt: time.Now().Add(-2 * time.Hour)},
		{Path: live, Source: ShellSourceStandard, ProbedAt: time.Now().Add(-1 * time.Hour)},
	})

	var probedLive bool
	restore := PinShellProbeForTest(func(p string) bool {
		if strings.EqualFold(p, live) {
			probedLive = true
			return true
		}
		return false
	})
	defer restore()
	InvalidateShellInventory()

	snap := buildShellSnapshot("windows", "", "")
	if len(snap.caps) == 0 || !snap.caps[0].Available || !strings.EqualFold(snap.caps[0].Path, live) {
		t.Fatalf("capability = %+v, want the live path %q after eviction", snap.caps, live)
	}
	if !probedLive {
		t.Fatalf("live path %q was never probed (priming lost it)", live)
	}
	f := readShellProbeCacheFile(t, dir)
	foundLive, foundDead := false, false
	for _, e := range f.Entries {
		if strings.EqualFold(e.Path, dead) {
			foundDead = true
		}
		if strings.EqualFold(e.Path, live) {
			foundLive = true
			if e.Source == "" {
				t.Fatalf("refreshed live entry lost its source: %+v", e)
			}
		}
	}
	if foundDead || !foundLive {
		t.Fatalf("refreshed entries = %+v, want live %q kept and dead %q evicted", f.Entries, live, dead)
	}
}

// TestTL1771CacheHostIsolation is the cross-environment guard (需求 4): a file
// written under another machine+user fingerprint — or a corrupt or
// future-version document — is discarded whole, so its paths never prime and
// never enter the candidate list.
func TestTL1771CacheHostIsolation(t *testing.T) {
	dir := primeCacheDir(t)
	foreign := filepath.Join(`C:\tl1771-foreign`, "bash.exe")

	seed := func(t *testing.T) {
		t.Helper()
		os.Remove(shellProbeCachePath(dir))
	}
	verifyDiscarded := func(t *testing.T, name string) {
		t.Helper()
		if got := loadShellProbeCache(dir); len(got) != 0 {
			t.Fatalf("%s: loadShellProbeCache = %+v, want the whole file discarded", name, got)
		}
		restore := PinShellProbeForTest(func(p string) bool { return strings.EqualFold(p, foreign) })
		defer restore()
		InvalidateShellInventory()
		snap := buildShellSnapshot("windows", "", "")
		for _, p := range snap.bashCands {
			if strings.EqualFold(p, foreign) {
				t.Fatalf("%s: foreign path %q primed into candidates %v", name, foreign, snap.bashCands)
			}
		}
	}

	t.Run("foreign-host", func(t *testing.T) {
		seed(t)
		writeShellProbeCacheFile(t, dir, "other-machine\x00otheruser", []shellProbeCacheEntry{
			{Path: foreign, Source: ShellSourceStandard, ProbedAt: time.Now()},
		})
		verifyDiscarded(t, "foreign-host")
	})
	t.Run("corrupt-json", func(t *testing.T) {
		seed(t)
		if err := os.WriteFile(shellProbeCachePath(dir), []byte("{not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		verifyDiscarded(t, "corrupt-json")
	})
	t.Run("future-version", func(t *testing.T) {
		seed(t)
		writeShellProbeCacheFile(t, dir, shellProbeCacheHost(), []shellProbeCacheEntry{
			{Path: foreign, Source: ShellSourceStandard, ProbedAt: time.Now()},
		})
		f := readShellProbeCacheFile(t, dir)
		f.Version = shellProbeCacheVersion + 1
		bb, err := json.Marshal(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(shellProbeCachePath(dir), bb, 0o644); err != nil {
			t.Fatal(err)
		}
		verifyDiscarded(t, "future-version")
	})

	// The positive half: a saved file carries the local fingerprint and loads.
	entries := []shellProbeCacheEntry{{Path: foreign, Source: ShellSourceStandard, ProbedAt: time.Now()}}
	saveShellProbeCache(dir, entries)
	if got := loadShellProbeCache(dir); len(got) != 1 || !strings.EqualFold(got[0].Path, foreign) {
		t.Fatalf("local save/load roundtrip = %+v, want the saved entry", got)
	}
}

// TestTL1771PersistenceDisabledWithoutCacheDir pins the off switch: without a
// usable cache dir (boot could not resolve one; tests that opted out), prime
// and refresh are no-ops and discovery is byte-identical to pre-tl1771.
func TestTL1771PersistenceDisabledWithoutCacheDir(t *testing.T) {
	t.Cleanup(func() { SetShellProbeCacheDir("") })
	SetShellProbeCacheDir("   ") // whitespace-only folds to disabled
	if dir := currentShellProbeCacheDir(); dir != "" {
		t.Fatalf("currentShellProbeCacheDir = %q, want empty after whitespace folding", dir)
	}

	live := filepath.Join(`C:\tl1771-off`, "bin", "bash.exe")
	snap := tl1771FakeSnapshot(map[string]string{}, fileExists, nil, []string{live})
	before := append([]string(nil), snap.bashCands...)
	snap.primeShellProbeCache()
	if len(snap.cachePrimed) != 0 {
		t.Fatalf("cachePrimed = %v, want empty with persistence disabled", snap.cachePrimed)
	}
	for i := range before {
		if before[i] != snap.bashCands[i] {
			t.Fatalf("candidate order changed under disabled persistence: %v -> %v", before, snap.bashCands)
		}
	}
	snap.refreshShellProbeCache() // must not create anything, anywhere
}

// TestTL1771PrimedOrderAndAttribution pins two contract details: the primed
// entries keep their persisted source attribution (so the settings surface
// explains the winner the same way it did when the path was first found), the
// primed subset is ordered by most-recent verification, and the route-derived
// tail keeps its order behind them.
func TestTL1771PrimedOrderAndAttribution(t *testing.T) {
	dir := primeCacheDir(t)
	now := time.Now()
	a := filepath.Join(`C:\tl1771-a`, "bin", "bash.exe")
	b := filepath.Join(`C:\tl1771-b`, "usr", "bin", "bash.exe")
	writeShellProbeCacheFile(t, dir, shellProbeCacheHost(), []shellProbeCacheEntry{
		{Path: a, Source: ShellSourceGitDerived, ProbedAt: now.Add(-2 * time.Hour)}, // older
		{Path: b, Source: ShellSourceRegistry, ProbedAt: now.Add(-1 * time.Hour)},   // newer
	})

	tail := filepath.Join(`C:\tl1771-tail`, "bash.exe")
	snap := tl1771FakeSnapshot(map[string]string{}, func(string) bool { return false }, nil, []string{tail})

	snap.primeShellProbeCache()
	if len(snap.bashCands) != 3 {
		t.Fatalf("bashCands = %v, want 2 primed + 1 tail", snap.bashCands)
	}
	if !strings.EqualFold(snap.bashCands[0], b) || !strings.EqualFold(snap.bashCands[1], a) {
		t.Fatalf("primed order = %v, want newest-verified first (b then a)", snap.bashCands)
	}
	if got := snap.sources[strings.ToLower(b)]; got != ShellSourceRegistry {
		t.Fatalf("primed source for b = %q, want %q", got, ShellSourceRegistry)
	}
	if got := snap.sources[strings.ToLower(a)]; got != ShellSourceGitDerived {
		t.Fatalf("primed source for a = %q, want %q", got, ShellSourceGitDerived)
	}
	if !strings.EqualFold(snap.bashCands[2], tail) {
		t.Fatalf("route-derived tail disturbed: %v (want %q last)", snap.bashCands, tail)
	}
}

// TestTL1771RefreshCapBoundsEntries pins the entry cap: a pathological probe
// cache cannot grow the persisted file past shellProbeCacheMaxEntries.
func TestTL1771RefreshCapBoundsEntries(t *testing.T) {
	dir := primeCacheDir(t)
	snap := &shellSnapshot{
		key:        "tl1771-cap",
		builtAt:    time.Now(),
		sources:    map[string]string{},
		probeCache: map[string]bool{},
	}
	for i := 0; i < shellProbeCacheMaxEntries+4; i++ {
		p := filepath.Join(`C:\tl1771-cap`, "d"+strconv.Itoa(i), "bash.exe")
		snap.probeCache[p] = true
	}
	snap.refreshShellProbeCache()
	f := readShellProbeCacheFile(t, dir)
	if len(f.Entries) != shellProbeCacheMaxEntries {
		t.Fatalf("persisted entries = %d, want the cap %d", len(f.Entries), shellProbeCacheMaxEntries)
	}
}

// TestTL1771PowerShellCandidatesUntouchedByCache guards验收 3's pwsh half: the
// tl1763 Store alias ordering is produced by windowsPowerShellCandidates
// exactly as before — the persistence wiring shares no state with it.
func TestTL1771PowerShellCandidatesUntouchedByCache(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root,
		filepath.Join("PowerShell", "7", "pwsh.exe"),
		filepath.Join("Microsoft", "WindowsApps", "pwsh.exe"),
	)
	t.Setenv("ProgramFiles", root)
	t.Setenv("ProgramW6432", root)
	t.Setenv("ProgramFiles(x86)", filepath.Join(root, "x86"))
	t.Setenv("LOCALAPPDATA", root)
	t.Setenv("SystemRoot", filepath.Join(root, "windows"))

	dir := primeCacheDir(t)
	writeShellProbeCacheFile(t, dir, shellProbeCacheHost(), []shellProbeCacheEntry{
		{Path: filepath.Join(`C:\elsewhere`, "bash.exe"), Source: ShellSourceStandard, ProbedAt: time.Now()},
	})

	// The pwsh candidate list is what this test asserts; pin the bash probe so
	// the machine's real msys cold start cannot dominate the runtime.
	restore := PinShellProbeForTest(func(string) bool { return true })
	defer restore()
	InvalidateShellInventory()

	snap := buildShellSnapshot("windows", "", "")
	wantMSI := filepath.Join(root, "PowerShell", "7", "pwsh.exe")
	wantAlias := filepath.Join(root, "Microsoft", "WindowsApps", "pwsh.exe")
	wantInbox := filepath.Join(root, "windows", "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	msiIdx, aliasIdx, inboxIdx := -1, -1, -1
	for i, p := range snap.psCands {
		switch {
		case strings.EqualFold(p, wantMSI):
			msiIdx = i
		case strings.EqualFold(p, wantAlias):
			aliasIdx = i
		case strings.EqualFold(p, wantInbox):
			inboxIdx = i
		}
	}
	if msiIdx < 0 || aliasIdx < 0 || inboxIdx < 0 || !(msiIdx < aliasIdx && aliasIdx < inboxIdx) {
		t.Fatalf("pwsh candidates under cache wiring = %v, want MSI < Store alias < inbox", snap.psCands)
	}
}

// TestTL1771EmptyProbeCacheWritesEmptyFile pins the nothing-found world: a
// discovery pass that verifies nothing writes an empty entry list, so the next
// start treats the cache as absent instead of retrying dead paths forever.
func TestTL1771EmptyProbeCacheWritesEmptyFile(t *testing.T) {
	dir := primeCacheDir(t)
	snap := &shellSnapshot{
		key:        "tl1771-empty",
		builtAt:    time.Now(),
		sources:    map[string]string{},
		probeCache: map[string]bool{"C:\\tl1771-dead\\bash.exe": false},
	}
	snap.refreshShellProbeCache()
	f := readShellProbeCacheFile(t, dir)
	if len(f.Entries) != 0 {
		t.Fatalf("entries = %+v, want an empty list after a nothing-found pass", f.Entries)
	}
	if got := loadShellProbeCache(dir); len(got) != 0 {
		t.Fatalf("reload = %+v, want no primable entries", got)
	}
}

// TestTL1771ProbeBudgetGuardStillTenSeconds restates the tl1763 guard next to
// the persistence wiring so a future edit cannot quietly trade the budget back
// while touching this file.
func TestTL1771ProbeBudgetGuardStillTenSeconds(t *testing.T) {
	if shellProbeBashTimeout != 10*time.Second {
		t.Fatalf("shellProbeBashTimeout = %v, want the tl1763 10s budget unchanged", shellProbeBashTimeout)
	}
}
