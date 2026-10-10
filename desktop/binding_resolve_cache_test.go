package main

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/agent"
)

// newBindingResolveFixture isolates the user dirs and creates one real
// project-scoped session whose binding resolves through the branch-meta
// fallback (the production shape for a project session whose directory is
// not yet in the registry). The walkHook counts real walks so the cache gate
// is observable as a count instead of a timing (reconcileReadProbe
// convention).
func newBindingResolveFixture(t *testing.T) (*App, string, string, *atomic.Int32) {
	t.Helper()
	isolateDesktopUserDirs(t)
	if err := os.MkdirAll(desktopConfigDir(), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	app := NewApp()
	projectRoot := t.TempDir()
	sessionPath, err := createEmptySessionFile(t.TempDir(), "test-model")
	if err != nil {
		t.Fatalf("createEmptySessionFile: %v", err)
	}
	if err := pinNewEmptySessionBranchMeta(sessionPath, "project", projectRoot, "topic_691", "Task 691 topic"); err != nil {
		t.Fatalf("pinNewEmptySessionBranchMeta: %v", err)
	}
	var walks atomic.Int32
	app.bindingResolve.walkHook = func() { walks.Add(1) }
	t.Cleanup(func() { app.bindingResolve.walkHook = nil })
	return app, projectRoot, sessionPath, &walks
}

// TestBindingResolveCacheServesWarmHitAndInvalidates is the task-691
// acceptance gate for the resolve half: the first resolve walks once, every
// repeat is served from the cache, and each input that could move the
// outcome (project registry, branch-meta sidecar) forces exactly one fresh
// walk reflecting the new truth.
func TestBindingResolveCacheServesWarmHitAndInvalidates(t *testing.T) {
	app, projectRoot, sessionPath, walks := newBindingResolveFixture(t)

	binding, ok := app.resolveSessionBinding(sessionPath)
	if !ok {
		t.Fatalf("prime resolve missed")
	}
	if binding.path != sessionPath || binding.scope != "project" || !sameProjectRoot(binding.workspaceRoot, projectRoot) {
		t.Fatalf("prime binding = %+v, want project/%q at %s", binding, projectRoot, sessionPath)
	}
	if binding.topicID != "topic_691" {
		t.Fatalf("prime topicID = %q, want topic_691", binding.topicID)
	}
	if got := walks.Load(); got != 1 {
		t.Fatalf("walks after prime resolve = %d, want 1", got)
	}

	// Warm resolves: the cached entry serves every repeat without a walk.
	for i := 0; i < 20; i++ {
		got, ok := app.resolveSessionBinding(sessionPath)
		if !ok || got.path != sessionPath || got.topicID != "topic_691" {
			t.Fatalf("warm resolve %d = (%+v, %v), mismatch", i, got, ok)
		}
	}
	if got := walks.Load(); got != 1 {
		t.Fatalf("walks after 20 warm resolves = %d, want 1 (cache did not gate the walk)", got)
	}

	// A registry change is an input the walk reads (dir list): the next
	// resolve re-walks once and re-settles.
	rewriteProjectsRegistry(t)
	if _, ok := app.resolveSessionBinding(sessionPath); !ok {
		t.Fatalf("post-registry-change resolve missed")
	}
	if got := walks.Load(); got != 2 {
		t.Fatalf("walks after registry change = %d, want 2 (registry stamp ignored)", got)
	}
	if _, ok := app.resolveSessionBinding(sessionPath); !ok {
		t.Fatalf("re-settled resolve missed")
	}
	if got := walks.Load(); got != 2 {
		t.Fatalf("walks drifted to %d on re-settled resolve, want 2", got)
	}

	// A sidecar rewrite is an input the walk reads (topic/scope truth): the
	// next resolve re-walks once and reports the new topic.
	if err := pinSessionBranchMeta(sessionPath, "project", projectRoot, "topic_691_renamed", "Renamed topic"); err != nil {
		t.Fatalf("pinSessionBranchMeta: %v", err)
	}
	got, ok := app.resolveSessionBinding(sessionPath)
	if !ok || got.topicID != "topic_691_renamed" {
		t.Fatalf("post-sidecar-resolve = (%+v, %v), want renamed topic", got, ok)
	}
	if got := walks.Load(); got != 3 {
		t.Fatalf("walks after sidecar rewrite = %d, want 3 (sidecar stamp ignored)", got)
	}
}

// TestBindingResolveCacheInvalidatedByMovedSession pins the freshness red
// line: a session whose file and sidecar disappear stops resolving
// immediately (no stale cached hit — the meta fallback cannot re-derive a
// binding without its sidecar), and their return resolves again. Both are
// moved because the fallback branch reads only the sidecar by design.
func TestBindingResolveCacheInvalidatedByMovedSession(t *testing.T) {
	app, _, sessionPath, walks := newBindingResolveFixture(t)

	if _, ok := app.resolveSessionBinding(sessionPath); !ok {
		t.Fatalf("prime resolve missed")
	}
	if got := walks.Load(); got != 1 {
		t.Fatalf("walks after prime = %d, want 1", got)
	}

	moved := sessionPath + ".moved"
	if err := os.Rename(sessionPath, moved); err != nil {
		t.Fatalf("rename away: %v", err)
	}
	meta := agent.BranchMetaPath(sessionPath)
	movedMeta := agent.BranchMetaPath(moved)
	if err := os.Rename(meta, movedMeta); err != nil {
		t.Fatalf("rename sidecar away: %v", err)
	}
	if _, ok := app.resolveSessionBinding(sessionPath); ok {
		t.Fatalf("resolve served a stale hit for a moved session")
	}
	if got := walks.Load(); got != 2 {
		t.Fatalf("walks after move = %d, want 2 (file stamp ignored)", got)
	}

	if err := os.Rename(moved, sessionPath); err != nil {
		t.Fatalf("rename back: %v", err)
	}
	if err := os.Rename(movedMeta, meta); err != nil {
		t.Fatalf("rename sidecar back: %v", err)
	}
	if _, ok := app.resolveSessionBinding(sessionPath); !ok {
		t.Fatalf("resolve missed after the session returned")
	}
	if got := walks.Load(); got != 3 {
		t.Fatalf("walks after move back = %d, want 3", got)
	}
}

// TestBindingResolveCacheSingleFlightsConcurrentWalks pins the storm half of
// task 691: N concurrent resolves of the same path join ONE walk instead of
// stampeding N of them, and everyone gets the same answer. The hook blocks
// the first walk so the whole burst piles up behind the flight; after
// release, strictly fewer walks than callers must have run.
func TestBindingResolveCacheSingleFlightsConcurrentWalks(t *testing.T) {
	app, _, sessionPath, walks := newBindingResolveFixture(t)

	release := make(chan struct{})
	var hookCalls atomic.Int32
	app.bindingResolve.walkHook = func() {
		hookCalls.Add(1)
		walks.Add(1)
		select {
		case <-release:
		case <-time.After(5 * time.Second):
			// Never block the suite if the test body died before release.
		}
	}

	const callers = 16
	type result struct {
		binding sessionBinding
		ok      bool
	}
	results := make([]result, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			binding, ok := app.resolveSessionBinding(sessionPath)
			results[i] = result{binding: binding, ok: ok}
		}(i)
	}

	// Wait for the flight to form (hook entered) plus a beat for joiners to
	// pile on, then let the walk finish.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && hookCalls.Load() == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := walks.Load(); got >= callers {
		t.Fatalf("walks = %d for %d concurrent callers, want strictly fewer (single flight did not collapse the stampede)", got, callers)
	}
	first := results[0]
	if !first.ok || first.binding.path != sessionPath || first.binding.topicID != "topic_691" {
		t.Fatalf("first result = %+v ok=%v, want the project binding", first.binding, first.ok)
	}
	for i, r := range results {
		if r.ok != first.ok || r.binding.path != first.binding.path || r.binding.topicID != first.binding.topicID {
			t.Fatalf("caller %d = (%+v, %v), disagrees with first (%+v, %v)", i, r.binding, r.ok, first.binding, first.ok)
		}
	}

	// The completed flight stored its entry: one more resolve is warm.
	if _, ok := app.resolveSessionBinding(sessionPath); !ok {
		t.Fatalf("post-storm resolve missed")
	}
	if got, want := walks.Load(), hookCalls.Load(); got != want {
		t.Fatalf("walks %d != hook calls %d (hook accounting drifted)", got, want)
	}
}

