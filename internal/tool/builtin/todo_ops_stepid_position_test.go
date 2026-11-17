package builtin

import (
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/evidence"
)

// tl1668: the todo_write ops element had two places to put a step_id and two
// historical records disagreed about which one was right, so a caller could
// only learn the shape by trial and error — each miss cost a whole turn.
//
// The canonical position is the ops ELEMENT'S TOP LEVEL, next to "op" and
// "item" (the form measured working on 2026-11-13). Inside "item" the step_id
// means something else: the payload item's own identity. This file locks:
//
//   - the canonical top-level form works for replace/delete/move;
//   - the older "step_id inside item" form ALSO works (compatibility), instead
//     of dying on a bare `step_id ""`;
//   - a missing step_id produces an error that names the position and carries a
//     correct, supported example;
//   - insert keeps its own semantics: item.step_id mints the NEW item and is
//     never treated as an op target;
//   - when both positions are present, the top level wins and an identity
//     change is still rejected.

// The canonical form: step_id at the ops element's top level.
func TestTodoOpsStepIDCanonicalTopLevel(t *testing.T) {
	base := []evidence.TodoItem{
		{Content: "step one", Status: "completed", StepID: "s1"},
		{Content: "step two", Status: "in_progress", StepID: "s2"},
		{Content: "step three", Status: "pending", StepID: "s3"},
	}
	list, err := applyTodoOps(base, []todoOp{{
		Op: "replace", StepID: "s2",
		Item: &todoOpItem{Content: "step two revised", Status: "completed", StepID: "s2"},
	}})
	if err != nil {
		t.Fatalf("canonical replace (top-level step_id) must succeed: %v", err)
	}
	if list[1].Content != "step two revised" {
		t.Fatalf("canonical replace did not apply: %+v", list)
	}
	list, err = applyTodoOps(base, []todoOp{{Op: "delete", StepID: "s3"}})
	if err != nil || len(list) != 2 {
		t.Fatalf("canonical delete (top-level step_id) must succeed: list=%+v err=%v", list, err)
	}
	list, err = applyTodoOps(base, []todoOp{{Op: "move", StepID: "s3", AfterStepID: "s1"}})
	if err != nil || list[1].StepID != "s3" {
		t.Fatalf("canonical move (top-level step_id) must succeed: list=%+v err=%v", list, err)
	}
}

// The compatibility form: the same edit with step_id inside item. Before
// tl1668 this failed with `ops[0] replace: step_id "" is not in the current
// list` — the error never mentioned that item.step_id would have answered it.
func TestTodoOpsStepIDInsideItemAlsoWorks(t *testing.T) {
	base := []evidence.TodoItem{
		{Content: "step one", Status: "completed", StepID: "s1"},
		{Content: "step two", Status: "in_progress", StepID: "s2"},
		{Content: "step three", Status: "pending", StepID: "s3"},
	}
	list, err := applyTodoOps(base, []todoOp{{
		Op: "replace",
		Item: &todoOpItem{Content: "step two revised", Status: "completed", StepID: "s2"},
	}})
	if err != nil {
		t.Fatalf("replace with step_id inside item must succeed: %v", err)
	}
	if list[1].Content != "step two revised" {
		t.Fatalf("replace with step_id inside item did not apply: %+v", list)
	}
	list, err = applyTodoOps(base, []todoOp{{
		Op: "delete",
		Item: &todoOpItem{StepID: "s3"},
	}})
	if err != nil || len(list) != 2 {
		t.Fatalf("delete with step_id inside item must succeed: list=%+v err=%v", list, err)
	}
	list, err = applyTodoOps(base, []todoOp{{
		Op: "move",
		Item: &todoOpItem{StepID: "s3"}, AfterStepID: "s1",
	}})
	if err != nil || list[1].StepID != "s3" {
		t.Fatalf("move with step_id inside item must succeed: list=%+v err=%v", list, err)
	}
}

// A missing step_id must produce an error that says WHERE the id goes and
// shows a shape that actually works — the fork rule is that an error message
// may only point at a next step that exists.
func TestTodoOpsStepIDErrorCarriesCorrectExample(t *testing.T) {
	base := []evidence.TodoItem{{Content: "step one", Status: "completed", StepID: "s1"}}
	for _, op := range []todoOp{
		{Op: "replace", Item: &todoOpItem{Content: "x", Status: "pending"}},
		{Op: "delete"},
		{Op: "move"},
	} {
		_, err := applyTodoOps(base, []todoOp{op})
		if err == nil {
			t.Fatalf("%s without a step_id must fail", op.Op)
		}
		msg := err.Error()
		if !strings.Contains(msg, "top level") {
			t.Fatalf("%s error must name the step_id position, got: %s", op.Op, msg)
		}
		if !strings.Contains(msg, `"op":"replace"`) || !strings.Contains(msg, `"step_id":"plan_step_02"`) {
			t.Fatalf("%s error must carry a correct example, got: %s", op.Op, msg)
		}
		// The example must be a shape the host really accepts.
		if !strings.Contains(msg, `"item"`) {
			t.Fatalf("%s error example must show the item payload, got: %s", op.Op, msg)
		}
	}
}

