package evidence

import (
	"strings"
	"testing"
)

// Task 152: explicit parent_id task trees — hierarchical IDs, the subtree
// convergence rule that replaces the flat "completed prefix" constraint, and
// the terminal lifecycle (abandoned / archived).

func TestHierarchicalTodoIDsFlatAndNested(t *testing.T) {
	cases := []struct {
		name  string
		todos []TodoItem
		want  []string
	}{
		{
			name:  "flat list counts roots",
			todos: []TodoItem{{Content: "a"}, {Content: "b"}, {Content: "c"}},
			want:  []string{"T1", "T2", "T3"},
		},
		{
			name: "level adjacency maps to depth-1 parent-child",
			todos: []TodoItem{
				{Content: "phase 1", Level: 0},
				{Content: "sub 1", Level: 1},
				{Content: "sub 2", Level: 1},
				{Content: "phase 2", Level: 0},
			},
			want: []string{"T1", "T1.1", "T1.2", "T2"},
		},
		{
			name: "explicit parent_id tree recurses past two levels",
			todos: []TodoItem{
				{Content: "stage", StepID: "s1"},
				{Content: "task", StepID: "s2", ParentID: "s1"},
				{Content: "subtask", StepID: "s3", ParentID: "s2"},
				{Content: "second stage", StepID: "s4"},
				{Content: "second task", StepID: "s5", ParentID: "s4"},
			},
			want: []string{"T1", "T1.1", "T1.1.1", "T2", "T2.1"},
		},
		{
			name: "sibling position follows same-parent runs",
			todos: []TodoItem{
				{Content: "root", StepID: "r"},
				{Content: "a", StepID: "a", ParentID: "r"},
				{Content: "b", StepID: "b", ParentID: "r"},
				{Content: "c", StepID: "c", ParentID: "r"},
			},
			want: []string{"T1", "T1.1", "T1.2", "T1.3"},
		},
	}
	for _, tc := range cases {
		got := HierarchicalTodoIDs(tc.todos)
		if len(got) != len(tc.want) {
			t.Fatalf("%s: got %v, want %v", tc.name, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("%s: item %d code = %q, want %q", tc.name, i, got[i], tc.want[i])
			}
		}
	}
}

func TestHierarchicalTodoIDsFallsBackOnDanglingParent(t *testing.T) {
	todos := []TodoItem{
		{Content: "root", StepID: "r"},
		{Content: "orphan", StepID: "x", ParentID: "missing"},
	}
	got := HierarchicalTodoIDs(todos)
	if got[0] != "T1" || got[1] != "T2" {
		t.Fatalf("dangling parent must fall back to flat codes, got %v", got)
	}
}

func TestValidateTreeSerialTodosAcceptsConvergedTree(t *testing.T) {
	// T1 in_progress; T1.1 completed; T2 completed with T2.1 abandoned — a
	// converged subtree (terminal by abandonment) signs off legally.
	todos := []TodoItem{
		{Content: "stage", Status: "in_progress", StepID: "s1"},
		{Content: "task done", Status: "completed", StepID: "s2", ParentID: "s1"},
		{Content: "phase", Status: "completed", StepID: "p1"},
		{Content: "given up", Status: "abandoned", StepID: "p2", ParentID: "p1"},
	}
	if err := ValidateSerialTodos(todos); err != nil {
		t.Fatalf("converged tree must be accepted: %v", err)
	}
}

func TestValidateTreeSerialTodosRejectsParentCompletedWithUnfinishedDescendant(t *testing.T) {
	// The converged-prefix walk skips the completed child and names the deep
	// grandchild by its hierarchical code.
	todos := []TodoItem{
		{Content: "stage", Status: "completed", StepID: "s1"},
		{Content: "task", Status: "completed", StepID: "s2", ParentID: "s1"},
		{Content: "subtask", Status: "pending", StepID: "s3", ParentID: "s2"},
	}
	err := ValidateSerialTodos(todos)
	if err == nil {
		t.Fatal("a completed parent with an unfinished descendant must be rejected")
	}
	// #9998 lesson: the rejection names the offending descendant by its
	// hierarchical code and the exact repair — no rule-text-only dead ends.
	for _, want := range []string{"T1", "T1.1.1", "subtask", "abandon"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("rejection must name %q for repair, got: %v", want, err)
		}
	}
	// A directly unfinished child is named just the same.
	shallow := []TodoItem{
		{Content: "stage", Status: "completed", StepID: "s1"},
		{Content: "task", Status: "in_progress", StepID: "s2", ParentID: "s1"},
	}
	err = ValidateSerialTodos(shallow)
	if err == nil || !strings.Contains(err.Error(), `T1.1 "task"`) {
		t.Fatalf("rejection must name the unfinished child T1.1, got: %v", err)
	}
}

