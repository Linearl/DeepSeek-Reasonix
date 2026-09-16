package sessioncollab

import "testing"

// Delivery is two-phase: Claim reports what is pending without consuming it, and
// only Ack settles it. This is what makes delivery at-least-once — a Claim that
// advanced the cursor would drop every message that failed afterwards.
func TestClaimDoesNotConsumeUntilAcked(t *testing.T) {
	mail := NewMailStore(t.TempDir())
	if _, err := mail.Deliver(MailMessage{To: "sc_a", Body: "one"}); err != nil {
		t.Fatal(err)
	}
	first, refused, err := mail.Claim("sc_a")
	if err != nil || len(first) != 1 || len(refused) != 0 {
		t.Fatalf("first claim: %v %v %v", first, refused, err)
	}
	// An un-acked claim must come back: that is the retry that prevents a loss.
	again, _, err := mail.Claim("sc_a")
	if err != nil || len(again) != 1 {
		t.Fatalf("un-acked message must be re-offered, got %d (%v)", len(again), err)
	}
	if err := mail.Ack("sc_a", first[0].ID); err != nil {
		t.Fatal(err)
	}
	after, _, err := mail.Claim("sc_a")
	if err != nil || len(after) != 0 {
		t.Fatalf("acked message must not be re-offered, got %d (%v)", len(after), err)
	}
}

// A redelivered message keeps its id, which is the inbox idempotency key, so the
// retry cannot turn into a duplicate turn.
func TestRedeliveryKeepsMessageIdentity(t *testing.T) {
	mail := NewMailStore(t.TempDir())
	sent, err := mail.Deliver(MailMessage{To: "sc_a", Body: "one"})
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := mail.Claim("sc_a")
	second, _, _ := mail.Claim("sc_a")
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("expected both claims to offer the message")
	}
	if first[0].ID != sent.ID || second[0].ID != sent.ID {
		t.Fatalf("redelivery must keep the id: %q / %q / %q", sent.ID, first[0].ID, second[0].ID)
	}
}

// Peek must not consume: a reader that only wants to render unread state must
// not steal the message from the delivery pass.
func TestPeekDoesNotAdvanceCursor(t *testing.T) {
	mail := NewMailStore(t.TempDir())
	if _, err := mail.Deliver(MailMessage{To: "sc_a", Body: "one"}); err != nil {
		t.Fatal(err)
	}
	if unread, err := mail.Peek("sc_a"); err != nil || len(unread) != 1 {
		t.Fatalf("peek: %v %v", unread, err)
	}
	if claimed, _, err := mail.Claim("sc_a"); err != nil || len(claimed) != 1 {
		t.Fatalf("claim after peek must still deliver: %v %v", claimed, err)
	}
}

// The hop ceiling has to be enforced where mail is handed over, not only where
// it is written: a sender can always report hop=0.
func TestClaimRefusesHopExhausted(t *testing.T) {
	mail := NewMailStore(t.TempDir())
	if _, err := mail.Deliver(MailMessage{To: "sc_a", Body: "deep", Hop: 0}); err != nil {
		t.Fatal(err)
	}
	raw := mail.inboxPath("sc_a")
	if err := appendJSONL(raw, MailMessage{ID: "msg_forced", To: "sc_a", Body: "too deep", Hop: MaxHop + 1}); err != nil {
		t.Fatal(err)
	}
	delivered, refused, err := mail.Claim("sc_a")
	if err != nil {
		t.Fatal(err)
	}
	if len(refused) != 1 || len(delivered) != 1 {
		t.Fatalf("want 1 delivered + 1 refused, got %d/%d", len(delivered), len(refused))
	}
	if refused[0].Body != "too deep" {
		t.Fatalf("refused wrong message: %+v", refused[0])
	}
	// Refused mail is settled by the caller's Ack, not by the claim itself; the
	// caller reports it to the sender first, so it must survive that step.
	if err := mail.Ack("sc_a", refused[0].ID, delivered[0].ID); err != nil {
		t.Fatal(err)
	}
	again, _, err := mail.Claim("sc_a")
	if err != nil || len(again) != 0 {
		t.Fatalf("refused message redelivered: %v %v", again, err)
	}
}

// Deliver must reject an over-limit hop so the common path fails loudly.
func TestDeliverRejectsOverLimitHop(t *testing.T) {
	mail := NewMailStore(t.TempDir())
	if _, err := mail.Deliver(MailMessage{To: "sc_a", Body: "x", Hop: MaxHop + 1}); err == nil {
		t.Fatal("want hop limit error")
	}
	if _, err := mail.Deliver(MailMessage{To: "sc_a", Body: "x", Hop: MaxHop}); err != nil {
		t.Fatalf("boundary hop must pass: %v", err)
	}
}

// ParentThread is what lets the delivery side derive chain depth instead of
// trusting the sender's number. A sender can only legitimately answer a thread
// it was sent, so the parent is looked up in the answering contact's mailbox —
// which is where the original request was delivered.
func TestParentThreadResolvesInReceiversMailbox(t *testing.T) {
	mail := NewMailStore(t.TempDir())
	parent, err := mail.Deliver(MailMessage{From: "sc_a", To: "sc_b", Body: "request"})
	if err != nil {
		t.Fatal(err)
	}
	// sc_a sent it and cannot reply to its own thread on sc_b's behalf.
	if _, ok := mail.ParentThread("sc_a", parent.ID); ok {
		t.Fatal("the sender must not be able to claim the thread it sent")
	}
	// sc_b received it, so sc_b may answer and have its depth derived.
	got, ok := mail.ParentThread("sc_b", parent.ID)
	if !ok || got.ID != parent.ID {
		t.Fatalf("receiver must resolve the parent: %+v %v", got, ok)
	}
	if got.Hop != parent.Hop {
		t.Fatalf("parent hop must be readable: %d vs %d", got.Hop, parent.Hop)
	}
}

// MarkNotified fires once per key so a persistently unreachable target produces
// one status note, not one per retry.
func TestMarkNotifiedIsOncePerKey(t *testing.T) {
	mail := NewMailStore(t.TempDir())
	if !mail.MarkNotified("sc_a", "msg_1:delivery_failed") {
		t.Fatal("first mark must report true")
	}
	if mail.MarkNotified("sc_a", "msg_1:delivery_failed") {
		t.Fatal("second mark must report false")
	}
	if !mail.MarkNotified("sc_a", "msg_1:steer_degraded") {
		t.Fatal("a different kind is a different key")
	}
}
