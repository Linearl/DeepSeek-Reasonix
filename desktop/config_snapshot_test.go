package main

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/event"
)

// snapshotTestConfig builds a config with one provider whose effort vocabulary
// is declared via supported_efforts, so EffortForTab's capability mapping is
// fully determined by the user config file content.
func snapshotTestConfig(supportedEfforts ...string) *config.Config {
	cfg := config.Default()
	cfg.DefaultModel = "snap/snap-model"
	cfg.Desktop.ProviderAccess = []string{"snap"}
	cfg.Providers = []config.ProviderEntry{{
		Name:             "snap",
		Kind:             "openai",
		BaseURL:          "https://example.invalid/v1",
		Model:            "snap-model",
		APIKeyEnv:        "SNAP_MODEL_KEY",
		SupportedEfforts: supportedEfforts,
	}}
	return cfg
}

// newSnapshotEffortFixture isolates the user dirs, writes the given provider
// config, and registers a tab whose model ref resolves against it. The
// returned counter wraps configLoadForRoot so tests can assert exactly how
// often the effort read path really loads the config from disk.
func newSnapshotEffortFixture(t *testing.T, cfg *config.Config) (*App, *WorkspaceTab, *atomic.Int32) {
	t.Helper()
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "SNAP_MODEL_KEY", "sk-test")
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()
	tab := &WorkspaceTab{
		ID:          "tab_effort_snapshot",
		Scope:       "global",
		Ready:       true,
		model:       "snap/snap-model",
		disabledMCP: map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	installNoopRuntimeEvents(app, tab.sink)
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	var loads atomic.Int32
	orig := configLoadForRoot
	configLoadForRoot = func(root string) (*config.Config, error) {
		loads.Add(1)
		return orig(root)
	}
	t.Cleanup(func() { configLoadForRoot = orig })
	return app, tab, &loads
}

// effortDirectRead is the deterministic value oracle for effort read tests:
// the exact read path EffortForTab drives (effortReadCompute →
// effortForTabDirect; the stub is nil) minus the 2s wall-clock cap. Task 611:
// three effort-domain tests flaked in a single loaded -count=3 round because
// the cap fired and its fallback blanked the assertion — value assertions
// must not ride a wall-clock bound. The cap and its cached/default fallback
// stay covered by the stub-based task-421 contract tests, which are
// deterministic by construction.
func effortDirectRead(app *App, tabID string) EffortInfo {
	return app.effortForTabDirect(tabID)
}

// TestEffortForTabWarmReadsDoNotReloadConfig is the task-609 acceptance gate:
// the first read on a root may pay one full config load, but every warm read
// afterwards must be served from the per-root snapshot — zero LoadForRoot
// calls — because that per-call full load (with on-disk migration) was the
// multi-second "effort read timed out" source task 421 capped. Reads go
// through effortDirectRead: the gate is about load counting, not about the
// 421 cap (task 611).
func TestEffortForTabWarmReadsDoNotReloadConfig(t *testing.T) {
	app, tab, loads := newSnapshotEffortFixture(t, snapshotTestConfig("low", "high"))

	got := effortDirectRead(app, tab.ID)
	if !got.Supported {
		t.Fatalf("prime read not supported: %+v", got)
	}
	if got.Current != "auto" {
		t.Fatalf("prime read current = %q, want auto", got.Current)
	}
	if want := []string{"auto", "low", "high"}; strings.Join(got.Levels, ",") != strings.Join(want, ",") {
		t.Fatalf("prime read levels = %v, want %v", got.Levels, want)
	}
	primed := loads.Load()
	if primed == 0 {
		t.Fatal("prime read never loaded the config from disk")
	}

	// Warm reads: the snapshot serves them all; a single reload here would
	// mean the read path still pays the full load task 609 removes.
	for i := 0; i < 10; i++ {
		got := effortDirectRead(app, tab.ID)
		if !got.Supported || got.Current != "auto" {
			t.Fatalf("warm read %d = %+v, want supported auto", i, got)
		}
	}
	if now := loads.Load(); now != primed {
		t.Fatalf("config loads = %d after prime + 10 warm reads (primed %d), want no reload on warm reads", now, primed)
	}
}

// TestEffortForTabReflectsConfigChangeImmediately guards the cache's
// invalidation side: a config write must show up on the very next read — the
// reload happens exactly once, then the new value is the served one.
func TestEffortForTabReflectsConfigChangeImmediately(t *testing.T) {
	app, tab, loads := newSnapshotEffortFixture(t, snapshotTestConfig("low", "high"))
	if got := effortDirectRead(app, tab.ID); !got.Supported {
		t.Fatalf("prime read not supported: %+v", got)
	}
	primed := loads.Load()

	if err := snapshotTestConfig("high", "max").SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("rewrite config: %v", err)
	}
	got := effortDirectRead(app, tab.ID)
	if want := []string{"auto", "high", "max"}; len(got.Levels) != 3 || got.Levels[1] != want[1] || got.Levels[2] != want[2] {
		t.Fatalf("levels after config change = %v, want %v", got.Levels, want)
	}
	if now := loads.Load(); now != primed+1 {
		t.Fatalf("config loads after change = %d, want exactly one reload (primed %d)", now, primed)
	}

	// The rewritten value is now the cached one; warm reads keep serving it
	// without touching the disk again.
	if got := effortDirectRead(app, tab.ID); len(got.Levels) != 3 || got.Levels[1] != "high" {
		t.Fatalf("warm read after change = %+v, want rewritten vocabulary", got)
	}
	if now := loads.Load(); now != primed+1 {
		t.Fatalf("config loads drifted to %d on warm read, want %d", now, primed+1)
	}
}

