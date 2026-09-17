package agent

import (
	"encoding/json"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/evidence"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// Task 23 P1-d: once out-of-order completion is legal (P1-a), the position of
// an already-completed step carries no meaning. A rewrite that only gathers the
// finished steps somewhere else is the same plan and must stay on the fast
// path, while a rewrite of the unfinished spine still reaches the reviewer.
func TestRecoveryPlanTransitionIgnoresReorderedCompletedSteps(t *testing.T) {
	a := &Agent{}
	a.setTodoState([]evidence.TodoItem{
		{Content: "Inspect environment", Status: "completed"},
		{Content: "Implement parser", Status: "in_progress"},
		{Content: "Run the suite", Status: "pending"},
	})

	// The finished step moves to the end; the unfinished spine keeps its order.
	// Every list below is a legal serial list, so the check under test really
	// runs instead of exiting early on a validation error.
	reordered := json.RawMessage(`{"todos":[
		{"content":"Implement parser","status":"in_progress"},
		{"content":"Run the suite","status":"pending"},
		{"content":"Inspect environment","status":"completed"}
	]}`)
	if changed, _, _, _ := a.recoveryPlanTransition("todo_write", reordered); changed {
		t.Fatal("moving a completed step is not a plan transition (#23 P1-d)")
	}

	// Finishing the current step is progress, not a new plan.
	progress := json.RawMessage(`{"todos":[
		{"content":"Implement parser","status":"completed"},
		{"content":"Run the suite","status":"in_progress"},
		{"content":"Inspect environment","status":"completed"}
	]}`)
	if changed, _, _, _ := a.recoveryPlanTransition("todo_write", progress); changed {
		t.Fatal("finishing the current step must not invoke the plan reviewer")
	}

	// Rewriting what the current step says is still a rewrite.
	rewrittenSpine := json.RawMessage(`{"todos":[
		{"content":"Inspect environment","status":"completed"},
		{"content":"Rewrite the parser from scratch","status":"in_progress"},
		{"content":"Run the suite","status":"pending"}
	]}`)
	if changed, _, _, _ := a.recoveryPlanTransition("todo_write", rewrittenSpine); !changed {
		t.Fatal("rewriting the current step must still reach the plan reviewer")
	}

	// Reordering work that has not finished is still a rewrite.
	reorderedSpine := json.RawMessage(`{"todos":[
		{"content":"Inspect environment","status":"completed"},
		{"content":"Run the suite","status":"in_progress"},
		{"content":"Implement parser","status":"pending"}
	]}`)
	if changed, _, _, _ := a.recoveryPlanTransition("todo_write", reorderedSpine); !changed {
		t.Fatal("reordering unfinished steps must still reach the plan reviewer")
	}
}

// Task 23 P1-c end to end: three todo_write rejections in a row trip the loop
// guard even though the model reworks the list (and therefore the message) on
// every retry.
func TestStormBreakerCatchesRepeatedTodoWriteRejections(t *testing.T) {
	a := New(nil, tool.NewRegistry(), NewSession(""), Options{}, event.Discard)
	calls := []provider.ToolCall{{Name: "todo_write"}}
	messages := []string{
		`todo 2 "step two" is a second in_progress item; a serial task list allows exactly one current item`,
		`todo 5 "another step" is in_progress after pending work; the current item must be the first unfinished item`,
		`todo 3 "third try" is a level-1 sub-step with no phase above it; add a level-0 phase header`,
	}
	var last intervention
	for _, msg := range messages {
		last = a.applyStormBreaker(calls, []toolOutcome{{errMsg: msg}}, 0)
	}
	if !last.fired() {
		t.Fatalf("three rejections of the same todo_write family must trip the loop guard, got %+v", last)
	}
}
