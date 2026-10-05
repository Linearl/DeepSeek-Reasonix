package agent

import (
	"path/filepath"
	"testing"
	"time"
)

// Task 499 acceptance tests for the byte ceilings. The accounted size is
// st.size (the replayed log byte count), so the tests fabricate states with
// explicit sizes — no log files needed. Every test restores the built-in
// defaults and an empty cache: these knobs are process-wide atomics shared
// with the rest of the package's tests.

// byteCapTestScope pushes test ceilings and guarantees restoration.
func byteCapTestScope(t *testing.T, totalBytes, entryBytes int64) {
	t.Helper()
	t.Cleanup(func() {
		SetSessionGraphCacheMaxBytes(sessionGraphCacheMaxBytesDefault)
		SetSessionGraphCacheEntryMaxBytes(sessionGraphCacheEntryMaxBytesDefault)
		resetGraphCacheForTest()
	})
	SetSessionGraphCacheMaxBytes(totalBytes)
	SetSessionGraphCacheEntryMaxBytes(entryBytes)
	resetGraphCacheForTest()
}

func putSizedState(t *testing.T, path string, size int64) {
	t.Helper()
	sessionGraphCachePut(path, &sessionDAGState{path: path, size: size})
}

// TestSessionGraphCacheTotalByteCapEvictsLRU: over the total ceiling the LRU
// victim goes first (task 239 tie-break included), the just-put entry always
// survives, and the accounted bytes land back under the cap.
func TestSessionGraphCacheTotalByteCapEvictsLRU(t *testing.T) {
	byteCapTestScope(t, 300<<20, 300<<20)
	a := filepath.Join(t.TempDir(), "a.events.jsonl")
	b := filepath.Join(t.TempDir(), "b.events.jsonl")
	c := filepath.Join(t.TempDir(), "c.events.jsonl")

	putSizedState(t, a, 100<<20)
	time.Sleep(time.Millisecond) // strict LRU ordering across clock ticks
	putSizedState(t, b, 100<<20)
	time.Sleep(time.Millisecond)
	putSizedState(t, c, 150<<20) // total 350 > 300: a (oldest) must go

	bytes, _, _, entries, _ := SessionGraphCacheByteStats()
	if sessionGraphCacheGet(a) != nil {
		t.Fatal("oldest entry a must have been evicted by byte pressure")
	}
	if sessionGraphCacheGet(b) == nil || sessionGraphCacheGet(c) == nil {
		t.Fatal("b and c must stay cached")
	}
	if bytes != 250<<20 {
		t.Fatalf("accounted bytes = %d, want exactly %d (replace/add bookkeeping)", bytes, 250<<20)
	}
	if entries != 2 {
		t.Fatalf("entries = %d, want 2", entries)
	}
}

// TestSessionGraphCacheJustPutSurvivesOwnPressure: a Put that alone pushes
// the total over the ceiling evicts the neighbours, never itself — the entry
// the next save needs cannot be evicted by its own save.
func TestSessionGraphCacheJustPutSurvivesOwnPressure(t *testing.T) {
	byteCapTestScope(t, 300<<20, 300<<20)
	a := filepath.Join(t.TempDir(), "a.events.jsonl")
	b := filepath.Join(t.TempDir(), "b.events.jsonl")

	putSizedState(t, a, 200<<20)
	time.Sleep(time.Millisecond)
	putSizedState(t, b, 250<<20) // total 450 > 300, just-put is b

	if sessionGraphCacheGet(a) != nil {
		t.Fatal("neighbour a must have been evicted")
	}
	if sessionGraphCacheGet(b) == nil {
		t.Fatal("the just-put entry must survive its own Put's eviction round")
	}
	bytes, _, _, entries, _ := SessionGraphCacheByteStats()
	if bytes != 250<<20 || entries != 1 {
		t.Fatalf("bytes=%d entries=%d, want 250MiB/1 (loop stops when only the just-put remains)", bytes, entries)
	}
}

// TestSessionGraphCacheEntryCapRefusesOversizedState: a single state above
// the per-entry ceiling is never admitted; the known-worst real log (728 MB)
// stays under the built-in 1 GiB and is admitted.
func TestSessionGraphCacheEntryCapRefusesOversizedState(t *testing.T) {
	byteCapTestScope(t, 2<<30, 512<<20)
	huge := filepath.Join(t.TempDir(), "huge.events.jsonl")
	putSizedState(t, huge, 600<<20)
	if sessionGraphCacheGet(huge) != nil {
		t.Fatal("a state over the per-entry ceiling must be refused admission")
	}
	if _, _, _, _, refusals := SessionGraphCacheByteStats(); refusals == 0 {
		t.Fatal("the refusal must be counted")
	}
	bytes, _, _, entries, _ := SessionGraphCacheByteStats()
	if bytes != 0 || entries != 0 {
		t.Fatalf("bytes=%d entries=%d, want 0/0 (refusal must not touch the budget)", bytes, entries)
	}

	ok := filepath.Join(t.TempDir(), "ok.events.jsonl")
	putSizedState(t, ok, 512<<20) // exactly at the ceiling: admitted
	if sessionGraphCacheGet(ok) == nil {
		t.Fatal("a state exactly at the per-entry ceiling must be admitted")
	}
}

