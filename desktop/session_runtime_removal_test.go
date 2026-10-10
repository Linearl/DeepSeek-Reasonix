package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

// Task 715 (714 复核 B4) acceptance: the removal funnel itself must drop the
// session's replayed-graph cache entry. Before this test the invalidate calls
// depended on each caller's discipline — deleteSession had one, TrashTopic and
// the collab delete had none, so deleted sessions kept pinning their DAG state
// in the process cache until the LRU pressure evicted them.

// TestCloseRemovedSessionRuntimeInvalidatesGraphCache: a session whose graph
// sits in the cache (a schema-2 load puts it there) has that entry dropped by
// closeRemovedSessionRuntime — the funnel every removal flow (delete, topic
// archive, collab delete) closes removed runtimes through. A second close of
// the now-absent entry is a silent no-op.
func TestCloseRemovedSessionRuntimeInvalidatesGraphCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "removed.jsonl")
	s := agent.NewSession("")
	s.Add(provider.Message{ID: "m0", Role: provider.RoleUser, Content: "seed"})
	if err := s.Save(path); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	// The load is what caches: the DAG event log replays into a graph state
	// that the shared loader puts into the process-wide cache.
	if _, err := agent.LoadSession(path); err != nil {
		t.Fatalf("load to populate the graph cache: %v", err)
	}
	_, _, _, entriesBefore, _ := agent.SessionGraphCacheByteStats()
	if entriesBefore == 0 {
		t.Fatal("fixture must populate the graph cache (a DAG-log load puts one entry)")
	}

	app := NewApp()
	app.closeRemovedSessionRuntime(removedSessionRuntime{sessionPath: path}, nil, nil, false)
	if _, _, _, entriesAfter, _ := agent.SessionGraphCacheByteStats(); entriesAfter != entriesBefore-1 {
		t.Fatalf("graph cache entries after funnel close = %d, want %d-1", entriesAfter, entriesBefore)
	}
	if _, ok := agent.InvalidateSessionGraph(path); ok {
		t.Fatal("the session's entry must be gone after the funnel close")
	}

	app.closeRemovedSessionRuntime(removedSessionRuntime{sessionPath: path}, nil, nil, false)
	if _, _, _, entriesAfter, _ := agent.SessionGraphCacheByteStats(); entriesAfter != entriesBefore-1 {
		t.Fatalf("a no-op funnel close must not touch the cache, entries=%d", entriesAfter)
	}
}

// The recovery-copy delete path refuses open sessions, so it never passes
// through the runtime-removal funnel — its invalidate exists only as explicit
// wiring, pinned here at source level like
// TestRecoveryGCUsesDeleteRecoveryCopyNotDeleteSession pins the GC's routing.
func TestDeleteRecoveryCopyInvalidatesGraphCache(t *testing.T) {
	source, err := os.ReadFile("recovery_copy_delete.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "agent.InvalidateSessionGraph(sessionPath)") {
		t.Fatal("DeleteRecoveryCopy must drop the deleted copy's replayed-graph cache entry (task 715 B4)")
	}
}