// TestReconcileMemoGatesEveryCallerTask691 extends the task-639 memo
// acceptance to the faces task 691 was cut for: ANY
// reconcileTabWithPinnedSessionMeta caller (not just the effort read) skips
// the walk while the binding is settled, and a sidecar rewrite re-heals
// exactly once.
func TestReconcileMemoGatesEveryCallerTask691(t *testing.T) {
	app, tab, reconciles := newReconcileMemoFixture(t)

	projectRoot := t.TempDir()
	sessionPath, err := createEmptySessionFile(t.TempDir(), "test-model")
	if err != nil {
		t.Fatalf("createEmptySessionFile: %v", err)
	}
	if err := pinNewEmptySessionBranchMeta(sessionPath, "project", projectRoot, "topic_691", "Task 691 topic"); err != nil {
		t.Fatalf("pinNewEmptySessionBranchMeta: %v", err)
	}
	app.mu.Lock()
	tab.SessionPath = sessionPath
	app.mu.Unlock()

	// First caller heals onto the sidecar binding.
	path, ok := app.reconcileTabWithPinnedSessionMeta(tab)
	if !ok || path != sessionPath {
		t.Fatalf("heal = (%q, %v), want the session path", path, ok)
	}
	app.mu.RLock()
	scope, root := tab.Scope, tab.WorkspaceRoot
	app.mu.RUnlock()
	if scope != "project" || !sameProjectRoot(root, projectRoot) {
		t.Fatalf("tab binding after heal = %q/%q, want project/%q", scope, root, normalizeProjectRoot(projectRoot))
	}
	if got := reconciles.Load(); got != 1 {
		t.Fatalf("reconciles after heal = %d, want 1", got)
	}

	// A second caller in the same switch re-derives nothing: the memo serves
	// the stored outcome.
	path2, ok2 := app.reconcileTabWithPinnedSessionMeta(tab)
	if !ok2 || path2 != sessionPath {
		t.Fatalf("settled caller = (%q, %v), want the memoized path", path2, ok2)
	}
	if got := reconciles.Load(); got != 1 {
		t.Fatalf("reconciles after settled caller = %d, want 1 (memo did not gate the walk)", got)
	}

	// A sidecar rewrite (topic rename) is an input the reconcile reads: the
	// next caller re-heals once and the tab follows the new topic.
	if err := pinSessionBranchMeta(sessionPath, "project", projectRoot, "topic_691_renamed", "Renamed topic"); err != nil {
		t.Fatalf("pinSessionBranchMeta: %v", err)
	}
	if _, ok := app.reconcileTabWithPinnedSessionMeta(tab); !ok {
		t.Fatalf("post-sidecar heal missed")
	}
	if got := reconciles.Load(); got != 2 {
		t.Fatalf("reconciles after sidecar rewrite = %d, want 2 (sidecar stamp ignored)", got)
	}
	app.mu.RLock()
	topic := tab.TopicID
	app.mu.RUnlock()
	if topic != "topic_691_renamed" {
		t.Fatalf("tab topic after re-heal = %q, want topic_691_renamed", topic)
	}

	// And it settles again.
	if _, ok := app.reconcileTabWithPinnedSessionMeta(tab); !ok {
		t.Fatalf("re-settled caller missed")
	}
	if got := reconciles.Load(); got != 2 {
		t.Fatalf("reconciles drifted to %d, want 2", got)
	}
}

