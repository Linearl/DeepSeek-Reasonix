package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// touch sets a file's mtime to t. Used by the listing-order test so it
// doesn't have to sleep between Saves.
func touch(path string, t time.Time) error {
	return os.Chtimes(path, t, t)
}


// TestListSessionsOrdersByMTime makes sure the picker shows the most
// recently used conversation first — that's what users reach for when they
// hit `reasonix --continue`.
func TestListSessionsOrdersByMTime(t *testing.T) {
	dir := t.TempDir()
	// Write two sessions with explicit mtimes so the order is deterministic.
	for _, name := range []string{"a.jsonl", "b.jsonl"} {
		s := NewSession("")
		s.Add(provider.Message{Role: provider.RoleUser, Content: "preview for " + name})
		if err := s.Save(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	oldT := time.Now().Add(-1 * time.Hour)
	newT := time.Now()
	if err := touch(filepath.Join(dir, "a.jsonl"), oldT); err != nil {
		t.Fatal(err)
	}
	if err := touch(filepath.Join(dir, "b.jsonl"), newT); err != nil {
		t.Fatal(err)
	}

	got, err := ListSessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if !strings.HasSuffix(got[0].Path, "b.jsonl") {
		t.Errorf("first entry = %s, want the newer 'b.jsonl'", got[0].Path)
	}
	if got[0].Turns != 1 || got[0].Preview != "preview for b.jsonl" {
		t.Errorf("preview/turns wrong on newest: turns=%d preview=%q", got[0].Turns, got[0].Preview)
	}
}


func TestListSessionsIncludesCustomTitle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "named.jsonl")
	s := NewSession("")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "first user prompt"})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	if err := SaveBranchMetaPreserveUpdated(path, BranchMeta{
		TopicTitle:    "Topic title",
		CustomTitle:   "Custom session title",
		Preview:       "first user prompt",
		Turns:         1,
		SchemaVersion: BranchMetaCountsVersion,
	}); err != nil {
		t.Fatalf("SaveBranchMetaPreserveUpdated: %v", err)
	}

	got, err := ListSessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].CustomTitle != "Custom session title" {
		t.Fatalf("custom title = %q, want Custom session title", got[0].CustomTitle)
	}
	if got[0].TopicTitle != "Topic title" {
		t.Fatalf("topic title = %q, want Topic title", got[0].TopicTitle)
	}
}


func TestListSessionsSkipsCleanupPending(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pending.jsonl")
	s := NewSession("")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "preview"})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	if err := MarkCleanupPending(path, "delete"); err != nil {
		t.Fatal(err)
	}
	if !IsCleanupPending(path) {
		t.Fatal("session should be marked cleanup-pending")
	}

	got, err := ListSessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("cleanup-pending session should be hidden, got %+v", got)
	}

	if err := ClearCleanupPending(path); err != nil {
		t.Fatal(err)
	}
	got, err = ListSessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != path {
		t.Fatalf("session should be visible after clearing marker, got %+v", got)
	}
}


func TestListSessionsOrdersByLastActivityMeta(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.jsonl")
	bPath := filepath.Join(dir, "b.jsonl")
	for _, path := range []string{aPath, bPath} {
		s := NewSession("")
		s.Add(provider.Message{Role: provider.RoleUser, Content: "preview for " + filepath.Base(path)})
		if err := s.Save(path); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now().UTC()
	olderActivity := now.Add(-2 * time.Hour)
	newerActivity := now.Add(-1 * time.Hour)
	writeBranchMeta(t, aPath, now.Add(-24*time.Hour), newerActivity)
	writeBranchMeta(t, bPath, now.Add(-24*time.Hour), olderActivity)
	if err := touch(aPath, now.Add(-3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := touch(bPath, now); err != nil {
		t.Fatal(err)
	}

	got, err := ListSessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Path != aPath {
		t.Fatalf("first entry = %s, want activity-newer a.jsonl despite older file mtime", got[0].Path)
	}
	if !got[0].LastActivityAt.Equal(newerActivity) || !got[0].ModTime.Equal(newerActivity) {
		t.Fatalf("activity fields = %s / %s, want %s", got[0].LastActivityAt, got[0].ModTime, newerActivity)
	}
}


func TestListSessionOrderIncludesEmptySessionsWithoutPreviewScan(t *testing.T) {
	dir := t.TempDir()
	emptyPath := filepath.Join(dir, "empty.jsonl")
	realPath := filepath.Join(dir, "real.jsonl")
	if err := os.WriteFile(emptyPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewSession("")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "real prompt"})
	if err := s.Save(realPath); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	writeBranchMeta(t, emptyPath, now, now.Add(time.Hour))
	writeBranchMeta(t, realPath, now, now)

	ordered, err := ListSessionOrder(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ordered) != 2 {
		t.Fatalf("lightweight order len = %d, want 2", len(ordered))
	}
	if ordered[0].Path != emptyPath {
		t.Fatalf("lightweight order first = %s, want newer empty session %s", ordered[0].Path, emptyPath)
	}

	listed, err := ListSessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Path != realPath {
		t.Fatalf("ListSessions = %+v, want only the non-empty real session", listed)
	}
}


func TestSessionListingsExposeRecoveryMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recovered.jsonl")
	s := NewSession("")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "continued recovery"})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}

	meta, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta: ok=%v err=%v", ok, err)
	}
	meta.Recovered = true
	meta.RecoveryDigest = strings.Repeat("a", 64)
	meta.ParentID = "parent"
	if err := SaveBranchMetaPreserveUpdated(path, meta); err != nil {
		t.Fatal(err)
	}

	ordered, err := ListSessionOrder(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ordered) != 1 {
		t.Fatalf("ListSessionOrder len = %d, want 1", len(ordered))
	}
	if ordered[0].RecoveryDigest != meta.RecoveryDigest || ordered[0].ParentID != meta.ParentID {
		t.Fatalf("ordered recovery metadata = digest:%q parent:%q, want digest:%q parent:%q", ordered[0].RecoveryDigest, ordered[0].ParentID, meta.RecoveryDigest, meta.ParentID)
	}

	listed, err := ListSessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 {
		t.Fatalf("ListSessions len = %d, want 1", len(listed))
	}
	if listed[0].RecoveryDigest != meta.RecoveryDigest || listed[0].ParentID != meta.ParentID {
		t.Fatalf("listed recovery metadata = digest:%q parent:%q, want digest:%q parent:%q", listed[0].RecoveryDigest, listed[0].ParentID, meta.RecoveryDigest, meta.ParentID)
	}
}


func writeBranchMeta(t *testing.T, path string, createdAt, updatedAt time.Time) {
	t.Helper()
	meta := BranchMeta{
		ID:        BranchID(path),
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}
	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(BranchMetaPath(path), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}


// TestListSessionsMissingDir returns nil + no error so callers can fall
// through to a fresh session without special-casing.
func TestListSessionsMissingDir(t *testing.T) {
	got, err := ListSessions(filepath.Join(t.TempDir(), "never-created"))
	if err != nil || got != nil {
		t.Errorf("missing dir = %v / %v, want nil/nil", got, err)
	}
}

