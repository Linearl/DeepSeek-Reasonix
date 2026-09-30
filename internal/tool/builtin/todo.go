package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/evidence"
	"reasonix/internal/tool"
)

func init() { tool.RegisterBuiltin(todoWrite{}) }

// todoWrite records the agent's running task list. It has no host side effects —
// the full list lives in the call's args (the model re-sends it whole on every
// update), which a frontend renders as a checklist. Execute validates serial
// shape and stable identities, then acks with a count. Progress is not a
// delivery receipt: complete_step remains the optional evidence sign-off.
type todoWrite struct{}

type todoItem struct {
	Content    string `json:"content"`
	Status     string `json:"status"`
	ActiveForm string `json:"activeForm,omitempty"`
	Level      int    `json:"level,omitempty"`
	StepID     string `json:"step_id,omitempty"`
	// Owner and Running are the task-68 option-B parallel markers. They never
	// change a status and no validator reads them.
	Owner   string `json:"owner,omitempty"`
	Running bool   `json:"running,omitempty"`
	// ParentID (task 152) references the parent task's step_id. Empty = root
	// (or a flat/level list item). step_id stays the only identity.
	ParentID string `json:"parent_id,omitempty"`
}

func (todoWrite) Name() string { return "todo_write" }

func (todoWrite) Description() string {
	return "Record and update a structured task list for the current work. Prefer `ops` for small edits (replace/insert/delete/move by step_id) after calling todo_read; send the COMPLETE `todos` list only for a wholesale rewrite. Use it to plan multi-step work and show progress: keep exactly one item in_progress at a time, and flip an item to completed the moment it's done (don't batch completions). Skip it for trivial single-step tasks. The list is two-level: a `level` 0 item is a PHASE (a milestone) and the `level` 1 items after it are its concrete sub-steps; omit `level` (0) for a flat list. Deeper nesting (task 152): give a sub-step `parent_id` = the parent's step_id to build a tree (T1 / T1.1 / T1.1.1 — a parent may be signed off only once every subtask under it is finished, abandoned, or archived). Statuses: pending | in_progress | completed, plus abandoned (you are giving this item up — say so instead of leaving it dangling) and archived (a completed item kept for the record; only completed items may be archived). Each item has `content` (imperative, e.g. \"Add the parser\"), `status`, `activeForm` (present-continuous shown while in progress, e.g. \"Adding the parser\"), optional `level` (0 phase | 1 sub-step), optional `parent_id`, and `step_id` — an item's stable identity. COPY `step_id` VERBATIM for every item that already has one: it is how a completion stays attached to its step when you retitle it, insert a step above it, or reorder the list. Give a new item a fresh unique id (e.g. \"plan_step_07\"); never reuse or renumber an existing one."
}

