package control

import (
	"testing"

	"reasonix/internal/event"
)

// Task 372: during an automatic stream retry the running face must stay on —
// the user's 0929 report was "running state vanished" while the 243 budget
// was still retrying. Kind=Retrying arrives while the turn executes, and
// runtimeActivity maps it to "thinking", which keeps the strip alive.
func TestRuntimeActivityKeepsRunningDuringRetrying(t *testing.T) {
	state := event.RuntimeStateSnapshot{Phase: "executing", TurnID: "t1"}
	activity := runtimeActivity(state, event.Event{Kind: event.Retrying, TurnID: "t1"}, "streaming")
	if activity != "thinking" {
		t.Fatalf("activity during Retrying = %q, want thinking (running face preserved)", activity)
	}
	// Outside executing the activity clears — the pre-372 semantics stay.
	idle := event.RuntimeStateSnapshot{Phase: "idle", TurnID: "t1"}
	if got := runtimeActivity(idle, event.Event{Kind: event.Retrying, TurnID: "t1"}, ""); got != "" {
		t.Fatalf("idle activity = %q, want empty (pre-372 semantics untouched)", got)
	}
}
