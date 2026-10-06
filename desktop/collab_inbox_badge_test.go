package main

// Task 320 遗留 #1: the icon-row unread badge counts on a strictly read-only
// path. Three contracts pinned here:
//  1. unread = the recipient's seen cursor does not cover the entry;
//  2. dismissed entries leave the default view and stop counting;
//  3. the count never prunes the transport layer — an entry older than the
//     retention window still exists on disk after counting (the panel path
//     WOULD have pruned it; the badge path must not).

import (
	"context"
	"testing"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/sessioncollab"
)

func TestCountUnreadCollabMailBadge(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()

	mail := sessioncollab.NewMailStore(config.SessionCollabMailDir())
	deliver := func(msg sessioncollab.MailMessage) sessioncollab.MailMessage {
		t.Helper()
		out, err := mail.Deliver(context.Background(), msg)
		if err != nil {
			t.Fatalf("deliver %q: %v", msg.Body, err)
		}
		return out
	}
	m1 := deliver(sessioncollab.MailMessage{From: "sc_alice", To: "sc_main", Body: "badge one"})
	m2 := deliver(sessioncollab.MailMessage{From: "sc_alice", To: "sc_main", Body: "badge two"})
	// The aged entry is asserted through mail.History(context.Background(), ) below, not an id.
	deliver(sessioncollab.MailMessage{
		From: "sc_alice", To: "sc_main", Body: "aged beyond the 7d retention",
		At: time.Now().Add(-8 * 24 * time.Hour).UnixMilli(),
	})

	n, err := app.CountUnreadCollabMail()
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Fatalf("unread = %d, want 3 (two fresh + one aged)", n)
	}
	// 只读证明：计数后超保留期的信件仍在传输层（panel 路径此时已把它 prune 掉）。
	if rows, degraded := mail.History(context.Background()); degraded || len(rows) != 3 {
		t.Fatalf("history rows after count = %d (degraded=%v), want 3 healthy — the badge path must never prune", len(rows), degraded)
	}

	// 收件方 Ack（seen 游标盖上）→ 不再计数。
	if err := mail.Ack(context.Background(), "sc_main", m1.ID); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if n, err = app.CountUnreadCollabMail(); err != nil {
		t.Fatalf("count after ack: %v", err)
	} else if n != 2 {
		t.Fatalf("unread after ack = %d, want 2", n)
	}

	// 消除 → 默认视图不再计数。
	if _, err := collabInboxStore().Dismiss(context.Background(), []string{m2.ID}); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if n, err = app.CountUnreadCollabMail(); err != nil {
		t.Fatalf("count after dismiss: %v", err)
	} else if n != 1 {
		t.Fatalf("unread after dismiss = %d, want 1 (only the aged entry remains)", n)
	}
}
