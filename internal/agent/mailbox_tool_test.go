package agent

// 任务 689：mailbox(action=peek|drain|query) 三合一测试。
//
// 验收锚点：
// ① mailbox 三 action 与旧工具逐一等价（同一实现代码路径，输出字节一致）；
// ② 旧三名转发器可调用：转发 + JSON 内 deprecated 淡化提示，业务字段不变；
// ③ M-a 单消费者不变式：drainRegistered=false 时 action=drain 拒绝（点名
//    泵/开关与 peek 替代），peek/query 不受门限制；
// ④ action 缺失/未知报错；mailbox 非 ReadOnly（drain 可消费）。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
)

func mailboxTestCfg(env statusTestEnv) SessionCollabConfig {
	return SessionCollabConfig{
		Enabled:            true,
		SessionDir:         env.dir,
		WorkspaceRoot:      env.dir,
		MailDir:            env.mailDir,
		CurrentSessionPath: env.to,
		CurrentContactID:   env.toID,
	}
}

func deliverMailboxFixture(t *testing.T, env statusTestEnv) {
	t.Helper()
	store := sessioncollab.NewMailStore(env.mailDir)
	for _, m := range []sessioncollab.MailMessage{
		{From: env.fromID, To: env.toID, Body: "hello one", ReplyTo: env.fromID},
		{From: env.fromID, To: env.toID, Body: "你发送的 steer 未能注入目标会话当轮，已自动降级为排队 follow-up（messageId=msg_x）", Kind: "system"},
	} {
		if _, err := store.Deliver(context.Background(), m); err != nil {
			t.Fatal(err)
		}
	}
}

