package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
)

// appendRawLine writes a line straight into a mailbox file, standing in for a
// sender that wrote a record the validation path would have rejected.
func appendRawLine(path, line string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}

// newCollabTestMail builds an isolated mailbox and returns a delivery harness
// whose enqueue always succeeds unless failFor matches the message body.
func newCollabTestMail(t *testing.T) (*sessioncollab.MailStore, string) {
	t.Helper()
	dir := t.TempDir()
	return sessioncollab.NewMailStore(filepath.Join(dir, "mail")), "sc_target"
}

func collabTestDelivery(t *testing.T, mail *sessioncollab.MailStore, fail func(sessioncollab.MailMessage) error) (collabDelivery, *[]string, *[]string) {
	t.Helper()
	deliveredBodies := &[]string{}
	notices := &[]string{}
	d := collabDelivery{
		enqueue: func(msg sessioncollab.MailMessage, body string) (bool, error) {
			if fail != nil {
				if err := fail(msg); err != nil {
					return false, err
				}
			}
			*deliveredBodies = append(*deliveredBodies, body)
			return false, nil
		},
		notify: func(msg sessioncollab.MailMessage, kind, text string) {
			_ = kind
			// Route notices through the real store so the "sender is told" claim
			// is verified against the sender's actual inbox.
			if strings.TrimSpace(msg.From) != "" {
				_, _ = mail.Deliver(sessioncollab.MailMessage{
					To: msg.From, Body: text, Hop: msg.Hop, ThreadID: msg.ThreadID,
				})
			}
			*notices = append(*notices, text)
		},
		deriveHop: func(sessioncollab.MailMessage) (int, error) { return 0, nil },
		render:    func(msg sessioncollab.MailMessage, hop int) string { return msg.Body },
	}
	return d, deliveredBodies, notices
}

// The audit's F1: a claim that consumed the cursor before delivery lost every
// message that failed afterwards. A failed delivery must stay pending.
func TestDeliveryFailureDoesNotLoseMessage(t *testing.T) {
	mail, target := newCollabTestMail(t)
	if _, err := mail.Deliver(sessioncollab.MailMessage{From: "sc_from", To: target, Body: "first"}); err != nil {
		t.Fatal(err)
	}
	d, _, notices := collabTestDelivery(t, mail, func(sessioncollab.MailMessage) error {
		return errors.New("workspace not ready")
	})
	delivered, refused, err := runCollabDelivery(mail, target, d)
	if delivered != 0 || refused != 0 {
		t.Fatalf("nothing should have settled: %d/%d", delivered, refused)
	}
	if err == nil {
		t.Fatal("the failure must surface to the caller, not only to the log")
	}
	if len(*notices) != 1 {
		t.Fatalf("the sender must be told once, got %d notices", len(*notices))
	}
	pending, _, claimErr := mail.Claim(target)
	if claimErr != nil || len(pending) != 1 {
		t.Fatalf("the message must still be pending for retry, got %d (%v)", len(pending), claimErr)
	}
}

// The retry must actually deliver, and only then settle the message.
func TestDeliveryRetrySucceedsAfterTransientFailure(t *testing.T) {
	mail, target := newCollabTestMail(t)
	if _, err := mail.Deliver(sessioncollab.MailMessage{From: "sc_from", To: target, Body: "first"}); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	d, bodies, _ := collabTestDelivery(t, mail, func(sessioncollab.MailMessage) error {
		attempts++
		if attempts == 1 {
			return errors.New("workspace not ready")
		}
		return nil
	})
	if _, _, err := runCollabDelivery(mail, target, d); err == nil {
		t.Fatal("first pass must report the failure")
	}
	delivered, refused, err := runCollabDelivery(mail, target, d)
	if err != nil || delivered != 1 || refused != 0 {
		t.Fatalf("retry must deliver: %d/%d (%v)", delivered, refused, err)
	}
	if len(*bodies) != 1 {
		t.Fatalf("exactly one delivery expected, got %d", len(*bodies))
	}
	after, _, _ := mail.Claim(target)
	if len(after) != 0 {
		t.Fatalf("settled message must not be re-offered, got %d", len(after))
	}
}