func (todoWrite) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "todos":{
    "type":"array",
    "description":"The complete task list, in order. Replaces any previous list. Prefer ops for small edits.",
    "items":{
      "type":"object",
      "properties":{
        "content":{"type":"string","description":"Imperative description of the task."},
        "status":{"type":"string","enum":["pending","in_progress","completed","abandoned","archived"],"description":"Task state. Keep at most one in_progress. abandoned = you are giving this item up; archived = completed work kept for the record (only completed items may be archived)."},
        "activeForm":{"type":"string","description":"Present-continuous form shown while the task is in progress (e.g. \"Running tests\")."},
        "level":{"type":"integer","enum":[0,1],"description":"Nesting level: 0 = phase/milestone, 1 = a sub-step of the phase above it. Omit for a flat list."},
        "parent_id":{"type":"string","description":"Task 152 tree: step_id of this item's parent task. The parent must appear earlier in the list. Build T1/T1.1/T1.1.1 hierarchies for stage→substep work; a parent may be completed or archived only after every subtask under it is finished, abandoned, or archived. A child must come after its parent."},
        "step_id":{"type":"string","description":"Stable identity for this item, e.g. \"plan_step_02\". Copy it verbatim from the item's previous entry so completions stay attached across retitles, insertions, and reordering; use a fresh unique id for a genuinely new item. Also the handle other items' parent_id references."},
        "owner":{"type":"string","description":"Optional executor label for this item (\"main\", \"subagent:api\", ...). Coordination metadata only: it never changes status and no validator reads it."},
        "running":{"type":"boolean","description":"Optional marker that a parallel executor is currently on this item. Metadata only."}
      },
      "required":["content","status"]
    }
  },
  "ops":{
    "type":"array",
    "description":"Optional incremental edits applied to the current host list, then validated as a full list. Use after todo_read. Omit when sending todos.",
    "items":{
      "type":"object",
      "properties":{
        "op":{"type":"string","enum":["replace","insert","delete","move"]},
        "step_id":{"type":"string","description":"Target item identity (replace/delete/move; ignored for insert)."},
        "after_step_id":{"type":"string","description":"Insert/move: place after this step_id; empty = end of list. When inserting a child, place it after its parent's subtree so the parent stays earlier in the list."},
        "item":{"type":"object","description":"replace/insert payload (content,status,activeForm,level,step_id,parent_id).","properties":{
          "content":{"type":"string"},"status":{"type":"string","enum":["pending","in_progress","completed","abandoned","archived"]},
          "activeForm":{"type":"string"},"level":{"type":"integer","enum":[0,1]},"step_id":{"type":"string"},
          "parent_id":{"type":"string","description":"Task 152 tree: parent task's step_id; the parent must appear earlier in the list."},
          "owner":{"type":"string"},"running":{"type":"boolean"}
        }}
      },
      "required":["op"]
    }
  }
}
}`)
}

// ReadOnly is true: todo_write only records a list (no filesystem or process
// effect), so it never needs approval and stays available in plan mode — where
// laying out a plan as todos is exactly the point.
func (todoWrite) ReadOnly() bool { return true }

func (todoWrite) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Todos []todoItem `json:"todos"`
		Ops   []todoOp   `json:"ops"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	// Ops path (task 68): apply to the host baseline, then validate the
	// resulting full list with the same five checks as a wholesale rewrite.
	if len(p.Ops) > 0 {
		if p.Todos != nil {
			return "", fmt.Errorf("send either todos (full list) or ops (incremental), not both")
		}
		applied, err := applyTodoOps(todoBaseline(ctx), p.Ops)
		if err != nil {
			return "", err
		}
		p.Todos = applied
	}
	// An explicit empty todos array is still a legal wholesale rewrite (the
	// continuity validators decide whether clearing is allowed). A call with
	// neither field is a protocol error.
	if p.Todos == nil {
		return "", fmt.Errorf("todos is required (or ops that produce a list)")
	}
	var done, active, pending, dropped int
	for i, t := range p.Todos {
		if t.Content == "" {
			return "", fmt.Errorf("todo %d: content is required", i+1)
		}
		if t.Level < 0 || t.Level > 1 {
			return "", fmt.Errorf("todo %d: invalid level %d (want 0 phase | 1 sub-step)", i+1, t.Level)
		}
		switch t.Status {
		case "completed", "archived":
			done++
		case "in_progress":
			active++
		case "abandoned":
			// Task 152: given-up work is terminal but not done — it gets its
			// own ack bucket so the model sees what it dropped.
			dropped++
		case "pending", "":
			pending++
		default:
			return "", fmt.Errorf("todo %d: invalid status %q (want pending|in_progress|completed|abandoned|archived)", i+1, t.Status)
		}
	}
	if err := evidence.ValidateSerialTodos(toEvidenceTodos(p.Todos)); err != nil {
		// Task 23 P0-a: attach the host's current plan so a rejected write can be
		// repaired without guessing (upstream #10023). The rule text alone left
		// the model retrying the same invalid list.
		if snapshot := todoPlanSnapshot(todoBaseline(ctx)); snapshot != "" {
			return "", fmt.Errorf("%w.%s", err, snapshot)
		}
		return "", err
	}
	if err := verifyUniqueStepIDs(p.Todos); err != nil {
		return "", err
	}
	if !tool.HasPlanReplacementAuthorization(ctx) {
		if err := verifyTodoCurrentContinuity(ctx, p.Todos); err != nil {
			return "", err
		}
		if err := verifyStepIDsPreserved(ctx, p.Todos); err != nil {
			return "", err
		}
	}
	if err := verifyCompletedTodoPositions(ctx, p.Todos); err != nil {
		return "", err
	}
	ack := fmt.Sprintf("Todos updated: %d total — %d completed, %d in progress, %d pending.",
		len(p.Todos), done, active, pending)
	if dropped > 0 {
		ack += fmt.Sprintf(" %d abandoned (given up; kept in the list as a record).", dropped)
	}
	return ack, nil
}

