package session

import (
	"context"
	"encoding/json"
	"testing"

	"reasonix/internal/event"
)

// Task 893-A contract: the projection todo list is session-scoped and outlives
// a turn, mirroring the canonical host list (internal/agent/todo_state.go).
// The negative list stays pinned elsewhere: the goal stall clear
// (agent.ReplaceTodoState(nil)) is covered by
// TestTodoStallClearsCanonicalListInOrdinaryChat, and plan-seed replacement by
// TestSeedTodoStateReplacesExisting — this file only pins the projection side.
func TestProjectionTodosSurviveTurnBoundaries(t *testing.T) {
	dir := t.TempDir() + "/s"
	s, err := Open(dir, "s")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	todos, _ := json.Marshal(map[string]any{"todos": []event.Todo{
		{Content: "A", Status: "completed"},
		{Content: "B", Status: "in_progress"},
	}})
	if _, err := s.Append(t.Context(), Batch{OperationID: "turn-1", TurnID: "turn-1", Events: []Event{
		{Kind: "turn/start"},
		{Kind: "todo/write", Payload: todos},
	}}); err != nil {
		t.Fatal(err)
	}
	// The next turn starts without any new todo_write: the visible list must
	// survive, only the per-turn written marker resets.
	if _, err := s.Append(t.Context(), Batch{OperationID: "turn-2", TurnID: "turn-2", Events: []Event{
		{Kind: "turn/start"},
	}}); err != nil {
		t.Fatal(err)
	}
	assertSurvived := func(p Projection, stage string) {
		if len(p.Todos) != 2 || p.Todos[0].Content != "A" || p.Todos[1].Status != "in_progress" {
			t.Fatalf("%s: cross-turn todos = %+v, want the turn-1 list kept", stage, p.Todos)
		}
		if p.TodoWritten {
			t.Fatalf("%s: TodoWritten = true after turn start, want the per-turn marker reset", stage)
		}
	}
	assertSurvived(s.Snapshot().Projection, "live projection")
	if _, err := s.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	commits, err := Replay(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := Project(commits)
	if err != nil {
		t.Fatal(err)
	}
	assertSurvived(replayed, "replayed projection")
}

// Contract: an explicit empty todo/write stays a legal wholesale clear — a
// real reclaim must still empty the visible list.
func TestProjectionTodosExplicitClearStillEmpties(t *testing.T) {
	dir := t.TempDir() + "/s"
	s, err := Open(dir, "s")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	todos, _ := json.Marshal(map[string]any{"todos": []event.Todo{{Content: "A", Status: "completed"}}})
	clear, _ := json.Marshal(map[string]any{"todos": []event.Todo{}})
	if _, err := s.Append(t.Context(), Batch{OperationID: "write", TurnID: "turn-1", Events: []Event{
		{Kind: "turn/start"},
		{Kind: "todo/write", Payload: todos},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(t.Context(), Batch{OperationID: "clear", TurnID: "turn-2", Events: []Event{
		{Kind: "turn/start"},
		{Kind: "todo/write", Payload: clear},
	}}); err != nil {
		t.Fatal(err)
	}
	projection := s.Snapshot().Projection
	if len(projection.Todos) != 0 || !projection.TodoWritten {
		t.Fatalf("explicit clear = %+v, want an empty list with TodoWritten set", projection.Todos)
	}
}
