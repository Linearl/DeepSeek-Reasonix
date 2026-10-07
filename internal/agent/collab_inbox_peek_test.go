package agent

// 任务 570 (c2)：peek_own_inbox 只读自读信箱测试。
// 验收锚点（验收③）：peek 不 Claim、不 Ack、不写游标——peek 前后
// seen.json 字节不变、MailStore.Claim 仍能看到全部待投消息（双读场景）；
// 行按最新在前；only_system / hide_settled 过滤；回执结局 join 进行。
// 硬边界：只读调用方自己的信箱（无 target 参数可言）。

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
)

func peekToolCfg(env statusTestEnv) SessionCollabConfig {
	return SessionCollabConfig{
		Enabled:            true,
		SessionDir:         env.dir,
		WorkspaceRoot:      env.dir,
		MailDir:            env.mailDir,
		CurrentSessionPath: env.from,
		CurrentContactID:   env.toID, // 收件人视角：env.to 是信箱主人
	}
}

func deliverToBox(t *testing.T, env statusTestEnv, body, kind string) string {
	t.Helper()
	store := sessioncollab.NewMailStore(env.mailDir)
	sent, err := store.Deliver(context.Background(), sessioncollab.MailMessage{
		From: env.fromID, To: env.toID, Body: body, Delivery: "steer", Kind: kind,
	})
	if err != nil {
		t.Fatal(err)
	}
	return sent.ID
}

func readCursorBytes(t *testing.T, env statusTestEnv) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(env.mailDir, env.toID+".seen.json"))
	if err != nil {
		return nil // 尚无游标文件
	}
	return b
}

// 验收③（双读验证）：peek 前后游标字节不变，泵的 Claim 仍见全部待投消息。
func TestPeekOwnInboxNeverConsumes(t *testing.T) {
	env := newStatusTestEnv(t)
	id1 := deliverToBox(t, env, "first note", "")
	id2 := deliverToBox(t, env, "你发送的 steer 未能注入目标会话当轮，已自动降级为排队 follow-up（messageId=msg_x）", "system")
	_ = id2

	mail := sessioncollab.NewMailStore(env.mailDir)
	before, _, err := mail.Claim(context.Background(), env.toID)
	if err != nil || len(before) != 2 {
		t.Fatalf("claim before peek: %d %v", len(before), err)
	}
	cursorBefore := readCursorBytes(t, env)

	tool := NewPeekOwnInboxTool(peekToolCfg(env))
	out, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, id1) || !strings.Contains(out, id2) {
		t.Fatalf("peek must show both rows: %s", out)
	}
	if cursorAfter := readCursorBytes(t, env); !bytes.Equal(cursorBefore, cursorAfter) {
		t.Fatalf("peek must not advance the pump cursor: before=%q after=%q", cursorBefore, cursorAfter)
	}
	after, _, err := mail.Claim(context.Background(), env.toID)
	if err != nil || len(after) != 2 {
		t.Fatalf("the pump must still claim both messages after a peek: %d %v", len(after), err)
	}
}

// 行渲染：最新在前、settled 标注、回执结局 join、only_system / hide_settled。
func TestPeekOwnInboxRowsAndFilters(t *testing.T) {
	env := newStatusTestEnv(t)
	idOld := deliverToBox(t, env, "old plain", "")
	idNote := deliverToBox(t, env, "你发送的 steer 未能注入目标会话当轮，已自动降级为排队 follow-up", "system")
	idNew := deliverToBox(t, env, "newest plain", "")
	mail := sessioncollab.NewMailStore(env.mailDir)
	// 最旧的一行由泵定局（ack）+ 留降级回执，验证 settled 标注与结局 join。
	if err := mail.Ack(context.Background(), env.toID, idOld); err != nil {
		t.Fatal(err)
	}
	if err := mail.RecordDeliveryReceipt(context.Background(), sessioncollab.DeliveryReceipt{
		MessageID: idOld, From: env.fromID, To: env.toID, Delivery: "steer",
		Outcome: sessioncollab.ReceiptQueuedFollowup,
	}); err != nil {
		t.Fatal(err)
	}

	tool := NewPeekOwnInboxTool(peekToolCfg(env))
	out, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	// 最新在前。
	if strings.Index(out, idNew) > strings.Index(out, idOld) {
		t.Fatalf("rows must be newest first: %s", out)
	}
	if !strings.Contains(out, `"settled":true`) || !strings.Contains(out, `"settled":false`) {
		t.Fatalf("both settled states must be visible: %s", out)
	}
	if !strings.Contains(out, `"outcome":"queued_followup"`) {
		t.Fatalf("the receipt outcome must join the row: %s", out)
	}
	if !strings.Contains(out, `"unsettled":2`) || !strings.Contains(out, `"unsettledSystem":1`) {
		t.Fatalf("counters must reflect the whole inbox: %s", out)
	}

	// only_system：只剩系统回执行。
	out, err = tool.Execute(context.Background(), json.RawMessage(`{"only_system":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, idNew) || !strings.Contains(out, idNote) {
		t.Fatalf("only_system must keep the status note only: %s", out)
	}

	// hide_settled：已定局的旧行退场，计数仍全量。
	out, err = tool.Execute(context.Background(), json.RawMessage(`{"hide_settled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, idOld) || !strings.Contains(out, `"totalInInbox":3`) {
		t.Fatalf("hide_settled filters rows but counters stay whole: %s", out)
	}
}

// 无调用方身份 → 明确报错；limit 截断但计数不缩水。
func TestPeekOwnInboxIdentityAndLimit(t *testing.T) {
	env := newStatusTestEnv(t)
	tool := NewPeekOwnInboxTool(SessionCollabConfig{
		Enabled: true, SessionDir: env.dir, WorkspaceRoot: env.dir, MailDir: env.mailDir,
	})
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("an unresolvable caller identity must be refused, not silently answered from someone else's box")
	}
	for i := 0; i < 3; i++ {
		// 正文逐条不同：投递层对「同发送方+同收件方+同正文」有重发折叠
		// （任务461 P8 ①），相同正文会被折叠成一条，计数断言就失真了。
		deliverToBox(t, env, "row "+strings.Repeat("x", i+1), "")
	}
	peek := NewPeekOwnInboxTool(peekToolCfg(env))
	out, err := peek.Execute(context.Background(), json.RawMessage(`{"limit":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"returned":2`) || !strings.Contains(out, `"totalInInbox":3`) {
		t.Fatalf("limit caps rows, counters stay whole: %s", out)
	}
}