// verifyUniqueStepIDs keeps a step id an identity: two items claiming the same
// id would make completion attribution ambiguous again, which is the whole
// problem ids exist to remove.
func verifyUniqueStepIDs(todos []todoItem) error {
	seen := make(map[string]int, len(todos))
	for i, todo := range todos {
		id := strings.TrimSpace(todo.StepID)
		if id == "" {
			continue
		}
		if prev, ok := seen[id]; ok {
			return fmt.Errorf("todo %d %q reuses step_id %q, already claimed by todo %d; give a new item its own id", i+1, todo.Content, id, prev+1)
		}
		seen[id] = i
	}
	return nil
}

// verifyStepIDsPreserved rejects a rewrite that keeps a step but drops the id it
// arrived with. Without this the model can silently return the list to
// title-and-position identity, which is exactly what a replan invalidates.
func verifyStepIDsPreserved(ctx context.Context, todos []todoItem) error {
	previous := todoBaseline(ctx)
	if len(previous) == 0 {
		return nil
	}
	next := toEvidenceTodos(todos)
	for _, todo := range previous {
		if todo.StepID == "" {
			continue
		}
		if _, ok := evidence.MatchStepID(todo.StepID, next); ok {
			continue
		}
		match, found := evidence.MatchTodoIdentity(todo, next)
		if !found || match.StepID != "" {
			continue
		}
		return fmt.Errorf("todo %d %q dropped its step_id %q; re-send it with step_id %q so its completion stays attached across retitles and reordering", match.Index, match.Content, todo.StepID, todo.StepID)
	}
	return nil
}

func verifyTodoCurrentContinuity(ctx context.Context, todos []todoItem) error {
	previous := todoBaseline(ctx)
	if len(previous) == 0 {
		return nil
	}
	next := toEvidenceTodos(todos)
	if len(next) == 0 {
		return fmt.Errorf("current todo cannot be cleared while the plan is active; get host approval to replace the plan")
	}
	inProgress := -1
	for i, todo := range next {
		if strings.TrimSpace(todo.Status) == "in_progress" {
			inProgress = i
			break
		}
	}
	for i, todo := range previous {
		if strings.TrimSpace(todo.Status) != "in_progress" {
			continue
		}
		match, found := evidence.MatchTodoIdentity(todo, next)
		if !found {
			// Fork: the model may rewrite the whole list (renamed or regrouped
			// steps). The plan still needs exactly one in_progress item, which
			// ValidateSerialTodos enforces, so a rewrite is not hard-rejected.
			continue
		}
		if match.Status == "pending" || match.Status == "" {
			// Task 152: in an explicit tree the current slot legitimately
			// DESCENDS — the parent steps aside while one of its own children
			// becomes the current item. Any other demotion stays rejected.
			if inProgress >= 0 && match.Index > 0 && evidence.TodoHasAncestor(next, inProgress, match.Index-1) {
				continue
			}
			return fmt.Errorf("current todo %d %q cannot move back to pending; keep it in_progress, mark it completed, or rewrite the list so another step is in_progress", i+1, todo.Content)
		}
		// Task 152: archived means completed work kept for the record — the
		// current step must actually finish before it can be archived.
		if match.Status == "archived" {
			return fmt.Errorf("current todo %d %q cannot jump from in_progress to archived; finish it (mark it completed) and archive it afterwards, or mark it abandoned if you are giving it up", i+1, todo.Content)
		}
	}
	return nil
}