func TestValidateTreeSerialTodosRejectsArchivedWithUnfinishedSubtree(t *testing.T) {
	todos := []TodoItem{
		{Content: "phase", Status: "archived", StepID: "p1"},
		{Content: "still pending", Status: "pending", StepID: "p2", ParentID: "p1"},
	}
	err := ValidateSerialTodos(todos)
	if err == nil {
		t.Fatal("an archived parent with unfinished descendants must be rejected")
	}
	if !strings.Contains(err.Error(), "archived") || !strings.Contains(err.Error(), "T1.1") {
		t.Fatalf("rejection must name the archive and the unfinished code, got: %v", err)
	}
}

func TestValidateTreeSerialTodosAbandonedParentCarriesNoConstraint(t *testing.T) {
	// Giving a parent up must not force the agent to individually resolve its
	// descendants — that would re-create the #9998 family of unsatisfiable
	// rejection loops.
	todos := []TodoItem{
		{Content: "given up stage", Status: "abandoned", StepID: "s1"},
		{Content: "leftover sub", Status: "pending", StepID: "s2", ParentID: "s1"},
		{Content: "current", Status: "in_progress", StepID: "s3"},
	}
	if err := ValidateSerialTodos(todos); err != nil {
		t.Fatalf("abandoned parent with unresolved descendants must be accepted: %v", err)
	}
}

func TestValidateTreeSerialTodosRejectsDanglingAndForwardParent(t *testing.T) {
	dangling := []TodoItem{
		{Content: "orphan", Status: "pending", StepID: "x", ParentID: "nope"},
	}
	err := ValidateSerialTodos(dangling)
	if err == nil || !strings.Contains(err.Error(), "parent_id") || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("dangling parent_id must be rejected with the bad id, got: %v", err)
	}
	// The parent must appear EARLIER (display order: parent above children).
	forward := []TodoItem{
		{Content: "child", Status: "pending", StepID: "c", ParentID: "p"},
		{Content: "parent", Status: "in_progress", StepID: "p"},
	}
	if err := ValidateSerialTodos(forward); err == nil || !strings.Contains(err.Error(), "earlier") {
		t.Fatalf("forward parent reference must be rejected, got: %v", err)
	}
}

func TestValidateTreeSerialTodosRejectsSecondInProgress(t *testing.T) {
	todos := []TodoItem{
		{Content: "a", Status: "in_progress", StepID: "a"},
		{Content: "b", Status: "in_progress", StepID: "b", ParentID: "a"},
	}
	err := ValidateSerialTodos(todos)
	if err == nil || !strings.Contains(err.Error(), "second in_progress") {
		t.Fatalf("two in_progress items must stay rejected in a tree, got: %v", err)
	}
}

func TestValidateTreeSerialTodosSpineStaysSerial(t *testing.T) {
	// The current item must precede untouched work — but a pending parent
	// above the current item is the legit running-subtree shape, not a
	// blocker (mirrors the flat [phase pending, sub in_progress]).
	runningSubtree := []TodoItem{
		{Content: "stage", Status: "pending", StepID: "a"},
		{Content: "current", Status: "in_progress", StepID: "b", ParentID: "a"},
	}
	if err := ValidateSerialTodos(runningSubtree); err != nil {
		t.Fatalf("pending parent of the current item must stay accepted: %v", err)
	}
	currentAfterPending := []TodoItem{
		{Content: "backlog", Status: "pending", StepID: "a"},
		{Content: "backlog sub", Status: "pending", StepID: "b", ParentID: "a"},
		{Content: "unrelated current", Status: "in_progress", StepID: "c"},
	}
	err := ValidateSerialTodos(currentAfterPending)
	if err == nil || !strings.Contains(err.Error(), "in_progress after pending") {
		t.Fatalf("current item after untouched pending work must be rejected, got: %v", err)
	}
	noCurrent := []TodoItem{
		{Content: "done", Status: "completed", StepID: "a"},
		{Content: "backlog", Status: "pending", StepID: "b"},
		{Content: "backlog sub", Status: "pending", StepID: "c", ParentID: "b"},
	}
	if err := ValidateSerialTodos(noCurrent); err == nil || !strings.Contains(err.Error(), "no in_progress item") {
		t.Fatalf("pending work without a current item must be rejected, got: %v", err)
	}
}

