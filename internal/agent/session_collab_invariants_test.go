package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/sessioncollab"
)

// 141: a topic rename must not move the address. Renaming only touches display
// metadata, so an existing contact_id reference keeps resolving.
func TestContactIDSurvivesTopicRename(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(session, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := EnsureContactID(session)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateBranchMeta(session, true, func(m *BranchMeta) error {
		m.CustomTitle = "renamed to something else"
		m.TopicTitle = "also renamed"
		m.TopicID = "topic_rotated"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	after, err := EnsureContactID(session)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("contact id moved on rename: %q -> %q", before, after)
	}
	if got := SessionContactID(session); got != before {
		t.Fatalf("SessionContactID must read the stored id: %q vs %q", got, before)
	}
}

// 145 / F12: the card machine must refuse a silent restart of finished work.
func TestCardStatusTransitions(t *testing.T) {
	cases := []struct {
		from, to sessioncollab.CardStatus
		want     bool
	}{
		{sessioncollab.StatusPending, sessioncollab.StatusRunning, true},
		{sessioncollab.StatusRunning, sessioncollab.StatusDone, true},
		{sessioncollab.StatusRunning, sessioncollab.StatusBlocked, true},
		{sessioncollab.StatusBlocked, sessioncollab.StatusRunning, true},
		{sessioncollab.StatusRunning, sessioncollab.StatusFailed, true},
		// Terminal states are terminal; a reopen goes through pending.
		{sessioncollab.StatusDone, sessioncollab.StatusRunning, false},
		{sessioncollab.StatusFailed, sessioncollab.StatusRunning, false},
		{sessioncollab.StatusDone, sessioncollab.StatusPending, true},
		{sessioncollab.StatusFailed, sessioncollab.StatusPending, true},
		// Same-state writes stay idempotent.
		{sessioncollab.StatusDone, sessioncollab.StatusDone, true},
		{sessioncollab.StatusPending, sessioncollab.StatusPending, true},
	}
	for _, c := range cases {
		if got := sessioncollab.StatusTransitionAllowed(c.from, c.to); got != c.want {
			t.Fatalf("%s -> %s: got %v want %v", c.from, c.to, got, c.want)
		}
	}
}

// The card tool must reject a forbidden transition instead of writing it, and
// must stamp a node timestamp so the chain can be read in order (F10).
func TestUpdateTaskCardRejectsSilentRestartAndStampsNodes(t *testing.T) {
	dir := t.TempDir()
	cfg := TaskCardConfig{Enabled: true, WorkspaceRoot: dir, CurrentContactID: "sc_a"}
	tools := NewTaskCardTools(cfg)
	create, update := tools[0], tools[1]

	out, err := create.Execute(nil, []byte(`{"title":"t","assignee":"sc_b"}`))
	if err != nil {
		t.Fatal(err)
	}
	var card struct {
		ID string `json:"id"`
	}
	if err := jsonUnmarshalInto(out, &card); err != nil {
		t.Fatal(err)
	}
	if _, err := update.Execute(nil, []byte(`{"id":"`+card.ID+`","status":"running","note":"start"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := update.Execute(nil, []byte(`{"id":"`+card.ID+`","status":"done","result":"ok"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := update.Execute(nil, []byte(`{"id":"`+card.ID+`","status":"running"}`)); err == nil {
		t.Fatal("done -> running must be rejected, not written")
	}
	// A failed node append must not have mutated the card.
	got, err := sessioncollab.NewCardStore(dir).Get(card.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != sessioncollab.StatusDone {
		t.Fatalf("rejected update changed the card: %s", got.Status)
	}
	if len(got.Nodes) == 0 {
		t.Fatal("the note must have appended a node")
	}
	for _, node := range got.Nodes {
		if node.At == 0 {
			t.Fatalf("node must carry a timestamp: %+v", node)
		}
		if node.Role == "" {
			t.Fatalf("node must carry a role: %+v", node)
		}
	}
}

func jsonUnmarshalInto(data string, v any) error {
	return json.Unmarshal([]byte(data), v)
}
