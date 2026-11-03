package agent

import (
	"errors"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/tool"
)

// tl1347: a steer loader that fails while the body itself is readable wraps
// the body in SteerBodyError; consumeSteer must surface it on the payload so
// the run loop's unapplied notice shows the user's original guidance instead
// of the "(body load failed)" placeholder.
func TestConsumeSteerCarriesBodyOnLoadFailure(t *testing.T) {
	a := New(nil, tool.NewRegistry(), NewSession(""), Options{}, event.Discard)
	a.steerMu.Lock()
	a.steerRunActive = true
	a.steerMu.Unlock()
	if !a.SteerItem("item-body", func() (SteerPayload, error) {
		return SteerPayload{}, &SteerBodyError{Body: "先看这张图再继续", Err: errors.New("frozen reference unavailable: missing.png")}
	}) {
		t.Fatal("active steer should be accepted")
	}
	payload, itemID, ok := a.consumeSteer()
	if ok {
		t.Fatal("load failure must not report ok")
	}
	if itemID != "item-body" {
		t.Fatalf("itemID = %q, want item-body", itemID)
	}
	if payload.Text != "先看这张图再继续" {
		t.Fatalf("payload.Text = %q, want the original body carried through", payload.Text)
	}
}

// When the loader cannot read any body at all (store error), no body may be
// invented — the payload stays empty and the run loop keeps its placeholder.
func TestConsumeSteerBodyEmptyWhenLoaderCannotRead(t *testing.T) {
	a := New(nil, tool.NewRegistry(), NewSession(""), Options{}, event.Discard)
	a.steerMu.Lock()
	a.steerRunActive = true
	a.steerMu.Unlock()
	if !a.SteerItem("item-blank", func() (SteerPayload, error) {
		return SteerPayload{}, errors.New("store boom")
	}) {
		t.Fatal("active steer should be accepted")
	}
	payload, itemID, ok := a.consumeSteer()
	if ok || itemID != "item-blank" {
		t.Fatalf("consume = (ok=%v, itemID=%q), want failure with itemID", ok, itemID)
	}
	if payload.Text != "" {
		t.Fatalf("payload.Text = %q, want empty so the literal fallback applies", payload.Text)
	}
}

// The run-end flush path shares the load-failure window; the dropped-steer
// notice and the local-only archive must carry the original body too.
func TestFlushSteerQueueCarriesBodyOnLoadFailure(t *testing.T) {
	var notices []event.Event
	a := New(nil, tool.NewRegistry(), NewSession(""), Options{}, event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && e.Code == event.NoticeCodeUnappliedSteer {
			notices = append(notices, e)
		}
	}))
	a.steerMu.Lock()
	a.steerRunActive = true
	a.steerMu.Unlock()
	if !a.SteerItem("flush-body", func() (SteerPayload, error) {
		return SteerPayload{}, &SteerBodyError{Body: "重试前先跑 go test", Err: errors.New("store boom")}
	}) {
		t.Fatal("active steer should be accepted")
	}
	a.flushSteerQueue()
	if len(notices) != 1 {
		t.Fatalf("unapplied notices = %d, want 1", len(notices))
	}
	if !strings.Contains(notices[0].Text, "重试前先跑 go test") {
		t.Fatalf("notice lost the original body: %q", notices[0].Text)
	}
	found := false
	for _, m := range a.Session().Messages {
		if m.LocalOnly && strings.Contains(m.Content, "重试前先跑 go test") {
			found = true
		}
	}
	if !found {
		t.Fatalf("archive lost the original body: %+v", a.Session().Messages)
	}
}
