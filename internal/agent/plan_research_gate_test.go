package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// Task 118: the plan-mode research gate cap clamps to [1, hardCap].
func TestNormalizePlanResearchNudgeLimit(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, maxPlanResearchNudges},
		{-2, maxPlanResearchNudges},
		{1, 1},
		{2, 2},
		{9, maxPlanResearchNudgeHardCap},
	}
	for _, c := range cases {
		if got := normalizePlanResearchNudgeLimit(c.in); got != c.want {
			t.Errorf("normalizePlanResearchNudgeLimit(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func planText() []provider.Chunk {
	return []provider.Chunk{{Type: provider.ChunkText, Text: "Here is the plan: touch a.go."}, {Type: provider.ChunkDone}}
}

func hostPlanResearchRounds(a *Agent) []string {
	var out []string
	for _, m := range a.sess.conversation.Snapshot() {
		if m.Origin == provider.MessageOriginHost && strings.Contains(m.Content, "no read-only investigation") {
			out = append(out, m.Content)
		}
	}
	return out
}

// Task 118 (opt-in): a plan-mode turn that never delegated a read-only
// investigation gets exactly one visible ask, and the plan still lands.
func TestPlanResearchGateAsksOnceInPlanMode(t *testing.T) {
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{planText(), planText()}}
	a := New(prov, tool.NewRegistry(), NewSession("sys"), Options{PlanResearchGate: true}, event.Discard)
	a.SetPlanMode(true)
	if err := a.Run(context.Background(), "plan the change"); err != nil {
		t.Fatalf("gated plan run = %v, want a clean finish", err)
	}
	if prov.call != 2 {
		t.Fatalf("provider calls = %d, want plan + one gated round", prov.call)
	}
	rounds := hostPlanResearchRounds(a)
	if len(rounds) != 1 {
		t.Fatalf("research-gate rounds = %d, want exactly 1", len(rounds))
	}
	if !strings.Contains(rounds[0], "did not investigate") {
		t.Fatalf("gate round must offer the stated-reason exit, got %q", rounds[0])
	}
}

// Task 118: off by default, the plan lands with no extra provider round.
func TestPlanResearchGateOffByDefaultInPlanMode(t *testing.T) {
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{planText()}}
	a := New(prov, tool.NewRegistry(), NewSession("sys"), Options{}, event.Discard)
	a.SetPlanMode(true)
	if err := a.Run(context.Background(), "plan the change"); err != nil {
		t.Fatalf("ungated plan run = %v, want a clean finish", err)
	}
	if prov.call != 1 {
		t.Fatalf("provider calls = %d, want one plan answer when the gate is off", prov.call)
	}
	if rounds := hostPlanResearchRounds(a); len(rounds) != 0 {
		t.Fatalf("gate must not fire when off, got %d rounds", len(rounds))
	}
}

// Task 118: the gate is a plan-mode step, not a whole-session rule.
func TestPlanResearchGateInactiveOutsidePlanMode(t *testing.T) {
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{planText()}}
	a := New(prov, tool.NewRegistry(), NewSession("sys"), Options{PlanResearchGate: true}, event.Discard)
	if err := a.Run(context.Background(), "just answer"); err != nil {
		t.Fatalf("non-plan run = %v, want a clean finish", err)
	}
	if rounds := hostPlanResearchRounds(a); len(rounds) != 0 {
		t.Fatalf("gate must stay plan-mode-only, got %d rounds", len(rounds))
	}
}

// Task 118: the gate reads the transcript, so a read-only delegation recorded in
// this turn satisfies it while research from an earlier turn does not.
func TestDispatchedReadOnlyResearchReadsTheCurrentTurn(t *testing.T) {
	a := New(&scriptedProvider{name: "p"}, tool.NewRegistry(), NewSession("sys"), Options{}, event.Discard)
	if a.dispatchedReadOnlyResearch() {
		t.Fatal("an empty transcript has no dispatch")
	}
	a.sess.conversation.Add(provider.Message{Role: provider.RoleTool, ToolCallID: "r", Name: "explore", Content: "research"})
	if !a.dispatchedReadOnlyResearch() {
		t.Fatal("a recorded explore dispatch must satisfy the gate")
	}
	a.sess.conversation.Add(provider.Message{Role: provider.RoleUser, Content: "new request"})
	if a.dispatchedReadOnlyResearch() {
		t.Fatal("research from an earlier user turn must not satisfy this turn's gate")
	}
	// Writer dispatches are not research and must not satisfy the gate.
	b := New(&scriptedProvider{name: "p"}, tool.NewRegistry(), NewSession("sys"), Options{}, event.Discard)
	b.sess.conversation.Add(provider.Message{Role: provider.RoleTool, ToolCallID: "w", Name: "task", Content: "Subagent reference: sa_x"})
	if b.dispatchedReadOnlyResearch() {
		t.Fatal("a writer-capable task dispatch is not a read-only investigation")
	}
}

// Task 118: read_only_task only advertises background execution when the
// experiment is on, so the default provider-visible schema is unchanged.
func TestReadOnlyTaskSchemaOffersBackgroundOnlyWhenEnabled(t *testing.T) {
	off := NewReadOnlyTaskTool(nil)
	if raw := string(off.Schema()); strings.Contains(raw, "run_in_background") {
		t.Fatalf("default read_only_task schema must not advertise background execution: %s", raw)
	}
	on := NewReadOnlyTaskTool(nil).WithBackgroundExecution(true)
	raw := string(on.Schema())
	if !strings.Contains(raw, "run_in_background") {
		t.Fatalf("enabled read_only_task schema must advertise run_in_background: %s", raw)
	}
	var parsed map[string]any
	if err := json.Unmarshal(on.Schema(), &parsed); err != nil {
		t.Fatalf("enabled schema must stay valid JSON: %v", err)
	}
	props, _ := parsed["properties"].(map[string]any)
	if _, ok := props["run_in_background"]; !ok {
		t.Fatalf("enabled schema properties lack run_in_background: %v", props)
	}
	if err := json.Unmarshal(off.Schema(), &parsed); err != nil {
		t.Fatalf("default schema must stay valid JSON: %v", err)
	}
}

// Task 118: background execution on the read-only path stays opt-in.
func TestReadOnlyTaskBackgroundRequiresOptIn(t *testing.T) {
	off := NewReadOnlyTaskTool(nil)
	if off.allowBackground {
		t.Fatal("read_only_task must default to no background execution")
	}
	if !off.WithBackgroundExecution(true).allowBackground {
		t.Fatal("WithBackgroundExecution(true) must enable the option")
	}
}
