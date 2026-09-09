package agent

import "testing"

// #39 P1: a deduplicated result carries the current plan only when the turn
// actually has todos, so unrelated duplicates stay short.
func TestDuplicateStateSnapshotWithoutTodos(t *testing.T) {
	var a Agent
	if got := a.duplicateStateSnapshot(); got != "" {
		t.Fatalf("snapshot without todos = %q; want empty", got)
	}
}
