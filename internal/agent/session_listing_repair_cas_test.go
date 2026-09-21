package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/provider"
)

// Task 206 (T4): the repair entry refuses trash entries outright - the catalog
// queue must not spend decode slots on storage the user chose to keep. Preview
// and restore load through the main path, so nothing user-facing needs this.
func TestRepairSessionListingProjectionRefusesTrashPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".trash", "item", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := RepairSessionListingProjection(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != SessionListingRepairUnsupported {
		t.Fatalf("trash repair status = %q, want unsupported", result.Status)
	}
}

// Task 206 (T2): the locked commit re-checks the CAS fence, so a source that
// changed between the unlocked decode and the locked commit is rejected with
// SourceChanged and nothing is written back.
func TestCommitSessionListingReplayRejectsSourceChangedAfterDecode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "question"},
		{Role: provider.RoleAssistant, Content: "answer"},
	}
	writeSessionFile(t, path, msgs)
	before, err := sessionRepairContentFingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	decoded, state, repairable, err := loadSessionDisplayMessagesContextUnlocked(context.Background(), path)
	if err != nil || !repairable {
		t.Fatalf("decode repairable=%v err=%v", repairable, err)
	}
	// The source moves after the unlocked decode, before the locked commit.
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append([]byte("{\"moved\":true}\n"), current...), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := commitSessionListingReplay(context.Background(), path, BranchMeta{ID: BranchID(path)}, before, decoded, state, SessionListingRepairResult{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != SessionListingRepairSourceChanged {
		t.Fatalf("commit status = %q, want source_changed", result.Status)
	}
	// The fence must also hold against the trash gate: a trashed path is
	// refused before any decode attempt even inside the commit half.
	if !SessionPathInTrash(filepath.Join("roots", ".trash", "k", "k.jsonl")) {
		t.Fatal("SessionPathInTrash missed a nested .trash segment")
	}
	if SessionPathInTrash(filepath.Join("roots", "sessions", "k.jsonl")) {
		t.Fatal("SessionPathInTrash flagged a live session")
	}
}
