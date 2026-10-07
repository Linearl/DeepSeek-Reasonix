package sessioncollab

// 任务 570 (c1)：投递回执存储的存储行为测试。
// 验收锚点：回执按 messageId 可查；重试失败累计 attempts；终局覆盖中间态；
// 非法结局拒绝入库；读不到 = ok=false（pending 语义，不是失败）。

import (
	"context"
	"path/filepath"
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