// TestConfigSnapshotInvalidatedByProjectTOMLChange covers the other tracked
// source: a project reasonix.toml edit must invalidate the root's snapshot
// just like a user-config edit does. Asserted at the snapshot layer because
// same-name project providers are shadowed by the user config by design
// (mergeTOMLProviders), so an effort-level assertion could not observe the
// project change.
func TestConfigSnapshotInvalidatedByProjectTOMLChange(t *testing.T) {
	home := isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "SNAP_MODEL_KEY", "sk-test")

	app := NewApp()
	app.ctx = context.Background()

	var loads atomic.Int32
	orig := configLoadForRoot
	configLoadForRoot = func(r string) (*config.Config, error) {
		loads.Add(1)
		return orig(r)
	}
	t.Cleanup(func() { configLoadForRoot = orig })

	if _, err := app.cachedConfigForRoot(home); err != nil {
		t.Fatalf("prime cachedConfigForRoot: %v", err)
	}
	primed := loads.Load()

	cfg, err := app.cachedConfigForRoot(home)
	if err != nil {
		t.Fatalf("warm cachedConfigForRoot: %v", err)
	}
	if _, ok := cfg.Provider("snap-project"); ok {
		t.Fatal("warm snapshot already contains the not-yet-written project provider")
	}

	// Writing the project reasonix.toml must invalidate the root's snapshot:
	// the next read reloads exactly once and sees the project provider (a
	// distinct name, so the user config's shadowing cannot mask it).
	projectCfg := config.Default()
	projectCfg.Providers = []config.ProviderEntry{{
		Name:      "snap-project",
		Kind:      "openai",
		BaseURL:   "https://example.invalid/v1",
		Model:     "snap-project-model",
		APIKeyEnv: "SNAP_MODEL_KEY",
	}}
	if err := projectCfg.SaveTo(filepath.Join(home, "reasonix.toml")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	cfg, err = app.cachedConfigForRoot(home)
	if err != nil {
		t.Fatalf("post-change cachedConfigForRoot: %v", err)
	}
	if _, ok := cfg.Provider("snap-project"); !ok {
		t.Fatalf("project provider missing after reasonix.toml write; levels of stale snapshot served")
	}
	if now := loads.Load(); now != primed+1 {
		t.Fatalf("config loads after project change = %d, want exactly one reload (primed %d)", now, primed)
	}
}

// TestEffortForTabFallbackStillBoundedAfterSnapshot keeps the task-421
// degradation contract honest on top of the snapshot: when the read source
// behind the binding still stalls (first load on a busy disk, session
// reconcile), the caller is served within the cap from the last cached value,
// with the notice — the snapshot must not have removed the safety net.
func TestEffortForTabFallbackStillBoundedAfterSnapshot(t *testing.T) {
	app, tab, _ := newSnapshotEffortFixture(t, snapshotTestConfig("low", "high"))
	// Prime the cache the same way the production read goroutine does
	// (storeEffortCache with the real snapshot value). Task 611: priming via
	// a plain EffortForTab call races its own 2s cap on a loaded machine and
	// could leave the cache empty when the stubbed read below fires; the
	// direct read plus the production store makes the primed state a fact
	// instead of a timing outcome.
	app.storeEffortCache(tab.ID, effortDirectRead(app, tab.ID))

	// From here the read source is artificially stalled; the caller must be
	// served within the (shortened) cap from the primed cache, with notice.
	app.effortReadLimitOverride = 100 * time.Millisecond
	notices := make(chan event.Event, 4)
	tab.sink.SetBotSink(event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice {
			notices <- e
		}
	}))

	app.effortReadStub = func(string) EffortInfo {
		time.Sleep(2 * time.Second)
		return EffortInfo{Supported: true, Current: "low"}
	}
	started := time.Now()
	got := app.EffortForTab(tab.ID)
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("EffortForTab took %s despite the 100ms cap", elapsed)
	}
	if got.Current != "auto" {
		t.Fatalf("fallback served %q, want the primed snapshot value auto", got.Current)
	}
	select {
	case e := <-notices:
		if !strings.Contains(e.Text, "timed out") {
			t.Fatalf("notice text %q, want timeout notice", e.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no timeout notice emitted")
	}
}
