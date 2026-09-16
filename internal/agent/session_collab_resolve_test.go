package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
)

// Naming a conversation by its title must work, and the first contact must mint
// a contact_id so the conversation becomes permanently addressable. This is the
// MiMo-style directory model: every session is in it, purpose is optional, and
// the title is the human way to pick someone you have not met yet.
func TestTalkToSessionAddressesByTitleAndMintsContactID(t *testing.T) {
	dir := t.TempDir()
	mailDir := filepath.Join(dir, "mail")
	from := filepath.Join(dir, "from.jsonl")
	to := filepath.Join(dir, "to.jsonl")
	writeEmpty(t, from)
	writeEmpty(t, to)

	// The target has a title but no contact_id and no purpose.
	if err := UpdateBranchMeta(to, true, func(m *BranchMeta) error {
		m.CustomTitle = "frontend expert"
		m.TopicID = "topic_front"
		return nil
	}); err != nil {
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
		MailDir:            mailDir,
		CurrentSessionPath: from,
		CurrentContactID:   fromID,
	})
	out, err := tool.Execute(nil, []byte(`{"to":"frontend expert","message":"look at the homepage"}`))
	if err != nil {
		t.Fatalf("title addressing failed: %v", err)
	}
	if !strings.Contains(out, `"queued"`) {
		t.Fatalf("out: %s", out)
	}
	minted := SessionContactID(to)
	if minted == "" {
		t.Fatal("first contact must mint a contact_id for the target")
	}
	box, err := sessioncollab.NewMailStore(mailDir).Inbox(minted)
	if err != nil || len(box) != 1 {
		t.Fatalf("inbox: %v %v", box, err)
	}
	if box[0].To != minted {
		t.Fatalf("mail must be addressed to the minted id: %+v", box[0])
	}
}

// Ambiguous titles must not silently pick a winner.
func TestTalkToSessionRejectsAmbiguousTitle(t *testing.T) {
	dir := t.TempDir()
	from := filepath.Join(dir, "from.jsonl")
	a := filepath.Join(dir, "a.jsonl")
	b := filepath.Join(dir, "b.jsonl")
	writeEmpty(t, from)
	writeEmpty(t, a)
	writeEmpty(t, b)
	for _, path := range []string{a, b} {
		if err := UpdateBranchMeta(path, true, func(m *BranchMeta) error {
			m.CustomTitle = "same title"
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	tool := NewTalkToSessionTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            filepath.Join(dir, "mail"),
		CurrentSessionPath: from,
	})
	if _, err := tool.Execute(nil, []byte(`{"to":"same title","message":"hi"}`)); err == nil || !strings.Contains(err.Error(), "matches 2 sessions") {
		t.Fatalf("ambiguous title must be refused, got %v", err)
	}
}

// The roster must see a session that has only a title — no contact_id, no
// purpose. That is the whole point of the directory.
func TestScanAddressableIncludesSessionsWithoutContactID(t *testing.T) {
	dir := t.TempDir()
	to := filepath.Join(dir, "to.jsonl")
	writeEmpty(t, to)
	if err := UpdateBranchMeta(to, true, func(m *BranchMeta) error {
		m.CustomTitle = "frontend expert"
		m.TopicID = "topic_front"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ids := scanAddressable(dir, dir)
	if len(ids) == 0 {
		t.Fatalf("roster is empty; meta load or scan dropped the session")
	}
	var found bool
	for _, id := range ids {
		if id.Title == "frontend expert" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("title not visible to the roster: %+v", ids)
	}
}
