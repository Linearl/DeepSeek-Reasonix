package session

import (
	"os"
	"path/filepath"
	"testing"
)

// Task 244 B4's orphan sweep lived inside the bolt cache that task 1013
// removed: with no operations records on disk there is nothing left to settle
// or to keep. These tests pin the removal contract instead — the open path
// never creates the cache layout, and the legacy probe setter stays inert so
// hosts wiring experimental_orphan_handling keep building unchanged.

func TestRecoveryOpenNeverCreatesCacheLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions-v4")
	dir := filepath.Join(root, "nocache")
	s, err := CreateWithOptions(dir, "nocache", OpenOptions{ExternalHistory: true})
	if err != nil {
		t.Fatal(err)
	}
	appendRecoveryTestMessage(t, s, "one", "first")
	if _, err := s.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenWithOptions(dir, "nocache", OpenOptions{ExternalHistory: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(t.Context())
	if info, err := os.Stat(filepath.Join(root, ".recovery-cache")); err == nil {
		t.Fatalf(".recovery-cache must not exist after open (found %v)", info)
	}
	if _, err := os.Stat(filepath.Join(dir, recentSnapshotName)); err == nil {
		t.Fatal("recent-v1.json must not be written into the session dir")
	}
	// The baseline still exists without the cache: the live session builds it
	// in memory from the replayed log.
	recent := reopened.RecentSnapshot()
	if len(recent.Entries) == 0 || recent.DurableSequence != reopened.EventSequence() {
		t.Fatalf("recent snapshot = %+v", recent)
	}
}

func TestOrphanSweepProbeSetterIsInert(t *testing.T) {
	// A host that still injects the legacy switch must not error and must not
	// turn a removed mechanism back on.
	SetOrphanSweepProbe(func() bool { return true })
	defer SetOrphanSweepProbe(nil)
	dir := t.TempDir()
	store, err := openRecoveryStore(dir, storageIdentity{Version: recoveryFormatVersion, SessionID: "s1", Generation: "gen-a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	if info, statErr := os.Stat(filepath.Join(filepath.Dir(dir), ".recovery-cache")); statErr == nil {
		t.Fatalf("probe must not resurrect the cache layout (found %v)", info)
	}
}
