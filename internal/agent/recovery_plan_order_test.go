package agent

import (
	"encoding/json"
	"testing"

	"reasonix/internal/evidence"
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
