package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fsnotify/fsnotify"
	"reasonix/internal/sessioncatalog"
)

// Task 389: the ported watch must (a) register new targets as dirty so the
// first batch reconciles them, (b) drop removed targets from the watch set,
// and (c) keep the periodic fallbacks (metadata ticker / audit sweep) as the
// authoritative path when notifications are dropped or unsupported.

func TestRefreshCatalogWatchTargetsTracksAddsAndRemovals(t *testing.T) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Skipf("fsnotify watcher unavailable: %v", err)
	}
	defer watcher.Close()
	dir := t.TempDir()
	targets := []sessioncatalog.DirectoryTarget{{Path: dir}}
	watched := map[string]bool{}
	dirty := map[string]bool{}
	current := map[string]sessioncatalog.DirectoryTarget{}

	next := refreshCatalogWatchTargets(watcher, current, targets, watched, dirty)
	key := filepath.Clean(dir)
	if len(next) != 1 {
		t.Fatalf("one target must be tracked, got %d", len(next))
	}
	if !dirty[key] {
		t.Fatal("a newly tracked target must be marked dirty for the first reconcile")
	}
	next = refreshCatalogWatchTargets(watcher, next, nil, watched, dirty)
	if len(next) != 0 {
		t.Fatalf("removed target must be untracked, got %d", len(next))
	}
	if dirty[key] {
		t.Fatal("removed target must leave the dirty set")
	}
}

// The 30s unconditional loop is gone: the only remaining tickers are the
// metadata sync (30s, light) and the audit sweep (5min, bounded one-target).
// This pins the loop's absence at the source level so the CPU-burn shape
// cannot silently return.
func TestRefreshLoopIsWatchDriven(t *testing.T) {
	raw, err := os.ReadFile("session_catalog_lifecycle.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	if strings.Contains(src, "time.NewTicker(30 * time.Second)") {
		t.Fatal("the unconditional 30s reconcile loop must not return (task 389)")
	}
	if !strings.Contains(src, "watchSessionCatalog(ctx, catalog)") {
		t.Fatal("the refresh loop must delegate to the fsnotify watch (task 389 port)")
	}
}
