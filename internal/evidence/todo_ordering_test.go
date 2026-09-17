package evidence

import "testing"

// Task 23 P1-a: parallel sub-agents finish out of order, so a serial list has
// to accept a completed item that sits after unfinished work. Only the single
// in_progress slot stays serial.

func TestValidateSerialTodosAcceptsOutOfOrderCompletion(t *testing.T) {
	cases := []struct {
		name  string
		todos []TodoItem
	}{
		{
			name: "completed item between the current one and pending work",
			todos: []TodoItem{
				{Content: "Inspect environment", Status: "completed"},
				{Content: "Implement parser", Status: "in_progress"},
				{Content: "Write parser tests", Status: "completed"},
				{Content: "Run the suite", Status: "pending"},
			},
		},
		{
			name: "completed item after pending work",
			todos: []TodoItem{
				{Content: "Implement parser", Status: "in_progress"},
				{Content: "Run the suite", Status: "pending"},
				{Content: "Inspect environment", Status: "completed"},
			},
		},
	}
	for _, tc := range cases {
		if err := ValidateSerialTodos(tc.todos); err != nil {
			t.Fatalf("%s: out-of-order completion must be accepted (#23 P1-a): %v", tc.name, err)
		}
	}
}

func TestValidateSerialTodosStillRejectsALostSerialSpine(t *testing.T) {
	// Only the completion order was relaxed. Two current items, or a current
	// item that follows work which has not started, stay rejected.
	twoCurrent := []TodoItem{
		{Content: "one", Status: "in_progress"},
		{Content: "two", Status: "in_progress"},
	}
	if err := ValidateSerialTodos(twoCurrent); err == nil {
		t.Fatal("two in_progress items must stay rejected")
	}
	currentAfterPending := []TodoItem{
		{Content: "one", Status: "pending"},
		{Content: "two", Status: "completed"},
		{Content: "three", Status: "in_progress"},
	}
	if err := ValidateSerialTodos(currentAfterPending); err == nil {
		t.Fatal("an in_progress item after pending work must stay rejected")
	}
}

// Task 23 P1-b: the replacement list is free to move finished steps around and
// to insert new steps above them. What must still be caught is a completed step
// that disappears or regresses to an unfinished status.

func TestPreservesCompletedTodoPositionsIgnoresReordering(t *testing.T) {
	previous := []TodoItem{
		{Content: "Inspect environment", Status: "completed"},
		{Content: "Implement parser", Status: "in_progress"},
		{Content: "Run the suite", Status: "pending"},
	}
	reordered := []TodoItem{
		{Content: "Implement parser", Status: "in_progress"},
		{Content: "Run the suite", Status: "pending"},
		{Content: "Inspect environment", Status: "completed"},
	}
	if !PreservesCompletedTodoPositions(previous, reordered) {
		t.Fatal("moving a completed step is not a violation (#23 P1-b)")
	}
	insertedAbove := []TodoItem{
		{Content: "Read the failing test", Status: "pending"},
		{Content: "Inspect environment", Status: "completed"},
		{Content: "Implement parser", Status: "in_progress"},
		{Content: "Run the suite", Status: "pending"},
	}
	if !PreservesCompletedTodoPositions(previous, insertedAbove) {
		t.Fatal("inserting a new step above a completed one is legitimate")
	}
}

func TestPreservesCompletedTodoPositionsStillCatchesLoss(t *testing.T) {
	previous := []TodoItem{
		{Content: "Inspect environment", Status: "completed"},
		{Content: "Implement parser", Status: "in_progress"},
	}
	dropped := []TodoItem{
		{Content: "Implement parser", Status: "in_progress"},
	}
	if PreservesCompletedTodoPositions(previous, dropped) {
		t.Fatal("a completed step that disappears must be rejected")
	}
	regressed := []TodoItem{
		{Content: "Inspect environment", Status: "pending"},
		{Content: "Implement parser", Status: "in_progress"},
	}
	if PreservesCompletedTodoPositions(previous, regressed) {
		t.Fatal("a completed step that regresses to pending must be rejected")
	}
}
