package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/store"
)

// Task 196 (T-e): a tailTruncated window state must never enter the graph
// cache - the write-path contract forbids window states anywhere near a save.
func TestSessionGraphCacheRefusesTailTruncatedState(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "truncated.events.jsonl")
	st := &sessionDAGState{path: logPath, tailTruncated: true, windowStart: 16}
	sessionGraphCachePut(logPath, st)
	if sessionGraphCacheGet(logPath) != nil {
		t.Fatal("a tailTruncated state must not be cached")
	}
}

// Task 196 (T-f): at capacity 1 the newest entry wins, the older one is
// evicted. The capacity became tunable (task 196fix2, built-in default 3), so
// this boundary behavior is pinned behind an explicit Set(1) — the test names
// the edge instead of assuming it is the default.
func TestSessionGraphCacheCapacityOneEvictsOldest(t *testing.T) {
	t.Cleanup(func() {
		SetSessionGraphCacheCapacity(sessionGraphCacheCapacityDefault)
		resetGraphCacheForTest()
	})
	SetSessionGraphCacheCapacity(1)
	resetGraphCacheForTest()
	logA := filepath.Join(t.TempDir(), "a.events.jsonl")
	logB := filepath.Join(t.TempDir(), "b.events.jsonl")
	sessionGraphCachePut(logA, &sessionDAGState{path: logA})
	sessionGraphCachePut(logB, &sessionDAGState{path: logB})
	if sessionGraphCacheGet(logA) != nil {
		t.Fatal("log A must have been evicted at capacity 1")
	}
	if sessionGraphCacheGet(logB) == nil {
		t.Fatal("log B must stay cached")
	}
	_, _, evictions := SessionGraphCacheStats()
	if evictions == 0 {
		t.Fatal("an eviction must have been counted")
	}
}

// Task 196 (T-a/T-b): a second, unrelated Session instance - exactly what a
// desktop rehydrate produces - must be served by the graph cache (same shared
// *sessionDAGState, no full replay) instead of re-reading the log under the
// save-path lock. Sharing the object is what keeps both instances consistent:
// every mutation happens on it inside the save locks.
func TestDagStateForSaveServesFreshSessionFromGraphCache(t *testing.T) {
	path := dagTestSession(t)
	dagLinearLog(t, path)
	hits0, misses0, _ := SessionGraphCacheStats()

	first := &Session{}
	stFirst, err := first.dagStateForSave(context.Background(), path, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if stFirst == nil || stFirst.tailTruncated {
		t.Fatalf("first replay state = %+v", stFirst)
	}
	hits1, misses1, _ := SessionGraphCacheStats()
	if misses1 != misses0+1 || hits1 != hits0 {
		t.Fatalf("first dagStateForSave must miss the cache: hits %d→%d misses %d→%d", hits0, hits1, misses0, misses1)
	}

	second := &Session{}
	stSecond, err := second.dagStateForSave(context.Background(), path, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	hits2, misses2, _ := SessionGraphCacheStats()
	if hits2 != hits1+1 || misses2 != misses1 {
		t.Fatalf("second instance must hit the cache: hits %d→%d misses %d→%d", hits1, hits2, misses1, misses2)
	}
	if stSecond != stFirst {
		t.Fatal("cache must hand out the shared graph object, not a replay copy")
	}
	if stSecond.selectedHead() == "" {
		t.Fatal("shared graph lost its selected head")
	}
}

// Task 196 (T-d): a vanished log must surface as an error, never as a stale
// cache hit panicking or silently reusing a graph for a file that is gone.
func TestDagStateForSaveErrorsWhenLogVanishes(t *testing.T) {
	path := dagTestSession(t)
	dagLinearLog(t, path)
	logPath := store.SessionEventLog(path)

	first := &Session{}
	if _, err := first.dagStateForSave(context.Background(), path, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(logPath); err != nil {
		t.Fatal(err)
	}
	second := &Session{}
	if _, err := second.dagStateForSave(context.Background(), path, time.Now().UTC()); err == nil {
		t.Fatal("a vanished log must error, not succeed from cache or replay")
	}
}

// TestSessionGraphCacheCapacitySetterClamps pins the task-196fix2 backstop:
// the pushed value is clamped into 1..16, an unset (zero) store reads as the
// built-in default, and the exported snapshot reflects what the save path
// will actually enforce (host setter tests prove the same push end to end).
func TestSessionGraphCacheCapacitySetterClamps(t *testing.T) {
	t.Cleanup(func() {
		SetSessionGraphCacheCapacity(sessionGraphCacheCapacityDefault)
		resetGraphCacheForTest()
	})

	// Never pushed (fresh zero) -> built-in default 3.
	sessionGraphCacheCapacityVar.Store(0)
	if got := SessionGraphCacheCapacityNow(); got != sessionGraphCacheCapacityDefault {
		t.Fatalf("unpushed capacity = %d, want built-in %d", got, sessionGraphCacheCapacityDefault)
	}
	SetSessionGraphCacheCapacity(5)
	if got := SessionGraphCacheCapacityNow(); got != 5 {
		t.Fatalf("pushed capacity = %d, want 5", got)
	}
	// 0 means "use the default", never "disable the cache".
	SetSessionGraphCacheCapacity(0)
	if got := SessionGraphCacheCapacityNow(); got != sessionGraphCacheCapacityDefault {
		t.Fatalf("capacity 0 pushed -> %d, want built-in %d (0 = unset)", got, sessionGraphCacheCapacityDefault)
	}
	// Out-of-range tops clamp instead of silently disabling or unbounding.
	SetSessionGraphCacheCapacity(99)
	if got := SessionGraphCacheCapacityNow(); got != 16 {
		t.Fatalf("capacity 99 pushed -> %d, want 16 (max clamp)", got)
	}
	SetSessionGraphCacheCapacity(-3)
	if got := SessionGraphCacheCapacityNow(); got != sessionGraphCacheCapacityDefault {
		t.Fatalf("negative pushed -> %d, want built-in %d", got, sessionGraphCacheCapacityDefault)
	}
}
