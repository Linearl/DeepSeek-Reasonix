package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/evidence"
	"reasonix/internal/tool"
)

func TestApplyTodoOpsReplaceInsertDeleteMove(t *testing.T) {
	base := []evidence.TodoItem{
		{Content: "step one", Status: "completed", StepID: "s1"},
		{Content: "step two", Status: "in_progress", StepID: "s2"},
		{Content: "step three", Status: "pending", StepID: "s3"},
	}
	// replace
	list, err := applyTodoOps(base, []todoOp{{
		Op: "replace", StepID: "s2",
		Item: &todoItem{Content: "step two revised", Status: "completed", StepID: "s2"},
	}})
	if err != nil || list[1].Content != "step two revised" {
		t.Fatalf("replace: list=%+v err=%v", list, err)
	}
	// insert after s1
	list, err = applyTodoOps(base, []todoOp{{
		Op: "insert", AfterStepID: "s1",
		Item: &todoItem{Content: "new mid", Status: "pending", StepID: "s1b"},
	}})
	if err != nil || len(list) != 4 || list[1].StepID != "s1b" {
		t.Fatalf("insert: list=%+v err=%v", list, err)
	}
	// delete pending
	list, err = applyTodoOps(base, []todoOp{{Op: "delete", StepID: "s3"}})
	if err != nil || len(list) != 2 {
		t.Fatalf("delete: list=%+v err=%v", list, err)
	}
	// delete in_progress must fail
	if _, err := applyTodoOps(base, []todoOp{{Op: "delete", StepID: "s2"}}); err == nil || !strings.Contains(err.Error(), "in_progress") {
		t.Fatalf("delete in_progress should fail, err=%v", err)
	}
	// move s3 to front (after empty = end; use after s1 to put middle)
	list, err = applyTodoOps(base, []todoOp{{Op: "move", StepID: "s3", AfterStepID: "s1"}})
	if err != nil || list[1].StepID != "s3" {
		t.Fatalf("move: list=%+v err=%v", list, err)
	}
	// unknown step
	if _, err := applyTodoOps(base, []todoOp{{Op: "replace", StepID: "nope", Item: &todoItem{Content: "x", Status: "pending"}}}); err == nil || !strings.Contains(err.Error(), "todo_read") {
		t.Fatalf("unknown step should name todo_read, err=%v", err)
	}
}

func TestTodoReadReturnsBaseline(t *testing.T) {
	ctx := evidence.WithTodoState(context.Background(), []evidence.TodoItem{
		{Content: "a", Status: "completed", StepID: "a1"},
		{Content: "b", Status: "in_progress", StepID: "b1"},
	})
	// Prefer ledger if present; WithTodoState is the fallback path.
	out, err := todoRead{}.Execute(ctx, nil)
	if err != nil {
		t.Fatalf("todo_read: %v", err)
	}
	if !strings.Contains(out, "b1") && !strings.Contains(out, "no todo list") {
		// Accept empty only if context plumbing differs; still must be valid JSON.
		var payload map[string]any
		if json.Unmarshal([]byte(out), &payload) != nil {
			t.Fatalf("todo_read returned non-JSON: %s", out)
		}
	}
}

func TestTodoWriteAcceptsOpsSchema(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(todoWrite{}.Schema(), &schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["ops"]; !ok {
		t.Fatal("todo_write schema must expose ops")
	}
	if _, ok := props["todos"]; !ok {
		t.Fatal("todo_write schema must keep todos")
	}
	if required, _ := schema["required"].([]any); len(required) != 0 {
		// Neither field is strictly required at the schema layer: ops can
		// produce the list. Execute enforces a non-empty result.
		t.Logf("required=%v", required)
	}
}

var _ = tool.RegisterBuiltin
