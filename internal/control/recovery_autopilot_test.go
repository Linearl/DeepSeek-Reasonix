package control

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/recovery"
	"reasonix/internal/tool"
)

func newRecoveryGateController(t *testing.T, autopilot bool) *Controller {
	t.Helper()
	ag := agent.New(nil, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	opts := Options{Runner: ag, Executor: ag}
	if autopilot {
		opts.Autopilot = true
		opts.AutopilotMaxRuntime = time.Minute
	}
	c := New(opts)
	c.SetToolApprovalMode(ToolApprovalAuto)
	c.mu.Lock()
	gate := c.recoveryGate
	c.mu.Unlock()
	if gate == nil {
		t.Fatal("recovery gate not initialised")
	}
	return c
}

// Task 109 B2 (option B): Auto Guard shows a card a human must answer, and it
// never drains on a mode switch, so an unattended run would hang on it forever.
// An autopilot session reports a non-auto mode to the gate, which makes it
// bypass itself; the unattended approval path stays the A5 guardian.
func TestAutoGuardBypassedForUnattendedRun(t *testing.T) {
	obs := recovery.Observation{
		TaskID:     "task",
		Tool:       "bash",
		Args:       json.RawMessage(`{"command":"go test ./..."}`),
		Mutates:    true,
		ErrSummary: "exit status 1",
	}

	interactive := newRecoveryGateController(t, false)
	if got := interactive.recoveryGateMode(); got != ToolApprovalAuto {
		t.Fatalf("interactive gate mode = %q, want %q", got, ToolApprovalAuto)
	}
	if guidance := interactive.recoveryGate.ObserveResult(context.Background(), obs); guidance == "" {
		t.Fatal("Auto Guard did not arm under an interactive Auto session")
	}

	unattended := newRecoveryGateController(t, true)
	if got := unattended.recoveryGateMode(); got != ToolApprovalYolo {
		t.Fatalf("unattended gate mode = %q, want the gate to bypass itself", got)
	}
	if guidance := unattended.recoveryGate.ObserveResult(context.Background(), obs); guidance != "" {
		t.Fatalf("Auto Guard armed for an unattended run: %q", guidance)
	}
}
