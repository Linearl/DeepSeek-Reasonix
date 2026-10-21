package sessioncollab

import (
	"strings"
	"testing"
)

// Task 309: the mailbox default channel is steer — immediate injection with
// automatic follow-up degradation — while an explicit followup keeps the old
// queued semantics and unknown values are still refused.
func TestValidateDeliveryDefaultsSteer(t *testing.T) {
	cases := []struct {
		in   string
		want Delivery
	}{
		{"", DeliverySteer},
		{"steer", DeliverySteer},
		{"STEER", DeliverySteer},
		{"followup", DeliveryFollowup},
		{" followup ", DeliveryFollowup},
	}
	for _, tc := range cases {
		got, err := ValidateDelivery(tc.in)
		if err != nil {
			t.Fatalf("ValidateDelivery(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ValidateDelivery(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if _, err := ValidateDelivery("bogus"); err == nil {
		t.Fatal("ValidateDelivery(bogus) accepted an unknown channel")
	}
}

// Task 309: read receipts are best-effort — a missing mailbox or missing
// addresses degrades to a no-op instead of failing the consume path.
func TestSendReadReceiptBestEffortNoop(t *testing.T) {
	if err := SendReadReceipt("", "sc_sender", "sc_recipient", "msg_1"); err != nil {
		t.Fatalf("empty mailDir must no-op, got %v", err)
	}
	if err := SendReadReceipt(t.TempDir(), "", "sc_recipient", "msg_1"); err != nil {
		t.Fatalf("empty sender must no-op, got %v", err)
	}
	if err := SendReadReceipt(t.TempDir(), "sc_sender", "", "msg_1"); err != nil {
		t.Fatalf("empty recipient must no-op, got %v", err)
	}
}

// 任务1000：自发信（发信方=收信方）的回执必须追加身世提醒；正常 A→B 回执
// 不受影响（正文与改动前逐字节一致，零回归）。
func TestSendReadReceiptSelfSendIdentityNotice(t *testing.T) {
	// 自发信：提醒句出现，且是系统回执（kind=system 桶，不顶未读）。
	selfDir := t.TempDir()
	if err := SendReadReceipt(selfDir, "sc_self", "sc_self", "msg_self1"); err != nil {
		t.Fatalf("self-send receipt: %v", err)
	}
	selfMsgs, err := NewMailStore(selfDir).InboxMessages("sc_self")
	if err != nil {
		t.Fatalf("read self inbox: %v", err)
	}
	if len(selfMsgs) != 1 {
		t.Fatalf("self-send receipt must land exactly one message, got %d", len(selfMsgs))
	}
	got := selfMsgs[0]
	if !strings.Contains(got.Body, SelfSendIdentityNotice) {
		t.Fatalf("self-send receipt missing identity notice, body=%q", got.Body)
	}
	if got.From != "sc_self" || got.To != "sc_self" {
		t.Fatalf("self-send receipt from/to must both be sc_self, got %q→%q", got.From, got.To)
	}
	if got.Kind != "system" {
		t.Fatalf("self-send receipt must stay kind=system (auto-read bucket), got %q", got.Kind)
	}
	if got.ReceiptRequested {
		t.Fatal("receipts never chain: ReceiptRequested must stay false")
	}

	// 正常 A→B：正文不含提醒句（与改动前同一前缀），零回归。
	abDir := t.TempDir()
	if err := SendReadReceipt(abDir, "sc_a", "sc_b", "msg_ab1"); err != nil {
		t.Fatalf("A→B receipt: %v", err)
	}
	abMsgs, err := NewMailStore(abDir).InboxMessages("sc_a")
	if err != nil {
		t.Fatalf("read A inbox: %v", err)
	}
	if len(abMsgs) != 1 {
		t.Fatalf("A→B receipt must land exactly one message, got %d", len(abMsgs))
	}
	if strings.Contains(abMsgs[0].Body, SelfSendIdentityNotice) {
		t.Fatalf("A→B receipt must NOT carry the self-send identity notice, body=%q", abMsgs[0].Body)
	}
	if !strings.HasPrefix(abMsgs[0].Body, "已读回执：你的消息 msg_ab1") {
		t.Fatalf("A→B receipt body changed its task-309 prefix, body=%q", abMsgs[0].Body)
	}
}
