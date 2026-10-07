package main

// 任务585 验收：邮件层「已发过的不重入」。
//
// 实测（2026-10-07）：更新重启窗口里投递泵的 mail Ack 可能丢失（写权限回收与
// 泵结算竞争；存量残留中实见 2 条邮件未结算的队列项）。游标丢 ack 后，泵下一
// 轮重新 Claim 同一条消息：若原队列项已被消费删除（幂等键随删除清账），重投会
// 造出新队列项——已处理消息再次注入。修复=570 回执已持结算结局（非
// failed_retrying）即视为「已发过」：泵跳过重投并补结算游标。回执只在
// enqueue/steer 真正成功后写入，因此该跳过不可能吞掉未投递的消息。

import (
	"context"
	"testing"
	"time"

	"reasonix/internal/sessioncollab"
)

// seedUnackedMail 落一条未 ack 的邮件行（游标无该 id，等价重启窗丢 ack 后的
// 泵视界）。hop=0 自成一线，必过溯源门。
func seedUnackedMail(t *testing.T, mail *sessioncollab.MailStore, target, id string) {
	t.Helper()
	if _, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{
		ID:       id,
		From:     "sc_from585",
		To:       target,
		Body:     "585: delayed follow-up already handled once",
		Delivery: "followup",
		At:       time.Now().UnixMilli() - 60_000,
	}); err != nil {
		t.Fatal(err)
	}
}

// settledReceiptSeam 与生产 deliverToTarget 同构的结算回执判据。
func settledReceiptSeam(mail *sessioncollab.MailStore) func(sessioncollab.MailMessage) string {
	return func(msg sessioncollab.MailMessage) string {
		if r, ok := mail.DeliveryReceipt(msg.ID); ok && sessioncollab.DeliveryReceiptSettled(r.Outcome) {
			return r.Outcome
		}
		return ""
	}
}

// 验收①（邮件层）：消息已投递过（结算回执在案）+ 游标丢 ack → 泵跳过重投、
// 补结算游标；此后任何一次泵扫都看不到该消息（注入零次）。
func TestSettledReceiptSkipsRedispatchAfterRestart(t *testing.T) {
	mailDir := setupIsolatedCollabMail(t)
	target := "sc_target585"
	mail := sessioncollab.NewMailStore(mailDir)
	seedUnackedMail(t, mail, target, "msg_585_settled")
	// 前一轮投递的定局回执（570 面真实写入，非夹具直插文件）。
	if err := mail.RecordDeliveryReceipt(context.Background(), sessioncollab.DeliveryReceipt{
		MessageID: "msg_585_settled",
		From:      "sc_from585",
		To:        target,
		Delivery:  "followup",
		Outcome:   sessioncollab.ReceiptQueuedFollowup,
		At:        time.Now().UnixMilli() - 30_000,
	}); err != nil {
		t.Fatal(err)
	}

	enqueued := 0
	pump := &sessionCollabPump{}
	d := collabDelivery{
		enqueue: func(sessioncollab.MailMessage, string) (bool, error) {
			enqueued++
			return false, nil
		},
		deriveHop:      pump.verifyHop,
		render:         sessionCollabDeliveryText,
		settledReceipt: settledReceiptSeam(mail),
	}
	delivered, refused, err := runCollabDelivery(mail, target, d)
	if err != nil {
		t.Fatal(err)
	}
	if enqueued != 0 {
		t.Fatalf("a settled message must not be re-handed to the target, enqueue ran %d times", enqueued)
	}
	if delivered != 1 || refused != 0 {
		t.Fatalf("the skip must settle the message: %d delivered / %d refused", delivered, refused)
	}

	// 游标已补结算：重启等价的再次 Claim 不再见该消息（注入日志零次的等价断言）。
	pending, _, err := mail.Claim(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("after the skip the mail must stay settled, claim returned %d pending", len(pending))
	}
}

