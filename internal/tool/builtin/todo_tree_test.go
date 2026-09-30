package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/evidence"
)

// Task 152: the #9998/#10023 deadlock family, tree edition. A tree-shaped
// todo_write that breaks the subtree-convergence rule must be rejected with an
// actionable fix plus the host's current plan, and the corrected rewrite must
// be accepted — no three-consecutive-rejection loop.

func treeContext(t *testing.T, baseline []evidence.TodoItem) context.Context {
	t.Helper()
	ctx := evidence.WithLedger(context.Background(), evidence.NewLedger())
	if len(baseline) > 0 {
		ctx = evidence.WithTodoState(ctx, baseline)
	}
	return ctx
}

func mustWriteTree(t *testing.T, ctx context.Context, args string) (string, error) {
	t.Helper()
	return todoWrite{}.Execute(ctx, json.RawMessage(args))
}

// TestTodoWriteTreeSubtreeConvergenceRepairableRejection replays the #9998
// pattern against a tree: rejected write → the error must carry (a) the exact
// offending subtask with its hierarchical code and (b) the current plan, so the
// next write repairs instead of retrying the same list.
func TestTodoWriteTreeSubtreeConvergenceRepairableRejection(t *testing.T) {
	baseline := []evidence.TodoItem{
		{Content: "阶段一：解析器", Status: "in_progress", StepID: "stage1"},
		{Content: "实现解析器", Status: "pending", StepID: "impl", ParentID: "stage1"},
		{Content: "解析器测试", Status: "pending", StepID: "tests", ParentID: "stage1"},
	}
	ctx := treeContext(t, baseline)

	// Attempt 1: the model signs the parent off while its subtask is open.
	attempt1 := `{"todos":[
		{"content":"阶段一：解析器","status":"completed","step_id":"stage1"},
		{"content":"实现解析器","status":"pending","step_id":"impl","parent_id":"stage1"},
		{"content":"解析器测试","status":"pending","step_id":"tests","parent_id":"stage1"}
	]}`
	_, err := mustWriteTree(t, ctx, attempt1)
	if err == nil {
		t.Fatal("parent completed over an open subtask must be rejected")
	}
	// The walk names the FIRST unfinished descendant in list order (T1.1) and
	// carries the current plan so the model can repair (#9998).
	for _, want := range []string{"T1.1", "实现解析器", "Current plan"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("rejection must carry %q so the model can repair (#9998), got: %v", want, err)
		}
	}

	// Attempt 2: the model repeats the same shape — still rejected, still with
	// the plan attached (no degradation into a bare rule).
	_, err = mustWriteTree(t, ctx, attempt1)
	if err == nil || !strings.Contains(err.Error(), "Current plan") {
		t.Fatalf("a repeated bad write must stay repairable, got: %v", err)
	}

	// Attempt 3: following the error's instruction — subtasks converge (one
	// done, one given up), then the parent signs off. Accepted: the loop ends.
	repaired := `{"todos":[
		{"content":"阶段一：解析器","status":"completed","step_id":"stage1"},
		{"content":"实现解析器","status":"completed","step_id":"impl","parent_id":"stage1"},
		{"content":"解析器测试","status":"abandoned","step_id":"tests","parent_id":"stage1"},
		{"content":"阶段二：面板","status":"in_progress","step_id":"stage2"}
	]}`
	out, err := mustWriteTree(t, ctx, repaired)
	if err != nil {
		t.Fatalf("the repaired tree write must be accepted (#9998 no deadlock): %v", err)
	}
	if !strings.Contains(out, "abandoned") {
		t.Fatalf("the ack must surface the abandoned count, got: %q", out)
	}
}

