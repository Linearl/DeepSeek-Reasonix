package sessioncollab

// 任务 570 (c1)：投递回执存储的存储行为测试。
// 验收锚点：回执按 messageId 可查；重试失败累计 attempts；终局覆盖中间态；
// 非法结局拒绝入库；读不到 = ok=false（pending 语义，不是失败）。

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func newReceiptTestStore(t *testing.T) *MailStore {
	t.Helper()
	return NewMailStore(filepath.Join(t.TempDir(), "mail"))
}

func TestDeliveryReceiptRoundtrip(t *testing.T) {
	store := newReceiptTestStore(t)
	if _, ok := store.DeliveryReceipt("msg_absent"); ok {
		t.Fatal("no receipt recorded yet — must answer ok=false (pending), not a fabricated outcome")
	}
	r := DeliveryReceipt{
		MessageID: "msg_abc",
		From:      "sc_from",
		To:        "sc_to",
		Delivery:  "steer",
		Outcome:   ReceiptQueuedFollowup,
	}
	if err := store.RecordDeliveryReceipt(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	got, ok := store.DeliveryReceipt("msg_abc")
	if !ok {
		t.Fatal("recorded receipt must be readable")
	}
	if got.Outcome != ReceiptQueuedFollowup || got.From != "sc_from" || got.To != "sc_to" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	if got.At == 0 {
		t.Fatal("the store must stamp the record time")
	}
}

func TestDeliveryReceiptUpsertAndAttempts(t *testing.T) {
	store := newReceiptTestStore(t)
	ctx := context.Background()
	// 第一次失败：attempts 从零开始计 1。
	if err := store.RecordDeliveryReceipt(ctx, DeliveryReceipt{
		MessageID: "msg_retry", To: "sc_to", Delivery: "steer",
		Outcome: ReceiptFailedRetrying, Detail: "workspace not ready",
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := store.DeliveryReceipt("msg_retry")
	if got.Outcome != ReceiptFailedRetrying || got.Attempts != 1 {
		t.Fatalf("first failure: want failed_retrying attempts=1, got %+v", got)
	}
	// 第二次失败：调用方不传 attempts，存储续接计数。
	if err := store.RecordDeliveryReceipt(ctx, DeliveryReceipt{
		MessageID: "msg_retry", To: "sc_to", Delivery: "steer",
		Outcome: ReceiptFailedRetrying, Detail: "still busy",
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = store.DeliveryReceipt("msg_retry")
	if got.Attempts != 2 || got.Detail != "still busy" {
		t.Fatalf("second failure must continue the chain: %+v", got)
	}
	// 终局成功：整条覆盖，失败计数留作诊断。
	if err := store.RecordDeliveryReceipt(ctx, DeliveryReceipt{
		MessageID: "msg_retry", From: "sc_from", To: "sc_to", Delivery: "steer",
		Outcome: ReceiptInjected,
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = store.DeliveryReceipt("msg_retry")
	if got.Outcome != ReceiptInjected || got.From != "sc_from" {
		t.Fatalf("settled outcome must overwrite the failure: %+v", got)
	}
}

func TestDeliveryReceiptValidation(t *testing.T) {
	store := newReceiptTestStore(t)
	ctx := context.Background()
	cases := []struct {
		name string
		r    DeliveryReceipt
	}{
		{"no id", DeliveryReceipt{To: "sc_to", Outcome: ReceiptInjected}},
		{"no recipient", DeliveryReceipt{MessageID: "msg_x", Outcome: ReceiptInjected}},
		{"unknown outcome", DeliveryReceipt{MessageID: "msg_x", To: "sc_to", Outcome: "delivered_fine_trust_me"}},
	}
	for _, tc := range cases {
		if err := store.RecordDeliveryReceipt(ctx, tc.r); err == nil {
			t.Fatalf("%s: invalid receipt must be refused", tc.name)
		}
	}
}

func TestValidDeliveryReceiptOutcomeVocabulary(t *testing.T) {
	for _, want := range []string{ReceiptInjected, ReceiptQueuedFollowup, ReceiptRefusedHop, ReceiptRefusedProvenance, ReceiptRefusedCrossWire, ReceiptFailedRetrying} {
		if !ValidDeliveryReceiptOutcome(want) {
			t.Fatalf("outcome %q must be part of the vocabulary", want)
		}
	}
	if ValidDeliveryReceiptOutcome("") || ValidDeliveryReceiptOutcome("ok") {
		t.Fatal("empty and unknown outcomes must be invalid")
	}
}

// 任务585：结算结局分类。只有 failed_retrying 非终态——泵的防重投判据据此放行
// 真失败的重试、拦下「已发过」的重投。
func TestDeliveryReceiptSettledClassifier(t *testing.T) {
	settled := []string{ReceiptInjected, ReceiptQueuedFollowup, ReceiptRefusedHop, ReceiptRefusedProvenance, ReceiptRefusedCrossWire, ReceiptOpenRetryExhausted}
	for _, outcome := range settled {
		if !DeliveryReceiptSettled(outcome) {
			t.Fatalf("outcome %q must count as settled", outcome)
		}
	}
	if DeliveryReceiptSettled(ReceiptFailedRetrying) {
		t.Fatal("failed_retrying must not count as settled — the pump must retry it")
	}
	if DeliveryReceiptSettled("") || DeliveryReceiptSettled("unknown") {
		t.Fatal("empty and unknown outcomes must not count as settled")
	}
}

// 任务894：回执分级判定的钉子。危险方向只有一个——需行动结局被分进静默侧
// （漏行动：发送方对 refused / 开轮耗尽 / 未送达永久失聪），所以表里逐个
// 钉死需行动集，并钉死「未知一律需行动」的 fail-safe 默认。
func TestReceiptNeedsActionTiering(t *testing.T) {
	noAction := map[string]string{
		ReceiptInjected:       "steer 注入当轮成功：纯绿灯，无需发送方做任何事",
		ReceiptQueuedFollowup: "正常排队落点（followup 模式）/ steer 降级——消息已在目标队列，空闲桥接管",
	}
	for outcome, why := range noAction {
		if ReceiptNeedsAction(outcome) {
			t.Fatalf("outcome %q 属无需行动侧（%s），判成需行动 = 白白多一个注入轮次", outcome, why)
		}
	}

	actionNeeded := []string{
		ReceiptRefusedHop, ReceiptRefusedProvenance, ReceiptRefusedCrossWire,
		ReceiptFailedRetrying, ReceiptOpenRetryExhausted,
		// 未知/空值：fail-safe——静默必须逐个已知结局显式开通，任何不认识的
		// 值都保留注入通道（反向用例的核心断言）。
		"", "delivered_fine_trust_me", "STEER_DEGRADED", "refused_hop ",
	}
	for _, outcome := range actionNeeded {
		if !ReceiptNeedsAction(outcome) {
			t.Fatalf("outcome %q 必须判为需行动（含未知值 fail-safe）：分进静默侧 = 漏行动", outcome)
		}
	}

	// 词表穷尽：词表内每个合法结局都必须有确定的分级，且分级与语义一致——
	// 新增词表值忘了接分级时，此循环给出明确失败而不是静默漏判。
	for _, outcome := range []string{
		ReceiptInjected, ReceiptQueuedFollowup, ReceiptRefusedHop,
		ReceiptRefusedProvenance, ReceiptRefusedCrossWire, ReceiptFailedRetrying,
		ReceiptOpenRetryExhausted,
	} {
		if !ValidDeliveryReceiptOutcome(outcome) {
			t.Fatalf("词汇表成员 %q 必须通过合法性校验（测试自身失效）", outcome)
		}
		needs := ReceiptNeedsAction(outcome)
		if strings.HasPrefix(outcome, "refused_") && !needs {
			t.Fatalf("拒收结局 %q 永远需要行动", outcome)
		}
	}
}
