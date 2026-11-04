package builtin

// tl1354 acceptance, end to end through todo_write.Execute: an ordering slip
// (the in_progress item behind pending work) now writes successfully with an
// ack note and the REORDERED list recorded, while every other failure class
// keeps its original rejection. The repair must also be visible on the ops
// path — both paths funnel through the same validation point.

import (
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/evidence"
)

// ② The task-book incident shape: a wholesale rewrite that puts the current
// item behind untouched pending work used to be hard-rejected; now it writes,
// the ack names the auto-reorder, and the production recording path (receipt
// parse + NormalizeSerialTodoInput) lands on the REORDERED shape — the same
// shape Execute accepted, so the next write's continuity check does not
// deadlock against a differently-shaped baseline.
func TestTodoWriteAutoRepairsOrderingSlip(t *testing.T) {
	ctx := ledgerWithPlanTodos(t, oldPlanArgs)

	args := `{"plan_id":"plan-old","todos":[
		{"content":"Ship the old task","status":"completed","step_id":"old_step_01"},
		{"content":"Clean up the old task","status":"abandoned","step_id":"old_step_02"},
		{"content":"Later chore","status":"pending","step_id":"old_step_04"},
		{"content":"Write the old report","status":"in_progress","step_id":"old_step_03"}
	]}`
	out, err := (todoWrite{}).Execute(ctx, json.RawMessage(args))
	if err != nil {
		t.Fatalf("ordering slip must be auto-repaired, not rejected: %v", err)
	}
	if !strings.Contains(out, "auto-reordered") {
		t.Fatalf("ack must name the auto-reorder, got %q", out)
	}

	// Production records through the receipt parser, then the shared entry
	// point setTodoState uses. The recorded order must match Execute's repair.
	rec := evidence.ReceiptFromToolCall("todo_write", json.RawMessage(args), true, true)
	items := evidence.NormalizeSerialTodoInput(rec.Todos)
	if len(items) != 4 {
		t.Fatalf("baseline must keep all four items, got %d", len(items))
	}
	// The abandoned item is converged work: it does not block the current
	// item, which lands right after it, ahead of the untouched pending chore.
	if items[2].StepID != "old_step_03" || items[2].Status != "in_progress" {
		t.Fatalf("the current item must be recorded right after the converged prefix, got %+v", items)
	}
	if items[3].StepID != "old_step_04" || items[3].Status != "pending" {
		t.Fatalf("the pending chore must follow the current item, got %+v", items)
	}
}

// ② The same tolerance on the ops path: a move op that lands the current item
// behind pending work is repaired by the same funnel, and the ops-expanded
// replay (what a reload reconstructs) records the repaired order too.
func TestTodoWriteOpsPathAutoRepairsOrderingSlip(t *testing.T) {
	base := []evidence.TodoItem{
		{Content: "done", Status: "completed", StepID: "s1"},
		{Content: "current", Status: "in_progress", StepID: "s2"},
		{Content: "backlog", Status: "pending", StepID: "s3"},
	}
	ctx := ledgerWithBaseline(t, base)

	// Move the current item below the backlog: an ordering slip on arrival.
	out, err := (todoWrite{}).Execute(ctx, json.RawMessage(`{"ops":[
		{"op":"move","step_id":"s2","after_step_id":"s3"}
	]}`))
	if err != nil {
		t.Fatalf("ops-induced ordering slip must be auto-repaired: %v", err)
	}
	if !strings.Contains(out, "auto-reordered") {
		t.Fatalf("ops ack must name the auto-reorder, got %q", out)
	}

	rec := evidence.ExpandTodoOpsReceipt(
		evidence.ReceiptFromToolCall("todo_write", json.RawMessage(`{"ops":[
			{"op":"move","step_id":"s2","after_step_id":"s3"}
		]}`), true, true), base)
	items := evidence.NormalizeSerialTodoInput(rec.Todos)
	if items[1].StepID != "s2" || items[1].Status != "in_progress" {
		t.Fatalf("repaired replay must record the current item in slot two, got %+v", items)
	}
	if items[2].StepID != "s3" || items[2].Status != "pending" {
		t.Fatalf("repaired replay must keep the backlog behind the current item, got %+v", items)
	}
}

// Tolerance is bounded: the repair fixes ONLY the order. A fabricated
// completed step riding on an ordering slip still hits the same-plan
// anti-forgery check after the reorder.
func TestTodoWriteRepairDoesNotOpenForgeryHole(t *testing.T) {
	ctx := ledgerWithPlanTodos(t, oldPlanArgs)

	_, err := (todoWrite{}).Execute(ctx, json.RawMessage(`{"plan_id":"plan-old","todos":[
		{"content":"Ship the old task","status":"completed","step_id":"old_step_01"},
		{"content":"Clean up the old task","status":"abandoned","step_id":"old_step_02"},
		{"content":"Fabricated glory","status":"completed","step_id":"old_step_77"},
		{"content":"Write the old report","status":"in_progress","step_id":"old_step_03"}
	]}`))
	if err == nil || !strings.Contains(err.Error(), "not a step from the current plan") {
		t.Fatalf("fabricated completed step must stay rejected alongside a repairable order, got %v", err)
	}
}

// Two in_progress items keep their dedicated rejection (the repair must not
// pick a winner), including the plan snapshot appendix.
func TestTodoWriteSecondInProgressStillRejected(t *testing.T) {
	ctx := ledgerWithPlanTodos(t, oldPlanArgs)

	_, err := (todoWrite{}).Execute(ctx, json.RawMessage(`{"plan_id":"plan-old","todos":[
		{"content":"Ship the old task","status":"completed","step_id":"old_step_01"},
		{"content":"Clean up the old task","status":"abandoned","step_id":"old_step_02"},
		{"content":"Write the old report","status":"in_progress","step_id":"old_step_03"},
		{"content":"Second current","status":"in_progress","step_id":"old_step_05"}
	]}`))
	if err == nil || !strings.Contains(err.Error(), "second in_progress item") {
		t.Fatalf("two currents must keep the dedicated rejection, got %v", err)
	}
}

// A well-formed list must not gain the reorder note: the note appears only
// when the host actually reordered.
func TestTodoWriteWellFormedListHasNoReorderNote(t *testing.T) {
	ctx := ledgerWithPlanTodos(t, oldPlanArgs)

	out, err := (todoWrite{}).Execute(ctx, json.RawMessage(`{"plan_id":"plan-old","todos":[
		{"content":"Ship the old task","status":"completed","step_id":"old_step_01"},
		{"content":"Clean up the old task","status":"abandoned","step_id":"old_step_02"},
		{"content":"Write the old report","status":"in_progress","step_id":"old_step_03"}
	]}`))
	if err != nil {
		t.Fatalf("well-formed rewrite should pass: %v", err)
	}
	if strings.Contains(out, "auto-reordered") {
		t.Fatalf("a correct order must not carry the reorder note: %q", out)
	}
}
