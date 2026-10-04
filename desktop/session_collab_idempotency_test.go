package main

import (
	"context"
	"testing"

	"reasonix/internal/sessioncollab"
	"reasonix/internal/sessioninbox"
)

// 任务461 P10 验收（重试方视角）：同内容重发在投递入队处撞上幂等冲突时，
// 泵必须把它当「重复已送达」静默结算——mail 被 ack（重投循环断根）、发送方
// 收不到任何「投递失败」通知、runCollabDelivery 不报错。真断言，无 SKIP。

func TestIdempotencyConflictSettlesAsDuplicateDelivery(t *testing.T) {
	mail, target := newCollabTestMail(t)
	ctx := context.Background()

	// 首投：worker 在同一任务线程上报告状态（显式 thread_id）。
	if _, err := mail.Deliver(ctx, sessioncollab.MailMessage{
		From: "sc_worker", To: target, Body: "任务仍在运行", ThreadID: "msg_task1",
	}); err != nil {
		t.Fatal(err)
	}
	d, _, _ := collabTestDelivery(t, mail, nil)
	delivered, _, err := runCollabDelivery(mail, target, d)
	if err != nil || delivered != 1 {
		t.Fatalf("first delivery: delivered=%d err=%v", delivered, err)
	}

	// 重试方视角：同线程同内容重发 → 新 id 新行（P8 前世界的存量形态；
	// P8 后该场景在 Deliver 层已折叠，这里直接落行模拟已污染数据）。
	if _, err := mail.Deliver(ctx, sessioncollab.MailMessage{
		From: "sc_worker", To: target, Body: "任务仍在运行", ThreadID: "msg_task1", ID: "msg_resend1",
	}); err != nil {
		t.Fatal(err)
	}

	// 目标收件箱对该内容键的再次入队返回幂等冲突（同 key、信封含逐条变化的
	// 元数据 → hash 不同）——即 desktop.log 里每 5-6s 一次的那个错误。
	d2, _, notices2 := collabTestDelivery(t, mail, func(sessioncollab.MailMessage) error {
		return sessioninbox.ErrIdempotencyConflict
	})
	delivered2, refused2, err := runCollabDelivery(mail, target, d2)
	if err != nil {
		t.Fatalf("conflict must settle as duplicate delivery, got err=%v", err)
	}
	if delivered2 != 1 || refused2 != 0 {
		t.Fatalf("duplicate delivery: delivered=%d refused=%d, want 1/0", delivered2, refused2)
	}
	if len(*notices2) != 0 {
		t.Fatalf("sender must NOT be notified on a duplicate, got notices: %v", *notices2)
	}
	// 重投循环断根：mail 已 ack，收件箱不再有待处理（下个泵周期无事可重投）。
	if unread, _ := mail.InboxStatus(target); unread != 0 {
		t.Fatalf("transport unread = %d, want 0 (the resend must be settled, not left pending)", unread)
	}
}
