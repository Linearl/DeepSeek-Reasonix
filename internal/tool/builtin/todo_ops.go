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
	Op          string    `json:"op"` // replace | insert | delete | move
	StepID      string    `json:"step_id,omitempty"`
	AfterStepID string    `json:"after_step_id,omitempty"`
	Item        *todoItem `json:"item,omitempty"`
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
			item := toEvidenceTodo(*op.Item)
			converted.Item = &item
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
