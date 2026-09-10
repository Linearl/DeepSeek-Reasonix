package hook

import (
	"context"
	"testing"
)

// Whole-domain declaration defect: a broad match:"*" hook used to fire inside
// delegated sub-agents too, so one blocking hook could freeze every tool a
// sub-agent called and stall the whole turn. applies_to scopes it.
func TestAppliesToMainSkipsSubagentContext(t *testing.T) {
	ran := 0
	spawner := func(context.Context, SpawnInput) SpawnResult {
		ran++
		return SpawnResult{ExitCode: 2, Stderr: "blocked by policy"}
	}
	hooks := []ResolvedHook{{
		HookConfig: HookConfig{Command: "deny", AppliesTo: AppliesToMain},
		Event:      PreToolUse,
	}}
	r := NewRunner(hooks, "/tmp", spawner, nil)

	block, _ := r.PreToolUse(context.Background(), "bash", nil)
	if !block || ran != 1 {
		t.Fatalf("the main context must still run the hook: block=%v ran=%d", block, ran)
	}

	sub := WithSubagentContext(context.Background(), true)
	block, _ = r.PreToolUse(sub, "bash", nil)
	if block {
		t.Fatal("applies_to: main must not run inside a sub-agent")
	}
	if ran != 1 {
		t.Fatalf("the hook must not have been spawned for the sub-agent: ran=%d", ran)
	}
	if !InSubagentContext(sub) || InSubagentContext(context.Background()) {
		t.Fatal("InSubagentContext must reflect the marker")
	}
}

func TestAppliesToSubagentRunsOnlyThere(t *testing.T) {
	ran := 0
	spawner := func(context.Context, SpawnInput) SpawnResult {
		ran++
		return SpawnResult{ExitCode: 0}
	}
	hooks := []ResolvedHook{{
		HookConfig: HookConfig{Command: "observe", AppliesTo: AppliesToSubagent},
		Event:      PreToolUse,
	}}
	r := NewRunner(hooks, "/tmp", spawner, nil)

	r.PreToolUse(context.Background(), "bash", nil)
	if ran != 0 {
		t.Fatalf("subagent-only hook must not run in the main context: ran=%d", ran)
	}
	r.PreToolUse(WithSubagentContext(context.Background(), true), "bash", nil)
	if ran != 1 {
		t.Fatalf("subagent-only hook must run inside a sub-agent: ran=%d", ran)
	}
}

// The field is additive: an unset applies_to keeps the behaviour hooks had
// before it existed, in both contexts.
func TestAppliesToUnsetKeepsHistoricalBehaviour(t *testing.T) {
	ran := 0
	spawner := func(context.Context, SpawnInput) SpawnResult {
		ran++
		return SpawnResult{ExitCode: 0}
	}
	hooks := []ResolvedHook{{HookConfig: HookConfig{Command: "observe"}, Event: PreToolUse}}
	r := NewRunner(hooks, "/tmp", spawner, nil)

	r.PreToolUse(context.Background(), "bash", nil)
	r.PreToolUse(WithSubagentContext(context.Background(), true), "bash", nil)
	if ran != 2 {
		t.Fatalf("unset applies_to must run everywhere: ran=%d", ran)
	}
}

func TestAppliesToUnknownValueFailsOpen(t *testing.T) {
	hooks := []ResolvedHook{{
		HookConfig: HookConfig{Command: "observe", AppliesTo: "everywhere-please"},
		Event:      PreToolUse,
	}}
	if !hooks[0].appliesHere(context.Background()) {
		t.Fatal("an unrecognised applies_to must not silently disable the hook")
	}
}
