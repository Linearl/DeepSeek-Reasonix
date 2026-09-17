package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A newly minted contact_id must not be derived from the file name: a rename
// would orphan every reference (incident 2026-09-17 R4). Existing sc_<basename>
// values stay valid; new ones are random.
func TestEnsureContactIDIsNotDerivedFromFilename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "20260907-041615.000-weird-name.jsonl")
	writeEmpty(t, path)
	id1, err := EnsureContactID(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id1, "sc_") {
		t.Fatalf("contact id must be sc_*: %q", id1)
	}
	if strings.Contains(id1, "20260907-041615") || strings.Contains(id1, "weird-name") {
		t.Fatalf("contact id must not echo the file name: %q", id1)
	}
	// A proper rename moves the sidecar with the transcript. The stored id must
	// survive it — it is opaque, not a function of the file name.
	renamed := filepath.Join(dir, "totally-different.jsonl")
	if err := os.Rename(path, renamed); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".meta", renamed+".meta"); err != nil {
		t.Fatal(err)
	}
	id2 := SessionContactID(renamed)
	if id2 != id1 {
		t.Fatalf("contact id moved on rename: %q -> %q", id1, id2)
	}
}

// delete_session refuses to trash the calling session — a secretary must not be
// able to destroy its own transcript mid-turn (154-A guard).
func TestDeleteSessionRefusesSelf(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "self.jsonl")
	writeEmpty(t, self)
	selfID, err := EnsureContactID(self)
	if err != nil {
		t.Fatal(err)
	}
	tool := NewDeleteSessionTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		CurrentSessionPath: self,
	}, func(contactID, sessionPath string) (DeleteSessionImpact, DeleteSessionResult, error) {
		t.Fatal("delete callback must not run for self-delete")
		return DeleteSessionImpact{}, DeleteSessionResult{}, nil
	})
	if _, err := tool.Execute(nil, []byte(`{"target":"`+selfID+`","confirm":true}`)); err == nil || !strings.Contains(err.Error(), "refusing to delete the calling session") {
		t.Fatalf("self-delete must be refused, got %v", err)
	}
}

// A dry run must not call the host delete, so the caller can see impact first.
func TestDeleteSessionDryRunDoesNotDelete(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.jsonl")
	writeEmpty(t, target)
	targetID, err := EnsureContactID(target)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	tool := NewDeleteSessionTool(SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
	}, func(contactID, sessionPath string) (DeleteSessionImpact, DeleteSessionResult, error) {
		called = true
		return DeleteSessionImpact{ContactID: contactID, SessionPath: sessionPath, OpenTab: true, HasTurn: false},
			DeleteSessionResult{ContactID: contactID, SessionPath: sessionPath, Trashed: true, RestoreUntil: "manual"}, nil
	})
	out, err := tool.Execute(nil, []byte(`{"target":"`+targetID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("dry run must call the host for a real impact report")
	}
	var payload struct {
		Status string `json:"status"`
		Impact struct {
			Title   string `json:"title"`
			OpenTab bool   `json:"openTab"`
			HasTurn bool   `json:"hasTurn"`
		} `json:"impact"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("dry run must return JSON: %v\n%s", err, out)
	}
	if payload.Status != "dry_run" {
		t.Fatalf("dry run status: %s", payload.Status)
	}
	if !payload.Impact.OpenTab {
		t.Fatal("dry run must surface the host's openTab so it cannot contradict the removal guard")
	}
}