// A batch is per-message: one bad message must not block or drop its siblings.
func TestOneFailedMessageDoesNotBlockTheRest(t *testing.T) {
	mail, target := newCollabTestMail(t)
	for _, body := range []string{"ok-1", "bad", "ok-2"} {
		if _, err := mail.Deliver(sessioncollab.MailMessage{From: "sc_from", To: target, Body: body}); err != nil {
			t.Fatal(err)
		}
	}
	d, bodies, _ := collabTestDelivery(t, mail, func(msg sessioncollab.MailMessage) error {
		if msg.Body == "bad" {
			return errors.New("workspace not ready")
		}
		return nil
	})
	delivered, _, err := runCollabDelivery(mail, target, d)
	if err == nil {
		t.Fatal("the failing message must surface")
	}
	if delivered != 2 || len(*bodies) != 2 {
		t.Fatalf("the two good messages must land: delivered=%d bodies=%v", delivered, *bodies)
	}
	pending, _, _ := mail.Claim(target)
	if len(pending) != 1 || pending[0].Body != "bad" {
		t.Fatalf("only the failed message should remain pending, got %+v", pending)
	}
}

// A hop-exhausted message is reported to its sender and settled, so it cannot
// be re-reported forever.
func TestHopExhaustedIsReportedAndSettled(t *testing.T) {
	mail, target := newCollabTestMail(t)
	if _, err := mail.Deliver(sessioncollab.MailMessage{From: "sc_from", To: target, Body: "ok"}); err != nil {
		t.Fatal(err)
	}
	d, bodies, notices := collabTestDelivery(t, mail, nil)
	// Force the over-limit record the way a lying relay would.
	raw := mail.InboxPath(target)
	if err := appendRawLine(raw, `{"id":"msg_deep","fromContactId":"sc_from","toContactId":"`+target+
		`","body":"too-deep","hop":6}`); err != nil {
		t.Fatal(err)
	}
	delivered, refused, err := runCollabDelivery(mail, target, d)
	if err != nil {
		t.Fatal(err)
	}
	if delivered != 1 || refused != 1 {
		t.Fatalf("want 1 delivered + 1 refused, got %d/%d", delivered, refused)
	}
	if len(*bodies) != 1 || (*bodies)[0] != "ok" {
		t.Fatalf("only the in-limit message may reach the target: %v", *bodies)
	}
	if len(*notices) != 1 {
		t.Fatalf("the sender must be told, got %d notices", len(*notices))
	}
	// Both are settled, so a second pass is quiet.
	again, _, _ := mail.Claim(target)
	if len(again) != 0 {
		t.Fatalf("settled messages must not reappear: %+v", again)
	}
}

// A steer that cannot inject must be reported as degraded, and the message is
// still delivered as a queued follow-up.
func TestDegradedSteerIsReported(t *testing.T) {
	mail, target := newCollabTestMail(t)
	if _, err := mail.Deliver(sessioncollab.MailMessage{
		From: "sc_from", To: target, Body: "urgent", Delivery: string(sessioncollab.DeliverySteer),
	}); err != nil {
		t.Fatal(err)
	}
	d, bodies, notices := collabTestDelivery(t, mail, nil)
	delivered, _, err := runCollabDelivery(mail, target, d)
	if err != nil || delivered != 1 {
		t.Fatalf("degraded steer is still a delivery: %d (%v)", delivered, err)
	}
	if len(*bodies) != 1 {
		t.Fatalf("the body must still reach the target: %v", *bodies)
	}
	if len(*notices) != 1 {
		t.Fatalf("the sender must hear about the degradation, got %d", len(*notices))
	}
}

// Provenance failure is a refusal, not a delivery: a reply that cannot name a
// parent is settled and reported rather than handed over.
func TestUnverifiableProvenanceIsRefusedAndReported(t *testing.T) {
	mail, target := newCollabTestMail(t)
	if _, err := mail.Deliver(sessioncollab.MailMessage{
		From: "sc_from", To: target, Body: "relay", ThreadID: "msg_ghost",
	}); err != nil {
		t.Fatal(err)
	}
	d, bodies, notices := collabTestDelivery(t, mail, nil)
	d.deriveHop = func(sessioncollab.MailMessage) (int, error) {
		return 0, errors.New("thread not in sender's mailbox")
	}
	delivered, refused, err := runCollabDelivery(mail, target, d)
	if err != nil {
		t.Fatal(err)
	}
	if delivered != 0 || refused != 1 {
		t.Fatalf("want 0 delivered + 1 refused, got %d/%d", delivered, refused)
	}
	if len(*bodies) != 0 {
		t.Fatal("an unverifiable message must not reach the target")
	}
	if len(*notices) != 1 {
		t.Fatalf("the sender must be told, got %d", len(*notices))
	}
}

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
