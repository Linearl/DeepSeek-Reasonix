package agent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
)

// TestTalkToSessionRejectsUnresolvableThreadId: a thread_id that is not a message in
// the caller's own mailbox must be reported by the call itself, and nothing may be
// written to the peer's inbox (task 194-P0: it used to answer "queued", land in the
// peer's inbox, and only then be dropped by the pump — invisible to both sides).
func TestTalkToSessionRejectsUnresolvableThreadId(t *testing.T) {
	dir := t.TempDir()
	mailDir := filepath.Join(dir, "mail")
	self := filepath.Join(dir, "self.jsonl")
	other := filepath.Join(dir, "other.jsonl")
	writeEmpty(t, self)
	writeEmpty(t, other)
	otherID, err := EnsureContactID(other)
	if err != nil {
		t.Fatal(err)
	}

	store := sessioncollab.NewMailStore(mailDir)
	// The id of a message this session itself sent: it lives in the peer's mailbox.
	own, err := store.Deliver(context.Background(), sessioncollab.MailMessage{To: otherID, From: SessionContactID(self), Body: "hello"})
	if err != nil {
		t.Fatal(err)
	}

	sendTool := NewTalkToSessionTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            mailDir,
		ResolveSessionPath: func() string { return self },
	})

	args, err := json.Marshal(map[string]any{"to": otherID, "message": "status?", "thread_id": own.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sendTool.Execute(context.Background(), args); !errors.Is(err, sessioncollab.ErrReplyThreadUnknown) {
		t.Fatalf("want ErrReplyThreadUnknown from the call itself, got %v", err)
	}

	box, err := store.Inbox(otherID)
	if err != nil {
		t.Fatal(err)
	}
	if len(box) != 1 {
		t.Fatalf("a rejected reply must not be written: peer inbox holds %d messages, want 1", len(box))
	}
}

// TestTalkToSessionAcceptsInboundThreadId: the documented usage — answering the id of
// the message you received — keeps working and is still written through (task 194
// acceptance: normal thread_id behaviour is unchanged).
func TestTalkToSessionAcceptsInboundThreadId(t *testing.T) {
	dir := t.TempDir()
	mailDir := filepath.Join(dir, "mail")
	self := filepath.Join(dir, "self.jsonl")
	other := filepath.Join(dir, "other.jsonl")
	writeEmpty(t, self)
	writeEmpty(t, other)
	otherID, err := EnsureContactID(other)
	if err != nil {
		t.Fatal(err)
	}
	selfID, err := EnsureContactID(self)
	if err != nil {
		t.Fatal(err)
	}

	store := sessioncollab.NewMailStore(mailDir)
	inbound, err := store.Deliver(context.Background(), sessioncollab.MailMessage{To: selfID, From: otherID, Body: "ping"})
	if err != nil {
		t.Fatal(err)
	}

	sendTool := NewTalkToSessionTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            mailDir,
		ResolveSessionPath: func() string { return self },
	})
	args, err := json.Marshal(map[string]any{"to": otherID, "message": "pong", "thread_id": inbound.ID})
	if err != nil {
		t.Fatal(err)
	}
	out, err := sendTool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("answering the inbound message must succeed: %v", err)
	}
	var res struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("result: %v (%s)", err, out)
	}
	if res.Status != "queued" {
		t.Fatalf("status = %q, want queued", res.Status)
	}
	box, err := store.Inbox(otherID)
	if err != nil {
		t.Fatal(err)
	}
	if len(box) != 1 || box[0].ThreadID != inbound.ID {
		t.Fatalf("reply must carry the thread it answers: %+v", box)
	}
}

// TestTalkToSessionSchemaNamesOwnInboundThreadId: task 535 — the schema text must
// say the reply id is the INBOUND id from the caller's own mailbox (task 187 实测：
// 用自己发出的 id 会落库后被链路校验丢弃), and the old ambiguous copy
// ("the threadId it carried") must be gone. The schema is static text: it must be
// byte-identical with the panel switch on or off (关闭态零行为 — the copy cannot
// leak gate state).
func TestTalkToSessionSchemaNamesOwnInboundThreadId(t *testing.T) {
	on := NewTalkToSessionTool(SessionCollabConfig{Enabled: true}).Schema()
	off := NewTalkToSessionTool(SessionCollabConfig{Enabled: false}).Schema()
	if string(on) != string(off) {
		t.Fatalf("schema must not depend on the gate state (关闭态零行为): on=%s off=%s", on, off)
	}

	s := string(on)
	if !strings.Contains(s, "message id you RECEIVED") || !strings.Contains(s, "never the id of a message you sent") {
		t.Fatalf("schema must name the own-inbound id (task 535), got: %s", s)
	}
	if strings.Contains(s, "pass the threadId it carried") {
		t.Fatalf("old ambiguous thread_id copy must be gone (task 535), got: %s", s)
	}
}