// TestReconcileMemoServesNoBindingOutcome pins the ok=false memo path: a tab
// with an unresolvable session path reconciles once, stores the negative
// outcome, and every later caller is served it without re-walking — a
// binding that appears on disk re-heals on the next call after the file
// stamp moves.
func TestReconcileMemoServesNoBindingOutcome(t *testing.T) {
	app, tab, reconciles := newReconcileMemoFixture(t)

	sessionPath := filepath.Join(t.TempDir(), "ghost.jsonl")
	app.mu.Lock()
	tab.SessionPath = sessionPath
	app.mu.Unlock()

	if _, ok := app.reconcileTabWithPinnedSessionMeta(tab); ok {
		t.Fatalf("ghost session resolved; want miss")
	}
	if got := reconciles.Load(); got != 1 {
		t.Fatalf("reconciles after ghost reconcile = %d, want 1", got)
	}
	for i := 0; i < 3; i++ {
		if _, ok := app.reconcileTabWithPinnedSessionMeta(tab); ok {
			t.Fatalf("ghost resolved on settled call %d; want memoized miss", i)
		}
	}
	if got := reconciles.Load(); got != 1 {
		t.Fatalf("reconciles after settled ghost calls = %d, want 1", got)
	}

	// The session appears on disk with a real binding: the file-stamp change
	// busts the memo and the next reconcile heals.
	projectRoot := t.TempDir()
	if err := pinNewEmptySessionBranchMeta(sessionPath, "project", projectRoot, "topic_ghost", "Ghost topic"); err != nil {
		t.Fatalf("pinNewEmptySessionBranchMeta: %v", err)
	}
	if _, ok := app.reconcileTabWithPinnedSessionMeta(tab); !ok {
		t.Fatalf("heal after session appears missed")
	}
	if got := reconciles.Load(); got != 2 {
		t.Fatalf("reconciles after session appears = %d, want 2", got)
	}
	app.mu.RLock()
	scope := tab.Scope
	app.mu.RUnlock()
	if scope != "project" {
		t.Fatalf("tab scope after heal = %q, want project", scope)
	}
}

