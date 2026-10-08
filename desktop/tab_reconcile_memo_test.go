package main

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// newReconcileMemoFixture isolates the user dirs, writes a resolvable provider
// config, and registers one tab whose model ref resolves against it. The
// counter fires (via reconcileReadProbe) exactly when the effort read path
// runs a real session reconcile, so the memo gate is observable as a count
// instead of a timing. Convention follows newSnapshotEffortFixture (task 609).
func newReconcileMemoFixture(t *testing.T) (*App, *WorkspaceTab, *atomic.Int32) {
	t.Helper()
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "SNAP_MODEL_KEY", "sk-test")
	if err := snapshotTestConfig("low", "high").SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()
	tab := &WorkspaceTab{
		ID:          "tab_reconcile_memo",
		Scope:       "global",
		Ready:       true,
		model:       "snap/snap-model",
		disabledMCP: map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	var reconciles atomic.Int32
	app.reconcileReadProbe = func(string) { reconciles.Add(1) }
	t.Cleanup(func() { app.reconcileReadProbe = nil })
	return app, tab, &reconciles
}

// rewriteProjectsRegistry bumps the registry files the memo stamps, so a test
// can prove a registry change invalidates the memo. The size differs from any
// prior state (0-byte absent vs 15 bytes), so the stamp mismatch does not
// depend on file mtime granularity.
func rewriteProjectsRegistry(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(desktopConfigDir(), desktopProjectsFile), []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatalf("rewrite projects registry: %v", err)
	}
}

// TestEffortReadReconcileMemoSkipsSettledTab is the task-639 acceptance gate:
// the first read reconciles once, and every settled read after it skips the
// session-binding walk — until something that could move a binding (the
// project registry here) changes, which re-reconciles exactly once. Before
// the memo, EVERY read re-ran the walk, which is the 1.2-1.4s switch-tab
// ancillary-effort cost task 639 removes.
func TestEffortReadReconcileMemoSkipsSettledTab(t *testing.T) {
	app, tab, reconciles := newReconcileMemoFixture(t)

	if _, err := app.currentProviderEntryForTab(tab.ID); err != nil {
		t.Fatalf("prime read: %v", err)
	}
	if got := reconciles.Load(); got != 1 {
		t.Fatalf("reconciles after prime read = %d, want 1", got)
	}

	// Settled reads: the memo matches, no reconcile runs, and the entry stays
	// identical.
	for i := 0; i < 5; i++ {
		entry, err := app.currentProviderEntryForTab(tab.ID)
		if err != nil {
			t.Fatalf("settled read %d: %v", i, err)
		}
		if entry.Name != "snap" {
			t.Fatalf("settled read %d provider = %q, want snap", i, entry.Name)
		}
	}
	if got := reconciles.Load(); got != 1 {
		t.Fatalf("reconciles after 5 settled reads = %d, want 1 (memo did not gate the walk)", got)
	}

	// A registry change is an input the reconcile reads: the next read must
	// re-reconcile once and then settle again.
	rewriteProjectsRegistry(t)
	if _, err := app.currentProviderEntryForTab(tab.ID); err != nil {
		t.Fatalf("post-registry-change read: %v", err)
	}
	if got := reconciles.Load(); got != 2 {
		t.Fatalf("reconciles after registry change = %d, want 2 (registry stamp ignored)", got)
	}
	if _, err := app.currentProviderEntryForTab(tab.ID); err != nil {
		t.Fatalf("re-settled read: %v", err)
	}
	if got := reconciles.Load(); got != 2 {
		t.Fatalf("reconciles drifted to %d on re-settled read, want 2", got)
	}
}

// TestEffortReadReconcileMemoInvalidatedByTabBindingChange proves the memo
// tracks the tab's own binding state: a session-path change and a build
// generation bump (session rebind) each force exactly one fresh reconcile.
func TestEffortReadReconcileMemoInvalidatedByTabBindingChange(t *testing.T) {
	app, tab, reconciles := newReconcileMemoFixture(t)
	if _, err := app.currentProviderEntryForTab(tab.ID); err != nil {
		t.Fatalf("prime read: %v", err)
	}
	if got := reconciles.Load(); got != 1 {
		t.Fatalf("reconciles after prime read = %d, want 1", got)
	}

	app.mu.Lock()
	tab.SessionPath = filepath.Join(t.TempDir(), "moved.jsonl")
	app.mu.Unlock()
	if _, err := app.currentProviderEntryForTab(tab.ID); err != nil {
		t.Fatalf("post-path-change read: %v", err)
	}
	if got := reconciles.Load(); got != 2 {
		t.Fatalf("reconciles after session path change = %d, want 2 (path not in memo key)", got)
	}

	app.mu.Lock()
	tab.buildGeneration++
	app.mu.Unlock()
	if _, err := app.currentProviderEntryForTab(tab.ID); err != nil {
		t.Fatalf("post-generation-bump read: %v", err)
	}
	if got := reconciles.Load(); got != 3 {
		t.Fatalf("reconciles after build generation bump = %d, want 3 (generation not in memo key)", got)
	}
}

