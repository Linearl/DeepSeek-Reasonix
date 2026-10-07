package main

// 任务 570 (c1)：投递泵在每次定局时写投递回执的测试。
// 验收锚点（验收①的服务端半边）：降级/注入/拒绝/暂时失败四类定局都留下按
// messageId 可查的回执；失败→成功的重试覆盖中间态。回执 seam 直连测试信箱，
// 绝不触碰真实 MailDir。

import (
	"context"
	"errors"
	"testing"

	"reasonix/internal/sessioncollab"
)

// receiptTestDelivery wires the runCollabDelivery receipt seam to the test
// store — the same wiring deliverToTarget gives the pump, minus the
// config-sourced mail dir (tests must never touch the real one).
func receiptTestDelivery(d collabDelivery, mail *sessioncollab.MailStore) collabDelivery {
	d.recordReceipt = func(msg sessioncollab.MailMessage, outcome, detail string) {
		_ = mail.RecordDeliveryReceipt(context.Background(), sessioncollab.DeliveryReceipt{
			MessageID: msg.ID,
			From:      msg.From,
			To:        msg.To,
			Delivery:  msg.Delivery,
			Outcome:   outcome,
			Detail:    detail,
		})
	}
	return d
}

// deliverOnePlain delivers exactly one message and returns its id.
func deliverOnePlain(t *testing.T, mail *sessioncollab.MailStore, msg sessioncollab.MailMessage) string {
	t.Helper()
	sent, err := mail.Deliver(context.Background(), msg)
	if err != nil {
		t.Fatal(err)
	}
	return sent.ID
}

// 验收①（服务端半边）：目标不可注入（enqueue 返回 steered=false）的 steer，
// 必须留下「降级排队」回执——发送方从此能按 id 查到「已降级/未注入」。
func TestReceiptRecordsDegradedSteer(t *testing.T) {
	mail, target := newCollabTestMail(t)
	id := deliverOnePlain(t, mail, sessioncollab.MailMessage{From: "sc_from", To: target, Body: "wake up", Delivery: "steer"})
	d, _, notices := collabTestDelivery(t, mail, nil) // enqueue 成功但不注入（steered=false）
	d = receiptTestDelivery(d, mail)
	if delivered, _, err := runCollabDelivery(mail, target, d); err != nil || delivered != 1 {
		t.Fatalf("delivery: %d %v", delivered, err)
	}
	if got, ok := mail.DeliveryReceipt("msg_absent"); ok {
		t.Fatalf("a wrong id must not answer, got %+v", got)
	}
	got, ok := mail.DeliveryReceipt(id)
	if !ok {
		t.Fatal("a degraded steer must leave a receipt")
	}
	if got.Outcome != sessioncollab.ReceiptQueuedFollowup {
		t.Fatalf("degraded steer must record queued_followup, got %q", got.Outcome)
	}
	if got.Delivery != "steer" || got.To != target {
		t.Fatalf("receipt must carry mode and recipient: %+v", got)
	}
	if len(*notices) != 1 {
		t.Fatalf("the degraded note must still go out, got %d", len(*notices))
	}
}

// 注入成功（steered=true）必须记 injected——「真正的绿灯」从此可查。
func TestReceiptRecordsInjectedSteer(t *testing.T) {
	mail, target := newCollabTestMail(t)
	id := deliverOnePlain(t, mail, sessioncollab.MailMessage{From: "sc_from", To: target, Body: "wake", Delivery: "steer"})
	d, _, _ := collabTestDelivery(t, mail, nil)
	// 把 enqueue 换成「注入成功」：steered=true。
	d.enqueue = func(sessioncollab.MailMessage, string) (bool, error) { return true, nil }
	d = receiptTestDelivery(d, mail)
	if _, _, err := runCollabDelivery(mail, target, d); err != nil {
		t.Fatal(err)
	}
	got, ok := mail.DeliveryReceipt(id)
	if !ok || got.Outcome != sessioncollab.ReceiptInjected {
		t.Fatalf("injected steer must be recorded, got %+v (ok=%v)", got, ok)
	}
}

// 暂时失败记 failed_retrying（累计 attempts），成功后覆盖——非终态语义。
func TestReceiptRecordsFailureThenOverwritesOnSuccess(t *testing.T) {
	mail, target := newCollabTestMail(t)
	id := deliverOnePlain(t, mail, sessioncollab.MailMessage{From: "sc_from", To: target, Body: "work"})
	attempts := 0
	d, _, _ := collabTestDelivery(t, mail, func(sessioncollab.MailMessage) error {
		attempts++
		if attempts <= 2 {
			return errors.New("workspace not ready")
		}
		return nil
	})
	d = receiptTestDelivery(d, mail)
	if _, _, err := runCollabDelivery(mail, target, d); err == nil {
		t.Fatal("first pass must report the failure")
	}
	if _, _, err := runCollabDelivery(mail, target, d); err == nil {
		t.Fatal("second pass must still report the failure")
	}
	got, ok := mail.DeliveryReceipt(id)
	if !ok || got.Outcome != sessioncollab.ReceiptFailedRetrying || got.Attempts != 2 {
		t.Fatalf("two failed passes must accumulate attempts: %+v ok=%v", got, ok)
	}
	// 第三次成功：回执被终局覆盖。
	if _, _, err := runCollabDelivery(mail, target, d); err != nil {
		t.Fatal(err)
	}
	got, _ = mail.DeliveryReceipt(id)
	if got.Outcome != sessioncollab.ReceiptQueuedFollowup {
		t.Fatalf("success must overwrite failed_retrying, got %q", got.Outcome)
	}
}

// 链路校验拒绝（provenance）与 hop 上限都是终局拒绝，必须留回执。
func TestReceiptRecordsRefusals(t *testing.T) {
	mail, target := newCollabTestMail(t)
	id := deliverOnePlain(t, mail, sessioncollab.MailMessage{From: "sc_from", To: target, Body: "reply", ThreadID: "msg_ghost"})
	d, _, _ := collabTestDelivery(t, mail, nil)
	d = receiptTestDelivery(d, mail)
	// 注入一个必然失败的 deriveHop（等价于真实链路上无法核实 thread 来源）。
	d.deriveHop = func(sessioncollab.MailMessage) (int, error) { return 0, errors.New("thread not in sender mailbox") }
	if _, refused, err := runCollabDelivery(mail, target, d); err != nil || refused != 1 {
		t.Fatalf("refusal: %d %v", refused, err)
	}
	got, ok := mail.DeliveryReceipt(id)
	if !ok || got.Outcome != sessioncollab.ReceiptRefusedProvenance {
		t.Fatalf("provenance refusal must be recorded, got %+v ok=%v", got, ok)
	}

	// hop 超限（Claim 阶段即拒绝）：raw 行写入超限 hop。
	if err := appendRawLine(mail.InboxPath(target), `{"id":"msg_deep","fromContactId":"sc_from","toContactId":"`+target+`","body":"too deep","hop":99}`); err != nil {
		t.Fatal(err)
	}
	d2, _, _ := collabTestDelivery(t, mail, nil)
	d2 = receiptTestDelivery(d2, mail)
	if _, refused, err := runCollabDelivery(mail, target, d2); err != nil || refused != 1 {
		t.Fatalf("hop ceiling refusal: %d %v", refused, err)
	}
	got, ok = mail.DeliveryReceipt("msg_deep")
	if !ok || got.Outcome != sessioncollab.ReceiptRefusedHop {
		t.Fatalf("hop ceiling refusal must be recorded, got %+v ok=%v", got, ok)
	}
}
