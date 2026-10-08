package boot

import (
	"context"
	"path/filepath"
	"testing"

	"reasonix/internal/sessioncollab"
	"reasonix/internal/sessioninbox"
)

// Task 641: the settled probe must consult the delivery record under the
// task-309 coordinates the pump stamps on every row (CollabMsgID = the real
// mail id, CollabMailTo = the recipient contact). The pre-641 closure parsed
// the idempotency key and read the SENDER's cursor — since task 309 the key
// is a content hash and the ack lives under the RECIPIENT's cursor file, so
// the probe answered false forever and the 263/300 recovery drop never fired
// (consumed cross-session messages replayed onto the shelf after every
// install restart).
func TestCollabInboxSettledProbe(t *testing.T) {
	mailDir := filepath.Join(t.TempDir(), "mail")
	mail := sessioncollab.NewMailStore(mailDir)
	ctx := context.Background()

	deliver := func(from, to, body string) sessioncollab.MailMessage {
		msg, err := mail.Deliver(ctx, sessioncollab.MailMessage{From: from, To: to, Body: body})
		if err != nil {
			t.Fatal(err)
		}
		return msg
	}
	// pump-style settle: record the delivery receipt, then ack the recipient's
	// cursor — the exact writes runCollabDelivery performs in order for a
	// settled message. A failed pass records failed_retrying and leaves the
	// mail unacked for the next pass.
	settle := func(msg sessioncollab.MailMessage, outcome string) {
		if err := mail.RecordDeliveryReceipt(ctx, sessioncollab.DeliveryReceipt{
			MessageID: msg.ID,
			From:      msg.From,
			To:        msg.To,
			Outcome:   outcome,
		}); err != nil {
			t.Fatal(err)
		}
		if outcome == sessioncollab.ReceiptFailedRetrying {
			return
		}
		if err := mail.Ack(ctx, msg.To, msg.ID); err != nil {
			t.Fatal(err)
		}
	}

	delivered := deliver("contact-a", "contact-b", "merged-line receipt")
	settle(delivered, sessioncollab.ReceiptInjected)
	queued := deliver("contact-a", "contact-b", "coordinator dispatch")
	settle(queued, sessioncollab.ReceiptQueuedFollowup)
	retrying := deliver("contact-a", "contact-b", "still failing pass")
	settle(retrying, sessioncollab.ReceiptFailedRetrying)
	cursorOnly := deliver("contact-a", "contact-b", "receipt write lost, ack survived")
	if err := mail.Ack(ctx, cursorOnly.To, cursorOnly.ID); err != nil {
		t.Fatal(err)
	}
	receiptOnly := deliver("contact-a", "contact-b", "ack lost, receipt survived")
	settle(receiptOnly, sessioncollab.ReceiptInjected)
	wrongCursor := deliver("contact-a", "contact-b", "acked under the sender only")
	if err := mail.Ack(ctx, wrongCursor.From, wrongCursor.ID); err != nil {
		t.Fatal(err)
	}
	fresh := deliver("contact-a", "contact-b", "never delivered")

	contentKey := func(msg sessioncollab.MailMessage) string {
		return "collab:" + msg.From + ":" + msg.ThreadID + ":deadbeef01"
	}
	meta := func(msg sessioncollab.MailMessage, idem string) sessioninbox.InboxItemMeta {
		return sessioninbox.InboxItemMeta{
			Source:       "collab:" + msg.From,
			CollabMsgID:  msg.ID,
			CollabMailTo: msg.To,
			Idempotency:  idem,
		}
	}

	probe := collabInboxSettledProbe(mailDir)

	cases := []struct {
		name string
		meta sessioninbox.InboxItemMeta
		want bool
	}{
		{"receipt injected", meta(delivered, contentKey(delivered)), true},
		{"receipt queued followup", meta(queued, contentKey(queued)), true},
		{"receipt failed retrying", meta(retrying, contentKey(retrying)), false},
		{"cursor ack only", meta(cursorOnly, contentKey(cursorOnly)), true},
		{"receipt only (cursor ack lost)", meta(receiptOnly, contentKey(receiptOnly)), true},
		// The contact fix: an ack under the SENDER's cursor file does not
		// settle — the pre-641 closure read exactly that file.
		{"ack under sender cursor only", meta(wrongCursor, contentKey(wrongCursor)), false},
		{"never delivered", meta(fresh, contentKey(fresh)), false},
		// No coordinates: no positive evidence, no drop. The content key must
		// never be looked up as a mail id.
		{"content key without delivery record", sessioninbox.InboxItemMeta{
			Source:      "collab:contact-a",
			Idempotency: contentKey(fresh),
		}, false},
		// Non-collab rows carry no mailbox record at all.
		{"non-collab source", sessioninbox.InboxItemMeta{
			Source:      "desktop",
			CollabMsgID: delivered.ID,
		}, false},
	}
	for _, tc := range cases {
		if got := probe(tc.meta); got != tc.want {
			t.Fatalf("%s: probe = %v, want %v (meta %+v)", tc.name, got, tc.want, tc.meta)
		}
	}
}

// Legacy rows: pre-309 per-message rows carried "collab:<msgID>" as the
// idempotency key. The fallback reads that key only against a known
// recipient — a pure pre-309 row (no CollabMailTo) has no consultable
// coordinate and answers false (honest keep, no drop without evidence). A
// content hash never resolves: it contains colons and is not a mail id.
func TestCollabInboxSettledProbeLegacyIdempotencyKey(t *testing.T) {
	mailDir := filepath.Join(t.TempDir(), "mail")
	mail := sessioncollab.NewMailStore(mailDir)
	ctx := context.Background()
	msg, err := mail.Deliver(ctx, sessioncollab.MailMessage{From: "contact-a", To: "contact-b", Body: "legacy delivery"})
	if err != nil {
		t.Fatal(err)
	}
	if err := mail.Ack(ctx, "contact-b", msg.ID); err != nil {
		t.Fatal(err)
	}
	probe := collabInboxSettledProbe(mailDir)
	if !probe(sessioninbox.InboxItemMeta{Source: "collab:contact-a", CollabMailTo: "contact-b", Idempotency: "collab:" + msg.ID}) {
		t.Fatal("legacy per-message key with recipient coordinate: probe = false, want true")
	}
	if probe(sessioninbox.InboxItemMeta{Source: "collab:contact-a", Idempotency: "collab:" + msg.ID}) {
		t.Fatal("legacy key without recipient coordinate: probe = true, want false (no consultable cursor)")
	}
	if probe(sessioninbox.InboxItemMeta{Source: "collab:contact-a", CollabMailTo: "contact-b", Idempotency: "collab:contact-a:thread:01234567"}) {
		t.Fatal("content key fallback: probe = true, want false (content hash is not a mail id)")
	}
}
