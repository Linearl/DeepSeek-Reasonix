package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// Task 1013: the published on-disk recent baseline (recent-v1.json) is gone.
// A live session still serves the bounded baseline in memory; a cold session
// reports "preparing" and the caller attaches execution to materialize it.
func TestOpenSessionServesRecentBaselineInMemory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions-v4")
	service, err := NewService("local", NewFilesystemPersistence(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	runtime, err := service.Create(t.Context(), CreateOptions{SessionID: "open-view"})
	if err != nil {
		t.Fatal(err)
	}
	appendRecoveryTestMessage(t, runtime.Session(), "message", strings.Repeat("recent", 12_000))
	if _, err := runtime.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	ref := runtime.Ref()
	live, err := service.OpenSession(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if live.Recovery != PreparationReady || !live.CanExecute || live.StorageGeneration == "" || len(live.Recent.Entries) != 1 {
		t.Fatalf("live open view = %+v", live)
	}
	var preview provider.Message
	if err := json.Unmarshal(live.Recent.Entries[0].Inline, &preview); err != nil {
		t.Fatalf("decode recent preview: %v", err)
	}
	if preview.Content == "" || len(preview.Content) >= len(strings.Repeat("recent", 12_000)) {
		t.Fatalf("recent preview content length=%d", len(preview.Content))
	}
	if err := service.Close(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, ".query-cache", ref.SessionID)); err != nil {
		t.Fatal(err)
	}

	// Cold: no on-disk snapshot and no live runtime ⇒ preparing, not a stale
	// or fabricated baseline.
	view, err := service.OpenSession(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if view.Recovery != PreparationPreparing || len(view.Recent.Entries) != 0 {
		t.Fatalf("cold open view = %+v, want preparing with empty baseline", view)
	}
	inspection, err := service.Query().InspectSession(t.Context(), ref)
	if err != nil || inspection.Commits != 1 || inspection.Events != 1 || inspection.DurableSequence == 0 {
		t.Fatalf("inspection = %+v, %v", inspection, err)
	}
	binding, err := service.EnsureExecution(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Release(context.Background())
	attached, err := service.OpenSession(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if attached.Recovery != PreparationReady || !attached.CanExecute || len(attached.Recent.Entries) != 1 {
		t.Fatalf("attached open view = %+v", attached)
	}
}

func TestOpenSessionRejectsRecentSnapshotAfterPhysicalLogReplacement(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions-v4")
	service, err := NewService("local", NewFilesystemPersistence(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	runtime, err := service.Create(t.Context(), CreateOptions{SessionID: "replaced"})
	if err != nil {
		t.Fatal(err)
	}
	appendRecoveryTestMessage(t, runtime.Session(), "message", "recent")
	if _, err := runtime.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	ref := runtime.Ref()
	if err := service.Close(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ref.SessionID)
	manifest, err := readStoredManifest(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	log, err := os.OpenFile(logPathForManifest(dir, manifest), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.WriteAt([]byte("X"), 0); err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	_ = log.Close()
	view, err := service.OpenSession(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if view.Recovery != PreparationPreparing || len(view.Recent.Entries) != 0 {
		t.Fatalf("replaced log exposed stale recent snapshot: %+v", view)
	}
}
