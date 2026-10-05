package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"reasonix/internal/event"
)

// All tabEventSink context mutations go through the locked setContext /
// clearContext accessors (no bare s.ctx = ... writes that data-race the
// s.context() reads in emitRuntimeEvent). After clearContext the sink stops
// emitting — emitRuntimeEvent sees a nil ctx and no-ops — and the queued
// emitter is drained, so a detached/backgrounded session can't flush stale
// events onto the now-rebound tab (#5352: stale "AI 不断输出" on the visible
// session after rapid session switching).
func TestTabEventSinkClearContextStopsEmission(t *testing.T) {
	var mu sync.Mutex
	var emitted int
	s := &tabEventSink{tabID: "t"}
	s.runtimeEvents.emit = func(context.Context, string, ...any) {
		mu.Lock()
		emitted++
		mu.Unlock()
	}

	s.setContext(context.Background())
	if s.context() == nil {
		t.Fatal("setContext did not install the context")
	}

	s.clearContext()
	if s.context() != nil {
		t.Fatal("clearContext did not clear the context")
	}

	// An emit after clearContext must not reach the runtime bridge.
	s.emitRuntimeEvent(eventChannel, toWireTab(event.Event{}, s.tabID))

	mu.Lock()
	defer mu.Unlock()
	if emitted != 0 {
		t.Fatalf("sink emitted %d events after clearContext, want 0", emitted)
	}
}

// 任务496片3（469 遗留②）：clearContext 的 ctx=nil 窗口内到达的事件走
// #9601 缓冲而不进 runtimeEvents 队列；缓冲若不随队列一并清空，复用该
// sink 的 setContext 会把旧会话缓冲 flush 到新 ctx——#5352「后台会话输出
// 串到可见会话」的小窗再现。判罪断言是同步的缓冲计数（flush 本身经异步
// emitter，行为断言存在 drain 前的观察窗）。
func TestTabEventSinkClearContextDropsContextlessBuffer(t *testing.T) {
	var mu sync.Mutex
	var emitted int
	s := &tabEventSink{tabID: "t"}
	s.runtimeEvents.emit = func(context.Context, string, ...any) {
		mu.Lock()
		emitted++
		mu.Unlock()
	}

	s.setContext(context.Background())
	s.clearContext()
	// Arrives in the ctx=nil window between retirements: buffered by #9601,
	// not queued.
	s.emitRuntimeEvent(eventChannel, toWireTab(event.Event{}, s.tabID))
	// The sink retires again (rapid switch/detach cycle): the second
	// clearContext must drop the buffered window too, not just the queue.
	s.clearContext()

	// Verdict (synchronous): clearContext must drop the buffer together with
	// the queue.
	s.mu.Lock()
	buffered := len(s.pendingRuntimeEvents)
	s.mu.Unlock()
	if buffered != 0 {
		t.Fatalf("clearContext left %d context-less buffered events; a reused sink's setContext would flush them onto the new context (#5352 window)", buffered)
	}

	// Behavior half: reinstalling a context (sink reuse) must deliver nothing.
	s.setContext(context.Background())
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if emitted != 0 {
		t.Fatalf("setContext flushed %d stale events onto the reused context, want 0", emitted)
	}
}

func TestTabEventSinkUsesBoundSessionGeneration(t *testing.T) {
	sink := &tabEventSink{tabID: "tab", ctx: context.Background()}
	sink.setSessionGeneration(7)
	delivered := make(chan uint64, 1)
	sink.runtimeEvents.emit = func(_ context.Context, name string, payload ...any) {
		if name != eventChannel || len(payload) != 1 {
			t.Fatalf("runtime event = %q/%d, want one %q event", name, len(payload), eventChannel)
		}
		wire, ok := payload[0].(wireEventTab)
		if !ok {
			t.Fatalf("payload type = %T, want wireEventTab", payload[0])
		}
		delivered <- wire.SessionGeneration
	}
	sink.Emit(event.Event{Kind: event.Notice, Text: "late"})
	select {
	case got := <-delivered:
		if got != 7 {
			t.Fatalf("event session generation = %d, want bound generation 7", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for sink event")
	}
}
