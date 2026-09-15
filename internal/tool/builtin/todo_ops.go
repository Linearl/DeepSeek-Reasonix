package builtin

import (
	"fmt"
	"strings"

	"reasonix/internal/evidence"
)

// Incremental todo ops (task 68). The host applies ops to the current baseline
// in memory, then runs the same serial/identity validators on the resulting
// full list — never on the model's remembered copy alone.

type todoOp struct {
	Op           string    `json:"op"` // replace | insert | delete | move
	StepID       string    `json:"step_id,omitempty"`
	AfterStepID  string    `json:"after_step_id,omitempty"`
	Item         *todoItem `json:"item,omitempty"`
}

// applyTodoOps mutates a copy of baseline according to ops and returns the
// resulting full list. Errors name the op and how to fix it.
func applyTodoOps(baseline []evidence.TodoItem, ops []todoOp) ([]todoItem, error) {
	list := make([]todoItem, 0, len(baseline))
	for _, t := range baseline {
		list = append(list, todoItem{
			Content:    t.Content,
			Status:     t.Status,
			ActiveForm: t.ActiveForm,
			Level:      t.Level,
			StepID:     t.StepID,
			Owner:      t.Owner,
			Running:    t.Running,
		})
	}
	if len(ops) == 0 {
		return list, nil
	}
	indexOf := func(stepID string) int {
		id := strings.TrimSpace(stepID)
		if id == "" {
			return -1
		}
		for i, it := range list {
			if strings.TrimSpace(it.StepID) == id {
				return i
			}
		}
		return -1
	}
	for i, op := range ops {
		switch strings.ToLower(strings.TrimSpace(op.Op)) {
		case "replace":
			idx := indexOf(op.StepID)
			if idx < 0 {
				return nil, fmt.Errorf("ops[%d] replace: step_id %q is not in the current list; call todo_read first%s", i, op.StepID, todoOpKnownIDs(list))
			}
			if op.Item == nil {
				return nil, fmt.Errorf("ops[%d] replace: item is required", i)
			}
			next := *op.Item
			if strings.TrimSpace(next.StepID) == "" {
				next.StepID = list[idx].StepID
			}
			if strings.TrimSpace(next.StepID) != strings.TrimSpace(list[idx].StepID) {
				return nil, fmt.Errorf("ops[%d] replace: step_id must stay %q (got %q); use insert+delete to change identity", i, list[idx].StepID, next.StepID)
			}
			if next.Content == "" {
				return nil, fmt.Errorf("ops[%d] replace: item.content is required", i)
			}
			list[idx] = next
		case "insert":
			if op.Item == nil || strings.TrimSpace(op.Item.Content) == "" {
				return nil, fmt.Errorf("ops[%d] insert: item.content is required", i)
			}
			next := *op.Item
			if strings.TrimSpace(next.StepID) == "" {
				return nil, fmt.Errorf("ops[%d] insert: item.step_id is required for a new step", i)
			}
			if indexOf(next.StepID) >= 0 {
				return nil, fmt.Errorf("ops[%d] insert: step_id %q already exists; give the new item a fresh id", i, next.StepID)
			}
			pos := len(list)
			if strings.TrimSpace(op.AfterStepID) != "" {
				idx := indexOf(op.AfterStepID)
				if idx < 0 {
					return nil, fmt.Errorf("ops[%d] insert: after_step_id %q is not in the current list%s", i, op.AfterStepID, todoOpKnownIDs(list))
				}
				pos = idx + 1
			}
			list = append(list[:pos], append([]todoItem{next}, list[pos:]...)...)
		case "delete":
			idx := indexOf(op.StepID)
			if idx < 0 {
				return nil, fmt.Errorf("ops[%d] delete: step_id %q is not in the current list%s", i, op.StepID, todoOpKnownIDs(list))
			}
			if list[idx].Status == "in_progress" {
				return nil, fmt.Errorf("ops[%d] delete: step %q is in_progress; mark another step in_progress (or complete this one) before deleting it", i, op.StepID)
			}
			list = append(list[:idx], list[idx+1:]...)
		case "move":
			idx := indexOf(op.StepID)
			if idx < 0 {
				return nil, fmt.Errorf("ops[%d] move: step_id %q is not in the current list%s", i, op.StepID, todoOpKnownIDs(list))
			}
			item := list[idx]
			list = append(list[:idx], list[idx+1:]...)
			pos := len(list)
			if strings.TrimSpace(op.AfterStepID) != "" {
				after := indexOf(op.AfterStepID)
				if after < 0 {
					return nil, fmt.Errorf("ops[%d] move: after_step_id %q is not in the current list%s", i, op.AfterStepID, todoOpKnownIDs(list))
				}
				pos = after + 1
			}
			list = append(list[:pos], append([]todoItem{item}, list[pos:]...)...)
		default:
			return nil, fmt.Errorf("ops[%d]: unknown op %q (want replace|insert|delete|move)", i, op.Op)
		}
	}
	return list, nil
}

func todoOpKnownIDs(list []todoItem) string {
	ids := make([]string, 0, len(list))
	for _, it := range list {
		if id := strings.TrimSpace(it.StepID); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return ""
	}
	return "; current step_ids: " + strings.Join(ids, ", ")
}
