package agent

import (
	"context"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// Task 820 (816 P0): the per-turn delegation fingerprint. The tier's only
// lever is the per-turn guidance block, so without these counters nothing
// could tell "aggressive changed behavior" from "aggressive changed words".

type delegationMetricsSink struct {
	audits []event.DelegationMetricsAudit
}

func (s *delegationMetricsSink) Emit(event.Event) {}
func (s *delegationMetricsSink) RecordDelegationMetrics(a event.DelegationMetricsAudit) {
	s.audits = append(s.audits, a)
}

// TestEmitTurnShadowsRecordsDelegationMetrics: the end-of-turn shadow pass
// records exactly one fingerprint carrying the tier, the dispatch counts,
// and the main-chain tool steps.
func TestEmitTurnShadowsRecordsDelegationMetrics(t *testing.T) {
	sink := &delegationMetricsSink{}
	a := New(nil, tool.NewRegistry(), NewSession(""), Options{}, sink)
	a.resetTurnEvidence()
	a.turn.turnInput = "refactor the cache module"
	a.SetSubagentPolicyTier(SubagentPolicyAggressive)
	a.turn.taskDispatches = 2
	a.turn.readOnlyTaskDispatches = 1
	a.turn.mainChainToolSteps = 9

	a.emitTurnShadows(a.turn.turnInput)

	if len(sink.audits) != 1 {
		t.Fatalf("recorded %d delegation fingerprints, want exactly one per turn", len(sink.audits))
	}
	got := sink.audits[0]
	if got.Policy != string(SubagentPolicyAggressive) {
		t.Fatalf("Policy = %q, want %q", got.Policy, string(SubagentPolicyAggressive))
	}
	if got.TaskDispatches != 2 || got.ReadOnlyTaskDispatches != 1 {
		t.Fatalf("dispatches = (task %d, read_only %d), want (2, 1)", got.TaskDispatches, got.ReadOnlyTaskDispatches)
	}
	if got.MainChainToolSteps != 9 {
		t.Fatalf("MainChainToolSteps = %d, want 9", got.MainChainToolSteps)
	}
}

// A tier the host never set reads as "", so a sub-agent run stays
// distinguishable from a real light turn in the metrics.
func TestDelegationMetricsTierMirror(t *testing.T) {
	a := New(nil, tool.NewRegistry(), NewSession(""), Options{}, event.Discard)
	if got := a.subagentPolicyTierValue(); got != "" {
		t.Fatalf("default tier = %q, want empty (sub-agent condition)", got)
	}
	a.SetSubagentPolicyTier(SubagentPolicyLight)
	if got := a.subagentPolicyTierValue(); got != string(SubagentPolicyLight) {
		t.Fatalf("tier after Set = %q, want light", got)
	}
}

// TestDelegationCountersCountAdmittedBatches: the counters live on the
// admitted-batch path — a flood-cancelled batch never executes, so its calls
// are neither dispatches nor steps.
func TestDelegationCountersCountAdmittedBatches(t *testing.T) {
	reg := tool.NewRegistry()
	var calls int32
	reg.Add(fakeTool{name: "task", calls: &calls})
	reg.Add(fakeTool{name: "read_only_task", calls: &calls})
	reg.Add(fakeTool{name: "bash", calls: &calls})

	a := New(nil, reg, NewSession(""), Options{}, event.Discard)
	batch := []provider.ToolCall{
		{Name: "task"}, {Name: "read_only_task"}, {Name: "bash"},
	}
	if b := a.executeBatch(context.Background(), &a.turn, batch); b.err != nil {
		t.Fatalf("mixed batch errored: %v", b.err)
	}
	if a.turn.taskDispatches != 1 || a.turn.readOnlyTaskDispatches != 1 {
		t.Fatalf("dispatches = (task %d, read_only %d), want (1, 1)",
			a.turn.taskDispatches, a.turn.readOnlyTaskDispatches)
	}
	if a.turn.mainChainToolSteps != 3 {
		t.Fatalf("MainChainToolSteps = %d, want 3", a.turn.mainChainToolSteps)
	}

	// A fresh turn zeroes the fingerprint with the runtime, like every other
	// per-turn counter.
	a.turn = turnRuntime{}
	if a.turn.taskDispatches != 0 || a.turn.readOnlyTaskDispatches != 0 || a.turn.mainChainToolSteps != 0 {
		t.Fatal("a fresh turn must start the delegation counters at zero")
	}

	// Flood-cancelled batches (nothing executed) must not inflate the
	// fingerprint: fill the cap with plain calls, then offer an overflow batch
	// carrying a task dispatch.
	a.toolFloodLimit = true
	full := make([]provider.ToolCall, toolFloodTurnLimit)
	for i := range full {
		full[i] = provider.ToolCall{Name: "bash"}
	}
	if b := a.executeBatch(context.Background(), &a.turn, full); b.err != nil {
		t.Fatalf("full batch errored: %v", b.err)
	}
	if b := a.executeBatch(context.Background(), &a.turn, []provider.ToolCall{{Name: "task"}}); b.err != nil {
		t.Fatalf("overflow batch errored: %v", b.err)
	}
	if a.turn.taskDispatches != 0 {
		t.Fatalf("taskDispatches = %d, want 0 (overflow batch never executed)", a.turn.taskDispatches)
	}
	if a.turn.mainChainToolSteps != toolFloodTurnLimit {
		t.Fatalf("MainChainToolSteps = %d, want %d (only the admitted batch)",
			a.turn.mainChainToolSteps, toolFloodTurnLimit)
	}
}
