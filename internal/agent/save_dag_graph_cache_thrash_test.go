package agent

import (
	"testing"
	"time"
)

// TestGraphCacheThrashUnderInterleavedSaves is the task-196fix2 attribution
// repro turned into the acceptance pair for the tunable capacity:
//
//   - capacity 1 (the 0928 shipped value): the desktop pattern — our save
//     interleaving with neighbour saves — evicts our entry every round, the
//     process cache never serves a hit, and dagStateForSave stays in the
//     nil_cache branch (extended=false follows from cached==nil by
//     construction). This is the observed reading, reproduced.
//   - capacity 3 (the new built-in default): the same sequence scores hits,
//     which is the on-device self-verification the fix must show
//     (hits>0 in the next reading).
//
// The paired control proves the mechanism itself works for the reviewed
// dominant pattern (one file saved repeatedly).
func TestGraphCacheThrashUnderInterleavedSaves(t *testing.T) {
	t.Cleanup(func() {
		SetSessionGraphCacheCapacity(sessionGraphCacheCapacityDefault)
		resetGraphCacheForTest()
	})
	putPath, otherPath := "C:/sessions/primary.events.jsonl", "C:/sessions/companion.events.jsonl"

	// Control: same session, repeated saves -> hit on every Get after the first Put.
	resetGraphCacheForTest()
	sessionGraphCachePut(putPath, &sessionDAGState{path: putPath})
	if got := sessionGraphCacheGet(putPath); got == nil {
		t.Fatal("immediate re-Get of the same path must hit (cache mechanism works)")
	}

	// Capacity 1: the interleaved sequence must thrash (the 0928 reading).
	SetSessionGraphCacheCapacity(1)
	resetGraphCacheForTest()
	hits1, misses1, evictions1 := runInterleavedSequence(putPath, otherPath, 3)
	if hits1 != 0 {
		t.Fatalf("capacity 1 interleaved sequence got %d hits, want 0 — a hit would contradict the nil_cache reading", hits1)
	}
	if misses1 < 3 {
		t.Fatalf("capacity 1 got %d misses, want >= 3 (every re-Get misses)", misses1)
	}
	if evictions1 < 3 {
		t.Fatalf("capacity 1 got %d evictions, want >= 3 (each foreign Put evicts our entry)", evictions1)
	}
	t.Logf("capacity-1 thrash: hits=%d misses=%d evictions=%d (reproduces extended=false reason=nil_cache x3)", hits1, misses1, evictions1)

	// Capacity 3 (built-in default): the same sequence must flip to hits —
	// the fix's on-device acceptance signal.
	SetSessionGraphCacheCapacity(3)
	resetGraphCacheForTest()
	hits3, misses3, evictions3 := runInterleavedSequence(putPath, otherPath, 3)
	if hits3 == 0 {
		t.Fatalf("capacity 3 interleaved sequence still got 0 hits (misses=%d evictions=%d) — the fix does not remove the thrash", misses3, evictions3)
	}
	if evictions3 != 0 {
		t.Fatalf("capacity 3 must not evict for a 2-key sequence, got %d evictions", evictions3)
	}
	t.Logf("capacity-3 fixed: hits=%d misses=%d evictions=%d (next reading must show cache_hits>0)", hits3, misses3, evictions3)
}

// runInterleavedSequence replays one round per iteration: our save, a
// neighbour save, then our next save's Get. The sleeps make the LRU
// timestamps strictly ordered — within one clock tick the strict Before
// comparison ties and the map order would decide the victim.
func runInterleavedSequence(putPath, otherPath string, rounds int) (hits, misses, evictions uint64) {
	hits0, misses0, evictions0 := SessionGraphCacheStats()
	for i := 0; i < rounds; i++ {
		sessionGraphCachePut(putPath, &sessionDAGState{path: putPath})
		time.Sleep(time.Millisecond)
		sessionGraphCachePut(otherPath, &sessionDAGState{path: otherPath})
		_ = sessionGraphCacheGet(putPath)
	}
	hits, misses, evictions = SessionGraphCacheStats()
	return hits - hits0, misses - misses0, evictions - evictions0
}

// resetGraphCacheForTest empties the map under the cache lock; counters are
// cumulative process-wide, so tests read before/after deltas instead.
func resetGraphCacheForTest() {
	sessionGraphCacheMu.Lock()
	defer sessionGraphCacheMu.Unlock()
	sessionGraphCache = map[string]*sessionGraphCacheEntry{}
	// Task 499: the byte budget is derived from the map, so it resets with it.
	sessionGraphCacheBytes = 0
}