// bindingWarmResolveBudgetMs is the per-resolve ceiling for a settled warm
// resolve, encoding the task-691 acceptance as an in-repo gate (same shape
// as effortWarmReadBudgetMs). A warm resolve is a handful of os.Stat
// freshness checks plus in-memory map work — orders of magnitude below the
// budget on any machine; breaching it means the read path regressed to
// walking the session directory list again.
const bindingWarmResolveBudgetMs = 50

// TestBindingResolveWarmResolveBudget pins the task-691 end state: after the
// first (cold) resolve, settled resolves stay far under the switch-tab
// budget — the 15-directory walk no longer runs per call.
func TestBindingResolveWarmResolveBudget(t *testing.T) {
	app, _, sessionPath, walks := newBindingResolveFixture(t)
	if _, ok := app.resolveSessionBinding(sessionPath); !ok {
		t.Fatalf("prime resolve missed")
	}

	worst := time.Duration(0)
	for i := 0; i < 20; i++ {
		start := time.Now()
		if _, ok := app.resolveSessionBinding(sessionPath); !ok {
			t.Fatalf("warm resolve %d missed", i)
		}
		if elapsed := time.Since(start); elapsed > worst {
			worst = elapsed
		}
	}
	t.Logf("worst warm resolve over 20 resolves: %s", worst)
	if worst.Milliseconds() >= bindingWarmResolveBudgetMs {
		t.Fatalf("worst warm resolve %s >= %dms budget (resolve path regressed to real walks)", worst, bindingWarmResolveBudgetMs)
	}
	if got := walks.Load(); got != 1 {
		t.Fatalf("walks = %d after prime + 20 warm resolves, want 1", got)
	}
}

// reconcileMemoWarmCallBudgetMs is the per-call ceiling for a settled
// reconcileTabWithPinnedSessionMeta call from ANY caller (task 691: the memo
// moved from the effort read into the reconcile itself). Same reasoning as
// bindingWarmResolveBudgetMs: a fresh memo serves a fingerprint comparison
// plus a handful of stats.
const reconcileMemoWarmCallBudgetMs = 50

// TestReconcileMemoWarmCallBudget pins that every reconcile caller — not
// just the effort read — is served under budget once the binding is settled.
func TestReconcileMemoWarmCallBudget(t *testing.T) {
	app, tab, reconciles := newReconcileMemoFixture(t)

	projectRoot := t.TempDir()
	sessionPath, err := createEmptySessionFile(t.TempDir(), "test-model")
	if err != nil {
		t.Fatalf("createEmptySessionFile: %v", err)
	}
	if err := pinNewEmptySessionBranchMeta(sessionPath, "project", projectRoot, "topic_691", "Task 691 topic"); err != nil {
		t.Fatalf("pinNewEmptySessionBranchMeta: %v", err)
	}
	app.mu.Lock()
	tab.SessionPath = sessionPath
	app.mu.Unlock()
	if _, ok := app.reconcileTabWithPinnedSessionMeta(tab); !ok {
		t.Fatalf("heal missed")
	}

	worst := time.Duration(0)
	for i := 0; i < 20; i++ {
		start := time.Now()
		if _, ok := app.reconcileTabWithPinnedSessionMeta(tab); !ok {
			t.Fatalf("warm reconcile %d missed", i)
		}
		if elapsed := time.Since(start); elapsed > worst {
			worst = elapsed
		}
	}
	t.Logf("worst warm reconcile over 20 calls: %s", worst)
	if worst.Milliseconds() >= reconcileMemoWarmCallBudgetMs {
		t.Fatalf("worst warm reconcile %s >= %dms budget (reconcile path regressed to real walks)", worst, reconcileMemoWarmCallBudgetMs)
	}
	if got := reconciles.Load(); got != 1 {
		t.Fatalf("reconciles = %d after heal + 20 warm calls, want 1", got)
	}
}