// insert keeps its own semantics: item.step_id is the NEW item's identity and
// is never read as the op's target (the op has none).
func TestTodoOpsInsertItemStepIDIsNewIdentityNotTarget(t *testing.T) {
	base := []evidence.TodoItem{{Content: "step one", Status: "completed", StepID: "s1"}}
	list, err := applyTodoOps(base, []todoOp{{
		Op: "insert",
		Item: &todoOpItem{Content: "new item", Status: "pending", StepID: "s2"},
	}})
	if err != nil {
		t.Fatalf("insert with an item step_id must succeed: %v", err)
	}
	if len(list) != 2 || list[1].StepID != "s2" || list[1].Content != "new item" {
		t.Fatalf("insert did not mint the new item: %+v", list)
	}
	// A duplicate id is still rejected — the insert path never silently
	// re-points at the existing entry.
	if _, err := applyTodoOps(base, []todoOp{{
		Op: "insert",
		Item: &todoOpItem{Content: "clash", Status: "pending", StepID: "s1"},
	}}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("insert with a duplicate step_id must be rejected, err=%v", err)
	}
}

// When both positions are present, the top level wins — and a replace that
// tries to change the item's identity is still rejected.
func TestTodoOpsReplaceTopLevelStepIDWinsOverItem(t *testing.T) {
	base := []evidence.TodoItem{
		{Content: "step one", Status: "completed", StepID: "s1"},
		{Content: "step two", Status: "in_progress", StepID: "s2"},
	}
	// Same id in both places: accepted, the top level names the target.
	list, err := applyTodoOps(base, []todoOp{{
		Op: "replace", StepID: "s2",
		Item: &todoOpItem{Content: "step two revised", Status: "completed", StepID: "s2"},
	}})
	if err != nil {
		t.Fatalf("replace with matching ids must succeed: %v", err)
	}
	if list[1].Content != "step two revised" {
		t.Fatalf("replace did not apply: %+v", list)
	}
	// Different ids: the top level names the target (s2) and the payload asks
	// to become s1 — an identity change, which stays rejected.
	_, err = applyTodoOps(base, []todoOp{{
		Op: "replace", StepID: "s2",
		Item: &todoOpItem{Content: "step two revised", Status: "completed", StepID: "s1"},
	}})
	if err == nil || !strings.Contains(err.Error(), "step_id must stay") {
		t.Fatalf("an identity change must stay rejected, err=%v", err)
	}
}

// End-to-end at the tool boundary: the canonical form succeeds end to end, and
// the compatibility form does too.
func TestTodoWriteOpsStepIDBothPositionsAccepted(t *testing.T) {
	for _, tc := range []struct {
		name string
		args string
	}{
		{
			name: "canonical top-level",
			args: `{"ops":[{"op":"replace","step_id":"p_a_1","item":{"content":"sub one revised","status":"in_progress"}}]}`,
		},
		{
			name: "compat inside item",
			args: `{"ops":[{"op":"replace","item":{"content":"sub one revised","status":"in_progress","step_id":"p_a_1"}}]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := ledgerWithBaseline(t, planList())
			out, err := (todoWrite{}).Execute(ctx, json.RawMessage(tc.args))
			if err != nil {
				t.Fatalf("%s must succeed: %v", tc.name, err)
			}
			if !strings.Contains(out, "3 total") {
				t.Fatalf("%s ack = %q, want the 3-item list", tc.name, out)
			}
		})
	}
}

// The schema must say where the step_id goes — the schema was the surface
// that left both positions undocumented, which is where the ambiguity started.
func TestTodoWriteOpsSchemaDocumentsStepIDPosition(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(todoWrite{}.Schema(), &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	props, _ := schema["properties"].(map[string]any)
	ops, _ := props["ops"].(map[string]any)
	opsDesc, _ := ops["description"].(string)
	if !strings.Contains(strings.ToLower(opsDesc), "top level") {
		t.Fatalf("ops description must state the step_id position, got: %q", opsDesc)
	}
	items, _ := ops["items"].(map[string]any)
	itemProps, _ := items["properties"].(map[string]any)
	top, _ := itemProps["step_id"].(map[string]any)
	topDesc, _ := top["description"].(string)
	lowTop := strings.ToLower(topDesc)
	if !strings.Contains(lowTop, "top level") || !strings.Contains(lowTop, "not inside") {
		t.Fatalf("top-level step_id description must state the position, got: %q", topDesc)
	}
	payload, _ := itemProps["item"].(map[string]any)
	payloadDesc, _ := payload["description"].(string)
	if !strings.Contains(strings.ToLower(payloadDesc), "not the op's target") {
		t.Fatalf("item description must distinguish the payload id from the target, got: %q", payloadDesc)
	}
	payloadProps, _ := payload["properties"].(map[string]any)
	inner, _ := payloadProps["step_id"].(map[string]any)
	innerDesc, _ := inner["description"].(string)
	if !strings.Contains(strings.ToLower(innerDesc), "not the op's target") {
		t.Fatalf("item.step_id description must distinguish it from the target, got: %q", innerDesc)
	}
}
