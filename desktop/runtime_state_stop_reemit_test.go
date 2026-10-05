package main

import (
	"context"
	"testing"
	"time"

	"reasonix/internal/event"
)

// 任务510（运行态未知时无终止按钮）后端一环：tabEventSink.RuntimeStateChanged 曾把
// 「落后修订/换纪元」的状态事件静默丢弃——若最新状态从未推给前端，turn 开始态只能
// 等周期 sync 兜底（用户实测约 40s 才恢复，期间前端 unknown 降级态吞掉停止按钮）。
// 钉住补发契约：过期事件也触发一次当前投影重发；投影内容不变时后端 revision 不前
// 移、前端 reducer 判 duplicate，补发不会形成事件风暴。无控制器的 tab 仍不发。
func TestTabSinkRuntimeStateChangedReemitsSupersededEvents(t *testing.T) {
	ctrl := &bindingRuntimeReader{state: event.RuntimeStateSnapshot{SchemaVersion: 1, RuntimeEpoch: "e1", Revision: 5, Phase: "executing", Running: true}}
	tab := &WorkspaceTab{ID: "tab-510", Scope: "global", Ctrl: ctrl}
	a := &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}, detachedSessions: map[string]*WorkspaceTab{}, ctx: context.Background()}
	delivered := make(chan capturedWireEvent, 16)
	a.runtimeEvents.emit = func(_ context.Context, name string, payload ...any) {
		if len(payload) != 1 {
			t.Errorf("runtime emit payload count = %d, want 1", len(payload))
			return
		}
		delivered <- capturedWireEvent{name: name, payload: payload[0]}
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: a}
	// 预热投影：revision 记账一次，此后内容不变则 revision 稳定（重复补发自去重）。
	a.GetRuntimeStateSnapshot()

	// ① 落后修订的事件（旧代码在此静默 return）：必须仍补发，且带控制器现状。
	tab.sink.RuntimeStateChanged(event.RuntimeStateSnapshot{SchemaVersion: 1, RuntimeEpoch: "e1", Revision: 1})
	projection := waitForRuntimeReemit(t, delivered)
	if len(projection.Sessions) != 1 || projection.Sessions[0].State.Revision != 5 || !projection.Sessions[0].State.Running {
		t.Fatalf("re-emitted projection lost the current controller state: %+v", projection.Sessions)
	}

	// ② 换纪元的事件同样补发（epoch 不匹配也曾被静默吞掉）。
	tab.sink.RuntimeStateChanged(event.RuntimeStateSnapshot{SchemaVersion: 1, RuntimeEpoch: "epoch-from-a-past-life", Revision: 99})
	waitForRuntimeReemit(t, delivered)

	// ③ 无控制器的 tab 不发（通道语义保持）。
	bare := &WorkspaceTab{ID: "tab-510-bare", Scope: "global"}
	a.tabs[bare.ID] = bare
	bare.sink = &tabEventSink{tabID: bare.ID, app: a}
	bare.sink.RuntimeStateChanged(event.RuntimeStateSnapshot{SchemaVersion: 1, RuntimeEpoch: "e1", Revision: 1})
	select {
	case got := <-delivered:
		t.Fatalf("sink without a controller must not emit: %+v", got)
	case <-time.After(150 * time.Millisecond):
	}
}

// waitForRuntimeReemit collects the three-event re-emit burst in wire order
// (project-tree:runtime-changed, runtime-state:changed, project-tree:changed)
// and returns the projection carried by runtime-state:changed.
func waitForRuntimeReemit(t *testing.T, delivered <-chan capturedWireEvent) RuntimeStateProjection {
	t.Helper()
	wantOrder := []string{"project-tree:runtime-changed", "runtime-state:changed", "project-tree:changed"}
	var projection RuntimeStateProjection
	for i, want := range wantOrder {
		select {
		case got := <-delivered:
			if got.name != want {
				t.Fatalf("re-emit #%d name = %q, want %q", i, got.name, want)
			}
			if got.name == "runtime-state:changed" {
				p, ok := got.payload.(RuntimeStateProjection)
				if !ok {
					t.Fatalf("runtime-state:changed payload type = %T, want RuntimeStateProjection", got.payload)
				}
				projection = p
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("expected %q re-emit never arrived", want)
		}
	}
	return projection
}
