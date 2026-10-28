package sessioncollab

import (
	"context"
	"testing"
	"time"
)

// tl1226 遗留①（新信到达角标即涨）：投递侧广播缝的存储契约——新行落盘成功
// 恰好广播一次；去重重发不落新行不广播；投递失败不广播；注销后不再广播。

type deliveryEvent struct {
	msg     MailMessage
	mailDir string
}

func receiveDelivery(t *testing.T, ch chan deliveryEvent) deliveryEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the delivery notification")
		return deliveryEvent{}
	}
}

func assertNoDelivery(t *testing.T, ch chan deliveryEvent) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("unexpected delivery notification: id=%s to=%s", ev.msg.ID, ev.msg.To)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestDeliverNotifiesObserver(t *testing.T) {
	dir := t.TempDir()
	s := NewMailStore(dir)
	ch := make(chan deliveryEvent, 4)
	cancel := OnMailDelivered(func(msg MailMessage, mailDir string) {
		ch <- deliveryEvent{msg: msg, mailDir: mailDir}
	})
	t.Cleanup(cancel)

	msg, err := s.Deliver(context.Background(), MailMessage{From: "sc_a", To: "sc_b", Body: "hello"})
	if err != nil {
		t.Fatalf("Deliver failed: %v", err)
	}
	ev := receiveDelivery(t, ch)
	if ev.msg.ID != msg.ID || ev.msg.To != "sc_b" || ev.msg.Body != "hello" {
		t.Fatalf("observer got wrong message: id=%s to=%s body=%s", ev.msg.ID, ev.msg.To, ev.msg.Body)
	}
	if ev.mailDir != dir {
		t.Fatalf("observer got mailDir=%q, want %q", ev.mailDir, dir)
	}
}

func TestDeliverDedupeResendDoesNotNotify(t *testing.T) {
	dir := t.TempDir()
	s := NewMailStore(dir)
	ch := make(chan deliveryEvent, 4)
	cancel := OnMailDelivered(func(msg MailMessage, mailDir string) {
		ch <- deliveryEvent{msg: msg, mailDir: mailDir}
	})
	t.Cleanup(cancel)

	first, err := s.Deliver(context.Background(), MailMessage{From: "sc_a", To: "sc_b", Body: "same content"})
	if err != nil {
		t.Fatalf("first Deliver failed: %v", err)
	}
	receiveDelivery(t, ch) // 第一封：必须通知

	// 任务461 P8 ①：内容幂等窗口内的重发返原信、不落新行——未读数没变，
	// 因此也不得广播第二事件。
	second, err := s.Deliver(context.Background(), MailMessage{From: "sc_a", To: "sc_b", Body: "same content"})
	if err != nil {
		t.Fatalf("resend Deliver failed: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("resend returned new id %s, want original %s", second.ID, first.ID)
	}
	assertNoDelivery(t, ch)
}

func TestDeliverFailureDoesNotNotify(t *testing.T) {
	s := NewMailStore(t.TempDir())
	ch := make(chan deliveryEvent, 4)
	cancel := OnMailDelivered(func(msg MailMessage, mailDir string) {
		ch <- deliveryEvent{msg: msg, mailDir: mailDir}
	})
	t.Cleanup(cancel)

	if _, err := s.Deliver(context.Background(), MailMessage{From: "sc_a", To: "   ", Body: "no target"}); err == nil {
		t.Fatal("Deliver with an empty target must fail")
	}
	assertNoDelivery(t, ch)
}

func TestOnMailDeliveredCancelStopsNotifications(t *testing.T) {
	s := NewMailStore(t.TempDir())
	ch := make(chan deliveryEvent, 4)
	cancel := OnMailDelivered(func(msg MailMessage, mailDir string) {
		ch <- deliveryEvent{msg: msg, mailDir: mailDir}
	})
	cancel()

	if _, err := s.Deliver(context.Background(), MailMessage{From: "sc_a", To: "sc_b", Body: "after cancel"}); err != nil {
		t.Fatalf("Deliver failed: %v", err)
	}
	assertNoDelivery(t, ch)
}

func TestOnMailDeliveredNilObserverIsNoop(t *testing.T) {
	cancel := OnMailDelivered(nil)
	cancel() // 不得 panic
}