// todoPlanSnapshot renders the host's current plan so a rejected todo_write can
// be repaired without guessing. Upstream #10023: the three state-machine errors
// used to name the rule but not the list, which deadlocked the model.
func todoPlanSnapshot(previous []evidence.TodoItem) string {
	if len(previous) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(" Current plan: ")
	for i, item := range previous {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%d. [%s] %s", i+1, item.Status, item.Content)
	}
	b.WriteString(".")
	return b.String()
}

func verifyCompletedTodoPositions(ctx context.Context, todos []todoItem) error {
	previous := todoBaseline(ctx)
	if len(previous) == 0 {
		return nil
	}
	snapshot := todoPlanSnapshot(previous)
	// Reordering is allowed, duplication is not: a completed step must stay a
	// single, unambiguous entry so later identity matching cannot pick the
	// wrong one. Task 152: archived items get the same treatment — they are
	// completed work kept for the record, so inventing or duplicating one is
	// rejected exactly like a completed invention.
	seenCompleted := make(map[string]struct{}, len(todos))
	for _, todo := range todos {
		if todo.Status != "completed" && todo.Status != "archived" {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(todo.Content))
		if key == "" {
			continue
		}
		if _, dup := seenCompleted[key]; dup {
			return fmt.Errorf("completed todo %q appears more than once; keep exactly one entry per completed step", todo.Content)
		}
		seenCompleted[key] = struct{}{}
	}
	for _, todo := range todos {
		if todo.Status != "completed" && todo.Status != "archived" {
			continue
		}
		// Fork: completed items may be reordered or moved by later edits (the
		// model often rewrites the whole list); they must still be steps the
		// plan already had. Inventing a brand-new "completed" entry stays
		// rejected, as does letting a completed step regress (checked below).
		if _, found := evidence.MatchTodoIdentity(toEvidenceTodo(todo), previous); !found {
			return fmt.Errorf("completed todo %q is not a step from the current plan; completed items may be reordered, but not invented — keep the real step content or leave it out.%s", todo.Content, snapshot)
		}
	}
	// Task 152 lifecycle rule: only completed work may be archived. Archiving
	// a step the plan still shows as open would launder unfinished work into
	// the record (with no baseline there is no history to check against).
	for _, todo := range todos {
		if todo.Status != "archived" {
			continue
		}
		match, found := evidence.MatchTodoIdentity(toEvidenceTodo(todo), previous)
		if !found {
			continue
		}
		if match.Status == "pending" || match.Status == "" || match.Status == "in_progress" {
			return fmt.Errorf("todo %q cannot be archived straight from %q; finish it (mark it completed) and archive it afterwards, or mark it abandoned if you are giving it up.%s", todo.Content, match.Status, snapshot)
		}
	}
	if len(evidence.IncompleteTodos(previous)) > 0 && !evidence.PreservesCompletedTodoPositions(previous, toEvidenceTodos(todos)) {
		return fmt.Errorf("a completed step disappeared or regressed to unfinished while the plan is active; keep every completed item in the list with status completed (reordering and inserting new steps are allowed).%s", snapshot)
	}
	return nil
}

func todoBaseline(ctx context.Context) []evidence.TodoItem {
	if ledger, ok := evidence.FromContext(ctx); ok {
		if previous, ok := ledger.LatestTodos(); ok && len(previous) > 0 {
			return previous
		}
	}
	previous, _ := evidence.TodoStateFromContext(ctx)
	return previous
}

func toEvidenceTodos(todos []todoItem) []evidence.TodoItem {
	out := make([]evidence.TodoItem, 0, len(todos))
	for _, t := range todos {
		out = append(out, toEvidenceTodo(t))
	}
	return out
}

func toEvidenceTodo(todo todoItem) evidence.TodoItem {
	return evidence.TodoItem{
		Content:    todo.Content,
		Status:     todo.Status,
		ActiveForm: todo.ActiveForm,
		Level:      todo.Level,
		StepID:     strings.TrimSpace(todo.StepID),
		Owner:      strings.TrimSpace(todo.Owner),
		Running:    todo.Running,
		ParentID:   strings.TrimSpace(todo.ParentID),
	}
}