// TestTodoWriteTreeLifecycleFlows covers the full task-152 lifecycle at the
// tool boundary: create → complete → abandon → archive, plus the guards.
func TestTodoWriteTreeLifecycleFlows(t *testing.T) {
	baseline := []evidence.TodoItem{
		{Content: "战役", Status: "in_progress", StepID: "root"},
		{Content: "任务A", Status: "pending", StepID: "a", ParentID: "root"},
		{Content: "任务B", Status: "pending", StepID: "b", ParentID: "root"},
	}
	ctx := treeContext(t, baseline)

	// 1. create: a fresh tree is accepted.
	create := `{"todos":[
		{"content":"战役","status":"in_progress","step_id":"root"},
		{"content":"任务A","status":"pending","step_id":"a","parent_id":"root"},
		{"content":"任务B","status":"pending","step_id":"b","parent_id":"root"}
	]}`
	if _, err := mustWriteTree(t, ctx, create); err != nil {
		t.Fatalf("tree create must pass: %v", err)
	}

	// 2. complete a leaf: allowed, parent carries the slot.
	step1 := `{"todos":[
		{"content":"战役","status":"pending","step_id":"root"},
		{"content":"任务A","status":"in_progress","step_id":"a","parent_id":"root"},
		{"content":"任务B","status":"pending","step_id":"b","parent_id":"root"}
	]}`
	c1 := treeContext(t, baseline)
	if _, err := mustWriteTree(t, c1, step1); err != nil {
		t.Fatalf("leaf in_progress must pass: %v", err)
	}
	doneA := `{"todos":[
		{"content":"战役","status":"pending","step_id":"root"},
		{"content":"任务A","status":"completed","step_id":"a","parent_id":"root"},
		{"content":"任务B","status":"in_progress","step_id":"b","parent_id":"root"}
	]}`
	c2 := treeContext(t, baseline)
	if _, err := mustWriteTree(t, c2, doneA); err != nil {
		t.Fatalf("leaf completion must pass: %v", err)
	}

	// 3. abandon a leaf while its sibling runs.
	abandonB := `{"todos":[
		{"content":"战役","status":"pending","step_id":"root"},
		{"content":"任务A","status":"completed","step_id":"a","parent_id":"root"},
		{"content":"任务B","status":"abandoned","step_id":"b","parent_id":"root"},
		{"content":"收尾","status":"in_progress","step_id":"c","parent_id":"root"}
	]}`
	c3 := treeContext(t, baseline)
	if _, err := mustWriteTree(t, c3, abandonB); err != nil {
		t.Fatalf("abandoning a leaf must pass: %v", err)
	}

	// 4. archive the finished leaf (completed → archived migration).
	archiveA := `{"todos":[
		{"content":"战役","status":"pending","step_id":"root"},
		{"content":"任务A","status":"archived","step_id":"a","parent_id":"root"},
		{"content":"任务B","status":"abandoned","step_id":"b","parent_id":"root"},
		{"content":"收尾","status":"in_progress","step_id":"c","parent_id":"root"}
	]}`
	c4 := treeContext(t, []evidence.TodoItem{
		{Content: "战役", Status: "in_progress", StepID: "root"},
		{Content: "任务A", Status: "completed", StepID: "a", ParentID: "root"},
		{Content: "任务B", Status: "abandoned", StepID: "b", ParentID: "root"},
		{Content: "收尾", Status: "in_progress", StepID: "c", ParentID: "root"},
	})
	if _, err := mustWriteTree(t, c4, archiveA); err != nil {
		t.Fatalf("completed → archived migration must pass: %v", err)
	}

	// 5. guards: in_progress cannot jump to archived, pending cannot archive
	// straight away, and a completed step cannot regress to abandoned.
	c5 := treeContext(t, []evidence.TodoItem{{Content: "任务A", Status: "in_progress", StepID: "a"}})
	jump := `{"todos":[
		{"content":"任务A","status":"archived","step_id":"a"}
	]}`
	if _, err := mustWriteTree(t, c5, jump); err == nil || !strings.Contains(err.Error(), "cannot jump from in_progress to archived") {
		t.Fatalf("in_progress → archived must be rejected with the fix, got: %v", err)
	}
	c5b := treeContext(t, []evidence.TodoItem{{Content: "任务A", Status: "pending", StepID: "a"}})
	jumpOpen := `{"todos":[
		{"content":"任务A","status":"archived","step_id":"a"}
	]}`
	if _, err := mustWriteTree(t, c5b, jumpOpen); err == nil || !strings.Contains(err.Error(), "cannot be archived straight from") {
		t.Fatalf("pending → archived must be rejected with the fix, got: %v", err)
	}
	c6 := treeContext(t, []evidence.TodoItem{
		{Content: "战役", Status: "in_progress", StepID: "root"},
		{Content: "任务A", Status: "completed", StepID: "a", ParentID: "root"},
		{Content: "任务B", Status: "pending", StepID: "b", ParentID: "root"},
	})
	regress := `{"todos":[
		{"content":"战役","status":"pending","step_id":"root"},
		{"content":"任务A","status":"abandoned","step_id":"a","parent_id":"root"},
		{"content":"任务B","status":"in_progress","step_id":"b","parent_id":"root"}
	]}`
	if _, err := mustWriteTree(t, c6, regress); err == nil || !strings.Contains(err.Error(), "regressed") {
		t.Fatalf("completed → abandoned must stay a regression, got: %v", err)
	}

	// 6. dangling parent_id is rejected with the id named.
	c7 := treeContext(t, nil)
	orphan := `{"todos":[
		{"content":"孤儿","status":"in_progress","step_id":"x","parent_id":"ghost"}
	]}`
	if _, err := mustWriteTree(t, c7, orphan); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("dangling parent_id must be rejected naming the id, got: %v", err)
	}
}

// TestTodoWriteFlatListUnaffectedByTreeLayer pins flat-list behavior at the
// tool boundary: no parent_id anywhere → the legacy segment rules, unchanged.
func TestTodoWriteFlatListUnaffectedByTreeLayer(t *testing.T) {
	ctx := treeContext(t, nil)
	flat := `{"todos":[
		{"content":"phase","status":"pending","level":0,"step_id":"p1"},
		{"content":"sub","status":"in_progress","level":1,"step_id":"s1"}
	]}`
	out, err := mustWriteTree(t, ctx, flat)
	if err != nil {
		t.Fatalf("flat legacy shape must stay accepted: %v", err)
	}
	if !strings.Contains(out, "Todos updated: 2 total") {
		t.Fatalf("unexpected ack: %q", out)
	}
	badFlat := `{"todos":[
		{"content":"phase","status":"completed","level":0,"step_id":"p1"},
		{"content":"sub","status":"pending","level":1,"step_id":"s1"}
	]}`
	if _, err := mustWriteTree(t, ctx, badFlat); err == nil || !strings.Contains(err.Error(), "is completed but sub-step") {
		t.Fatalf("flat completed-phase rejection must be unchanged, got: %v", err)
	}
}