// TestSessionGraphCacheLoneOvershootUnderTotalCap: with the entry cap
// clamped under the total, the worst retained set stays exactly the total —
// a just-put entry over the remaining budget evicts everything else and the
// loop stops there.
func TestSessionGraphCacheLoneOvershootUnderTotalCap(t *testing.T) {
	byteCapTestScope(t, 1<<30, 900<<20)
	a := filepath.Join(t.TempDir(), "a.events.jsonl")
	b := filepath.Join(t.TempDir(), "b.events.jsonl")

	putSizedState(t, a, 800<<20)
	time.Sleep(time.Millisecond)
	putSizedState(t, b, 900<<20) // total 1.7GiB > 1GiB → a goes; b alone ≤ 900MiB cap

	bytes, totalCap, entryCap, entries, _ := SessionGraphCacheByteStats()
	if sessionGraphCacheGet(a) != nil || sessionGraphCacheGet(b) == nil {
		t.Fatal("a must be evicted and b (just-put) retained")
	}
	if bytes != 900<<20 || entries != 1 {
		t.Fatalf("bytes=%d entries=%d, want 900MiB/1", bytes, entries)
	}
	if totalCap != 1<<30 || entryCap != 900<<20 {
		t.Fatalf("effective caps total=%d entry=%d, want 1GiB/900MiB", totalCap, entryCap)
	}
}

// TestSessionGraphCacheEntryCapClampedUnderTotal: a stored entry cap above
// the total is clamped at read time — one admission must never defeat the
// total ceiling.
func TestSessionGraphCacheEntryCapClampedUnderTotal(t *testing.T) {
	byteCapTestScope(t, 300<<20, 0)
	SetSessionGraphCacheEntryMaxBytes(5 << 30) // above the 300MiB total
	_, totalCap, entryCap, _, _ := SessionGraphCacheByteStats()
	if totalCap != 300<<20 || entryCap != 300<<20 {
		t.Fatalf("caps total=%d entry=%d, want both 300MiB (entry clamped under total)", totalCap, entryCap)
	}
}

// TestSessionGraphCacheReplaceRefreshesBytes: re-putting a grown state
// replaces the old share instead of adding to it (the save path re-Puts the
// same extended state on every save).
func TestSessionGraphCacheReplaceRefreshesBytes(t *testing.T) {
	byteCapTestScope(t, 2<<30, 2<<30)
	p := filepath.Join(t.TempDir(), "p.events.jsonl")
	putSizedState(t, p, 100<<20)
	time.Sleep(time.Millisecond)
	putSizedState(t, p, 250<<20)
	bytes, _, _, entries, _ := SessionGraphCacheByteStats()
	if bytes != 250<<20 || entries != 1 {
		t.Fatalf("bytes=%d entries=%d, want 250MiB/1 (replacement, not accumulation)", bytes, entries)
	}
}

// TestSessionGraphCacheByteDefaults: unpushed vars read as the built-in
// defaults, so CLI/serve hosts and tests are bounded before any config push.
func TestSessionGraphCacheByteDefaults(t *testing.T) {
	t.Cleanup(func() {
		SetSessionGraphCacheMaxBytes(sessionGraphCacheMaxBytesDefault)
		SetSessionGraphCacheEntryMaxBytes(sessionGraphCacheEntryMaxBytesDefault)
	})
	sessionGraphCacheMaxBytesVar.Store(0)
	sessionGraphCacheEntryMaxBytesVar.Store(0)
	_, totalCap, entryCap, _, _ := SessionGraphCacheByteStats()
	if totalCap != sessionGraphCacheMaxBytesDefault {
		t.Fatalf("total cap = %d, want built-in default %d", totalCap, sessionGraphCacheMaxBytesDefault)
	}
	if entryCap != sessionGraphCacheEntryMaxBytesDefault {
		t.Fatalf("entry cap = %d, want built-in default %d", entryCap, sessionGraphCacheEntryMaxBytesDefault)
	}
}

// TestSessionGraphCacheCountCapUnchanged: the 196fix2 count cap still applies
// beside the byte ceilings — small sessions (size 0) keep the old LRU
// semantics and never trip byte pressure.
func TestSessionGraphCacheCountCapUnchanged(t *testing.T) {
	byteCapTestScope(t, sessionGraphCacheMaxBytesDefault, sessionGraphCacheEntryMaxBytesDefault)
	SetSessionGraphCacheCapacity(2)
	t.Cleanup(func() { SetSessionGraphCacheCapacity(sessionGraphCacheCapacityDefault) })
	a := filepath.Join(t.TempDir(), "a.events.jsonl")
	b := filepath.Join(t.TempDir(), "b.events.jsonl")
	c := filepath.Join(t.TempDir(), "c.events.jsonl")
	putSizedState(t, a, 0)
	putSizedState(t, b, 0)
	putSizedState(t, c, 0)
	if sessionGraphCacheGet(a) != nil {
		t.Fatal("count cap 2 must still evict the oldest")
	}
	if sessionGraphCacheGet(b) == nil || sessionGraphCacheGet(c) == nil {
		t.Fatal("b and c must stay cached under the count cap")
	}
	_, _, _, entries, _ := SessionGraphCacheByteStats()
	if entries != 2 {
		t.Fatalf("entries = %d, want 2", entries)
	}
}
