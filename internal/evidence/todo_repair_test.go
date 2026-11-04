package evidence

// tl1354 acceptance: the "current item must be the first unfinished item"
// ordering slip is auto-repaired instead of hard-rejected, and ONLY that slip.
// Every case here pins one side of that boundary: a repair case asserts the
// exact reordered list (identity and status must survive untouched), a
// non-repair case asserts the original list comes back with ok=false so the
// caller keeps its rejection path.

import (
	"reflect"
	"testing"
)

func TestRepairCurrentOrderFlatList(t *testing.T) {
	in := []TodoItem{
		{Content: "done step", Status: "completed", StepID: "s1"},
		{Content: "backlog step", Status: "pending", StepID: "s2"},
		{Content: "current step", Status: "in_progress", StepID: "s3"},
		{Content: "later step", Status: "pending", StepID: "s4"},
	}
	want := []TodoItem{
		{Content: "done step", Status: "completed", StepID: "s1"},
		{Content: "current step", Status: "in_progress", StepID: "s3"},
		{Content: "backlog step", Status: "pending", StepID: "s2"},
		{Content: "later step", Status: "pending", StepID: "s4"},
	}
	got, ok := RepairSerialTodoCurrentOrder(in)
	if !ok {
		t.Fatal("a flat ordering slip must be repairable")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repaired list mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestRepairCurrentOrderTwoLevelSegmentBehindPendingPhase(t *testing.T) {
	// The phase whose SUB-STEP is current sits behind an untouched phase: the
	// whole segment moves in front of the first unfinished one. (A phase
	// itself cannot be the running item while its sub-steps are unfinished —
	// that is the sign-off rule, not an ordering slip, and stays rejected.)
	in := []TodoItem{
		{Content: "phase A", Status: "completed", Level: 0, StepID: "a"},
		{Content: "sub a1", Status: "completed", Level: 1, StepID: "a1"},
		{Content: "phase B", Status: "pending", Level: 0, StepID: "b"},
		{Content: "sub b1", Status: "pending", Level: 1, StepID: "b1"},
		{Content: "phase C", Status: "pending", Level: 0, StepID: "c"},
		{Content: "sub c1", Status: "in_progress", Level: 1, StepID: "c1"},
	}
	want := []TodoItem{
		{Content: "phase A", Status: "completed", Level: 0, StepID: "a"},
		{Content: "sub a1", Status: "completed", Level: 1, StepID: "a1"},
		{Content: "phase C", Status: "pending", Level: 0, StepID: "c"},
		{Content: "sub c1", Status: "in_progress", Level: 1, StepID: "c1"},
		{Content: "phase B", Status: "pending", Level: 0, StepID: "b"},
		{Content: "sub b1", Status: "pending", Level: 1, StepID: "b1"},
	}
	got, ok := RepairSerialTodoCurrentOrder(in)
	if !ok {
		t.Fatal("a current sub-step behind a pending phase must be repairable")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repaired list mismatch:\n got %+v\nwant %+v", got, want)
	}
	if err := ValidateSerialTodos(got); err != nil {
		t.Fatalf("repaired two-level list must re-validate: %v", err)
	}
}

func TestRepairCurrentOrderSubStepBehindPendingSibling(t *testing.T) {
	// In-segment slip: sub two is in_progress behind pending sub one.
	in := []TodoItem{
		{Content: "phase A", Status: "pending", Level: 0, StepID: "a"},
		{Content: "sub one", Status: "pending", Level: 1, StepID: "a1"},
		{Content: "sub two", Status: "in_progress", Level: 1, StepID: "a2"},
	}
	want := []TodoItem{
		{Content: "phase A", Status: "pending", Level: 0, StepID: "a"},
		{Content: "sub two", Status: "in_progress", Level: 1, StepID: "a2"},
		{Content: "sub one", Status: "pending", Level: 1, StepID: "a1"},
	}
	got, ok := RepairSerialTodoCurrentOrder(in)
	if !ok {
		t.Fatal("a sub-step running behind a pending sibling must be repairable")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repaired list mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestRepairCurrentOrderTreeMovesBlockWithSubtree(t *testing.T) {
	// Tree slip: unrelated pending task T1 (with its subtree) sits in front of
	// the running T2 — the blocked task moves behind the current item TOGETHER
	// with its whole subtree so parent-order resolution still holds.
	in := []TodoItem{
		{Content: "blocked task", Status: "pending", StepID: "t1"},
		{Content: "blocked sub", Status: "pending", StepID: "t1_1", ParentID: "t1"},
		{Content: "current task", Status: "in_progress", StepID: "t2"},
	}
	want := []TodoItem{
		{Content: "current task", Status: "in_progress", StepID: "t2"},
		{Content: "blocked task", Status: "pending", StepID: "t1"},
		{Content: "blocked sub", Status: "pending", StepID: "t1_1", ParentID: "t1"},
	}
	got, ok := RepairSerialTodoCurrentOrder(in)
	if !ok {
		t.Fatal("a tree ordering slip must be repairable")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repaired list mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestRepairCurrentOrderTreeKeepsPendingAncestor(t *testing.T) {
	// A pending ANCESTOR of the current item is legitimate (it owns the running
	// subtree) and must not be treated as a blocker; only the unrelated pending
	// sibling moves behind.
	in := []TodoItem{
		{Content: "parent phase", Status: "pending", StepID: "p"},
		{Content: "unrelated task", Status: "pending", StepID: "x"},
		{Content: "current sub", Status: "in_progress", StepID: "p_c", ParentID: "p"},
	}
	want := []TodoItem{
		{Content: "parent phase", Status: "pending", StepID: "p"},
		{Content: "current sub", Status: "in_progress", StepID: "p_c", ParentID: "p"},
		{Content: "unrelated task", Status: "pending", StepID: "x"},
	}
	got, ok := RepairSerialTodoCurrentOrder(in)
	if !ok {
		t.Fatal("a blocked sibling in a tree must be repairable")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repaired list mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestRepairCurrentOrderAlreadyValidIsNoOp(t *testing.T) {
	valid := []TodoItem{
		{Content: "done", Status: "completed", StepID: "s1"},
		{Content: "current", Status: "in_progress", StepID: "s2"},
		{Content: "later", Status: "pending", StepID: "s3"},
	}
	got, ok := RepairSerialTodoCurrentOrder(valid)
	if ok {
		t.Fatal("a valid list must not be touched")
	}
	if !reflect.DeepEqual(got, valid) {
		t.Fatalf("no-op must return the original list, got %+v", got)
	}
}

func TestRepairCurrentOrderSecondInProgressNotRepaired(t *testing.T) {
	// Two currents is an anti-forgery-adjacent failure with its own rejection:
	// tolerance must not pick a winner silently.
	in := []TodoItem{
		{Content: "first", Status: "in_progress", StepID: "s1"},
		{Content: "second", Status: "in_progress", StepID: "s2"},
	}
	got, ok := RepairSerialTodoCurrentOrder(in)
	if ok {
		t.Fatal("two in_progress items must not be auto-repaired")
	}
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("non-repair must return the original list, got %+v", got)
	}
}

func TestRepairCurrentOrderNoCurrentNotRepaired(t *testing.T) {
	// Pending work with no in_progress item at all: reordering has nothing to
	// move — the "no current step" rejection stays.
	in := []TodoItem{
		{Content: "done", Status: "completed", StepID: "s1"},
		{Content: "backlog", Status: "pending", StepID: "s2"},
	}
	if _, ok := RepairSerialTodoCurrentOrder(in); ok {
		t.Fatal("a list with no current item must not be auto-repaired")
	}
}

func TestRepairCurrentOrderRespectsTerminalStates(t *testing.T) {
	// The repair reorders; it must never resurrect abandoned work or rewrite
	// statuses. Abandoned backlog keeps its status and just moves aside.
	in := []TodoItem{
		{Content: "given up", Status: "abandoned", StepID: "s1"},
		{Content: "current", Status: "in_progress", StepID: "s2"},
	}
	// [abandoned, in_progress, (no pending)] is already valid: abandoned work
	// is converged, so there is no ordering slip to repair.
	if _, ok := RepairSerialTodoCurrentOrder(in); ok {
		t.Fatal("abandoned work ahead of the current item is converged, not a slip")
	}
}

func TestRepairCurrentOrderTreeSecondInProgressNotRepaired(t *testing.T) {
	// Tree variant of the two-current boundary: tolerance must not pick a
	// winner silently — the dedicated rejection stays.
	in := []TodoItem{
		{Content: "first", Status: "in_progress", StepID: "a"},
		{Content: "second", Status: "in_progress", StepID: "b", ParentID: "a"},
	}
	if _, ok := RepairSerialTodoCurrentOrder(in); ok {
		t.Fatal("two in_progress items in a tree must not be auto-repaired")
	}
}