// TestEffortReadMemoKeepsEntryValuesFresh guards the red line: the memo only
// skips the binding reconcile — the entry itself must keep resolving live, so
// an effort override set on the tab shows up on the very next read with no
// reconcile in between.
func TestEffortReadMemoKeepsEntryValuesFresh(t *testing.T) {
	app, tab, reconciles := newReconcileMemoFixture(t)
	if _, err := app.currentProviderEntryForTab(tab.ID); err != nil {
		t.Fatalf("prime read: %v", err)
	}

	high := "high"
	app.mu.Lock()
	tab.effort = &high
	app.mu.Unlock()
	entry, err := app.currentProviderEntryForTab(tab.ID)
	if err != nil {
		t.Fatalf("post-override read: %v", err)
	}
	if entry.Effort != "high" {
		t.Fatalf("entry effort = %q, want high (memo must not cache entry values)", entry.Effort)
	}
	if got := reconciles.Load(); got != 1 {
		t.Fatalf("reconciles after effort override = %d, want 1 (override must not need a reconcile)", got)
	}
}

// effortWarmReadBudgetMs is the per-read ceiling for a settled warm effort
// read, encoding the task-639 acceptance "P50 < 300ms" as an in-repo gate.
// A warm read is two os.Stat freshness checks (config snapshot) plus two more
// (registry stamps) and in-memory resolution — measured around a millisecond
// locally, so even a loaded machine stays orders of magnitude below the
// budget. This is a ceiling, not a sleep gate: it can only fail when the read
// path itself regressed to doing real work again.
const effortWarmReadBudgetMs = 300

// TestEffortReadSettledWarmReadBudget pins the end state task 639 delivers:
// after the first (healing) read, every settled read stays far under the
// switch-tab budget — the reconcile walk (the old 1.2-1.4s cost) no longer
// runs per read, and the config snapshot serves from memory.
func TestEffortReadSettledWarmReadBudget(t *testing.T) {
	app, tab, reconciles := newReconcileMemoFixture(t)
	if _, err := app.currentProviderEntryForTab(tab.ID); err != nil {
		t.Fatalf("prime read: %v", err)
	}

	worst := time.Duration(0)
	for i := 0; i < 20; i++ {
		start := time.Now()
		if _, err := app.currentProviderEntryForTab(tab.ID); err != nil {
			t.Fatalf("warm read %d: %v", i, err)
		}
		if elapsed := time.Since(start); elapsed > worst {
			worst = elapsed
		}
	}
	t.Logf("worst warm read over 20 reads: %s", worst)
	if worst.Milliseconds() >= effortWarmReadBudgetMs {
		t.Fatalf("worst warm read %s >= %dms budget (read path regressed to real work)", worst, effortWarmReadBudgetMs)
	}
	if got := reconciles.Load(); got != 1 {
		t.Fatalf("reconciles = %d after prime + 20 warm reads, want 1", got)
	}
}

// TestEffortReadReconcileMemoSettlesRealBinding walks the production shape on
// real files: a tab pinned to a session whose branch meta binds a project
// root heals onto that binding on the first read, and the second read — with
// the binding settled — skips the walk entirely while the entry still
// resolves against the healed workspace.
func TestEffortReadReconcileMemoSettlesRealBinding(t *testing.T) {
	app, tab, reconciles := newReconcileMemoFixture(t)

	projectRoot := t.TempDir()
	sessionPath, err := createEmptySessionFile(t.TempDir(), "test-model")
	if err != nil {
		t.Fatalf("createEmptySessionFile: %v", err)
	}
	if err := pinNewEmptySessionBranchMeta(sessionPath, "project", projectRoot, "topic_memo", "Memo topic"); err != nil {
		t.Fatalf("pinNewEmptySessionBranchMeta: %v", err)
	}
	app.mu.Lock()
	tab.SessionPath = sessionPath
	app.mu.Unlock()

	// The registry write registerProjectRoot performs inside the heal lands
	// before the memo is stored, so the stored stamp already covers it.
	if _, err := app.currentProviderEntryForTab(tab.ID); err != nil {
		t.Fatalf("healing read: %v", err)
	}
	if got := reconciles.Load(); got != 1 {
		t.Fatalf("reconciles after healing read = %d, want 1", got)
	}
	app.mu.RLock()
	healedScope, healedRoot := tab.Scope, tab.WorkspaceRoot
	app.mu.RUnlock()
	if healedScope != "project" || !sameProjectRoot(healedRoot, projectRoot) {
		t.Fatalf("tab binding after heal = %q/%q, want project/%q", healedScope, healedRoot, normalizeProjectRoot(projectRoot))
	}

	if _, err := app.currentProviderEntryForTab(tab.ID); err != nil {
		t.Fatalf("settled read after heal: %v", err)
	}
	if got := reconciles.Load(); got != 1 {
		t.Fatalf("reconciles after settled read = %d, want 1 (settled binding must not re-walk)", got)
	}

	// The healed binding must still survive a sidecar re-read from scratch:
	// the meta on disk binds the same project, so the entry resolution is
	// deterministic across both reads.
	if _, ok, err := agent.LoadBranchMeta(sessionPath); err != nil || !ok {
		t.Fatalf("LoadBranchMeta after heal: ok=%v err=%v", ok, err)
	}
}
