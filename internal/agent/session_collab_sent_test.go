package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
)

// Task 175 ②: the return must put the recipient in the caller's face. The
// historical failure was a correct-looking "queued" for the WRONG peer.
func TestTalkReturnCarriesDeliveredTo(t *testing.T) {
	cfg, _ := gateFixture(t)
	tool := NewTalkToSessionTool(cfg)
	out, err := tool.Execute(nil, []byte(`{"to":"gate target","message":"batch reply"}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		DeliveredTo string `json:"delivered_to"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload.DeliveredTo, "gate target") {
		t.Fatalf("delivered_to must carry the human-readable title: %s", out)
	}
}

// Task 175 ①: every successful send lands in the sender's own sent log with
// the recipient title, so a misdirected message is visible on this side too.
func TestSuccessfulSendIsRecordedInTheSentLog(t *testing.T) {
	cfg, fromID := gateFixture(t)
	tool := NewTalkToSessionTool(cfg)
	if _, err := tool.Execute(nil, []byte(`{"to":"gate target","message":"one"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(nil, []byte(`{"to":"gate target","message":"two"}`)); err != nil {
		t.Fatal(err)
	}
	sent := sessioncollab.NewMailStore(cfg.MailDir).ListSent(fromID, 10)
	if len(sent) != 2 {
		t.Fatalf("both sends must be logged: %d", len(sent))
	}
	if sent[0].Body != "two" || sent[0].ToTitle != "gate target" {
		t.Fatalf("newest first with the recipient title: %+v", sent[0])
	}
}

// Task 175 ②b: the hard guard. A thread opened by peer B must not be answerable
// to peer C — the refusal happens at send time, not after a confused silence.
func TestCrossWiredThreadIsRefusedAtSendTime(t *testing.T) {
	dir := t.TempDir()
	mailDir := filepath.Join(dir, "mail")
	mail := sessioncollab.NewMailStore(mailDir)
	from := filepath.Join(dir, "from.jsonl")
	writeEmpty(t, from)
	fromID, err := EnsureContactID(from)
	if err != nil {
		t.Fatal(err)
	}

	// Peer B opens a chain; the message lands in the sender's own mailbox.
	if _, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{From: "sc_peer_b", To: fromID, Body: "b's chain"}); err != nil {
		t.Fatal(err)
	}
	bInbox, err := mail.Inbox(fromID)
	if err != nil || len(bInbox) != 1 {
		t.Fatalf("fixture inbox: %v %v", bInbox, err)
	}

	if err := UpdateBranchMeta(filepath.Join(dir, "c.jsonl"), true, func(m *BranchMeta) error {
		m.CustomTitle = "peer c"
		m.ContactID = "sc_peer_c"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	writeEmpty(t, filepath.Join(dir, "c.jsonl"))

	cfg := SessionCollabConfig{
		Enabled: true, SessionDir: dir, WorkspaceRoot: dir, MailDir: mailDir,
		CurrentSessionPath: from, CurrentContactID: fromID,
	}
	// The B-thread id, aimed at C: exactly the misdirected reply.
	args := `{"to":"peer c","message":"here is the answer","thread_id":"` + bInbox[0].ID + `"}`
	if _, err := NewTalkToSessionTool(cfg).Execute(nil, []byte(args)); err == nil {
		t.Fatal("a thread opened by B must not be answerable to C")
	} else if !strings.Contains(err.Error(), "another peer") && !strings.Contains(err.Error(), "thread") {
		t.Fatalf("refusal must explain the cross-wiring: %v", err)
	}
	// Nothing reached C.
	sent := sessioncollab.NewMailStore(mailDir).ListSent(fromID, 10)
	if len(sent) != 0 {
		t.Fatalf("a refused call must not leave a sent record: %+v", sent)
	}
}
