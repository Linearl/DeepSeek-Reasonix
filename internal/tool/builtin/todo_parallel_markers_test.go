package builtin

import (
	"strings"
	"testing"

	"reasonix/internal/evidence"
)

// Task 68 option B: owner/running are coordination metadata that travel with an
// item, and they never relax the serial state machine.
func TestTodoOpsCarryOwnerAndRunningMarkers(t *testing.T) {
	baseline := []evidence.TodoItem{
		{Content: "phase", Status: "in_progress", StepID: "p1", Owner: "main", Level: 0},
		{Content: "sub", Status: "pending", StepID: "s1", Owner: "subagent:api", Level: 1},
	}
	ops := []todoOp{{
		Op:     "replace",
		StepID: "s1",
		Item:   &todoItem{Content: "sub", Status: "pending", StepID: "s1", Owner: "subagent:api", Running: true},
	}}
	list, err := applyTodoOps(baseline, ops)
	if err != nil {
		t.Fatalf("applyTodoOps: %v", err)
	}
	if len(list) != 2 || !list[1].Running || list[1].Owner != "subagent:api" {
		t.Fatalf("markers lost: %+v", list)
	}
	round := toEvidenceTodo(list[1])
	if !round.Running || round.Owner != "subagent:api" {
		t.Fatalf("toEvidenceTodo dropped markers: %+v", round)
	}
}

// The markers are metadata only: the serial rules still reject exactly the lists
// they rejected before, owners or not.
func TestParallelMarkersDoNotRelaxSerialValidation(t *testing.T) {
	list := []evidence.TodoItem{
		{Content: "a", Status: "in_progress", StepID: "a", Owner: "main"},
		{Content: "b", Status: "in_progress", StepID: "b", Owner: "subagent:api", Running: true},
	}
	if err := evidence.ValidateSerialTodos(list); err == nil {
		t.Fatal("two in_progress items must still be rejected even with distinct owners")
	}
}

// The model can only use the markers if the tool schema documents them.
func TestTodoWriteSchemaDocumentsParallelMarkers(t *testing.T) {
	schema := string(todoWrite{}.Schema())
	for _, want := range []string{`"owner"`, `"running"`} {
		if !strings.Contains(schema, want) {
			t.Fatalf("todo_write schema is missing %s: %s", want, schema)
		}
	}
}
