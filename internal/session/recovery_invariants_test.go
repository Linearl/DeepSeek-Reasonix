package session

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// Task 243 A3 (sub-report 02-④A): the invariants behind MiMo #2419's
// "pure classification + in-lock cleanup", expressed for the Reasonix shape
// (markers and rebuilds, never deletion of the durable log):
//
//  1. classification reads are pure — deciding identity/snapshot validity
//     must not write a byte;
//  2. cleanup happens inside the single bolt Update transaction — a
//     generation rebuild leaves one consistent result, never a torn one;
//  3. reject paths are byte-preserving — a refused open changes nothing.

func hashFile(t *testing.T, path string) [32]byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(raw)
}

func writeRecentSnapshot(t *testing.T, dir string, snap RecentSnapshot) {
	t.Helper()
	cache := recoveryCacheDir(dir)
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, recentSnapshotName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeStorageIdentity(t *testing.T, dir string, id storageIdentity) string {
	t.Helper()
	raw, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, storageIdentityName)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Invariant 1: the classification reads are pure functions of the files.
func TestRecoveryClassificationReadsArePure(t *testing.T) {
	dir := t.TempDir()
	identPath := writeStorageIdentity(t, dir, storageIdentity{
		Version: recoveryFormatVersion, SessionID: "s1", Generation: "gen-a",
	})
	writeRecentSnapshot(t, dir, RecentSnapshot{
		Version: recoveryFormatVersion, SessionID: "s1", StorageGeneration: "gen-a",
	})
	cachePath := filepath.Join(recoveryCacheDir(dir), recentSnapshotName)

	beforeIdent := hashFile(t, identPath)
	beforeRecent := hashFile(t, cachePath)

	manifest := Manifest{SessionID: "s1"}
	if _, err := readStorageIdentity(dir, manifest); err != nil {
		t.Fatalf("readStorageIdentity: %v", err)
	}
	if _, err := readRecentSnapshot(dir, storageIdentity{SessionID: "s1", Generation: "gen-a"}); err != nil {
		t.Fatalf("readRecentSnapshot: %v", err)
	}

	if hashFile(t, identPath) != beforeIdent {
		t.Fatal("identity classification must not write the identity file (pure read)")
	}
	if hashFile(t, cachePath) != beforeRecent {
		t.Fatal("snapshot classification must not write the recent file (pure read)")
	}
}

// Invariant 3: both reject paths leave every file byte-identical — a refused
// open never half-cleans or half-marks anything.
func TestRecoveryRejectPathsLeaveFilesUntouched(t *testing.T) {
	dir := t.TempDir()

	// Reject 1: stale generation (session id mismatch between file and manifest).
	identPath := writeStorageIdentity(t, dir, storageIdentity{
		Version: recoveryFormatVersion, SessionID: "other-session", Generation: "gen-a",
	})
	recentPath := filepath.Join(recoveryCacheDir(dir), recentSnapshotName)
	writeRecentSnapshot(t, dir, RecentSnapshot{
		Version: recoveryFormatVersion, SessionID: "s1", StorageGeneration: "gen-a",
	})
	b1, b2 := hashFile(t, identPath), hashFile(t, recentPath)

	if _, err := readStorageIdentity(dir, Manifest{SessionID: "s1"}); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("want ErrStaleGeneration, got %v", err)
	}
	if hashFile(t, identPath) != b1 || hashFile(t, recentPath) != b2 {
		t.Fatal("stale-generation reject must leave the files untouched")
	}

	// Reject 2: damaged snapshot (entry count over the bound).
	entries := make([]PersistentMessage, RecentMessageLimit+1)
	damaged := RecentSnapshot{
		Version: recoveryFormatVersion, SessionID: "s1",
		StorageGeneration: "gen-a", Entries: entries,
	}
	writeRecentSnapshot(t, dir, damaged)
	b3 := hashFile(t, recentPath)
	if _, err := readRecentSnapshot(dir, storageIdentity{SessionID: "s1", Generation: "gen-a"}); !errors.Is(err, ErrDamagedStore) {
		t.Fatalf("want ErrDamagedStore, got %v", err)
	}
	if hashFile(t, recentPath) != b3 {
		t.Fatal("damaged-store reject must leave the snapshot untouched")
	}
}

// Invariant 2: a generation mismatch rebuilds buckets inside one Update
// transaction — after open, meta/operations/checkpoints must agree (generation
// stamped, both buckets empty), with no torn state observable, and a reopen of
// the same generation is a no-op.
func TestRecoveryGenerationRebuildIsAtomic(t *testing.T) {
	dir := t.TempDir()
	genA := storageIdentity{Version: recoveryFormatVersion, SessionID: "s1", Generation: "gen-a"}

	first, err := openRecoveryStore(dir, genA)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists(recoveryOperationBucket)
		if err != nil {
			return err
		}
		return bucket.Put([]byte("op-x"), []byte(`{"operationId":"op-x"}`))
	}); err != nil {
		t.Fatal(err)
	}
	if err := first.db.Close(); err != nil {
		t.Fatal(err)
	}

	// gen-b open takes the delete-and-recreate branch.
	SetOrphanSweepProbe(nil)
	genB := storageIdentity{Version: recoveryFormatVersion, SessionID: "s1", Generation: "gen-b"}
	second, err := openRecoveryStore(dir, genB)
	if err != nil {
		t.Fatal(err)
	}
	defer second.db.Close()

	assertConsistent := func(store *recoveryStore, stage string) {
		t.Helper()
		if err := store.db.View(func(tx *bolt.Tx) error {
			meta := tx.Bucket(recoveryMetaBucket)
			if meta == nil {
				return fmt.Errorf("%s: meta bucket missing", stage)
			}
			if got := string(meta.Get([]byte("storage_generation"))); got != "gen-b" {
				return fmt.Errorf("%s: generation = %q, want gen-b", stage, got)
			}
			if b := tx.Bucket(recoveryOperationBucket); b == nil || b.Stats().KeyN != 0 {
				return fmt.Errorf("%s: operations bucket not empty after rebuild", stage)
			}
			if b := tx.Bucket(recoveryCheckpointBucket); b == nil || b.Stats().KeyN != 0 {
				return fmt.Errorf("%s: checkpoints bucket not empty after rebuild", stage)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	assertConsistent(second, "after rebuild")

	// Reopen: same generation, no second rebuild, state idempotent.
	if err := second.db.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := openRecoveryStore(dir, genB)
	if err != nil {
		t.Fatalf("reopen must be a no-op: %v", err)
	}
	defer third.db.Close()
	assertConsistent(third, "after idempotent reopen")
}
