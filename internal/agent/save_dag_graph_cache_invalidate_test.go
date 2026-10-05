package agent

import (
	"path/filepath"
	"testing"

	"reasonix/internal/store"
)

// Task 499 ② acceptance tests: InvalidateSessionGraph must be the missing
// "session closed → graph released" point — the 10-05 incident pinned the
// replayed graphs of closed sessions in this map forever (no invalidate call
// existed anywhere in the repo).

// invalidateTestScope starts every case from a clean cache and restores the
// invalidation counter baseline implicitly via deltas.
func invalidateTestScope(t *testing.T) {
	t.Helper()
	t.Cleanup(resetGraphCacheForTest)
	resetGraphCacheForTest()
}

// TestInvalidateSessionGraphDropsEntry: a cached entry is dropped, the freed
// byte share is reported, the budget shrinks by the same amount, and the
// invalidation counter moves — the on-device signal that a close path fired.
func TestInvalidateSessionGraphDropsEntry(t *testing.T) {
	invalidateTestScope(t)
	sessionPath := filepath.Join(t.TempDir(), "sess_abc")
	logPath := store.SessionEventLog(sessionPath)
	sessionGraphCachePut(logPath, &sessionDAGState{path: logPath, size: 700 << 20})

	before, _, _, entriesBefore, _ := SessionGraphCacheByteStats()
	invalidationsBefore := SessionGraphCacheInvalidations()

	freed, ok := InvalidateSessionGraph(sessionPath)
	if !ok {
		t.Fatal("invalidate must report an existing entry")
	}
	if freed != 700<<20 {
		t.Fatalf("freed = %d, want 700MiB (the entry's accounted share)", freed)
	}
	if sessionGraphCacheGet(logPath) != nil {
		t.Fatal("the entry must be gone after invalidation")
	}
	after, _, _, entriesAfter, _ := SessionGraphCacheByteStats()
	if after != before-(700<<20) || entriesAfter != entriesBefore-1 {
		t.Fatalf("budget after invalidate: bytes=%d entries=%d, want bytes-%dMiB entries-1", after, entriesAfter, 700)
	}
	if SessionGraphCacheInvalidations() != invalidationsBefore+1 {
		t.Fatal("the invalidation counter must move")
	}

	// A second invalidate of the now-absent entry is a no-op, not an error.
	freed, ok = InvalidateSessionGraph(sessionPath)
	if ok || freed != 0 {
		t.Fatalf("second invalidate = (%d, %v), want (0, false)", freed, ok)
	}
	if SessionGraphCacheInvalidations() != invalidationsBefore+1 {
		t.Fatal("a no-op invalidate must not move the counter")
	}
}

// TestInvalidateSessionGraphPathNormalization: invalidation goes through the
// same canonical key as Put, so a case/separator variant of the session path
// (the Windows split that task 196fix/239 had to repair for Put) cannot
// orphan an entry.
func TestInvalidateSessionGraphPathNormalization(t *testing.T) {
	invalidateTestScope(t)
	sessionPath := filepath.Join(t.TempDir(), "sess_norm")
	sessionGraphCachePut(store.SessionEventLog(sessionPath), &sessionDAGState{path: store.SessionEventLog(sessionPath), size: 5})

	upper := filepath.Join(filepath.Dir(sessionPath), "SESS_NORM")
	if _, ok := InvalidateSessionGraph(upper); !ok {
		t.Fatal("a case variant of the session path must still hit the canonical entry")
	}
	if sessionGraphCacheGet(store.SessionEventLog(sessionPath)) != nil {
		t.Fatal("the entry must be gone after the case-variant invalidate")
	}
}

// TestInvalidateSessionGraphEmptyPath: the no-session edge (a tab that never
// attached) must be a silent no-op.
func TestInvalidateSessionGraphEmptyPath(t *testing.T) {
	invalidateTestScope(t)
	if freed, ok := InvalidateSessionGraph(""); ok || freed != 0 {
		t.Fatalf("empty path = (%d, %v), want (0, false)", freed, ok)
	}
}

// TestInvalidateThenPutReCaches: invalidation is a drop, not a ban — the next
// save re-Puts and the cache serves it again (reopen takes one full replay,
// then the fast path resumes).
func TestInvalidateThenPutReCaches(t *testing.T) {
	invalidateTestScope(t)
	sessionPath := filepath.Join(t.TempDir(), "sess_re")
	logPath := store.SessionEventLog(sessionPath)
	sessionGraphCachePut(logPath, &sessionDAGState{path: logPath, size: 10})
	if _, ok := InvalidateSessionGraph(sessionPath); !ok {
		t.Fatal("first invalidate must drop the entry")
	}
	sessionGraphCachePut(logPath, &sessionDAGState{path: logPath, size: 12})
	if sessionGraphCacheGet(logPath) == nil {
		t.Fatal("a put after invalidation must re-cache the entry")
	}
	if bytes, _, _, entries, _ := SessionGraphCacheByteStats(); bytes != 12 || entries != 1 {
		t.Fatalf("bytes=%d entries=%d, want 12/1 (clean replacement, no stale share)", bytes, entries)
	}
}
