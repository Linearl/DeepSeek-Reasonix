package main

import (
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
)

// The delivery text must carry both sides of the conversation so the recipient
// can verify it landed on the right session by ID, not by a title that may have
// been auto-renamed since (user feedback 2026-09-17 #3).
func TestSessionCollabDeliveryTextCarriesBothIDs(t *testing.T) {
	msg := sessioncollab.MailMessage{
		ID:   "msg_1",
		From: "sc_alice",
		To:   "sc_bob",
		Body: "please review the PR",
	}
	text := sessionCollabDeliveryText(msg, 0)
	if !strings.Contains(text, "contact_id=sc_alice") {
		t.Fatalf("delivery text must carry the sender id: %s", text)
	}
	if !strings.Contains(text, "contact_id=sc_bob") {
		t.Fatalf("delivery text must carry the recipient id: %s", text)
	}
	if !strings.Contains(text, "please review the PR") {
		t.Fatalf("delivery text must carry the body: %s", text)
	}
	if strings.Contains(text, "hop=") {
		t.Fatalf("hop 0 should not print a hop token: %s", text)
	}
}

// A hop of 0 must not force the recipient into a reply loop.
func TestSessionCollabDeliveryTextWithoutReplyAddress(t *testing.T) {
	msg := sessioncollab.MailMessage{ID: "msg_1", From: "sc_alice", To: "sc_bob", ReplyTo: "sc_alice", Body: "n"}
	text := sessionCollabDeliveryText(msg, 0)
	if strings.Contains(text, "无需回复") {
		t.Fatalf("a message with a ReplyTo must not be labelled one-way: %s", text)
	}
}
