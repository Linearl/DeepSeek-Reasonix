package control

import (
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// Task 435: the controller's settle method hands the session's pending effect
// records to the restart resume chain. Nil-safe on a controller without an
// executor (restore-time windows), and a real delegation on a live one — the
// desktop resume chain calls it through this method, never the agent directly.
func TestSettleRestartInterruptedEffectsDelegatesAndNilGuards(t *testing.T) {
	empty := &Controller{}
	if got := empty.SettleRestartInterruptedEffects(); got != 0 {
		t.Fatalf("nil-executor controller settled %d, want 0", got)
	}

	record := provider.ToolCallRecord{Identity: provider.ActionIdentity{AttemptID: "a1", CallID: "call-w", CanonicalTool: "write_file"}, State: provider.ToolRunUnknown, ReadOnly: false}
	s := agent.NewSession("")
	s.Messages = []provider.Message{{Role: provider.RoleAssistant, ID: "turn-1", ToolCalls: []provider.ToolCall{{ID: "call-w", Name: "write_file", Recovery: &record}}}}
	a := agent.New(nil, tool.NewRegistry(), s, agent.Options{}, event.Discard)
	c := &Controller{executor: a}

	if got := c.SettleRestartInterruptedEffects(); got != 1 {
		t.Fatalf("settled %d, want 1", got)
	}
	if pending := a.PendingToolRecovery(); len(pending) != 0 {
		t.Fatalf("pending recovery must be empty after the settle: %+v", pending)
	}
}
