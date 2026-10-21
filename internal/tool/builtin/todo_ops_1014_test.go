package builtin

// Task 1014: todo_write incremental ops defects, reproduced from the
// ai-passport-续 field log (sc_6a4e12a01e5673933a962978, 2026-10-19, 4
// rejections in a row).
//
// Defect ①: an ops.replace item that OMITS fields resets them on the
// replacement instead of inheriting the baseline's values — most damagingly
// `level`, where a level-1 sub-step silently became a level-0 root, its
// level-1 siblings then read as ITS sub-steps, and the batch died on
// "phase completed but sub-step unfinished". Fix a: replace/insert inherit
// omitted fields from the original entry; only explicitly sent fields
// overwrite.
//
// Defect ② (fix b): a batch of ops must be judged on the FINAL state after all
// ops apply, never on per-op intermediates. The application layer itself never
// validated per-op — the real-world deadlock is the model trying one op per
// todo_write CALL: each call lands as a receipt, so the intermediate state
// gets rejected by the serial state machine and the batch dies halfway. An
// ops batch is one atomic write: applied and validated once, at the end; a
// batch whose final state is illegal is rejected whole, never half-applied.
//
// Fix c: the rejection that misjudges siblings must carry the misjudged item's
// level value so the model can repair without guessing.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/evidence"
)

// planList is the ai-passport reproduction baseline: one phase (level 0) with
// two sub-steps (level 1), sub one in progress.
func ptrInt(v int) *int { return &v }

func planList() []evidence.TodoItem {
	return []evidence.TodoItem{
		{Content: "phase A", Status: "pending", Level: 0, StepID: "p_a"},
		{Content: "sub one", Status: "in_progress", Level: 1, StepID: "p_a_1"},
		{Content: "sub two", Status: "pending", Level: 1, StepID: "p_a_2"},
	}
}

// ledgerWithBaseline wires a prior successful todo_write into the ctx the way
// production does, so Execute validates against the same baseline.
func ledgerWithBaseline(t *testing.T, todos []evidence.TodoItem) context.Context {
	t.Helper()
	ledger := evidence.NewLedger()
	ledger.Record(evidence.Receipt{ToolName: "todo_write", Success: true, Todos: todos})
	return evidence.WithLedger(context.Background(), ledger)
}

// ① A replace whose item omits level (and activeForm/owner/parent_id) must not
// reset them: a partial update changes only the given fields.
func TestApplyTodoOpsReplaceInheritsOmittedFields(t *testing.T) {
	base := planList()
	base[1].ActiveForm = "Doing sub one"
	base[1].Owner = "main"

	list, err := applyTodoOps(base, []todoOp{{
		Op: "replace", StepID: "p_a_1",
		Item: &todoOpItem{Content: "sub one revised", Status: "completed", StepID: "p_a_1"},
	}})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	got := list[1]
	if got.Level != 1 {
		t.Fatalf("replace without level reset level: got %d, want 1 (kept from baseline)", got.Level)
	}
	if got.ActiveForm != "Doing sub one" {
		t.Fatalf("replace without activeForm reset it: got %q, want the baseline value", got.ActiveForm)
	}
	if got.Owner != "main" {
		t.Fatalf("replace without owner reset it: got %q, want the baseline value", got.Owner)
	}
	if got.Content != "sub one revised" || got.Status != "completed" {
		t.Fatalf("explicitly sent fields must overwrite: got %+v", got)
	}
}

// ① An item that explicitly sends level 0 must win over the inherited value
// (the model can still promote or demote on purpose).
func TestApplyTodoOpsReplaceExplicitLevelWins(t *testing.T) {
	list, err := applyTodoOps(planList(), []todoOp{{
		Op: "replace", StepID: "p_a_2",
		Item: &todoOpItem{Content: "sub two", Status: "pending", Level: ptrInt(0), StepID: "p_a_2"},
	}})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if list[2].Level != 0 {
		t.Fatalf("explicit level must overwrite: got %d, want 0", list[2].Level)
	}
}

// ① At the tool boundary: replacing a level-1 sub-step without sending level
// must not demote it to a phase header (which is what made its siblings read
// as sub-steps and deadlocked the write).
func TestTodoWriteOpsReplaceKeepsSubStepLevel(t *testing.T) {
	ctx := ledgerWithBaseline(t, planList())

	// Complete sub one AND start sub two in one batch — per-op intermediates
	// would both be rejected; the batch's final state is legal, and (the ①
	// part) the level-1 items must keep their level so no sibling reads as a
	// sub-step of a demoted phase header.
	out, err := (todoWrite{}).Execute(ctx, json.RawMessage(`{"ops":[
		{"op":"replace","step_id":"p_a_1","item":{"content":"sub one","status":"completed","step_id":"p_a_1"}},
		{"op":"replace","step_id":"p_a_2","item":{"content":"sub two","status":"in_progress","step_id":"p_a_2"}}
	]}`))
	if err != nil {
		t.Fatalf("ops replace of level-1 sub-steps must not fail: %v", err)
	}
	if !strings.Contains(out, "3 total") {
		t.Fatalf("ack = %q, want the 3-item list", out)
	}
}

