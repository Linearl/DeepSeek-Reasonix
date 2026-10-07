package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
)

type drainPayload struct {
	Settled  bool `json:"settled"`
	Took     int  `json:"took"`
	Refused  int  `json:"refused"`
	Unread   int  `json:"unreadAfter"`
	Messages []struct {
		ID       string `json:"id"`
		From     string `json:"fromContactId"`
		Body     string `json:"body"`
		ThreadID string `json:"threadId"`
		ReplyTo  string `json:"replyTo"`
		Text     string `json:"text"`
	} `json:"messages"`
	RefusedMsgs []struct {
		ID string `json:"id"`
	} `json:"refusedMessages"`
}

func execDrain(t *testing.T, cfg SessionCollabConfig, args string) drainPayload {
	t.Helper()
	out, err := NewDrainInboxTool(cfg).Execute(nil, []byte(args))
	if err != nil {
		t.Fatal(err)
	}
	var p drainPayload
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("drain_inbox must return JSON: %v\n%s", err, out)
	}
	return p
}

// drainFixture creates an addressable session and returns its contact id.
func drainFixture(t *testing.T, dir, stem, title, contact string) string {
	t.Helper()
	p := filepath.Join(dir, stem+".jsonl")
	writeEmpty(t, p)
	if err := UpdateBranchMeta(p, true, func(m *BranchMeta) error {
		m.CustomTitle = title
		m.ContactID = contact
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return p
}

// Task 235 acceptance: settle=true pulls unread and zeroes InboxStatus;
// settle=false peeks without advancing the cursor.
func TestDrainInboxSettleZeroesUnreadPeekDoesNot(t *testing.T) {
	dir := t.TempDir()
	mailDir := filepath.Join(t.TempDir(), "mail")
	mePath := drainFixture(t, dir, "me", "Me", "sc_me")
	_ = mePath

	cfg := SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            mailDir,
		CurrentSessionPath: mePath,
		CurrentContactID:   "sc_me",
	}

	mail := sessioncollab.NewMailStore(mailDir)
	for i, body := range []string{"hello one", "hello two"} {
		if _, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{
			From:        "sc_peer",
			FromSession: filepath.Join(dir, "peer.jsonl"),
			To:          "sc_me",
			Body:        body,
			ReplyTo:     "sc_peer",
		}); err != nil {
			t.Fatalf("deliver %d: %v", i, err)
		}
	}

	// Peek: both visible, cursor untouched.
	peek := execDrain(t, cfg, `{"settle":false}`)
	if peek.Settled {
		t.Fatal("peek must report settled=false")
	}
	if peek.Took != 2 {
		t.Fatalf("peek took %d, want 2", peek.Took)
	}
	if peek.Unread != 2 {
		t.Fatalf("peek unreadAfter %d, want 2 (cursor must not advance)", peek.Unread)
	}
	if peek.Messages[0].Body == "" || peek.Messages[0].Text == "" {
		t.Fatal("peek must surface body and rendered text")
	}
	if !strings.Contains(peek.Messages[0].Text, "talk_to_session") {
		t.Fatalf("rendered text must carry the reply contract:\n%s", peek.Messages[0].Text)
	}

	// Settle: both consumed, unread drops to zero.
	got := execDrain(t, cfg, `{}`)
	if !got.Settled {
		t.Fatal("default must settle (drain semantics)")
	}
	if got.Took != 2 {
		t.Fatalf("settle took %d, want 2", got.Took)
	}
	if got.Unread != 0 {
		t.Fatalf("settle unreadAfter %d, want 0", got.Unread)
	}

	// Second settle: empty.
	again := execDrain(t, cfg, `{}`)
	if again.Took != 0 || again.Unread != 0 {
		t.Fatalf("second drain took=%d unread=%d, want 0/0", again.Took, again.Unread)
	}
}

// Task 235: limit caps how many are taken; the tail stays queued.
func TestDrainInboxLimitLeavesTailQueued(t *testing.T) {
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
	mail := sessioncollab.NewMailStore(mailDir)
	// 任务461 P8 ①：同内容新线程投递会折叠，limit 语义测试需要 5 条独立消息。
	for i := 0; i < 5; i++ {
		if _, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{
			From: "sc_peer", To: "sc_me", Body: fmt.Sprintf("m-%d", i), ReplyTo: "sc_peer",
		}); err != nil {
			t.Fatal(err)
		}
	}
	got := execDrain(t, cfg, `{"limit":2}`)
	if got.Took != 2 {
		t.Fatalf("took %d, want 2", got.Took)
	}
	if got.Unread != 3 {
		t.Fatalf("unreadAfter %d, want 3", got.Unread)
	}
}

