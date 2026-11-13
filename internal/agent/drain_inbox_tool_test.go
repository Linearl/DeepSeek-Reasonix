package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/collabchannel"
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

// 任务 1058（drain 路径镜像，与 desktop sessionCollabDeliveryText 同一修复）：
// 正文自带「无需回信」声明戳的系统发件方（意见箱 feedback_inbox 通知），drain
// 渲染不得再按「无 ReplyTo + 有 From」普通回退拼出「回信到 contact_id=...」。
func TestDrainInboxRenderTextOneWayNoticeOverridesReplyFallback(t *testing.T) {
	oneWay := drainInboxRenderText(sessioncollab.MailMessage{
		ID:   "msg_1058",
		From: "sc_minted_inbox_addr",
		To:   "sc_worker",
		Body: "[意见箱] 新反馈（bug）：示例标题\nmd: feedback-x.md\n" + FeedbackInboxOneWayNotice,
	}, 0)
	if strings.Contains(oneWay, "回信到 contact_id=") {
		t.Fatalf("a body that declares 无需回信 must NOT get a reply-address fallback: %s", oneWay)
	}
	if !strings.Contains(oneWay, "单向通知") || !strings.Contains(oneWay, "无需回信") {
		t.Fatalf("the one-way trailer must name the no-reply state: %s", oneWay)
	}

	// 对照：普通 From 消息（无声明戳）仍走原回退指示。
	ordinary := drainInboxRenderText(sessioncollab.MailMessage{
		ID:   "msg_ordinary",
		From: "sc_main",
		To:   "sc_worker",
		Body: "普通系统消息",
	}, 0)
	if !strings.Contains(ordinary, "回信到 contact_id=sc_main") {
		t.Fatalf("an ordinary registered sender must keep the reply fallback: %s", ordinary)
	}
}

// ── tl1566：群聊投递注入头（与 desktop/session_collab_test.go 镜像）─────────

// tl1566 同形状 token 钉——与 desktop/session_collab_test.go 的
// channelGuidanceTokens 镜像：两面（sessionCollabDeliveryText /
// drainInboxRenderText）共用同一清单防漂移，改动必须两边同步。
var channelGuidanceTokens = []string{
	"[跨会话消息·群聊]",
	"群聊：调研组",
	"channel_send",
	"回复方式：完成后用 channel_send 工具把回信发到群聊 调研组",
	"@ 其 contact_id",
	"效果要求",
	"附件引用",
	"投递编号：chf_",
}

// tl1566 验收1（drain 面）：群聊 fan-out 件的注入文本必须带群聊来源 +
// channel_send 回群聊引导 + 效果层尾注；严禁出现 talk_to_session 引导（G2
// 回信引导指向错误的负钉——20261112 用户实测 15:16 AI 被引回对话正是此面）。
func TestDrainInboxRenderTextChannelFanoutGuidesChannelReply(t *testing.T) {
	text := drainInboxRenderText(sessioncollab.MailMessage{
		ID:      "chf_fffd34282aaa2d73",
		From:    "human:local",
		To:      "sc_worker",
		Body:    "帮我确认下构建状态",
		Channel: "调研组",
	}, 0)
	for _, tok := range channelGuidanceTokens {
		if !strings.Contains(text, tok) {
			t.Fatalf("channel fan-out text must carry %q:\n%s", tok, text)
		}
	}
	if strings.Contains(text, "回复方式：完成后用 talk_to_session") {
		t.Fatalf("a group copy must NOT steer the reply into a private talk_to_session exchange:\n%s", text)
	}
	if strings.Contains(text, "会话线程") {
		t.Fatalf("a fan-out id is not a thread — must be labeled 投递编号:\n%s", text)
	}

	// RequireReply 防御位：fan-out 件今天不带该戳，但一旦带上必须照常渲染
	// 索回信要求（与直接件同句式）。
	demanded := drainInboxRenderText(sessioncollab.MailMessage{
		ID: "chf_d", From: "sc_peer", To: "sc_worker", Body: "b",
		Channel: "调研组", RequireReply: true,
	}, 0)
	if !strings.Contains(demanded, "require_reply") {
		t.Fatalf("a demanded channel reply must keep the require_reply line:\n%s", demanded)
	}
}

// tl1566 验收1 负钉（drain 面）：直接跨会话消息形态逐字节不变——头部标签、
// threadId 行、talk_to_session 回信引导全部保持原字节。
func TestDrainInboxRenderTextDirectMailBytesUnchanged(t *testing.T) {
	text := drainInboxRenderText(sessioncollab.MailMessage{
		ID:      "msg_1",
		From:    "sc_alice",
		To:      "sc_bob",
		ReplyTo: "sc_alice",
		Body:    "please review",
	}, 0)
	want := "[跨会话消息] 来自 contact_id=sc_alice → 发至 contact_id=sc_bob\n" +
		"\nplease review\n\n---\n" +
		"会话线程：threadId=msg_1\n" +
		"回复方式：完成后用 talk_to_session 回信到 contact_id=sc_alice，hop 传 1，并把 thread_id 设为 msg_1。"
	if text != want {
		t.Fatalf("direct-mail delivery bytes are frozen (tl1566 negative pin):\ngot:  %q\nwant: %q", text, want)
	}
}

// tl1566 验收3（机制级端到端，drain 面）：群聊 Publish → DrainFanout 落箱 →
// drain_inbox 工具读出的注入文本即带群聊地址与 channel_send 回信引导。回信侧
// 落库（chm_）由 collabchannel 包自测承载。
func TestDrainInboxChannelFanoutEndToEnd(t *testing.T) {
	mailDir := filepath.Join(t.TempDir(), "mail")

	store, err := collabchannel.Open(mailDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateChannel("调研组", "", 0, "sc_me"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish("调研组", "human:local", "帮我确认下构建状态"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DrainFanout(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	cfg := SessionCollabConfig{
		Enabled:          true,
		MailDir:          mailDir,
		CurrentContactID: "sc_me",
	}
	payload := execDrain(t, cfg, `{}`)
	if payload.Took != 1 {
		t.Fatalf("want the fan-out copy drained, took=%d", payload.Took)
	}
	msg := payload.Messages[0]
	if msg.From != "human:local" || !strings.HasPrefix(msg.ID, "chf_") {
		t.Fatalf("fan-out provenance missing: id=%s from=%s", msg.ID, msg.From)
	}
	for _, tok := range channelGuidanceTokens {
		if !strings.Contains(msg.Text, tok) {
			t.Fatalf("drained fan-out text must carry %q:\n%s", tok, msg.Text)
		}
	}
	if strings.Contains(msg.Text, "回复方式：完成后用 talk_to_session") {
		t.Fatalf("drained fan-out text must not steer into a private reply:\n%s", msg.Text)
	}
}
