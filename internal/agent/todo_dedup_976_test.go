package agent

// Task 976: dedup 判重键含完整参数指纹 + 面板空自愈。0528 装机回归——
// 不同 ops/content 的 todo_write 因 ack 计数相同被误判去重，且 ops 写入的
// receipt 不含 todos 致面板恒空，write gate 连锁死锁。以下测试钉死四条验收。

import (
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/evidence"
	"reasonix/internal/provider"
)

// 验收①（判重键面）：不同 ops/content 的 todo_write，只要参数不同，即使
// ack 文本逐字节相同（计数一致），也不得互相去重。
func TestDedupeSplitsIdenticalAcksWithDifferentOps(t *testing.T) {
	a := &Agent{task: taskRuntime{ledger: evidence.NewLedger()}}
	a.task.ledger.Record(evidence.ReceiptFromToolCall("todo_write", json.RawMessage(`{"todos":[{"content":"plan","status":"in_progress"}]}`), true, true))
	ack := "Todos updated: 1 total — 0 completed, 1 in progress, 0 pending."
	first := a.dedupeProviderVisibleResult("todo_write", "c1", `{"ops":[{"op":"insert","item":{"content":"probe-A","status":"pending"}}]}`, ack, ack)
	if first != ack {
		t.Fatalf("first write was altered: %q", first)
	}
	second := a.dedupeProviderVisibleResult("todo_write", "c2", `{"ops":[{"op":"insert","item":{"content":"probe-B","status":"pending"}}]}`, ack, ack)
	if second != ack {
		t.Fatalf("different ops with an identical ack must not dedup (the 0528 regression): %q", second)
	}
}

// 验收②（972 本意不回归）：相同参数 + 相同结果的重复 todo_write 仍然去重，
// 面板有数据时回执「已去重，上次结果仍有效」。
func TestDedupeKeepsIdenticalRepeatWithSameOps(t *testing.T) {
	a := &Agent{task: taskRuntime{ledger: evidence.NewLedger()}}
	args := `{"ops":[{"op":"insert","item":{"content":"probe","status":"pending"}}]}`
	a.task.ledger.Record(evidence.ReceiptFromToolCall("todo_write", json.RawMessage(args), true, true))
	ack := "Todos updated: 2 total — 0 completed, 1 in progress, 1 pending."
	if got := a.dedupeProviderVisibleResult("todo_write", "c1", args, ack, ack); got != ack {
		t.Fatalf("first = %q", got)
	}
	second := a.dedupeProviderVisibleResult("todo_write", "c2", args, ack, ack)
	if !strings.Contains(second, "已去重，上次结果仍有效") || !strings.Contains(second, ack) {
		t.Fatalf("identical repeat must keep the 972 dedup reply: %q", second)
	}
}

// 验收③（防自锁断言）：面板空 + 去重命中 → 自愈放行真实结果，绝不回
// 「上次结果仍有效」的谎报——面板空时该承诺为假，模型会停止重试而 write
// gate 持续拦截，正是 0528 的自锁链。
func TestDedupeTodoWriteSelfHealsWhenPanelEmpty(t *testing.T) {
	a := &Agent{task: taskRuntime{ledger: evidence.NewLedger()}}
	args := `{"ops":[{"op":"insert","item":{"content":"probe","status":"in_progress"}}]}`
	ack := "Todos updated: 1 total — 0 completed, 1 in progress, 0 pending."
	if got := a.dedupeProviderVisibleResult("todo_write", "c1", args, ack, ack); got != ack {
		t.Fatalf("first = %q", got)
	}
	// 面板（ledger 最新 todo_write receipt）为空：第二次命中去重必须放行真实 ack。
	second := a.dedupeProviderVisibleResult("todo_write", "c2", args, ack, ack)
	if second != ack {
		t.Fatalf("empty panel must self-heal to the real result, got dedup reply: %q", second)
	}
}

// 验收①（面板面）+④：两次不同内容的 todo_write 经真实 receipt 流程落盘，
// 面板（canonical state / ledger baseline）反映第二次内容；write gate 读取
// 的 in_progress 信号（hasActiveCanonicalTodo → ctx.HasTodo）随之为真。
func TestOpsTodoWritesLandOnPanelThroughReceiptFlow(t *testing.T) {
	a := &Agent{svc: agentServices{sink: event.Discard}, task: taskRuntime{ledger: evidence.NewLedger()}}
	planFor := func(id, args string) *toolCallPlan {
		raw := json.RawMessage(args)
		return &toolCallPlan{
			call:         provider.ToolCall{ID: id, Name: "todo_write", Arguments: args},
			tool:         fakeTool{name: "todo_write", readOnly: true},
			evidenceName: "todo_write",
			evidenceArgs: raw,
		}
	}
	firstArgs := `{"ops":[{"op":"insert","item":{"content":"first-content","status":"in_progress"}}]}`
	rec1 := a.recordToolReceipts(planFor("c1", firstArgs), "Todos updated: 1 total", nil, nil)
	if len(rec1.Todos) != 1 || rec1.Todos[0].Content != "first-content" {
		t.Fatalf("first ops receipt todos = %+v, want the written item", rec1.Todos)
	}
	secondArgs := `{"ops":[{"op":"replace","step_id":"` + rec1.Todos[0].StepID + `","item":{"content":"second-content","status":"in_progress","step_id":"` + rec1.Todos[0].StepID + `"}}]}`
	rec2 := a.recordToolReceipts(planFor("c2", secondArgs), "Todos updated: 1 total", nil, nil)
	if len(rec2.Todos) != 1 || rec2.Todos[0].Content != "second-content" {
		t.Fatalf("second ops receipt todos = %+v, want the panel to reflect the second content", rec2.Todos)
	}

	canonical := a.CanonicalTodoState()
	if len(canonical) != 1 || canonical[0].Content != "second-content" {
		t.Fatalf("canonical panel = %+v, want the second content", canonical)
	}
	// 验收④的输入面：write gate（ContractPreconditionGuard）经 execution_engine
	// 读 ctx.HasTodo ← hasActiveCanonicalTodo；面板真实有数据后必须为真。
	if !a.hasActiveCanonicalTodo() {
		t.Fatalf("write gate input must be true after real ops writes: %+v", canonical)
	}
	// todo_read 的数据源（ledger 最新 todo_write）同样反映第二次内容。
	if todos := a.CurrentTaskTodoState(); len(todos) != 1 || todos[0].Content != "second-content" {
		t.Fatalf("todo_read source = %+v, want the second content", todos)
	}
}
