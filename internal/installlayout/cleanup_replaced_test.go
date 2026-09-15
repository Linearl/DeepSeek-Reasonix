package installlayout

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A crashed or force-killed publish leaves versions/<version>.replaced-<nonce>
// behind (the backup rename happened, the final RemoveAll did not). The GC
// must clear only those stale backups: never the active version, never a
// directory whose name does not parse as version.replaced-nonce, never fresh
// backups that may still be part of a running publish.
func TestCleanupStaleReplacedVersions(t *testing.T) {
	root := t.TempDir()
	versions := filepath.Join(root, VersionsDirName)
	if err := os.MkdirAll(filepath.Join(versions, "v1.38.3"), 0o755); err != nil {
		t.Fatal(err)
	}

	stale := filepath.Join(versions, "v1.38.3.replaced-1789471114918672300")
	fresh := filepath.Join(versions, "v1.38.4.replaced-1789471114918672301")
	junk := filepath.Join(versions, "not-a-version.replaced-123")
	dotDir := filepath.Join(versions, ".staging-v1.38.3-abc")
	for _, dir := range []string{stale, fresh, junk, dotDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(dotDir, old, old); err != nil {
		t.Fatal(err)
	}

	if err := CleanupStaleReplacedVersions(root, 24*time.Hour); err != nil {
		t.Fatalf("CleanupStaleReplacedVersions: %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale replaced backup was not removed: %v", err)
	}
	for _, keep := range []string{filepath.Join(versions, "v1.38.3"), fresh, junk, dotDir} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("unexpected removal of %s: %v", keep, err)
		}
	}
}

func TestCleanupStaleReplacedVersionsMissingVersionsDir(t *testing.T) {
	root := t.TempDir()
	if err := CleanupStaleReplacedVersions(root, 0); err != nil {
		t.Fatalf("missing versions dir must be a no-op, got %v", err)
	}
}
