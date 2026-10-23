package session

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Task 243 A3 (sub-report 02-④A) held three invariants behind MiMo #2419's
// "pure classification + in-lock cleanup". Task 1013 removed the bolt cache
// those invariants guarded; what survives of them is the property the remaining
// readers still have to keep:
//
//  1. classification reads are pure — deciding identity validity must not
//     write a byte;
//  2. reject paths are byte-preserving — a refused read changes nothing;
//  3. the removed recent snapshot stays removed (no re-creation on read).

func hashFile(t *testing.T, path string) [32]byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(raw)
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

// Invariant 1: the identity classification read is a pure function of the file.
func TestRecoveryClassificationReadsArePure(t *testing.T) {
	dir := t.TempDir()
	identPath := writeStorageIdentity(t, dir, storageIdentity{
		Version: recoveryFormatVersion, SessionID: "s1", Generation: "gen-a",
	})
	beforeIdent := hashFile(t, identPath)

	manifest := Manifest{SessionID: "s1"}
	identity, err := readStorageIdentity(dir, manifest)
	if err != nil {
		t.Fatalf("readStorageIdentity: %v", err)
	}
	if identity.Generation != "gen-a" {
		t.Fatalf("generation = %q, want gen-a", identity.Generation)
	}
	if hashFile(t, identPath) != beforeIdent {
		t.Fatal("identity classification must not write the identity file (pure read)")
	}

	// The removed on-disk recent snapshot is reported absent, and reading must
	// not conjure the cache directory back into existence.
	if _, err := readRecentSnapshot(dir, identity); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("readRecentSnapshot = %v, want os.ErrNotExist", err)
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(dir), ".recovery-cache", filepath.Base(dir))); statErr == nil {
		t.Fatal("readRecentSnapshot must not create the removed cache layout")
	}
}

// Invariant 2: the stale-generation reject leaves the identity file
// byte-identical — a refused read never half-cleans or half-marks anything.
func TestRecoveryRejectPathsLeaveFilesUntouched(t *testing.T) {
	dir := t.TempDir()
	identPath := writeStorageIdentity(t, dir, storageIdentity{
		Version: recoveryFormatVersion, SessionID: "other-session", Generation: "gen-a",
	})
	b1 := hashFile(t, identPath)

	if _, err := readStorageIdentity(dir, Manifest{SessionID: "s1"}); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("want ErrStaleGeneration, got %v", err)
	}
	if hashFile(t, identPath) != b1 {
		t.Fatal("stale-generation reject must leave the files untouched")
	}
}

// Task 1013 acceptance: a create/flush/close/reopen cycle on the v4 store never
// produces the removed cache layout or the snapshot file, and every durability
// publish keeps working (identity maintenance only, no error).
func TestRecoveryPublishKeepsIdentityWithoutCacheFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions-v4")
	dir := filepath.Join(root, "identity")
	s, err := CreateWithOptions(dir, "identity", OpenOptions{ExternalHistory: true})
	if err != nil {
		t.Fatal(err)
	}
	appendRecoveryTestMessage(t, s, "one", "first")
	if _, err := s.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	appendRecoveryTestMessage(t, s, "two", "second")
	if _, err := s.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	// The storage identity must exist with the log-prefix digest backfilled:
	// content authorization and the history index key off it.
	manifest, err := readStoredManifest(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := readStorageIdentity(dir, manifest)
	if err != nil {
		t.Fatalf("identity after publishes: %v", err)
	}
	if identity.Generation == "" || identity.LogPrefixDigest == "" {
		t.Fatalf("identity = %+v, want generation and log prefix digest", identity)
	}

	reopened, err := OpenWithOptions(dir, "identity", OpenOptions{ExternalHistory: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(t.Context())
	if got := reopened.DeriveMessages(); len(got) != 2 || got[0].ID != "one" || got[1].ID != "two" {
		t.Fatalf("replay messages = %#v", got)
	}
	if info, statErr := os.Stat(filepath.Join(root, ".recovery-cache")); statErr == nil {
		t.Fatalf(".recovery-cache must stay absent after reopen (found %v)", info)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "storage.identity.json")); statErr != nil {
		t.Fatalf("storage identity must remain: %v", statErr)
	}
}
