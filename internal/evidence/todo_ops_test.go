package evidence

import (
	"encoding/json"
	"strings"
	"testing"
)

func todoWriteReceipt(t *testing.T, args string) Receipt {
	t.Helper()
	return ReceiptFromToolCall("todo_write", json.RawMessage(args), true, true)
}

// Task 976, panel truth: an ops-based todo_write must record the resulting
// full list, not an empty one. Receipts that only parsed the todos field made
// every ops write land as "no todo list", which emptied the panel, made
// todo_read lie, and fed the write gate the empty state that deadlocked the
// turn.
func TestLedgerRecordExpandsOpsIntoReceiptTodos(t *testing.T) {
	l := NewLedger()
	l.Record(todoWriteReceipt(t, `{"todos":[{"content":"first","status":"in_progress","step_id":"s1"}]}`))

	ops := todoWriteReceipt(t, `{"ops":[{"op":"insert","item":{"content":"second","status":"pending"}}]}`)
	if len(ops.Todos) != 0 {
		t.Fatalf("precondition: ReceiptFromToolCall itself stays args-pure, got %+v", ops.Todos)
	}
	stored := l.Record(ops)
	if len(stored.Todos) != 2 {
		t.Fatalf("recorded ops receipt todos = %+v, want the 2-item applied list", stored.Todos)
	}
	if stored.Todos[1].Content != "second" {
		t.Fatalf("recorded ops receipt lost the inserted item: %+v", stored.Todos)
	}

	todos, ok := l.LatestTodos()
	if !ok || len(todos) != 2 || todos[1].Content != "second" {
		t.Fatalf("LatestTodos after ops write = %+v ok=%v, want the applied list", todos, ok)
	}
}

// Task 976: sequential ops writes chain baselines — the second write replays
// against the list the first recorded, exactly as todo_write.Execute applied
// them against the live baseline.
func TestLedgerRecordChainsOpsBaselines(t *testing.T) {
	l := NewLedger()
	l.Record(todoWriteReceipt(t, `{"ops":[{"op":"insert","item":{"content":"alpha","status":"in_progress","step_id":"s1"}}]}`))
	stored := l.Record(todoWriteReceipt(t, `{"ops":[{"op":"replace","step_id":"s1","item":{"content":"alpha-renamed","status":"in_progress","step_id":"s1"}}]}`))
	if len(stored.Todos) != 1 || stored.Todos[0].Content != "alpha-renamed" {
		t.Fatalf("chained ops receipt = %+v, want the renamed single item", stored.Todos)
	}
}

// Task 976: a full-todos receipt keeps its own list; a receipt whose ops no
// longer re-apply stays empty rather than inventing state.
func TestLedgerRecordKeepsFullTodosAndUnappliableOps(t *testing.T) {
	l := NewLedger()
	full := l.Record(todoWriteReceipt(t, `{"todos":[{"content":"solo","status":"in_progress","step_id":"s1"}]}`))
	if len(full.Todos) != 1 || full.Todos[0].Content != "solo" {
		t.Fatalf("full-todos receipt changed: %+v", full.Todos)
	}
	broken := l.Record(todoWriteReceipt(t, `{"ops":[{"op":"replace","step_id":"ghost","item":{"content":"x","status":"pending"}}]}`))
	if len(broken.Todos) != 0 {
		t.Fatalf("unappliable ops must not invent a list: %+v", broken.Todos)
	}
}

// Task 976: the expanded receipt mirrors the full-todos path — plan marker
// stamped on every item, host-assigned stable ids for id-less inserts.
func TestExpandTodoOpsReceiptStampsPlanAndStableIDs(t *testing.T) {
	rec := todoWriteReceipt(t, `{"plan_id":"plan-A","ops":[{"op":"insert","item":{"content":"Watch the build","status":"pending"}}]}`)
	expanded := ExpandTodoOpsReceipt(rec, nil)
	if len(expanded.Todos) != 1 {
		t.Fatalf("expanded todos = %+v", expanded.Todos)
	}
	if expanded.Todos[0].PlanID != "plan-A" {
		t.Fatalf("plan marker not stamped: %+v", expanded.Todos[0])
	}
	if !strings.HasPrefix(expanded.Todos[0].StepID, AutoStepIDPrefix) {
		t.Fatalf("host stable id not assigned: %+v", expanded.Todos[0])
	}

	// No ops → unchanged (an explicit clear must stay an explicit clear).
	clear := todoWriteReceipt(t, `{"todos":[]}`)
	if got := ExpandTodoOpsReceipt(clear, expanded.Todos); len(got.Todos) != 0 {
		t.Fatalf("empty full-list receipt must stay empty: %+v", got.Todos)
	}
}

// Task 976: transcript rebuild replays the same expansion — a reload after a
// rewind restores the ops-written list instead of wiping it.
func TestExpandTodoOpsReceiptReplaysAgainstRebuiltBase(t *testing.T) {
	rec := todoWriteReceipt(t, `{"ops":[{"op":"insert","item":{"content":"probe-976","status":"in_progress"}}]}`)
	base := []TodoItem{{Content: "anchor", Status: "completed", StepID: "s0"}}
	expanded := ExpandTodoOpsReceipt(rec, base)
	if len(expanded.Todos) != 2 || expanded.Todos[1].Content != "probe-976" {
		t.Fatalf("rebuilt expansion = %+v, want base plus inserted item", expanded.Todos)
	}
}
