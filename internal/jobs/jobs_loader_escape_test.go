package jobs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/event"
)

// writeArtifactMetaFile persists one artifact meta JSON under dir so
// loadSessionArtifacts will encounter it on reload.
func writeArtifactMetaFile(t *testing.T, dir, fileName string, meta artifactMeta) {
	t.Helper()
	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal meta %s: %v", fileName, err)
	}
	if err := os.WriteFile(filepath.Join(dir, fileName), b, 0o600); err != nil {
		t.Fatalf("write meta %s: %v", fileName, err)
	}
}

// TestLoadSessionArtifactsRejectsTraversalJobID covers the ../ injection
// escape attempt against artifact loading: a persisted meta whose id carries
// a traversal segment must never register a job whose artifact path points
// outside the sidecar directory.
func TestLoadSessionArtifactsRejectsTraversalJobID(t *testing.T) {
	root := t.TempDir()
	transcript := filepath.Join(root, "s.jsonl")
	dir := ArtifactDir(transcript)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeArtifactMetaFile(t, dir, "bash-1.json", artifactMeta{
		ID:               "bash-1",
		Kind:             "bash",
		Status:           Done,
		StartedAt:        1,
		FinishedAt:       2,
		ArtifactComplete: true,
	})
	// Injection attempt: a tampered meta claims id "../../evil". Before the
	// segment validator ran at load time this produced artifact paths like
	// <dir>/../../evil.log, escaping the sidecar directory.
	writeArtifactMetaFile(t, dir, "injected.json", artifactMeta{
		ID:               "../../evil",
		Kind:             "bash",
		Status:           Done,
		StartedAt:        1,
		FinishedAt:       2,
		ArtifactComplete: true,
	})

	m := NewManager(event.Discard)
	defer m.Close()
	m.SetActiveSessionPath("sess", transcript)

	if j := m.get("sess", "../../evil"); j != nil {
		t.Fatalf("traversal job id was loaded with artifact path %q", j.artifactPath)
	}
	j := m.get("sess", "bash-1")
	if j == nil {
		t.Fatal("valid job was not loaded alongside the rejected one")
	}
	if want := filepath.Join(dir, "bash-1.log"); j.artifactPath != want {
		t.Fatalf("valid job artifact path = %q, want %q", j.artifactPath, want)
	}
	// Every loaded job must stay confined to the sidecar directory.
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, key := range m.order {
		loaded := m.jobs[key]
		if loaded == nil {
			continue
		}
		if !artifactPathInDir(loaded.artifactPath, dir) {
			t.Fatalf("loaded job %s artifact path %q escapes sidecar dir %q", loaded.ID, loaded.artifactPath, dir)
		}
	}
}

// TestSetActiveSessionPathRejectsTraversalParentSession covers the parent
// session half of the boundary: a traversal-shaped parent session must be
// rejected before it can name a temporary artifact subdirectory outside the
// manager's temp root.
func TestSetActiveSessionPathRejectsTraversalParentSession(t *testing.T) {
	sink := &captureSink{}
	m := NewManager(sink)
	defer m.Close()

	transcript := filepath.Join(t.TempDir(), "s.jsonl")
	m.SetActiveSessionPath("..", transcript)

	if !sink.hasText("Ignoring SetActiveSessionPath with invalid parent session") {
		t.Fatalf("expected rejection notice, got events: %v", sink.texts())
	}
	m.mu.Lock()
	_, bound := m.artifactDirs[".."]
	_, loaded := m.loaded[".."]
	m.mu.Unlock()
	if bound {
		t.Fatal("persistent artifact binding was recorded for traversal parent session")
	}
	if loaded {
		t.Fatal("loaded flag was recorded for traversal parent session")
	}
}
