package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
)

// tl1697：hop 上限可见化——超限拒发/拒收文案必须给出真实上限值与已存在的
// 出路（新链 / 设置面调高上限）。三颗钉对应三处模型可见文案：
//  1. talk_to_session 参数预检（hop 明显越界）；
//  2. talk_to_session 退信（NDR）——store 层 ErrHopLimit 经包装后必须带指引；
//  3. get_message_status 的 refused_hop 语义——实时上限值内联。

func hopLimitTestCfg(env statusTestEnv, viewer string, limit int) SessionCollabConfig {
	cfg := statusToolCfg(env, viewer)
	cfg.HopLimit = func() int { return limit }
	return cfg
}

// 钉 1：hop 越过预检线（> limit+1）时，错误必须报出真实上限并给出 hop=0 出路。
func TestTalkToSessionHopPrecheckNamesCeilingAndWayOut(t *testing.T) {
	env := newStatusTestEnv(t)
	tool := NewTalkToSessionTool(hopLimitTestCfg(env, env.fromID, 3))
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"to":"`+env.toID+`","message":"hi","hop":999}`))
	if err == nil {
		t.Fatal("expected the hop precheck to refuse hop=999")
	}
	msg := err.Error()
	if !strings.Contains(msg, "上限为 3") {
		t.Fatalf("refusal must name the live ceiling (3): %s", msg)
	}
	if !strings.Contains(msg, "hop=0") {
		t.Fatalf("refusal must point at the existing way out (start a new chain, hop=0): %s", msg)
	}
}

// 钉 2：合法回信（hop=limit+1 过预检、thread_id 可解析）在 Deliver 处被
// ErrHopLimit 拒绝时，NDR 文案必须附加可执行指引——不能只回显 store 层的
// 英文数值错误。
func TestTalkToSessionHopExhaustedNDRNamesWayOut(t *testing.T) {
	env := newStatusTestEnv(t)
	const limit = 3
	// 对端先给发送方投一条链首消息（store 直写，绕开工具面）——发送方才有
	// 可解析的父 thread_id，回信才能通过源头溯源门走到 Deliver。
	writer := sessioncollab.NewMailStoreWithHopLimit(env.mailDir, sessioncollab.MaxHopCeiling)
	inbound, err := writer.Deliver(context.Background(), sessioncollab.MailMessage{
		From: env.toID, To: env.fromID, Body: "chain head", Delivery: "steer",
	})
	if err != nil {
		t.Fatal(err)
	}
	tool := NewTalkToSessionTool(hopLimitTestCfg(env, env.fromID, limit))
	_, err = tool.Execute(context.Background(), json.RawMessage(
		`{"to":"`+env.toID+`","message":"still on the chain","hop":`+ // limit+1 过预检
			"4"+`,"thread_id":"`+inbound.ID+`"}`))
	if err == nil {
		t.Fatal("expected the store ceiling to refuse hop=4 under limit 3")
	}
	msg := err.Error()
	if !strings.Contains(msg, "退信（NDR）") {
		t.Fatalf("refusal must surface as an NDR: %s", msg)
	}
	if !strings.Contains(msg, "已到协作链深度上限 3") {
		t.Fatalf("NDR must name the live ceiling (3): %s", msg)
	}
	if !strings.Contains(msg, "新起一条链") || !strings.Contains(msg, "hop=0") {
		t.Fatalf("NDR must point at the existing way out (new chain, hop=0): %s", msg)
	}
}

// 钉 3：refused_hop 回执的语义文案必须内联实时上限值（非出厂默认也如实报）。
func TestGetMessageStatusRefusedHopSemanticsNamesLiveCeiling(t *testing.T) {
	env := newStatusTestEnv(t)
	store := newMailStoreForTest(t, env.mailDir)
	sent, err := store.Deliver(context.Background(), newMailMsgForTest(env.fromID, env.toID, "too deep"))
	if err != nil {
		t.Fatal(err)
	}
	receipt := receiptForTest(sent, sessioncollab.ReceiptRefusedHop)
	receipt.Detail = "collaboration chain hop limit reached"
	if err := store.RecordDeliveryReceipt(context.Background(), receipt); err != nil {
		t.Fatal(err)
	}
	tool := NewGetMessageStatusTool(hopLimitTestCfg(env, env.fromID, 7))
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"message_id":"`+sent.ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"semantics"`) {
		t.Fatalf("outcome must carry semantics: %s", out)
	}
	if !strings.Contains(out, "当前上限 7") {
		t.Fatalf("semantics must inline the live ceiling (7), not a hard-coded default: %s", out)
	}
	if !strings.Contains(out, "hop=0") {
		t.Fatalf("semantics must point at the existing way out (hop=0): %s", out)
	}
}
