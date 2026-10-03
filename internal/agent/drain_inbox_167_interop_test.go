package agent

import (
	"context"
	"path/filepath"
	"testing"

	"reasonix/internal/sessioncollab"
)

// Task 167 acceptance: a message delivered via the 167 path (MailStore.Deliver,
// same channel as queueCollabFirstMessage) is consumable by 235 drain_inbox
// exactly once — no double-consumption between host delivery and agent pull.
func TestDrainInboxConsumes167DeliveryExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	mailDir := filepath.Join(t.TempDir(), "mail")
	mePath := drainFixture(t, dir, "me", "Me", "sc_me")

	cfg := SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            mailDir,
		CurrentSessionPath: mePath,
		CurrentContactID:   "sc_me",
	}

	// 167 path: deliver a first message to the target's mailbox.
	mail := sessioncollab.NewMailStore(mailDir)
	sent, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{
		From:     "sc_creator",
		To:       "sc_me",
		Body:     "first message from create_collab_session",
		Delivery: "steer",
		ReplyTo:  "sc_creator",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sent.ThreadID == "" {
		t.Fatal("Deliver must stamp a ThreadID for reply routing")
	}

	// 235 path: drain_inbox consumes it.
	first := execDrain(t, cfg, `{}`)
	if first.Took != 1 {
		t.Fatalf("first drain took %d, want 1", first.Took)
	}
	if first.Messages[0].Body != "first message from create_collab_session" {
		t.Fatalf("body = %q", first.Messages[0].Body)
	}
	if first.Unread != 0 {
		t.Fatalf("unreadAfter %d, want 0", first.Unread)
	}

	// Second drain must be empty — no double-consumption.
	second := execDrain(t, cfg, `{}`)
	if second.Took != 0 || second.Unread != 0 {
		t.Fatalf("second drain took=%d unread=%d, want 0/0 (double-consumption)", second.Took, second.Unread)
	}

	// Peek also must not resurface the settled message.
	peek := execDrain(t, cfg, `{"settle":false}`)
	if peek.Took != 0 {
		t.Fatalf("peek after settle took %d, want 0", peek.Took)
	}
}
