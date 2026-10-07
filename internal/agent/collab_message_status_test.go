package agent

// 任务 570 (c1)：get_message_status 查询工具 + talk_to_session 返回体增量
// 字段测试。
// 验收锚点：
//   - 回执存在 → known=true 且 status=回执结局（降级场景给出明确信号，验收①
//     的发送方半边）；
//   - 消息双方之外查询 → 显式权限拒绝（task 570 必答 3）；
//   - 无回执但已发送 → status=pending（未知≠成功，不给假绿灯）；
//   - 既有 talk_to_session 返回体零破坏（原字段不动，仅增量）。

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
)

func newMailStoreForTest(t *testing.T, mailDir string) *sessioncollab.MailStore {
	t.Helper()
	return sessioncollab.NewMailStore(mailDir)
}

func newMailMsgForTest(from, to, body string) sessioncollab.MailMessage {
	return sessioncollab.MailMessage{From: from, To: to, Body: body, Delivery: "steer"}
}

func receiptForTest(msg sessioncollab.MailMessage, outcome string) sessioncollab.DeliveryReceipt {
	return sessioncollab.DeliveryReceipt{
		MessageID: msg.ID,
		From:      msg.From,
		To:        msg.To,
		Delivery:  msg.Delivery,
		Outcome:   outcome,
	}
}

// statusTestEnv builds two registered sessions and a shared mail dir.
type statusTestEnv struct {
	dir     string
	mailDir string
	from    string
	to      string
	fromID  string
	toID    string
}

func newStatusTestEnv(t *testing.T) statusTestEnv {
	t.Helper()
	dir := t.TempDir()
	env := statusTestEnv{
		dir:     dir,
		mailDir: filepath.Join(dir, "mail"),
		from:    filepath.Join(dir, "from.jsonl"),
		to:      filepath.Join(dir, "to.jsonl"),
	}
	writeEmpty(t, env.from)
	writeEmpty(t, env.to)
	var err error
	if env.fromID, err = EnsureContactID(env.from); err != nil {
		t.Fatal(err)
	}
	if env.toID, err = EnsureContactID(env.to); err != nil {
		t.Fatal(err)
	}
	return env
}

func statusToolCfg(env statusTestEnv, viewer string) SessionCollabConfig {
	return SessionCollabConfig{
		Enabled:            true,
		SessionDir:         env.dir,
		WorkspaceRoot:      env.dir,
		MailDir:            env.mailDir,
		CurrentSessionPath: env.from,
		CurrentContactID:   viewer,
		AllowSteer:         true,
	}
}