func runTool(t *testing.T, tl interface {
	Execute(context.Context, json.RawMessage) (string, error)
}, args string) string {
	t.Helper()
	out, err := tl.Execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// 验收①：action=peek 与 peek_own_inbox 输出字节一致（同一实现代码路径）。
func TestMailboxPeekEquivalentToOldTool(t *testing.T) {
	env := newStatusTestEnv(t)
	deliverMailboxFixture(t, env)
	cfg := mailboxTestCfg(env)

	for _, args := range []string{`{}`, `{"only_system":true}`, `{"limit":1}`} {
		got := runTool(t, NewMailboxTool(cfg, true), withAction(t, args, "peek"))
		want := runTool(t, NewPeekOwnInboxTool(cfg), args)
		if got != want {
			t.Fatalf("mailbox(action=peek) %s diverged from peek_own_inbox:\n got: %s\nwant: %s", args, got, want)
		}
	}
}

// 验收①：action=drain 与 drain_inbox 逐一等价——peek 模式（非破坏）做字节
// 级对比；settle 模式有破坏性（先跑的会消费掉信），改做结构断言，settle
// 行为本体由 drain_inbox_tool_test.go（235）钉住。
func TestMailboxDrainEquivalentToOldTool(t *testing.T) {
	env := newStatusTestEnv(t)
	deliverMailboxFixture(t, env)
	cfg := mailboxTestCfg(env)

	peeked := runTool(t, NewMailboxTool(cfg, true), `{"action":"drain","settle":false}`)
	wantPeeked := runTool(t, NewDrainInboxTool(cfg), `{"settle":false}`)
	if peeked != wantPeeked {
		t.Fatalf("mailbox(action=drain, settle=false) diverged from drain_inbox:\n got: %s\nwant: %s", peeked, wantPeeked)
	}

	got := runTool(t, NewMailboxTool(cfg, true), `{"action":"drain"}`)
	var p struct {
		Settled  bool `json:"settled"`
		Took     int  `json:"took"`
		Refused  int  `json:"refused"`
		Unread   int  `json:"unreadAfter"`
		Messages []struct {
			Body string `json:"body"`
			Text string `json:"text"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(got), &p); err != nil {
		t.Fatalf("mailbox drain must return JSON: %v", err)
	}
	if !p.Settled || p.Took != 2 || p.Refused != 0 || p.Unread != 0 {
		t.Fatalf("settle semantics broken: %+v", p)
	}
	if len(p.Messages) != 2 || p.Messages[0].Body != "hello one" || p.Messages[0].Text == "" {
		t.Fatalf("settle must surface bodies and rendered text: %s", got)
	}

	again := runTool(t, NewMailboxTool(cfg, true), `{"action":"drain"}`)
	var p2 struct {
		Took int `json:"took"`
	}
	if err := json.Unmarshal([]byte(again), &p2); err != nil || p2.Took != 0 {
		t.Fatalf("second drain must be empty: %s %v", again, err)
	}
}

// 验收①：action=query 与 query_collab_mail 输出字节一致。
func TestMailboxQueryEquivalentToOldTool(t *testing.T) {
	env := newStatusTestEnv(t)
	deliverMailboxFixture(t, env)
	cfg := mailboxTestCfg(env)

	for _, args := range []string{`{"to":"` + env.toID + `"}`, `{"to":"` + env.toID + `","limit":1}`} {
		got := runTool(t, NewMailboxTool(cfg, true), withAction(t, args, "query"))
		want := runTool(t, NewQueryCollabMailTool(cfg), args)
		if got != want {
			t.Fatalf("mailbox(action=query) %s diverged from query_collab_mail:\n got: %s\nwant: %s", args, got, want)
		}
	}
}

// 验收②：三个旧名转发器——转发结果仍是可解析 JSON，业务字段与直调一致，
// 且带 deprecated/useInstead/deprecationNote 淡化提示。
func TestMailboxAliasesForwardAndStampDeprecated(t *testing.T) {
	env := newStatusTestEnv(t)
	deliverMailboxFixture(t, env)
	cfg := mailboxTestCfg(env)

	// peek 别名：游标不动的只读转发 + 提示。
	out := runTool(t, NewPeekOwnInboxAliasTool(cfg), `{}`)
	var p map[string]any
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("alias result must stay parseable JSON: %v\n%s", err, out)
	}
	if p["deprecated"] != true || p["useInstead"] != "mailbox(action=peek)" {
		t.Fatalf("peek alias must stamp the deprecation marker: %s", out)
	}
	if n, _ := p["returned"].(float64); int(n) != 2 {
		t.Fatalf("peek alias must forward the payload: %s", out)
	}

	// drain 别名：settle 消费 + 提示。
	out = runTool(t, NewDrainInboxAliasTool(cfg, true), `{}`)
	p = nil
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("drain alias result must stay parseable JSON: %v\n%s", err, out)
	}
	if p["deprecated"] != true || p["useInstead"] != "mailbox(action=drain)" {
		t.Fatalf("drain alias must stamp the deprecation marker: %s", out)
	}
	if took, _ := p["took"].(float64); int(took) != 2 {
		t.Fatalf("drain alias must forward the consuming payload: %s", out)
	}
	if unread, _ := p["unreadAfter"].(float64); int(unread) != 0 {
		t.Fatalf("drain alias must settle (unreadAfter=0): %s", out)
	}

	// query 别名：只读转发 + 提示。
	out = runTool(t, NewQueryCollabMailAliasTool(cfg), `{"to":"`+env.toID+`"}`)
	p = nil
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("query alias result must stay parseable JSON: %v\n%s", err, out)
	}
	if p["deprecated"] != true || p["useInstead"] != "mailbox(action=query)" {
		t.Fatalf("query alias must stamp the deprecation marker: %s", out)
	}
	if returned, _ := p["returned"].(float64); int(returned) != 2 {
		t.Fatalf("query alias must forward the payload: %s", out)
	}
}

// M-a 单消费者不变式：泵拥有信箱时（boot 门 OFF）action=drain 拒绝且文案
// 点名开关与 peek 替代；peek / query 不受门限制；旧名转发器同样被门拦。
func TestMailboxDrainActionFollowsConsumerGate(t *testing.T) {
	env := newStatusTestEnv(t)
	deliverMailboxFixture(t, env)
	cfg := mailboxTestCfg(env)

	_, err := NewMailboxTool(cfg, false).Execute(context.Background(), json.RawMessage(`{"action":"drain"}`))
	if err == nil {
		t.Fatal("action=drain must refuse when the pump owns the mailbox")
	}
	for _, want := range []string{"experimental_collab_background_delivery", "action=peek"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("drain refusal must name %q: %v", want, err)
		}
	}

	_, err = NewDrainInboxAliasTool(cfg, false).Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "experimental_collab_background_delivery") {
		t.Fatalf("drain alias must follow the same gate: %v", err)
	}

	// 门 OFF 只限 drain：peek / query 照常可用。
	if out := runTool(t, NewMailboxTool(cfg, false), `{"action":"peek"}`); !strings.Contains(out, env.fromID) {
		t.Fatalf("action=peek must work with the gate off: %s", out)
	}
	if out := runTool(t, NewMailboxTool(cfg, false), `{"action":"query","to":"`+env.toID+`"}`); !strings.Contains(out, `"returned":2`) {
		t.Fatalf("action=query must work with the gate off: %s", out)
	}
}

// 路由边界：缺 action、未知 action 报错；ReadOnly 立场照旧（mailbox 合并了
// 消费半面 → 非 ReadOnly；peek/query 别名只读；drain 别名非只读）。
func TestMailboxRoutingAndStances(t *testing.T) {
	env := newStatusTestEnv(t)
	cfg := mailboxTestCfg(env)
	mb := NewMailboxTool(cfg, true)

	if _, err := mb.Execute(context.Background(), json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "action is required") {
		t.Fatalf("missing action must be refused: %v", err)
	}
	if _, err := mb.Execute(context.Background(), json.RawMessage(`{"action":"send"}`)); err == nil || !strings.Contains(err.Error(), "peek|drain|query") {
		t.Fatalf("unknown action must be refused: %v", err)
	}
	if mb.ReadOnly() {
		t.Fatal("mailbox must not be ReadOnly — action=drain can consume")
	}
	if !NewPeekOwnInboxAliasTool(cfg).ReadOnly() || !NewQueryCollabMailAliasTool(cfg).ReadOnly() {
		t.Fatal("peek/query aliases must stay ReadOnly")
	}
	if NewDrainInboxAliasTool(cfg, true).ReadOnly() {
		t.Fatal("drain alias must not be ReadOnly")
	}
	if got := (peekOwnInboxAliasTool{}).Schema(); string(got) != string((peekOwnInboxTool{}).Schema()) {
		t.Fatal("alias schema must stay the old tool's schema during the transition")
	}
}

// withAction injects "action" into a JSON object literal (test helper).
func withAction(t *testing.T, args, action string) string {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(args), &raw); err != nil {
		t.Fatalf("withAction: bad args %q: %v", args, err)
	}
	raw["action"] = action
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
