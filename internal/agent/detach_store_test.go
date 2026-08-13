package agent

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/provider"
)

func TestPrepareDetachedCopiesSnapshot(t *testing.T) {
	store := NewSubagentStore(t.TempDir())
	dir := t.TempDir()
	snapshot := filepath.Join(dir, "main-session.jsonl")

	// Build a valid transcript file to use as the detach snapshot source.
	seed := NewSubagentStore(filepath.Join(t.TempDir(), "sub"))
	spec := testSubagentSpec(t, "review")
	seedRun, err := seed.PrepareFresh(spec)
	if err != nil {
		t.Fatalf("seed PrepareFresh: %v", err)
	}
	seedRun.Session.Add(provider.Message{Role: provider.RoleUser, Content: "detach me"})
	seedRun.Session.Add(provider.Message{Role: provider.RoleAssistant, Content: "tool result committed"})
	if err := seed.SaveCompleted(seedRun); err != nil {
		t.Fatalf("seed SaveCompleted: %v", err)
	}
	seedPath := seed.sessionPath(seedRun.Ref)
	seedRun.Release()
	data, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatalf("read seed transcript: %v", err)
	}
	if err := os.WriteFile(snapshot, data, 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	run, err := store.PrepareDetached(snapshot, spec)
	if err != nil {
		t.Fatalf("PrepareDetached: %v", err)
	}
	defer run.Release()
	if run.Meta.Kind != "detach" {
		t.Fatalf("meta.Kind = %q, want detach", run.Meta.Kind)
	}
	msgs := run.Session.Snapshot()
	if len(msgs) < 2 || msgs[1].Content != "detach me" {
		t.Fatalf("detached session messages = %+v, want snapshot contents", msgs)
	}
	// Transcript file must exist and differ from the source path.
	if _, err := os.Stat(store.sessionPath(run.Ref)); err != nil {
		t.Fatalf("transcript not copied: %v", err)
	}
}

func TestPrepareDetachedRejectsMissingSnapshot(t *testing.T) {
	store := NewSubagentStore(t.TempDir())
	spec := testSubagentSpec(t, "review")
	if _, err := store.PrepareDetached(filepath.Join(t.TempDir(), "nope.jsonl"), spec); err == nil {
		t.Fatal("PrepareDetached with missing snapshot should fail")
	}
}

func TestDeleteTranscriptRemovesFiles(t *testing.T) {
	store := NewSubagentStore(t.TempDir())
	dir := t.TempDir()
	snapshot := filepath.Join(dir, "main-session.jsonl")
	os.WriteFile(snapshot, []byte("{\"role\":\"system\",\"content\":\"sys\"}\n"), 0o644)

	spec := testSubagentSpec(t, "review")
	run, err := store.PrepareDetached(snapshot, spec)
	if err != nil {
		t.Fatalf("PrepareDetached: %v", err)
	}
	ref := run.Ref
	run.Release()
	if err := store.DeleteTranscript(ref); err != nil {
		t.Fatalf("DeleteTranscript: %v", err)
	}
	if _, err := os.Stat(store.sessionPath(ref)); !os.IsNotExist(err) {
		t.Fatalf("transcript still exists after DeleteTranscript (err=%v)", err)
	}
	if _, err := os.Stat(store.metaPath(ref)); !os.IsNotExist(err) {
		t.Fatalf("meta still exists after DeleteTranscript (err=%v)", err)
	}
}