// 验收①：目标非活跃 ⇒ 泵写下降级回执 ⇒ 发送方按 messageId 查到「已降级」，
// 不再只有绿灯。
func TestGetMessageStatusReportsDegradedOutcome(t *testing.T) {
	env := newStatusTestEnv(t)
	store := newMailStoreForTest(t, env.mailDir)
	sent, err := store.Deliver(context.Background(), newMailMsgForTest(env.fromID, env.toID, "check the build"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordDeliveryReceipt(context.Background(), receiptForTest(sent, "queued_followup")); err != nil {
		t.Fatal(err)
	}
	tool := NewGetMessageStatusTool(statusToolCfg(env, env.fromID))
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"message_id":"`+sent.ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"status":"queued_followup"`) || !strings.Contains(out, `"known":true`) {
		t.Fatalf("sender must see the degraded outcome: %s", out)
	}
	// 降级语义必须说人话：明说 steer 降级条件。
	if !strings.Contains(out, "降级") {
		t.Fatalf("semantics must name the degradation: %s", out)
	}
}

// 接收方同样可查；第三方显式拒绝。
func TestGetMessageStatusPartyBoundary(t *testing.T) {
	env := newStatusTestEnv(t)
	store := newMailStoreForTest(t, env.mailDir)
	sent, err := store.Deliver(context.Background(), newMailMsgForTest(env.fromID, env.toID, "hello"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordDeliveryReceipt(context.Background(), receiptForTest(sent, "injected")); err != nil {
		t.Fatal(err)
	}
	// 接收方视角。
	tool := NewGetMessageStatusTool(statusToolCfg(env, env.toID))
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"message_id":"`+sent.ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"status":"injected"`) {
		t.Fatalf("recipient must read the outcome: %s", out)
	}
	// 第三方视角：拒绝而不是结果。
	tool = NewGetMessageStatusTool(statusToolCfg(env, "sc_other"))
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"message_id":"`+sent.ID+`"}`)); err == nil {
		t.Fatal("a non-party caller must be refused")
	}
}

// 无回执：自己发过的 → pending（未知，不是成功）；完全无关的 → not_found。
func TestGetMessageStatusPendingAndNotFound(t *testing.T) {
	env := newStatusTestEnv(t)
	store := newMailStoreForTest(t, env.mailDir)
	sent, err := store.Deliver(context.Background(), newMailMsgForTest(env.fromID, env.toID, "still flying"))
	if err != nil {
		t.Fatal(err)
	}
	store.RecordSent(context.Background(), sent, "target")
	tool := NewGetMessageStatusTool(statusToolCfg(env, env.fromID))
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"message_id":"`+sent.ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"status":"pending"`) || !strings.Contains(out, `"known":false`) {
		t.Fatalf("a sent message without receipt must read pending/unknown: %s", out)
	}
	out, err = tool.Execute(context.Background(), json.RawMessage(`{"message_id":"msg_nope"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"status":"not_found"`) {
		t.Fatalf("unknown id must read not_found: %s", out)
	}
}

// 接收方视角的无回执消息：in_my_inbox + 泵游标状态。
func TestGetMessageStatusInMyInbox(t *testing.T) {
	env := newStatusTestEnv(t)
	store := newMailStoreForTest(t, env.mailDir)
	sent, err := store.Deliver(context.Background(), newMailMsgForTest(env.fromID, env.toID, "for you"))
	if err != nil {
		t.Fatal(err)
	}
	tool := NewGetMessageStatusTool(statusToolCfg(env, env.toID))
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"message_id":"`+sent.ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"status":"in_my_inbox_unclaimed"`) {
		t.Fatalf("recipient without a settled cursor must see in_my_inbox_unclaimed: %s", out)
	}
}

// 既有调用方零破坏（验收②）：原返回体字段原样，增量字段追加。
func TestTalkToSessionReturnStaysAdditive(t *testing.T) {
	env := newStatusTestEnv(t)
	cfg := statusToolCfg(env, env.fromID)
	tool := NewTalkToSessionTool(cfg)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"to":"`+env.toID+`","message":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	// 原字段逐一仍在（既有消费方的契约面）。
	for _, want := range []string{`"status":"queued"`, `"queued":true`, `"messageId"`, `"delivery":"steer"`, `"delivered_to"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("existing field %s missing from the return body: %s", want, out)
		}
	}
	// 增量字段：诚实标注 pending + 查询入口。
	if !strings.Contains(out, `"deliveryOutcome":"pending"`) || !strings.Contains(out, "get_message_status") {
		t.Fatalf("the additive outcome field must name the query tool: %s", out)
	}
	if strings.Contains(out, `"deliveryDegradedByPanel"`) {
		t.Fatalf("no panel degradation happened here, the field must be absent: %s", out)
	}
}

// 面板开关降级 steer：同步可知的部分 disposition 必须出现在返回体里。
func TestTalkToSessionShowsPanelDegradation(t *testing.T) {
	env := newStatusTestEnv(t)
	cfg := statusToolCfg(env, env.fromID)
	cfg.AllowSteer = false
	tool := NewTalkToSessionTool(cfg)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"to":"`+env.toID+`","message":"hi","delivery":"steer"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"deliveryDegradedByPanel":true`) {
		t.Fatalf("the panel degradation must be visible: %s", out)
	}
	if !strings.Contains(out, `"delivery":"followup"`) {
		t.Fatalf("the effective mode must read followup: %s", out)
	}
}
