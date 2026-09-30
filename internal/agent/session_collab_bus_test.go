package agent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
	"reasonix/internal/tool"
)

// busToolFor builds a talk_to_session against an isolated mailbox with a
// pinned bus contact table (the injectable production boot never sets —
// nil there means the live user config). Returns the tool and the sender's
// contact id.
func busToolFor(t *testing.T, dir string, contacts []string) (tool tool.Tool, fromID string) {
	t.Helper()
	from := filepath.Join(dir, "from.jsonl")
	writeEmpty(t, from)
	fromID, err := EnsureContactID(from)
	if err != nil {
		t.Fatal(err)
	}
	return NewTalkToSessionTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            filepath.Join(dir, "mail"),
		CurrentSessionPath: from,
		CurrentContactID:   fromID,
		BusContacts:        func() []string { return contacts },
	}), fromID
}

// Bus #1 headline: a Reasonix session can hand work to the headless pool —
// talk_to_session to "zcode-worker" resolves the synthetic contact and the
// mail lands in the pool's inbox on the SHARED mailbox stream (the same
// MailStore root the bus endpoint and the pool read).
func TestTalkToSessionDeliversToBusPoolContact(t *testing.T) {
	dir := t.TempDir()
	tool, fromID := busToolFor(t, dir, []string{"zcode-dev", "zcode-worker"})
	body := `{"kind":"bus-task","card_id":"card_1","prompt":"run the checks","title":"checks"}`
	payload, err := json.Marshal(map[string]string{"to": "zcode-worker", "message": body})
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Execute(context.Background(), payload)
	if err != nil {
		t.Fatalf("pool contact must be addressable: %v", err)
	}
	if !strings.Contains(out, `"queued"`) || !strings.Contains(out, `"zcode-worker"`) {
		t.Fatalf("out: %s", out)
	}
	// The busmcp-side view: the pool's MailStore must see exactly this mail.
	box, err := sessioncollab.NewMailStore(filepath.Join(dir, "mail")).Inbox("zcode-worker")
	if err != nil || len(box) != 1 {
		t.Fatalf("pool inbox: %v %v", box, err)
	}
	if box[0].From != fromID || box[0].Body != body {
		t.Fatalf("mail must carry sender and contract body: %+v", box[0])
	}
}

// Role contacts are addressable the same way, matching case-insensitively
// like the directory's own matching, with the canonical lower-case contact
// winning.
func TestTalkToSessionDeliversToBusRoleContactCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	tool, _ := busToolFor(t, dir, []string{"zcode-dev"})
	out, err := tool.Execute(context.Background(), []byte(`{"to":"ZCODE-Dev","message":"status?"}`))
	if err != nil {
		t.Fatalf("case-insensitive bus contact failed: %v", err)
	}
	if !strings.Contains(out, `"to":"zcode-dev"`) {
		t.Fatalf("receipt must carry the canonical contact: %s", out)
	}
	box, err := sessioncollab.NewMailStore(filepath.Join(dir, "mail")).Inbox("zcode-dev")
	if err != nil || len(box) != 1 || box[0].To != "zcode-dev" {
		t.Fatalf("mail must land on the canonical contact: %v %v", box, err)
	}
}

// The security boundary: an unenrolled zcode-* target is never minted into
// existence. It stays ErrNotFound (errors.Is must hold for callers that
// classify misses) and the refusal points at the enroll step that exists.
func TestTalkToSessionUnenrolledBusContactStaysNotFound(t *testing.T) {
	dir := t.TempDir()
	tool, _ := busToolFor(t, dir, []string{"zcode-dev"})
	_, err := tool.Execute(context.Background(), []byte(`{"to":"zcode-ghost","message":"hi"}`))
	if err == nil {
		t.Fatal("unenrolled contact must be refused")
	}
	if !errors.Is(err, sessioncollab.ErrNotFound) {
		t.Fatalf("must wrap sessioncollab.ErrNotFound, got %v", err)
	}
	if !strings.Contains(err.Error(), "reasonix bus enroll") {
		t.Fatalf("refusal must point at the enroll step, got %v", err)
	}
	// And nothing may have been written to the would-be inbox.
	if box, _ := sessioncollab.NewMailStore(filepath.Join(dir, "mail")).Inbox("zcode-ghost"); len(box) != 0 {
		t.Fatalf("refused send must write nothing: %+v", box)
	}
}

// A real directory session always wins over the bus table — the fallback
// fires only on a directory MISS.
func TestTalkToSessionDirectoryBeatsBusContact(t *testing.T) {
	dir := t.TempDir()
	from := filepath.Join(dir, "from.jsonl")
	to := filepath.Join(dir, "zed.jsonl")
	writeEmpty(t, from)
	writeEmpty(t, to)
	fromID, err := EnsureContactID(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateBranchMeta(to, true, func(m *BranchMeta) error {
		m.ContactID = "zcode-dev" // a session that manually carries a bus-shaped id
		m.CustomTitle = "zed session"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	tool := NewTalkToSessionTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            filepath.Join(dir, "mail"),
		CurrentSessionPath: from,
		CurrentContactID:   fromID,
		BusContacts:        func() []string { return []string{"zcode-dev"} },
	})
	out, err := tool.Execute(context.Background(), []byte(`{"to":"zcode-dev","message":"hello"}`))
	if err != nil {
		t.Fatalf("directory session must stay addressable: %v", err)
	}
	if !strings.Contains(out, "zed session") {
		t.Fatalf("delivered_to must name the directory session, not the bus contact: %s", out)
	}
}
