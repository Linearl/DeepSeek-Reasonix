package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/event"
)

// 任务461-P16 交付验收：ask 投递延迟链的后端两段——①每条 wire 事件携带
// emittedAt（前端据其量化 emit→收到 延迟，三症状共用通道的公共锚点）；
// ②单通道异步发射队列积压超阈值时告警（一次拥塞只报一次），给「弹窗延迟/
// 不弹」提供后端侧瓶颈证据。

func TestToWireTabStampsEmittedAt(t *testing.T) {
	before := time.Now().UnixMilli()
	wire, ok := toWireTabWithSubmission(event.Event{Kind: event.AskRequest}, "tab1", "", "", 0).(wireEventTab)
	if !ok {
		t.Fatal("toWireTabWithSubmission did not return a wireEventTab")
	}
	after := time.Now().UnixMilli()
	if wire.EmittedAt < before || wire.EmittedAt > after {
		t.Fatalf("EmittedAt = %d, want a stamp within [%d, %d]", wire.EmittedAt, before, after)
	}
	if wire.TabID != "tab1" {
		t.Fatalf("TabID = %q, want tab1 (stamping must not disturb routing)", wire.TabID)
	}
}

func TestAsyncRuntimeEmitterWarnsOnQueueLag(t *testing.T) {
	oldThreshold, oldWarn := runtimeEventLagWarnThreshold, runtimeEventLagWarnFunc
	runtimeEventLagWarnThreshold = 30 * time.Millisecond
	t.Cleanup(func() {
		runtimeEventLagWarnThreshold, runtimeEventLagWarnFunc = oldThreshold, oldWarn
	})
	warns := make(chan string, 8)
	runtimeEventLagWarnFunc = func(name string, _ time.Duration, _ int) {
		warns <- name
	}

	gate := make(chan struct{})
	var calls atomic.Int32
	em := &asyncRuntimeEmitter{emit: func(context.Context, string, ...any) {
		if calls.Add(1) == 1 {
			// 首条卡住 drain（webview 通道堵塞的现场形状），后到的在队列里老化。
			<-gate
		}
	}}

	em.Emit(context.Background(), "ask first")
	time.Sleep(50 * time.Millisecond) // 让 drain 进入首条的 emit 并停住
	em.Emit(context.Background(), "ask second")
	time.Sleep(80 * time.Millisecond) // 第二条在队列里老化过阈值
	close(gate)

	// 拥塞条目：必须报一次滞后。
	select {
	case name := <-warns:
		if name != "ask second" {
			t.Fatalf("lag reported for %q, want the queued-behind event", name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a queue-lagged runtime event never reported the lag")
	}

	// 拥塞结束后的新鲜事件不得再报（一次拥塞只报一次）。
	em.Emit(context.Background(), "ask fresh")
	select {
	case name := <-warns:
		t.Fatalf("fresh event %q was reported lagged after the congestion cleared", name)
	case <-time.After(300 * time.Millisecond):
	}
}
