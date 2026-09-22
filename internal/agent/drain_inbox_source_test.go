package agent

import (
	"testing"

	"reasonix/internal/sessioncollab"
)

// H2: --source filters by fromContactId; only matching messages are returned
// and acked. Non-matching stay queued.
func TestDrainInboxSourceFilter(t *testing.T) {
	dir := t.TempDir()
	mailDir := t.TempDir() + "/mail"
	mePath := drainFixture(t, dir, "me", "Me", "sc_me")

	cfg := SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            mailDir,
		CurrentSessionPath: mePath,
		CurrentContactID:   "sc_me",
	}
	mail := sessioncollab.NewMailStore(mailDir)
	mail.Deliver(sessioncollab.MailMessage{From: "sc_alice", To: "sc_me", Body: "from alice"})
	mail.Deliver(sessioncollab.MailMessage{From: "sc_bob", To: "sc_me", Body: "from bob"})
	mail.Deliver(sessioncollab.MailMessage{From: "sc_alice", To: "sc_me", Body: "alice again"})

	got := execDrain(t, cfg, `{"source":"sc_alice"}`)
	if got.Took != 2 {
		t.Fatalf("source=sc_alice took %d, want 2", got.Took)
	}
	for _, m := range got.Messages {
		if m.From != "sc_alice" {
			t.Fatalf("got message from %q, want only sc_alice", m.From)
		}
	}
	if got.Unread != 1 {
		t.Fatalf("unreadAfter %d, want 1 (bob's message stays queued)", got.Unread)
	}

	bob := execDrain(t, cfg, `{"source":"sc_bob"}`)
	if bob.Took != 1 || bob.Messages[0].Body != "from bob" {
		t.Fatalf("source=sc_bob took=%d body=%q", bob.Took, bob.Messages[0].Body)
	}
	if bob.Unread != 0 {
		t.Fatalf("unreadAfter %d, want 0", bob.Unread)
	}
}
