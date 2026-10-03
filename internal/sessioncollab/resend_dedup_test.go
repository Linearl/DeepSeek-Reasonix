package sessioncollab

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 任务461 P8 ①：投递层内容幂等。同 from+to+精确内容在重发窗口内返原 id
// （不再落新行）；窗口外/不同内容/不同收件人照常投递。全部真断言。

func seedInboxLine(t *testing.T, dir, contact string, msg MailMessage) {
	t.Helper()
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, contact+".inbox.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
}

func inboxLineCount(t *testing.T, dir, contact string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, contact+".inbox.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

func TestDeliverDedupesSameContentWithinWindow(t *testing.T) {
	dir := t.TempDir()
	s := NewMailStore(dir)
	ctx := context.Background()
	first, err := s.Deliver(ctx, MailMessage{From: "sc_a", To: "sc_b", Body: "same content"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Deliver(ctx, MailMessage{From: "sc_a", To: "sc_b", Body: "same content"})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("resent mail got new id %s, want original %s", second.ID, first.ID)
	}
	if second.ThreadID != first.ThreadID || second.At != first.At {
		t.Fatalf("resent mail drifted: thread %s/%s at %d/%d", second.ThreadID, first.ThreadID, second.At, first.At)
	}
	if n := inboxLineCount(t, dir, "sc_b"); n != 1 {
		t.Fatalf("inbox has %d lines, want 1 (no duplicate row)", n)
	}
}

func TestDeliverKeepsDistinctContentAndContacts(t *testing.T) {
	dir := t.TempDir()
	s := NewMailStore(dir)
	ctx := context.Background()
	a, err := s.Deliver(ctx, MailMessage{From: "sc_a", To: "sc_b", Body: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Deliver(ctx, MailMessage{From: "sc_a", To: "sc_b", Body: "hello — follow-up"})
	if err != nil {
		t.Fatal(err)
	}
	if b.ID == a.ID {
		t.Fatal("different content must not dedupe")
	}
	// 同内容发往不同联系人：各自投递（To 参与键）。
	c, err := s.Deliver(ctx, MailMessage{From: "sc_a", To: "sc_c", Body: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == a.ID {
		t.Fatal("same content to a different contact must not dedupe")
	}
	if n := inboxLineCount(t, dir, "sc_b"); n != 2 {
		t.Fatalf("sc_b has %d lines, want 2", n)
	}
	if n := inboxLineCount(t, dir, "sc_c"); n != 1 {
		t.Fatalf("sc_c has %d lines, want 1", n)
	}
}

func TestDeliverDedupWindowExpiresForOrdinaryMail(t *testing.T) {
	dir := t.TempDir()
	s := NewMailStore(dir)
	ctx := context.Background()
	old := MailMessage{ID: "msg_old", ThreadID: "msg_old", From: "sc_a", To: "sc_b",
		Body: "periodic hello", At: time.Now().Add(-resendDedupWindowDefault - time.Minute).UnixMilli()}
	seedInboxLine(t, dir, "sc_b", old)

	fresh, err := s.Deliver(ctx, MailMessage{From: "sc_a", To: "sc_b", Body: "periodic hello"})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ID == old.ID {
		t.Fatal("same content past the ordinary window must deliver as a new message")
	}
	if n := inboxLineCount(t, dir, "sc_b"); n != 2 {
		t.Fatalf("inbox has %d lines, want 2", n)
	}
}

func TestDeliverDedupSystemKindUsesLongWindow(t *testing.T) {
	dir := t.TempDir()
	s := NewMailStore(dir)
	ctx := context.Background()
	old := MailMessage{ID: "msg_sys", ThreadID: "msg_sys", From: "sc_heartbeat", To: "sc_b",
		Body: "heartbeat confirmation", Kind: "system",
		At: time.Now().Add(-2 * time.Hour).UnixMilli()}
	seedInboxLine(t, dir, "sc_b", old)

	again, err := s.Deliver(ctx, MailMessage{From: "sc_heartbeat", To: "sc_b", Body: "heartbeat confirmation", Kind: "system"})
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != old.ID {
		t.Fatalf("system-stamped mail stays idempotent for %v, got new id %s", resendDedupWindowSystem, again.ID)
	}
	if n := inboxLineCount(t, dir, "sc_b"); n != 1 {
		t.Fatalf("inbox has %d lines, want 1", n)
	}
	// 普通邮件的窗口不因 system 长窗而放宽：同内容无戳 → 新 id。
	plain, err := s.Deliver(ctx, MailMessage{From: "sc_heartbeat", To: "sc_b", Body: "heartbeat confirmation"})
	if err != nil {
		t.Fatal(err)
	}
	if plain.ID == old.ID {
		t.Fatal("unstamped mail must use the ordinary window, not the system window")
	}
}
