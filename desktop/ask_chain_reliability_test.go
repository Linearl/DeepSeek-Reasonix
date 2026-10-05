package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"reasonix/internal/event"
)

// 任务469：ask 弹窗可靠性——链路级回归钉。断点在 webview 侧 fence（前端已修，
// 前端单测另钉），这里钉住后端两段在复现矩阵场景下的行为契约：
// ①「ask 前切走切回」：切走（clearContext）→ ask emit（#9601 缓冲）→ 切回
//   （setContext）冲账——ask 必须按序到达 webview 通道，wire 三要素
//   （tabId 路由 / emittedAt 锚 / promptKind=ask）齐全；
// ②「多会话并发 ask」：两个 tab sink 各持独立发射队列，一个 tab 的通道拥堵
//   不得饿死另一个 tab 的 ask（弹窗延迟/不弹的单通道瓶颈在此被 per-sink 队列
//   隔离，这是「多会话并发 ask 都能弹」的后端前提）。

type capturedWireEvent struct {
	name    string
	payload any
}

func captureRuntimeEmits(t *testing.T, sink *tabEventSink) <-chan capturedWireEvent {
	t.Helper()
	delivered := make(chan capturedWireEvent, 64)
	sink.runtimeEvents.emit = func(_ context.Context, name string, payload ...any) {
		if len(payload) != 1 {
			t.Errorf("runtime emit payload count = %d, want 1", len(payload))
			return
		}
		delivered <- capturedWireEvent{name: name, payload: payload[0]}
	}
	return delivered
}

// ① 切走切回：detach 窗口（ctx=nil）内的 ask 不丢，setContext 冲账时按序送达。
func TestTabSinkFlushesBufferedAskOnSetContext(t *testing.T) {
	sink := &tabEventSink{tabID: "tab-469"}
	delivered := captureRuntimeEmits(t, sink)

	// 切走：clearContext 后 sink 进入缓冲模式（#9601）。
	sink.clearContext()
	askEvent := event.Event{
		Kind:   event.AskRequest,
		TurnID: "turn-469",
		ItemID: "ask-1",
		Ask:    event.Ask{ID: "ask-1", TurnID: "turn-469"},
	}
	sink.Emit(askEvent)

	// 冲账前不得有送达（缓冲，不静默丢）。
	select {
	case got := <-delivered:
		t.Fatalf("buffered ask delivered before setContext: %+v", got)
	case <-time.After(150 * time.Millisecond):
	}

	// 切回：setContext 冲账。
	sink.setContext(context.Background())
	select {
	case got := <-delivered:
		wire, ok := got.payload.(wireEventTab)
		if !ok {
			t.Fatalf("flushed ask payload type = %T, want wireEventTab", got.payload)
		}
		if got.name != eventChannel {
			t.Fatalf("flushed ask channel = %q, want %q", got.name, eventChannel)
		}
		if wire.TabID != "tab-469" {
			t.Fatalf("flushed ask TabID = %q, want tab-469 (routing must survive the buffered window)", wire.TabID)
		}
		if wire.Kind != "ask_request" || wire.PromptKind != "ask" || wire.PromptID != "ask-1" {
			t.Fatalf("flushed ask wire kind/prompt = %q/%q/%q, want ask_request/ask/ask-1", wire.Kind, wire.PromptKind, wire.PromptID)
		}
		if wire.EmittedAt <= 0 {
			t.Fatal("flushed ask carries no emittedAt anchor (P16 latency chain broken)")
		}
		if wire.Ask == nil || wire.Ask.ID != "ask-1" {
			t.Fatalf("flushed ask payload lost the ask body: %+v", wire.Ask)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("buffered ask was never flushed on setContext")
	}
}

// ② 多会话并发：A 通道拥堵时 B 的 ask 即时送达；A 解阻后自身 ask 也不丢。
func TestConcurrentTabSinksDeliverAsksIndependently(t *testing.T) {
	newTabSink := func(id string) (*tabEventSink, <-chan capturedWireEvent) {
		sink := &tabEventSink{tabID: id}
		sink.setContext(context.Background())
		return sink, captureRuntimeEmits(t, sink)
	}
	askFor := func(id string) event.Event {
		return event.Event{
			Kind:   event.AskRequest,
			TurnID: "turn-" + id,
			ItemID: "ask-" + id,
			Ask:    event.Ask{ID: "ask-" + id, TurnID: "turn-" + id},
		}
	}

	sinkA, deliveredA := newTabSink("tab-a")
	sinkB, deliveredB := newTabSink("tab-b")

	// A 的 webview 通道卡住（单通道拥堵现场形状）。
	blockA := make(chan struct{})
	aEmitted := make(chan struct{}, 8)
	prevEmit := sinkA.runtimeEvents.emit
	sinkA.runtimeEvents.emit = func(ctx context.Context, name string, payload ...any) {
		aEmitted <- struct{}{}
		prevEmit(ctx, name, payload...)
		if len(aEmitted) == 1 {
			<-blockA
		}
	}
	// 事件1 占住 A 的 drain。
	sinkA.Emit(event.Event{Kind: event.Text, Text: "A flood"})
	select {
	case <-aEmitted:
	case <-time.After(2 * time.Second):
		t.Fatal("sink A drain never started")
	}

	// 拥堵期间：两会话并发各发一条 ask。
	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Add(2)
	go func() { defer wg.Done(); sinkA.Emit(askFor("a")) }()
	go func() { defer wg.Done(); sinkB.Emit(askFor("b")) }()

	// B 的 ask 不等 A：必须即时送达。
	select {
	case got := <-deliveredB:
		wire, ok := got.payload.(wireEventTab)
		if !ok {
			t.Fatalf("B ask payload type = %T, want wireEventTab", got.payload)
		}
		if wire.TabID != "tab-b" || wire.PromptID != "ask-b" {
			t.Fatalf("B ask = tab %q prompt %q, want tab-b/ask-b (cross-tab routing or starvation)", wire.TabID, wire.PromptID)
		}
		if wire.Kind != "ask_request" {
			t.Fatalf("B ask kind = %q, want ask_request", wire.Kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tab B ask starved behind tab A's congested channel")
	}

	// A 解阻：A 的 ask 按序送达（拥堵只延迟，不丢）。
	close(blockA)
	for remaining := 2; remaining > 0; remaining-- {
		select {
		case got := <-deliveredA:
			wire, ok := got.payload.(wireEventTab)
			if !ok {
				continue
			}
			if wire.PromptID == "ask-a" {
				if wire.TabID != "tab-a" {
					t.Fatalf("A ask TabID = %q, want tab-a", wire.TabID)
				}
				if wire.EmittedAt <= 0 {
					t.Fatal("A ask carries no emittedAt anchor")
				}
				return
			}
		case <-time.After(2 * time.Second):
			t.Fatal("tab A ask lost after congestion cleared")
		}
	}
}
