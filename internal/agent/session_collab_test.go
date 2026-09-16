package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureContactIDStableAcrossPurposeUpdate(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(session, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	id1, err := EnsureContactID(session)
	if err != nil || !strings.HasPrefix(id1, "sc_") {
		t.Fatalf("ensure1: %q %v", id1, err)
	}
	id2, err := EnsureContactID(session)
	if err != nil || id1 != id2 {
		t.Fatalf("ensure2 must be stable: %q vs %q (%v)", id1, id2, err)
	}
	contact, err := SetSessionPurpose(session, "frontend expert")
	if err != nil || contact != id1 {
		t.Fatalf("purpose contact: %q %v", contact, err)
	}
	m, ok, err := LoadBranchMeta(session)
	if err != nil || !ok || m.ContactID != id1 || m.Purpose != "frontend expert" {
		t.Fatalf("meta: %+v ok=%v err=%v", m, ok, err)
	}
}

func TestTalkToSessionUnknownContactFails(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(session, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewTalkToSessionTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		CurrentSessionPath: session,
	})
	_, err := tool.Execute(nil, []byte(`{"to":"sc_missing","message":"hi"}`))
	if err == nil || !strings.Contains(err.Error(), "not in the contact directory") {
		t.Fatalf("expected not-found, got %v", err)
	}
}

func TestTalkToSessionQueuesToRegisteredTarget(t *testing.T) {
	dir := t.TempDir()
	from := filepath.Join(dir, "from.jsonl")
	to := filepath.Join(dir, "to.jsonl")
	for _, p := range []string{from, to} {
		if err := os.WriteFile(p, []byte{}, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	toID, err := EnsureContactID(to)
	if err != nil {
		t.Fatal(err)
	}
	fromID, err := EnsureContactID(from)
	if err != nil {
		t.Fatal(err)
	}
	tool := NewTalkToSessionTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		CurrentSessionPath: from,
		CurrentContactID:   fromID,
	})
	out, err := tool.Execute(nil, []byte(`{"to":"`+toID+`","message":"please review PR","card_id":"card_x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "queued") {
		t.Fatalf("out: %s", out)
	}
}

func TestTaskCardToolsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	created, err := NewTaskCardTools(TaskCardConfig{Enabled: true, WorkspaceRoot: dir, CurrentContactID: "sc_a"})[0].
		Execute(nil, []byte(`{"title":"build collab","assignee":"sc_b"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(created, `"id"`) {
		t.Fatalf("create: %s", created)
	}
	var card struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(created), &card); err != nil {
		t.Fatal(err)
	}
	upd, err := NewTaskCardTools(TaskCardConfig{Enabled: true, WorkspaceRoot: dir})[1].
		Execute(nil, []byte(`{"id":"`+card.ID+`","status":"done","result":"ok"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(upd, `"done"`) {
		t.Fatalf("update: %s", upd)
	}
	list, err := NewTaskCardTools(TaskCardConfig{Enabled: true, WorkspaceRoot: dir})[3].Execute(nil, nil)
	if err != nil || !strings.Contains(list, card.ID) {
		t.Fatalf("list: %s %v", list, err)
	}
}