// Hop-ceiling messages are refused by Claim and always acked so they do not
// reappear. Deliver enforces the ceiling at write time too, so the refused
// path is exercised by delivering under a high limit and claiming under a low
// one (the live ceiling drops after the message landed).
func TestDrainInboxRefusedAreAcked(t *testing.T) {
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
		HopLimit:           func() int { return 1 },
	}
	// Write under the package default ceiling (MaxHop=5) so hop=4 is accepted
	// at Deliver. Claim under ClampHopLimit floor (MinHop=3) then refuses it
	// because 4 > 3. 任务548 P0-1: the write side now also requires a parent
	// threadId for hop>0, so the probe names one to stay write-acceptable.
	writer := sessioncollab.NewMailStoreWithHopLimit(mailDir, sessioncollab.MaxHop)
	if _, err := writer.Deliver(context.Background(), sessioncollab.MailMessage{
		From: "sc_peer", To: "sc_me", Body: "too deep", Hop: 4, ReplyTo: "sc_peer", ThreadID: "msg_chain",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Deliver(context.Background(), sessioncollab.MailMessage{
		From: "sc_peer", To: "sc_me", Body: "ok", Hop: 0, ReplyTo: "sc_peer",
	}); err != nil {
		t.Fatal(err)
	}
	got := execDrain(t, cfg, `{}`)
	if got.Refused != 1 {
		t.Fatalf("refused %d, want 1", got.Refused)
	}
	if got.Took != 1 {
		t.Fatalf("took %d, want 1", got.Took)
	}
	if got.Unread != 0 {
		t.Fatalf("unreadAfter %d, want 0 (refused must be acked too)", got.Unread)
	}
	// Second drain sees nothing.
	again := execDrain(t, cfg, `{}`)
	if again.Took != 0 || again.Refused != 0 {
		t.Fatalf("second drain took=%d refused=%d, want 0/0", again.Took, again.Refused)
	}
}

// Naming: the acceptance grep must find drain_inbox / DrainInbox.
func TestDrainInboxNamedForAcceptance(t *testing.T) {
	tool := NewDrainInboxTool(SessionCollabConfig{Enabled: true})
	if tool.Name() != "drain_inbox" {
		t.Fatalf("Name() = %q, want drain_inbox", tool.Name())
	}
	if tool.ReadOnly() {
		t.Fatal("drain_inbox must not be ReadOnly — settle advances the cursor")
	}
}

// Task 487: mirror of desktop sessionCollabDeliveryText — a From-bearing
// message without ReplyTo (a task-309 read receipt) must not be labelled
// 「发送方未登记」; the trailer falls back to From as the reply address.
func TestDrainInboxRenderTextReadReceiptSenderConsistent(t *testing.T) {
	receipt := sessioncollab.MailMessage{
		ID:   "msg_r",
		From: "sc_main",
		To:   "sc_worker",
		Body: "已读回执：你的消息 msg_1 已进入本会话上下文。本条为系统回执，无需回复。",
		Kind: "system",
	}
	text := drainInboxRenderText(receipt, 0)
	if !strings.Contains(text, "来自 contact_id=sc_main") {
		t.Fatalf("header must carry the sender id: %s", text)
	}
	if strings.Contains(text, "未登记") || strings.Contains(text, "无法回信") {
		t.Fatalf("a From-bearing receipt must not be labelled unregistered: %s", text)
	}
	if strings.Count(text, "contact_id=sc_main") < 2 {
		t.Fatalf("header and trailer must agree on the sender id: %s", text)
	}

	oneway := drainInboxRenderText(sessioncollab.MailMessage{
		ID: "msg_2", To: "sc_worker", Body: "notice",
	}, 0)
	if !strings.Contains(oneway, "未登记") || !strings.Contains(oneway, "无法回信") {
		t.Fatalf("an empty From must keep the one-way notice: %s", oneway)
	}
}