// TestTalkToSessionRefusesHopWithoutThreadId: 任务548 P0-1（源头拒发）——
// hop>0 却不带 thread_id 的组合曾在发送侧被静默接受，落盘后被消费泵的溯源
// 门丢弃：发送方只看到 "queued"（事后核查 seen 还命中——拒收也结算游标），
// 目标永远收不到（2026-10-06 事故四封派单）。修复后发送即拒、错误可见，
// 模型能自纠重发；任何情况下对端收件箱都不得出现这封信。
func TestTalkToSessionRefusesHopWithoutThreadId(t *testing.T) {
	dir := t.TempDir()
	mailDir := filepath.Join(dir, "mail")
	self := filepath.Join(dir, "self.jsonl")
	other := filepath.Join(dir, "other.jsonl")
	writeEmpty(t, self)
	writeEmpty(t, other)
	otherID, err := EnsureContactID(other)
	if err != nil {
		t.Fatal(err)
	}
	selfID, err := EnsureContactID(self)
	if err != nil {
		t.Fatal(err)
	}

	store := sessioncollab.NewMailStore(mailDir)
	sendTool := NewTalkToSessionTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            mailDir,
		ResolveSessionPath: func() string { return self },
	})

	// 事故形态：hop=2、无 thread_id——发送即拒，错误里给出可执行的下一步。
	args, err := json.Marshal(map[string]any{"to": otherID, "message": "3 支待合并", "hop": 2})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sendTool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("hop>0 without thread_id must be refused at send time")
	}
	if !strings.Contains(err.Error(), "thread_id") || !strings.Contains(err.Error(), "hop") {
		t.Fatalf("the refusal must name hop and thread_id so the model can self-correct: %v", err)
	}
	box, err := store.Inbox(otherID)
	if err != nil {
		t.Fatal(err)
	}
	if len(box) != 0 {
		t.Fatalf("a refused send must not be written: peer inbox holds %d messages", len(box))
	}

	// 合法路径零回归一：hop=0 新链照常通过。
	args, err = json.Marshal(map[string]any{"to": otherID, "message": "new chain", "hop": 0})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := sendTool.Execute(context.Background(), args); err != nil {
		t.Fatalf("hop=0 new chain must keep working: %v (%s)", err, out)
	}

	// 合法路径零回归二：hop>0 + 收到的入向消息 id 照常通过（对端先来一封，
	// 本端按其 thread 回信）。
	inbound, err := store.Deliver(context.Background(), sessioncollab.MailMessage{To: selfID, From: otherID, Body: "ping"})
	if err != nil {
		t.Fatal(err)
	}
	args, err = json.Marshal(map[string]any{"to": otherID, "message": "pong", "thread_id": inbound.ID, "hop": 1})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := sendTool.Execute(context.Background(), args); err != nil {
		t.Fatalf("hop>0 with the inbound thread_id must keep working: %v (%s)", err, out)
	}
	box, err = store.Inbox(otherID)
	if err != nil {
		t.Fatal(err)
	}
	if len(box) != 2 {
		t.Fatalf("legal sends must land exactly once each: peer inbox holds %d", len(box))
	}
}

// TestTalkToSessionSchemaHopRequiresThreadId: 任务548 —— hop 参数文档必须写明
// 「hop>0 必须携带父 thread_id，否则发送时即拒」，让模型在读文档阶段就能避坑
// （关闭态零行为：schema 为静态文本，不得依赖开关状态）。
func TestTalkToSessionSchemaHopRequiresThreadId(t *testing.T) {
	on := NewTalkToSessionTool(SessionCollabConfig{Enabled: true}).Schema()
	off := NewTalkToSessionTool(SessionCollabConfig{Enabled: false}).Schema()
	if string(on) != string(off) {
		t.Fatalf("schema must not depend on the gate state: on=%s off=%s", on, off)
	}
	if !strings.Contains(string(on), "hop>0 REQUIRES thread_id") {
		t.Fatalf("schema must pin the hop/thread_id coupling (task 548), got: %s", on)
	}
}
