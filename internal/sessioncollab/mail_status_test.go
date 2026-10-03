package sessioncollab

import (
	"context"
	"testing"
)

// Task 221#6 claim bookkeeping: the desktop badge (UnreadMailCount) reads
// InboxStatus, so the number a user sees must equal the inbox's ground truth
// — delivered-but-unacked messages — at every stage of the consume cycle.
// Ack is the only cursor advance; a probe never moves it.
func TestInboxStatusMatchesGroundTruthThroughConsumeCycle(t *testing.T) {
	mail := NewMailStore(t.TempDir())
	deliver := func(body string) MailMessage {
		msg, err := mail.Deliver(context.Background(), MailMessage{To: "sc_a", From: "sc_b", Body: body})
		if err != nil {
			t.Fatalf("deliver %q: %v", body, err)
		}
		return msg
	}
	status := func() int {
		unread, _ := mail.InboxStatus("sc_a")
		return unread
	}

	first := deliver("one")
	second := deliver("two")
	third := deliver("three")

	if got := status(); got != 3 {
		t.Fatalf("fresh inbox must report 3 unread (ground truth = unacked deliveries), got %d", got)
	}

	// Claim offers everything but must not move the badge: two-phase
	// delivery keeps unread == unacked until the explicit Ack.
	pending, _, err := mail.Claim(context.Background(), "sc_a")
	if err != nil || len(pending) != 3 {
		t.Fatalf("claim: %v %v", pending, err)
	}
	if got := status(); got != 3 {
		t.Fatalf("a claim must not clear the badge (probe is read-only), got %d", got)
	}

	// One ack: badge tracks the partial consume exactly.
	if err := mail.Ack(context.Background(), "sc_a", first.ID); err != nil {
		t.Fatal(err)
	}
	if got := status(); got != 2 {
		t.Fatalf("after one ack the badge must drop to 2, got %d", got)
	}

	// "read all" is the remaining acks: the badge reaches zero and stays
	// there across re-probes (no cursor regression).
	if err := mail.Ack(context.Background(), "sc_a", second.ID, third.ID); err != nil {
		t.Fatal(err)
	}
	if got := status(); got != 0 {
		t.Fatalf("after acking everything the badge must read 0, got %d", got)
	}
	if got := status(); got != 0 {
		t.Fatalf("a repeated probe must not resurrect unread, got %d", got)
	}

	// Late mail re-arms the badge on top of the consumed cursor.
	fourth := deliver("four")
	if got := status(); got != 1 {
		t.Fatalf("new delivery must raise the badge to 1, got %d", got)
	}
	if err := mail.Ack(context.Background(), "sc_a", fourth.ID); err != nil {
		t.Fatal(err)
	}
	if got := status(); got != 0 {
		t.Fatalf("badge must return to 0, got %d", got)
	}
}

// The desktop badge treats "no mailbox identity" as zero. At the store layer
// the same contract shows up as: an unknown contact has an empty inbox and
// reports zero unread with a zero last-delivery stamp.
func TestInboxStatusUnknownContactIsZero(t *testing.T) {
	mail := NewMailStore(t.TempDir())
	unread, last := mail.InboxStatus("sc_never_mailboxed")
	if unread != 0 || last != 0 {
		t.Fatalf("unknown contact must report 0/0, got %d/%d", unread, last)
	}
}