// 验收②（B 场景，防误杀）：停在 queued 未注入的消息没有结算回执 → 泵照常投递。
func TestUnsettledMessageStillDeliversAfterRestart(t *testing.T) {
	mailDir := setupIsolatedCollabMail(t)
	target := "sc_target585b"
	mail := sessioncollab.NewMailStore(mailDir)
	seedUnackedMail(t, mail, target, "msg_585_fresh")

	enqueued := 0
	pump := &sessionCollabPump{}
	d := collabDelivery{
		enqueue: func(sessioncollab.MailMessage, string) (bool, error) {
			enqueued++
			return false, nil // 落为 queued follow-up（降级语义），消息不丢
		},
		deriveHop:      pump.verifyHop,
		render:         sessionCollabDeliveryText,
		settledReceipt: settledReceiptSeam(mail),
		recordReceipt: func(msg sessioncollab.MailMessage, outcome, detail string) {
			if outcome != sessioncollab.ReceiptQueuedFollowup {
				t.Fatalf("first delivery must settle as queued_followup, got %q", outcome)
			}
		},
	}
	delivered, _, err := runCollabDelivery(mail, target, d)
	if err != nil {
		t.Fatal(err)
	}
	if enqueued != 1 || delivered != 1 {
		t.Fatalf("an unsettled message must deliver exactly once: enqueued=%d delivered=%d", enqueued, delivered)
	}
}

// failed_retrying 非终态：目标上一次投递失败 → 泵必须重试投递，不得按「已发过」跳过；
// 重试成功后结算回执覆盖失败记录（570 语义）。
func TestFailedRetryingReceiptStillRedelivers(t *testing.T) {
	mailDir := setupIsolatedCollabMail(t)
	target := "sc_target585c"
	mail := sessioncollab.NewMailStore(mailDir)
	seedUnackedMail(t, mail, target, "msg_585_retry")
	if err := mail.RecordDeliveryReceipt(context.Background(), sessioncollab.DeliveryReceipt{
		MessageID: "msg_585_retry",
		From:      "sc_from585",
		To:        target,
		Delivery:  "followup",
		Outcome:   sessioncollab.ReceiptFailedRetrying,
		At:        time.Now().UnixMilli() - 30_000,
	}); err != nil {
		t.Fatal(err)
	}

	enqueued := 0
	pump := &sessionCollabPump{}
	d := collabDelivery{
		enqueue: func(sessioncollab.MailMessage, string) (bool, error) {
			enqueued++
			return false, nil
		},
		deriveHop:      pump.verifyHop,
		render:         sessionCollabDeliveryText,
		settledReceipt: settledReceiptSeam(mail),
		// 与生产 recordDeliveryReceipt 同构：重试成功后结算回执覆盖失败记录。
		recordReceipt: func(msg sessioncollab.MailMessage, outcome, detail string) {
			if err := mail.RecordDeliveryReceipt(context.Background(), sessioncollab.DeliveryReceipt{
				MessageID: msg.ID,
				From:      msg.From,
				To:        msg.To,
				Delivery:  msg.Delivery,
				Outcome:   outcome,
				Detail:    detail,
			}); err != nil {
				t.Fatal(err)
			}
		},
	}
	if _, _, err := runCollabDelivery(mail, target, d); err != nil {
		t.Fatal(err)
	}
	if enqueued != 1 {
		t.Fatalf("failed_retrying is not settled — the pump must retry, enqueue ran %d times", enqueued)
	}
	r, ok := mail.DeliveryReceipt("msg_585_retry")
	if !ok || r.Outcome != sessioncollab.ReceiptQueuedFollowup {
		t.Fatalf("the retry must overwrite the failure receipt, got %+v ok=%v", r, ok)
	}
}

// seam 为 nil（直接构造/旧测试形态）→ 行为逐字不变：照常投递。
func TestNilSettledReceiptKeepsLegacyDelivery(t *testing.T) {
	mailDir := setupIsolatedCollabMail(t)
	target := "sc_target585d"
	mail := sessioncollab.NewMailStore(mailDir)
	seedUnackedMail(t, mail, target, "msg_585_legacy")
	if err := mail.RecordDeliveryReceipt(context.Background(), sessioncollab.DeliveryReceipt{
		MessageID: "msg_585_legacy",
		From:      "sc_from585",
		To:        target,
		Delivery:  "followup",
		Outcome:   sessioncollab.ReceiptQueuedFollowup,
		At:        time.Now().UnixMilli() - 30_000,
	}); err != nil {
		t.Fatal(err)
	}

	enqueued := 0
	pump := &sessionCollabPump{}
	d := collabDelivery{
		enqueue: func(sessioncollab.MailMessage, string) (bool, error) {
			enqueued++
			return false, nil
		},
		deriveHop: pump.verifyHop,
		render:    sessionCollabDeliveryText,
		// settledReceipt 刻意缺省：无 seam 的调用方保持原行为。
	}
	if _, _, err := runCollabDelivery(mail, target, d); err != nil {
		t.Fatal(err)
	}
	if enqueued != 1 {
		t.Fatalf("nil seam must keep the legacy delivery, enqueue ran %d times", enqueued)
	}
}
