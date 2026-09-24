package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// TestContentSnapshotCacheKeySurvivesMetadataChurn pins task 195 / 187's rule:
// the memoization key must be the content identity, so touching the file
// (mtime) never evicts a cached snapshot — while a real content change still
// moves the key (no stale hits under a too-loose key).
func TestContentSnapshotCacheKeySurvivesMetadataChurn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "phoenix marker"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}
	before := contentSnapshotCacheKey(path)
	if before == "" || before == path+"|?" {
		t.Fatalf("identity-backed key expected, got %q", before)
	}

	// Touching the transcript — the exact churn that made the old size:mtime
	// key self-destruct after every save — must keep the key stable.
	if err := os.Chtimes(path, time.Now(), time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if after := contentSnapshotCacheKey(path); after != before {
		t.Fatalf("mtime churn moved the key: before=%q after=%q", before, after)
	}

	// A real content change must move it, or the cache would serve stale text.
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "appended tail"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}
	if after := contentSnapshotCacheKey(path); after == before {
		t.Fatalf("content change kept the key stale: %q", after)
	}
}

// TestContentSnapshotCacheKeyFallsBackWithoutIdentity: a source with no
// readable identity keeps the legacy size:mtime key instead of caching under
// a key too loose to trust.
func TestContentSnapshotCacheKeyFallsBackWithoutIdentity(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.jsonl")
	if key := contentSnapshotCacheKey(missing); key != missing+"|?" {
		t.Fatalf("missing file key = %q, want the legacy sentinel", key)
	}
}
