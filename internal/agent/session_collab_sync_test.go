package agent

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/sessioncollab"
)

func writeEmpty(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// The thread id must survive delivery, or a reply cannot be correlated.
func TestDeliveredMessageCarriesThreadID(t *testing.T) {
	dir := t.TempDir()
	mailDir := filepath.Join(dir, "mail")
	from := filepath.Join(dir, "from.jsonl")
	to := filepath.Join(dir, "to.jsonl")
	writeEmpty(t, from)
	writeEmpty(t, to)
	toID, _ := EnsureContactID(to)
	fromID, _ := EnsureContactID(from)

	tool := NewTalkToSessionTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            mailDir,
		CurrentSessionPath: from,
		CurrentContactID:   fromID,
	})
	if _, err := tool.Execute(nil, []byte(`{"to":"`+toID+`","message":"question"}`)); err != nil {
		t.Fatal(err)
	}
	box, err := sessioncollab.NewMailStore(mailDir).Inbox(toID)
	if err != nil || len(box) != 1 {
		t.Fatalf("inbox: %v %v", box, err)
	}
	if box[0].ThreadID != box[0].ID {
		t.Fatalf("delivered message must carry its own id as the thread: %+v", box[0])
	}
}
