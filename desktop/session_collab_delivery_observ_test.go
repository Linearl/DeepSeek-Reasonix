package main

// tl1747：投递可靠性打点接线测试。
//
// 验收锚点（任务书验收矩阵 1/3）：
//  1. failed_retrying——消息级 enqueue_error 在门关（observ=false）时也出
//     结构化记录（tl1747 摘 gate）；contact 级退避打点带 retry_attempt（连续
//     失败序号）与 retry_cap（0=无次数上限）。
//  2. exhausted——controller 侧预算耗尽（open_retry_exhausted）在门关时也出
//     结构化记录，带 attempts 与 retry_cap=3（5/15/45s 预算）。
//  3. recovered——contact 失败链被成功 pass 收口时出 contact_retry_recovered
//     记录（重试成功率分子）。
//
// 全部 t.Fatal 断言，无静默 SKIP。

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"reasonix/internal/control"
	"reasonix/internal/sessioncollab"
)

// failedEnqueueDelivery 装配一个 enqueue 必失败的 collabDelivery（其余面
// 与生产夹具同形）。
func failedEnqueueDelivery(err error) collabDelivery {
	return collabDelivery{
		enqueue:   func(sessioncollab.MailMessage, string, bool) (bool, error) { return false, err },
		deriveHop: func(sessioncollab.MailMessage) (int, error) { return 0, nil },
		render:    func(msg sessioncollab.MailMessage, hop int) string { return msg.Body },
	}
}

// ① 消息级 failed_retrying：门关也出结构化记录（tl1747 摘 gate 断言）。
func TestFailedRetryingEmitsWithoutAdmissionGate(t *testing.T) {
	mail, target := newGateTestMail(t)
	buf := captureDecisionLog(t)
	if _, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{From: "sc_a", To: target, Body: "投不出去"}); err != nil {
		t.Fatal(err)
	}
	d := failedEnqueueDelivery(errors.New("runtime gone"))
	// 不设 admit/observ —— 生产 deliverToTarget 在门关时正是这样装配。
	if _, _, err := runCollabDelivery(mail, target, d); err == nil {
		t.Fatal("enqueue error must surface")
	}
	logText := buf.String()
	for _, want := range []string{
		"delivery_decision=failed_retrying",
		"decision_reason=enqueue_error",
		"session=" + target,
		"msg_id=",
		"from=sc_a",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("gate-off enqueue_error must carry %s, got:\n%s", want, logText)
		}
	}
}

// ② contact 级退避打点：retry_attempt 随连续失败递增，retry_cap=0（无上限）。
func TestContactRetryFailedCarriesAttemptAndUncappedCap(t *testing.T) {
	p := newSessionCollabPump(&App{})
	buf := captureDecisionLog(t)
	now := time.Now()

	// 首次失败：retry_attempt=1。
	p.deferContactRetry("sc_x", now)
	logContactRetryFailed(p, "sc_x", "topic_x", "contact_pass_failed")
	// 二次失败：retry_attempt=2（session_open_failed 在真实 drain 里无 topic，
	// 这里传 topic_x 只为断言字段逐字格式）。
	p.deferContactRetry("sc_x", now)
	logContactRetryFailed(p, "sc_x", "topic_x", "session_open_failed")

	logText := buf.String()
	if !strings.Contains(logText, "delivery_decision=failed_retrying decision_reason=contact_pass_failed session=sc_x topic=topic_x retry_attempt=1 retry_cap=0") {
		t.Fatalf("first failed pass must read attempt=1 cap=0, got:\n%s", logText)
	}
	if !strings.Contains(logText, "delivery_decision=failed_retrying decision_reason=session_open_failed session=sc_x topic=topic_x retry_attempt=2 retry_cap=0") {
		t.Fatalf("second failed pass must read attempt=2, got:\n%s", logText)
	}
	if got := p.contactRetryFailures("sc_x"); got != 2 {
		t.Fatalf("chain count must be 2 after two failures, got %d", got)
	}
}

// ③ recovered：失败链被成功 pass 收口时出 contact_retry_recovered（成功率分子）。
func TestContactRetryRecoveredCarriesChainCount(t *testing.T) {
	p := newSessionCollabPump(&App{})
	buf := captureDecisionLog(t)
	now := time.Now()

	p.deferContactRetry("sc_y", now)
	p.deferContactRetry("sc_y", now)
	prev := p.contactRetryFailures("sc_y")
	if prev != 2 {
		t.Fatalf("chain count must be 2 before recovery, got %d", prev)
	}
	logContactRetryRecovered("sc_y", "topic_y", prev)

	logText := buf.String()
	if !strings.Contains(logText, "delivery_decision=delivered decision_reason=contact_retry_recovered session=sc_y topic=topic_y retry_attempt=2 retry_cap=0") {
		t.Fatalf("recovery must carry the chain count, got:\n%s", logText)
	}
}

// ④ exhausted：controller 侧预算耗尽在门关时也出结构化记录（tl1747 摘 gate），
// attempts/retry_cap 齐备。isolateDesktopUserDirs 让 mailDir 落到临时目录。
func TestInboxDispatchExhaustedEmitsWithRetryCap(t *testing.T) {
	isolateDesktopUserDirs(t)
	buf := captureDecisionLog(t)
	a := &App{}
	a.inboxDispatchExhausted(control.InboxDispatchExhausted{
		ItemID:       "item_1",
		Source:       "collab:sc_sender",
		CollabMsgID:  "msg_ex",
		CollabMailTo: "sc_target",
		Attempts:     control.InboxRuntimeRetryAttempts,
		Err:          context.DeadlineExceeded,
	})
	logText := buf.String()
	for _, want := range []string{
		"delivery_decision=degraded",
		"decision_reason=open_retry_exhausted",
		"session=sc_target",
		"msg_id=msg_ex",
		"attempts=3",
		"retry_cap=3",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("exhaustion record must carry %s, got:\n%s", want, logText)
		}
	}
}
