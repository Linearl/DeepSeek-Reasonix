package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/sessioncollab"
)

func writeEmpty(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A synchronous send must not lose the request when nobody answers in time:
// the call reports a timeout status and the message stays queued.
func TestTalkToSessionSyncTimesOutWithoutLosingRequest(t *testing.T) {
	dir := t.TempDir()
	mailDir := filepath.Join(dir, "mail")
	from := filepath.Join(dir, "from.jsonl")
	to := filepath.Join(dir, "to.jsonl")
	writeEmpty(t, from)
	writeEmpty(t, to)
	toID, err := EnsureContactID(to)
	if err != nil {
		t.Fatal(err)
	}
	fromID, err := EnsureContactID(from)
	if err != nil {
		t.Fatal(err)
	}
	tool := NewTalkToSessionSyncTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            mailDir,
		CurrentSessionPath: from,
		CurrentContactID:   fromID,
	})
	start := time.Now()
	out, err := tool.Execute(nil, []byte(`{"to":"`+toID+`","message":"status?","timeout_ms":300}`))
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("timeout was not bounded: %s", elapsed)
	}
	if !strings.Contains(out, `"status":"timeout"`) {
		t.Fatalf("want timeout status, got %s", out)
	}
	var sent struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal([]byte(out), &sent); err != nil || sent.MessageID == "" {
		t.Fatalf("timeout must still report the delivered message: %s", out)
	}
	// The request is on disk, so a later pass still delivers it.
	queued, err := sessioncollab.NewMailStore(mailDir).Inbox(toID)
	if err != nil || len(queued) != 1 {
		t.Fatalf("request was lost: %v %v", queued, err)
	}
}

// A reply that carries the thread id resolves the synchronous call.
func TestTalkToSessionSyncReturnsReply(t *testing.T) {
	dir := t.TempDir()
	mailDir := filepath.Join(dir, "mail")
	from := filepath.Join(dir, "from.jsonl")
	to := filepath.Join(dir, "to.jsonl")
	writeEmpty(t, from)
	writeEmpty(t, to)
	toID, _ := EnsureContactID(to)
	fromID, _ := EnsureContactID(from)

	mail := sessioncollab.NewMailStore(mailDir)
	go func() {
		// Stand in for the answering session: wait for the request, then reply
		// on the same thread.
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			box, _ := mail.Inbox(toID)
			if len(box) > 0 {
				_, _ = mail.Deliver(sessioncollab.MailMessage{
					From:     toID,
					To:       fromID,
					Body:     "done: build is green",
					ThreadID: box[0].ID,
				})
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()

	tool := NewTalkToSessionSyncTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            mailDir,
		CurrentSessionPath: from,
		CurrentContactID:   fromID,
	})
	out, err := tool.Execute(nil, []byte(`{"to":"`+toID+`","message":"is it green?","timeout_ms":4000}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"status":"replied"`) || !strings.Contains(out, "build is green") {
		t.Fatalf("want the reply body, got %s", out)
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