// Flat compatibility: level 0/1 lists without parent_id keep the segment
// machine's exact behavior, with terminal statuses folding in as converged.

func TestFlatLevelListsKeepLegacyBehavior(t *testing.T) {
	// Legacy valid shape: the phase stays pending while its sub-steps run; the
	// sub-step carries the in_progress slot.
	valid := []TodoItem{
		{Content: "phase", Status: "pending", Level: 0},
		{Content: "sub", Status: "in_progress", Level: 1},
	}
	if err := ValidateSerialTodos(valid); err != nil {
		t.Fatalf("legacy two-level shape must stay valid: %v", err)
	}
	broken := []TodoItem{
		{Content: "phase", Status: "completed", Level: 0},
		{Content: "sub", Status: "pending", Level: 1},
	}
	err := ValidateSerialTodos(broken)
	if err == nil || !strings.Contains(err.Error(), "is completed but sub-step") {
		t.Fatalf("legacy completed-phase rejection must be unchanged, got: %v", err)
	}
	phaseIP := []TodoItem{
		{Content: "phase", Status: "in_progress", Level: 0},
		{Content: "sub", Status: "pending", Level: 1},
	}
	err = ValidateSerialTodos(phaseIP)
	if err == nil || !strings.Contains(err.Error(), "cannot be in_progress while sub-step") {
		t.Fatalf("legacy in_progress-phase rejection must be unchanged, got: %v", err)
	}
	orphan := []TodoItem{{Content: "sub", Status: "pending", Level: 1}}
	if err := ValidateSerialTodos(orphan); err == nil || !strings.Contains(err.Error(), "no phase above it") {
		t.Fatalf("legacy orphan sub-step rejection must be unchanged, got: %v", err)
	}
}

func TestFlatListWithAbandonedSubStepSignsPhaseOff(t *testing.T) {
	// An abandoned sub-step is converged: the phase may sign off over it.
	todos := []TodoItem{
		{Content: "phase", Status: "completed", Level: 0},
		{Content: "done sub", Status: "completed", Level: 1},
		{Content: "given up sub", Status: "abandoned", Level: 1},
	}
	if err := ValidateSerialTodos(todos); err != nil {
		t.Fatalf("phase over abandoned sub-step must be accepted: %v", err)
	}
}

func TestIncompleteTodosTreatsTerminalStatesAsConverged(t *testing.T) {
	todos := []TodoItem{
		{Content: "done", Status: "completed"},
		{Content: "given up", Status: "abandoned"},
		{Content: "archived record", Status: "archived"},
		{Content: "still open", Status: "pending"},
		{Content: "live", Status: "in_progress"},
	}
	incomplete := IncompleteTodos(todos)
	if len(incomplete) != 2 {
		t.Fatalf("only pending/in_progress are incomplete, got %d: %+v", len(incomplete), incomplete)
	}
	if incomplete[0].Content != "still open" || incomplete[1].Content != "live" {
		t.Fatalf("unexpected incomplete set: %+v", incomplete)
	}
}

func TestPreservesCompletedTodoPositionsAllowsArchiveRejectsAbandon(t *testing.T) {
	previous := []TodoItem{
		{Content: "done step", Status: "completed", StepID: "a"},
		{Content: "current", Status: "in_progress", StepID: "b"},
	}
	archived := []TodoItem{
		{Content: "done step", Status: "archived", StepID: "a"},
		{Content: "current", Status: "in_progress", StepID: "b"},
	}
	if !PreservesCompletedTodoPositions(previous, archived) {
		t.Fatal("completed → archived is the task-152 migration rule, not a regression")
	}
	abandoned := []TodoItem{
		{Content: "done step", Status: "abandoned", StepID: "a"},
		{Content: "current", Status: "in_progress", StepID: "b"},
	}
	if PreservesCompletedTodoPositions(previous, abandoned) {
		t.Fatal("completed → abandoned loses a completion and must stay a regression")
	}
}

