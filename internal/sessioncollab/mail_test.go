package sessioncollab

import "testing"

// A resumed polling loop must not re-deliver mail it already handed to the
// target; the cursor is what makes async delivery at-least-once instead of
// every-pass.
func TestClaimAdvancesCursorAndDoesNotRedeliver(t *testing.T) {
	mail := NewMailStore(t.TempDir())
	if _, err := mail.Deliver(MailMessage{To: "sc_a", Body: "one", Hop: 0}); err != nil {
		t.Fatal(err)
	}
	first, refused, err := mail.Claim("sc_a")
	if err != nil || len(first) != 1 || len(refused) != 0 {
		t.Fatalf("first claim: %v %v %v", first, refused, err)
	}
	second, _, err := mail.Claim("sc_a")
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Fatalf("cursor did not advance: redelivered %d", len(second))
	}
	if _, err := mail.Deliver(MailMessage{To: "sc_a", Body: "two", Hop: 0}); err != nil {
		t.Fatal(err)
	}
	third, _, err := mail.Claim("sc_a")
	if err != nil || len(third) != 1 || third[0].Body != "two" {
		t.Fatalf("third claim: %v %v", third, err)
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
	// Write an over-limit message directly, bypassing Deliver's validation, to
	// model a relaying session that lied about its depth.
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
	// Refused mail must not come back on the next pass either.
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