// ② The ai-passport batch: completing sub one AND starting sub two in one ops
// batch. Either op alone is an illegal intermediate (completed with a pending
// sibling / in_progress after pending work); the batch must be judged on its
// final state only.
func TestTodoWriteOpsBatchValidatesFinalStateOnly(t *testing.T) {
	ctx := ledgerWithBaseline(t, planList())

	// Final state: sub one completed, sub two in_progress, phase still pending —
	// legal. Per-op intermediates would both be rejected.
	if _, err := (todoWrite{}).Execute(ctx, json.RawMessage(`{"ops":[
		{"op":"replace","step_id":"p_a_1","item":{"content":"sub one","status":"completed","level":1,"step_id":"p_a_1"}},
		{"op":"replace","step_id":"p_a_2","item":{"content":"sub two","status":"in_progress","level":1,"step_id":"p_a_2"}}
	]}`)); err != nil {
		t.Fatalf("a batch whose final state is legal must be accepted: %v", err)
	}
}

// ② 完成 A → 启动 B across phases in one batch: each replace flips one
// segment and only the combined final state satisfies the single
// in_progress rule.
func TestTodoWriteOpsBatchAdvancesAcrossPhases(t *testing.T) {
	base := []evidence.TodoItem{
		{Content: "phase A", Status: "in_progress", Level: 0, StepID: "p_a"},
		{Content: "sub a1", Status: "completed", Level: 1, StepID: "p_a_1"},
		{Content: "phase B", Status: "pending", Level: 0, StepID: "p_b"},
		{Content: "sub b1", Status: "pending", Level: 1, StepID: "p_b_1"},
	}
	ctx := ledgerWithBaseline(t, base)

	if _, err := (todoWrite{}).Execute(ctx, json.RawMessage(`{"ops":[
		{"op":"replace","step_id":"p_a","item":{"content":"phase A","status":"completed","level":0,"step_id":"p_a"}},
		{"op":"replace","step_id":"p_b_1","item":{"content":"sub b1","status":"in_progress","level":1,"step_id":"p_b_1"}}
	]}`)); err != nil {
		t.Fatalf("完成 A→启动 B as one batch must be accepted: %v", err)
	}
}

// ② The batch is still one atomic write: a batch whose FINAL state is illegal
// is rejected whole (never half-applied).
func TestTodoWriteOpsBatchStillRejectsIllegalFinalState(t *testing.T) {
	ctx := ledgerWithBaseline(t, planList())

	_, err := (todoWrite{}).Execute(ctx, json.RawMessage(`{"ops":[
		{"op":"replace","step_id":"p_a_1","item":{"content":"sub one","status":"completed","level":1,"step_id":"p_a_1"}}
	]}`))
	if err == nil {
		t.Fatal("completing sub one while sub two stays pending leaves no in_progress item; the batch must be rejected")
	}
	if !strings.Contains(err.Error(), "no in_progress") {
		t.Fatalf("want the final-state rejection, got %v", err)
	}
}

// ② The batch lands end-to-end: the recorded receipt carries the applied list
// with the inherited levels (no empty baseline, no demoted sub-step), so the
// next write chains on the correct state.
func TestTodoWriteOpsBatchRecordsAppliedList(t *testing.T) {
	ledger := evidence.NewLedger()
	ledger.Record(evidence.Receipt{ToolName: "todo_write", Success: true, Todos: planList()})
	ctx := evidence.WithLedger(context.Background(), ledger)

	if _, err := (todoWrite{}).Execute(ctx, json.RawMessage(`{"ops":[
		{"op":"replace","step_id":"p_a_1","item":{"content":"sub one","status":"completed","step_id":"p_a_1"}},
		{"op":"replace","step_id":"p_a_2","item":{"content":"sub two","status":"in_progress","step_id":"p_a_2"}}
	]}`)); err != nil {
		t.Fatalf("batched progression must land: %v", err)
	}
	// Mimic production: agent records the receipt from the raw args; Record
	// expands ops against the pre-write baseline.
	stored := ledger.Record(evidence.Receipt{
		ToolName: "todo_write",
		Success:  true,
		Args:     json.RawMessage(`{"ops":[{"op":"replace","step_id":"p_a_1","item":{"content":"sub one","status":"completed","step_id":"p_a_1"}}]}`),
	})
	if len(stored.Todos) != 3 {
		t.Fatalf("recorded todos = %+v, want the expanded 3-item list", stored.Todos)
	}
	if stored.Todos[1].Level != 1 || stored.Todos[1].Status != "completed" {
		t.Fatalf("receipt expansion lost the sub-step level or status: %+v", stored.Todos[1])
	}
}

// ③ Fix c: the segment rule that misjudges a demoted sibling as a phase
// header must carry the item's level value, so the model sees the level it
// was judged by instead of guessing.
func TestSerialValidationRejectionNamesLevelValue(t *testing.T) {
	// The defect-① end state: sub one lost its level to 0 while sub two is
	// still level 1 — sub two reads as sub one's sub-step, and the demoted
	// completed sibling trips the phase rule.
	demoted := []evidence.TodoItem{
		{Content: "sub one", Status: "completed", Level: 0, StepID: "p_a_1"},
		{Content: "sub two", Status: "pending", Level: 1, StepID: "p_a_2"},
	}
	err := evidence.ValidateSerialTodos(demoted)
	if err == nil {
		t.Fatal("the demoted list must still be rejected (this is the reported deadlock trigger)")
	}
	if !strings.Contains(err.Error(), "level") || !strings.Contains(err.Error(), "0") {
		t.Fatalf("rejection must name the misjudged item's level value, got %v", err)
	}
}
