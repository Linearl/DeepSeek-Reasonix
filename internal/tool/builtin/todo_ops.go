package builtin

import (
	"reasonix/internal/evidence"
)

// Incremental todo ops (task 68). The host applies ops to the current baseline
// in memory, then runs the same serial/identity validators on the resulting
// full list — never on the model's remembered copy alone.
//
// Task 976: the application itself lives in evidence.ApplyTodoOps so receipt
// recording (Ledger.Record) and transcript rebuild replay the same ops against
// the same baseline with byte-identical semantics. This file keeps only the
// tool-facing JSON shape and the conversion into the tool's item type.

type todoOp struct {
	Op          string `json:"op"` // replace | insert | delete | move
	StepID      string `json:"step_id,omitempty"`
	AfterStepID string `json:"after_step_id,omitempty"`
	// Item is the ops payload (task 1014): Level is a *int so the JSON layer
	// distinguishes an omitted level (nil — inherit the baseline entry's value
	// on replace, default to 0 on insert) from an explicit level 0. The rest of
	// the payload shape matches todoItem's ops fields.
	Item *todoOpItem `json:"item,omitempty"`
}

// todoOpItem is the tool-side ops payload item. It mirrors todoItem's fields
// but carries Level as a pointer (task 1014): nil = omitted = inherit on
// replace.
type todoOpItem struct {
	Content    string `json:"content"`
	Status     string `json:"status"`
	ActiveForm string `json:"activeForm,omitempty"`
	Level      *int   `json:"level,omitempty"`
	StepID     string `json:"step_id,omitempty"`
	Owner      string `json:"owner,omitempty"`
	Running    bool   `json:"running,omitempty"`
	ParentID   string `json:"parent_id,omitempty"`
	PlanID     string `json:"plan_id,omitempty"`
}

// applyTodoOps mutates a copy of baseline according to ops and returns the
// resulting full list. Errors name the op and how to fix them.
func applyTodoOps(baseline []evidence.TodoItem, ops []todoOp) ([]todoItem, error) {
	applied, err := evidence.ApplyTodoOps(baseline, toEvidenceOps(ops))
	if err != nil {
		return nil, err
	}
	return toTodoItems(applied), nil
}

func toEvidenceOps(ops []todoOp) []evidence.TodoOp {
	if len(ops) == 0 {
		return nil
	}
	out := make([]evidence.TodoOp, 0, len(ops))
	for _, op := range ops {
		converted := evidence.TodoOp{
			Op:          op.Op,
			StepID:      op.StepID,
			AfterStepID: op.AfterStepID,
		}
		if op.Item != nil {
			converted.Item = &evidence.TodoOpItem{
				Content:    op.Item.Content,
				Status:     op.Item.Status,
				ActiveForm: op.Item.ActiveForm,
				Level:      op.Item.Level, // pointer passes through: nil = omitted
				StepID:     op.Item.StepID,
				Owner:      op.Item.Owner,
				Running:    op.Item.Running,
				ParentID:   op.Item.ParentID,
				PlanID:     op.Item.PlanID,
			}
		}
		out = append(out, converted)
	}
	return out
}

func toTodoItems(todos []evidence.TodoItem) []todoItem {
	out := make([]todoItem, 0, len(todos))
	for _, t := range todos {
		out = append(out, todoItem{
			Content:    t.Content,
			Status:     t.Status,
			ActiveForm: t.ActiveForm,
			Level:      t.Level,
			StepID:     t.StepID,
			Owner:      t.Owner,
			Running:    t.Running,
			ParentID:   t.ParentID,
			PlanID:     t.PlanID,
		})
	}
	return out
}
