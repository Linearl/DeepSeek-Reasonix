package evidence

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Incremental todo ops (task 68, moved to evidence in task 976). The host
// applies ops to the current baseline in memory, then runs the same
// serial/identity validators on the resulting full list — never on the model's
// remembered copy alone. The evidence package owns the application so a
// todo_write receipt can expand its ops payload into the resulting full list:
// receipt recording (Ledger.Record) and transcript rebuild both replay ops
// against the reconstructed baseline instead of recording an empty list.

// TodoOpItem is an ops payload item (replace/insert). It mirrors todo_write's
// ops item JSON but carries Level as a POINTER so "omitted" is distinguishable
// from an explicit 0: task 1014 fix a — an omitted field inherits the baseline
// entry's value on replace instead of resetting it. A nil Level means "keep
// whatever the baseline had" (or, on insert, default to 0, a root/flat item).
type TodoOpItem struct {
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

// toTodoItem converts an ops payload item into a baseline item, resolving an
// omitted level to the fallback (the replace target's level, or 0 for insert).
func (i *TodoOpItem) toTodoItem(levelFallback int) TodoItem {
	level := levelFallback
	if i.Level != nil {
		level = *i.Level
	}
	return TodoItem{
		Content:    i.Content,
		Status:     i.Status,
		ActiveForm: i.ActiveForm,
		Level:      level,
		StepID:     i.StepID,
		Owner:      i.Owner,
		Running:    i.Running,
		ParentID:   i.ParentID,
		PlanID:     i.PlanID,
	}
}

// TodoOp is one incremental edit against the host's current todo baseline.
// The JSON shape mirrors the todo_write tool's ops items.
type TodoOp struct {
	Op          string       `json:"op"` // replace | insert | delete | move
	StepID      string       `json:"step_id,omitempty"`
	AfterStepID string       `json:"after_step_id,omitempty"`
	Item        *TodoOpItem  `json:"item,omitempty"`
}

// ApplyTodoOps mutates a copy of baseline according to ops and returns the
// resulting full list. Errors name the op and how to fix them.
func ApplyTodoOps(baseline []TodoItem, ops []TodoOp) ([]TodoItem, error) {
	list := make([]TodoItem, 0, len(baseline))
	list = append(list, baseline...)
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
			// Task 1014, fix a: replace is a PARTIAL update. The payload is
			// resolved against the baseline entry BEFORE it overwrites: fields
			// the item omits inherit the baseline value instead of resetting to
			// zero — most damagingly level, where a level-1 sub-step silently
			// became a level-0 root, its level-1 siblings then read as ITS
			// sub-steps, and the write died on "phase completed but sub-step
			// unfinished". An explicitly sent field (including level 0) wins.
			next := op.Item.toTodoItem(list[idx].Level)
			if op.Item.ActiveForm == "" {
				next.ActiveForm = list[idx].ActiveForm
			}
			if op.Item.Owner == "" {
				next.Owner = list[idx].Owner
			}
			if !op.Item.Running {
				next.Running = list[idx].Running
			}
			if op.Item.ParentID == "" {
				next.ParentID = list[idx].ParentID
			}
			if op.Item.PlanID == "" {
				next.PlanID = list[idx].PlanID
			}
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
			// Task 1014: a new item defaults to level 0 (a root/flat item) when
			// the payload omits level; an explicit level wins.
			next := op.Item.toTodoItem(0)
			// Task 972, fix 2: an insert without step_id is no longer an error —
			// the host assigns a stable id after the ops are applied, so the
			// model is not required to mint ids itself.
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
			list = append(list[:pos], append([]TodoItem{next}, list[pos:]...)...)
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
			list = append(list[:pos], append([]TodoItem{item}, list[pos:]...)...)
		default:
			return nil, fmt.Errorf("ops[%d]: unknown op %q (want replace|insert|delete|move)", i, op.Op)
		}
	}
	return list, nil
}

func todoOpKnownIDs(list []TodoItem) string {
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

// ExpandTodoOpsReceipt fills a todo_write receipt's Todos from its ops payload
// by replaying the ops against the current baseline (task 976). Receipts used
// to record only the todos field, so an ops-based write — the tool's preferred
// small-edit path — landed as an empty list: the ledger baseline, the canonical
// panel state, and todo_read all read empty, and the write gate then blocked
// follow-up tools while the model believed its write had succeeded. A receipt
// that carried a full todos list, carried no ops, or whose ops no longer
// re-apply cleanly is returned unchanged: the tool validated the call against
// this same baseline, so a mismatch means state moved underneath and the honest
// answer is "no recorded list", never an invented one.
func ExpandTodoOpsReceipt(r Receipt, baseline []TodoItem) Receipt {
	if r.ToolName != "todo_write" || len(r.Todos) > 0 || r.Args == nil {
		return r
	}
	var payload struct {
		Ops    []TodoOp `json:"ops"`
		PlanID string   `json:"plan_id"`
	}
	if json.Unmarshal(r.Args, &payload) != nil || len(payload.Ops) == 0 {
		return r
	}
	applied, err := ApplyTodoOps(baseline, payload.Ops)
	if err != nil {
		return r
	}
	out := normalizeTodos(applied)
	if pid := strings.TrimSpace(payload.PlanID); pid != "" {
		for i := range out {
			out[i].PlanID = pid
		}
	}
	// Task 972, fix 2: host-assigned stable ids, same as the full-todos path,
	// so an ops-created item is completable by identity later.
	AssignStableTodoStepIDs(out)
	r.Todos = out
	return r
}