func TestNormalizeSerialTodosKeepsTerminalStatuses(t *testing.T) {
	// The legacy repair must not resurrect work the agent gave up, and must
	// not rewrite an archived record back into the active spine.
	legacy := []TodoItem{
		{Content: "given up", Status: "abandoned"},
		{Content: "archived", Status: "archived"},
		{Content: "open work", Status: "pending"},
	}
	out := NormalizeSerialTodos(legacy)
	if out[0].Status != "abandoned" {
		t.Fatalf("abandoned item must survive normalization, got %q", out[0].Status)
	}
	if out[1].Status != "archived" {
		t.Fatalf("archived item must survive normalization, got %q", out[1].Status)
	}
	if out[2].Status != "in_progress" {
		t.Fatalf("first unfinished item must become current, got %q", out[2].Status)
	}
}

func TestNormalizeSerialTodosRepairsLegacyFlatStateUnchanged(t *testing.T) {
	// The original repair (leading completed prefix kept, first unfinished
	// sub-step becomes current, everything later pending) is untouched.
	legacy := []TodoItem{
		{Content: "phase", Status: "pending", Level: 0},
		{Content: "sub 1", Status: "completed", Level: 1},
		{Content: "sub 2", Status: "pending", Level: 1},
		{Content: "sub 3", Status: "in_progress", Level: 1},
	}
	out := NormalizeSerialTodos(legacy)
	want := []string{"pending", "completed", "in_progress", "pending"}
	for i, w := range want {
		if out[i].Status != w {
			t.Fatalf("item %d status = %q, want %q (legacy repair changed)", i, out[i].Status, w)
		}
	}
}

func TestAdvanceSerialTodoSkipsAbandonedSubStep(t *testing.T) {
	// [phase pending, sub1 in_progress, sub2 abandoned]: completing sub1 must
	// promote the phase (sub2 is converged — an abandoned sibling is not a
	// blocker), keeping exactly one current item.
	todos := []TodoItem{
		{Content: "phase", Status: "pending", Level: 0},
		{Content: "sub 1", Status: "in_progress", Level: 1},
		{Content: "sub 2", Status: "abandoned", Level: 1},
	}
	if !AdvanceSerialTodo(todos, 1) {
		t.Fatal("completing sub 1 must advance the chain")
	}
	if todos[1].Status != "completed" {
		t.Fatalf("sub 1 should be completed, got %q", todos[1].Status)
	}
	if todos[0].Status != "in_progress" {
		t.Fatalf("the phase must become current once sub-steps converge (abandoned counts), got %q", todos[0].Status)
	}
}

// Task 420 Q2a: the tree validator mirrors the flat machine's three hard
// rejections; its messages must carry the same positive example.
func TestValidateTreeSerialTodosRejectionsCarryPositiveExample(t *testing.T) {
	const example = "a well-formed list reads [completed, in_progress, pending]"
	cases := []struct {
		name  string
		todos []TodoItem
	}{
		{
			name: "second in_progress",
			todos: []TodoItem{
				{Content: "a", Status: "in_progress", StepID: "a"},
				{Content: "b", Status: "in_progress", StepID: "b", ParentID: "a"},
			},
		},
		{
			name: "in_progress after pending work",
			todos: []TodoItem{
				{Content: "backlog", Status: "pending", StepID: "a"},
				{Content: "backlog sub", Status: "pending", StepID: "b", ParentID: "a"},
				{Content: "unrelated current", Status: "in_progress", StepID: "c"},
			},
		},
		{
			name: "pending work without a current item",
			todos: []TodoItem{
				{Content: "done", Status: "completed", StepID: "a"},
				{Content: "backlog", Status: "pending", StepID: "b"},
				{Content: "backlog sub", Status: "pending", StepID: "c", ParentID: "b"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSerialTodos(tc.todos)
			if err == nil {
				t.Fatal("expected a serial-shape rejection, got nil")
			}
			if !strings.Contains(err.Error(), example) {
				t.Fatalf("rejection must carry the positive example %q, got: %v", example, err)
			}
		})
	}
}
