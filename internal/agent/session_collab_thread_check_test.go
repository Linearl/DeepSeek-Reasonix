package agent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"reasonix/internal/sessioncollab"
)

// TestTalkToSessionRejectsUnresolvableThreadId: a thread_id that is not a message in
// the caller's own mailbox must be reported by the call itself, and nothing may be
// written to the peer's inbox (task 194-P0: it used to answer "queued", land in the
// peer's inbox, and only then be dropped by the pump — invisible to both sides).
func TestTalkToSessionRejectsUnresolvableThreadId(t *testing.T) {
	dir := t.TempDir()
	mailDir := filepath.Join(dir, "mail")
	self := filepath.Join(dir, "self.jsonl")
	other := filepath.Join(dir, "other.jsonl")
	writeEmpty(t, self)
	writeEmpty(t, other)
	otherID, err := EnsureContactID(other)
	if err != nil {
		t.Fatal(err)
	}

	store := sessioncollab.NewMailStore(mailDir)
	// The id of a message this session itself sent: it lives in the peer's mailbox.
	own, err := store.Deliver(sessioncollab.MailMessage{To: otherID, From: SessionContactID(self), Body: "hello"})
	if err != nil {
		t.Fatal(err)
	}

	sendTool := NewTalkToSessionTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            mailDir,
		ResolveSessionPath: func() string { return self },
	})

	args, err := json.Marshal(map[string]any{"to": otherID, "message": "status?", "thread_id": own.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sendTool.Execute(context.Background(), args); !errors.Is(err, sessioncollab.ErrReplyThreadUnknown) {
		t.Fatalf("want ErrReplyThreadUnknown from the call itself, got %v", err)
	}

	box, err := store.Inbox(otherID)
	if err != nil {
		t.Fatal(err)
	}
	if len(box) != 1 {
		t.Fatalf("a rejected reply must not be written: peer inbox holds %d messages, want 1", len(box))
	}
}

// TestTalkToSessionAcceptsInboundThreadId: the documented usage — answering the id of
// the message you received — keeps working and is still written through (task 194
// acceptance: normal thread_id behaviour is unchanged).
func TestTalkToSessionAcceptsInboundThreadId(t *testing.T) {
	dir := t.TempDir()
	mailDir := filepath.Join(dir, "mail")
	self := filepath.Join(dir, "self.jsonl")
	other := filepath.Join(dir, "other.jsonl")
	writeEmpty(t, self)
	writeEmpty(t, other)
	otherID, err := EnsureContactID(other)
	if err != nil {
		t.Fatal(err)
	}
	selfID, err := EnsureContactID(self)
	if err != nil {
		t.Fatal(err)
	}

	store := sessioncollab.NewMailStore(mailDir)
	inbound, err := store.Deliver(sessioncollab.MailMessage{To: selfID, From: otherID, Body: "ping"})
	if err != nil {
		t.Fatal(err)
	}

	sendTool := NewTalkToSessionTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            mailDir,
		ResolveSessionPath: func() string { return self },
	})
	args, err := json.Marshal(map[string]any{"to": otherID, "message": "pong", "thread_id": inbound.ID})
	if err != nil {
		t.Fatal(err)
	}
	out, err := sendTool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("answering the inbound message must succeed: %v", err)
	}
	var res struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("result: %v (%s)", err, out)
	}
	if res.Status != "queued" {
		t.Fatalf("status = %q, want queued", res.Status)
	}
	box, err := store.Inbox(otherID)
	if err != nil {
		t.Fatal(err)
	}
	if len(box) != 1 || box[0].ThreadID != inbound.ID {
		t.Fatalf("reply must carry the thread it answers: %+v", box)
	}
}
