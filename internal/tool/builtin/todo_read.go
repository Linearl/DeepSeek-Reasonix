package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"reasonix/internal/tool"
)

func init() { tool.RegisterBuiltin(todoRead{}) }

// todoRead exposes the current task list so the model can refresh step_ids and
// statuses without faking a validation error (task 68 P0). It is a pure read of
// the host's todo baseline — no filesystem, no approval.
type todoRead struct{}

func (todoRead) Name() string { return "todo_read" }

func (todoRead) Description() string {
	return "Read the current structured task list (content, status, step_id, level) as JSON. Use this before todo_write ops or when the earlier todo_write call may have been compacted out of context, so you edit the real list instead of guessing."
}

func (todoRead) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}

func (todoRead) ReadOnly() bool { return true }

func (todoRead) Execute(ctx context.Context, _ json.RawMessage) (string, error) {
	items := todoBaseline(ctx)
	if len(items) == 0 {
		return `{"todos":[],"note":"no todo list is active"}`, nil
	}
	out := make([]todoItem, 0, len(items))
	for _, t := range items {
		out = append(out, todoItem{
			Content:    t.Content,
			Status:     t.Status,
			ActiveForm: t.ActiveForm,
			Level:      t.Level,
			StepID:     t.StepID,
			Owner:      t.Owner,
			Running:    t.Running,
		})
	}
	payload, err := json.Marshal(map[string]any{"todos": out})
	if err != nil {
		return "", fmt.Errorf("encode todos: %w", err)
	}
	return string(payload), nil
}
