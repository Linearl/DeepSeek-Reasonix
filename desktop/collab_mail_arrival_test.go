package main

// tl1226 遗留①（新信到达角标即涨）：投递→Wails 事件（CollabMailChanged）的
// desktop 接线契约——只广播本应用信箱目录的投递、payload 携带 id/from/to、
// ctx 未就绪不广播、注册后真实 Deliver 触发事件。

import (
	"context"
	"testing"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/sessioncollab"
)

type collabMailArrivalCapture struct {
	name    string
	payload map[string]string
}

func stubCollabMailArrivalEmitter(t *testing.T) chan collabMailArrivalCapture {
	t.Helper()
	ch := make(chan collabMailArrivalCapture, 8)
	orig := runtimeEventsEmitFallback
	runtimeEventsEmitFallback = func(_ context.Context, name string, data ...any) {
		cap := collabMailArrivalCapture{name: name}
		if len(data) > 0 {
			if m, ok := data[0].(map[string]string); ok {
				cap.payload = m
			}
		}
		ch <- cap
	}
	t.Cleanup(func() { runtimeEventsEmitFallback = orig })
	return ch
}

func TestCollabMailArrivalObserverEmitsForOwnMailDir(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	ch := stubCollabMailArrivalEmitter(t)

	observer := app.collabMailArrivalObserver()
	observer(sessioncollab.MailMessage{ID: "msg_1", From: "sc_a", To: "sc_b"}, config.SessionCollabMailDir())

	select {
	case got := <-ch:
		if got.name != collabMailChangedEvent {
			t.Fatalf("event name = %q, want %q", got.name, collabMailChangedEvent)
		}
		if got.payload["id"] != "msg_1" || got.payload["from"] != "sc_a" || got.payload["to"] != "sc_b" {
			t.Fatalf("payload = %v, want id/from/to of the delivered message", got.payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the CollabMailChanged event")
	}
}

func TestCollabMailArrivalObserverSkipsForeignMailDir(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	ch := stubCollabMailArrivalEmitter(t)

	observer := app.collabMailArrivalObserver()
	observer(sessioncollab.MailMessage{ID: "msg_2", From: "sc_a", To: "sc_b"}, t.TempDir())

	select {
	case got := <-ch:
		t.Fatalf("foreign mailDir must not emit, got event %q", got.name)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestCollabMailArrivalObserverNoCtxNoEmit(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp() // ctx 未就绪（startup 之前）
	ch := stubCollabMailArrivalEmitter(t)

	observer := app.collabMailArrivalObserver()
	observer(sessioncollab.MailMessage{ID: "msg_3", From: "sc_a", To: "sc_b"}, config.SessionCollabMailDir())

	select {
	case got := <-ch:
		t.Fatalf("nil ctx must not emit, got event %q", got.name)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestWatchCollabMailArrivalsDeliverEmitsEvent(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	ch := stubCollabMailArrivalEmitter(t)

	app.watchCollabMailArrivals()

	mail := sessioncollab.NewMailStore(config.SessionCollabMailDir())
	if _, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{From: "sc_a", To: "sc_main", Body: "badge rises"}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	select {
	case got := <-ch:
		if got.name != collabMailChangedEvent {
			t.Fatalf("event name = %q, want %q", got.name, collabMailChangedEvent)
		}
		if got.payload["to"] != "sc_main" {
			t.Fatalf("payload = %v, want the delivered message's recipient", got.payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the delivery-triggered CollabMailChanged event")
	}
}
