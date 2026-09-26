package agent

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// Task 243 A1: the per-turn flood cap. Borrowed rule (MiMo TOOLCALL_FLOODING
// LIMIT=16): a batch whose admission crosses the line is cancelled WHOLE,
// nothing executes, the structured reminder tells the model to continue in
// smaller batches, and the batch is never replayed (no side effect, no
// recovery entry). Off by default (fork rule 2).

// TestToolFloodCapCancelsBatchWhole: 16 admits, the 17th call cancels its
// whole batch; later overflow batches keep holding; the counter is
// turn-cumulative; a fresh turn starts at zero again.
func TestToolFloodCapCancelsBatchWhole(t *testing.T) {
	reg := tool.NewRegistry()
	var calls int32
	// A non-read-only fakeTool serializes the whole batch (the parallel
	// partition is read-only by construction): the flood rule under test is
	// admission, not parallelism, and the read-only parallel path trips the
	// PRE-EXISTING concurrent-map race in recordToolErrorStats
	// (tool_error_stats.go:100 — shared baseline red, unrelated to 243 A1).
	reg.Add(fakeTool{name: "a", calls: &calls})

	a := New(nil, reg, NewSession(""), Options{}, event.Discard)
	a.toolFloodLimit = true

	full := make([]provider.ToolCall, 16)
	for i := range full {
		full[i] = provider.ToolCall{Name: "a"}
	}
	batch := a.executeBatch(context.Background(), &a.turn, full)
	if batch.err != nil {
		t.Fatalf("first 16 calls errored: %v", batch.err)
	}
	if got := int32(16); calls != got {
		t.Fatalf("executed %d of the first batch, want %d (admitted)", calls, got)
	}
	if a.turn.toolCallsThisTurn != 16 {
		t.Fatalf("turn counter = %d, want 16", a.turn.toolCallsThisTurn)
	}

	// The 17th call: the whole batch cancels, nothing runs.
	batch = a.executeBatch(context.Background(), &a.turn, []provider.ToolCall{{Name: "a"}, {Name: "a"}})
	if calls != 16 {
		t.Fatalf("executed %d total after overflow, want 16 (overflow batch never ran)", calls)
	}
	if len(batch.results) != 2 {
		t.Fatalf("results = %d, want 2 (whole batch answered)", len(batch.results))
	}
	for i, r := range batch.results {
		for _, want := range []string{"tool_flood_cancelled", "NOT be replayed", "limit 16"} {
			if !strings.Contains(r, want) {
				t.Errorf("result[%d] missing %q: %s", i, want, r)
			}
		}
	}
	for i, o := range batch.outcomes {
		if !o.blocked {
			t.Errorf("outcome[%d].blocked = false, want true (cancelled)", i)
		}
		if o.executed {
			t.Errorf("outcome[%d].executed = true, want false", i)
		}
	}

	// Cumulative: the counter advanced, so the NEXT overflow batch holds too.
	if a.turn.toolCallsThisTurn != 18 {
		t.Fatalf("turn counter = %d, want 18 (cancelled calls still count)", a.turn.toolCallsThisTurn)
	}
	batch = a.executeBatch(context.Background(), &a.turn, []provider.ToolCall{{Name: "a"}})
	if calls != 16 {
		t.Fatalf("executed %d total after third batch, want 16", calls)
	}
	if !strings.Contains(batch.results[0], "tool_flood_cancelled") {
		t.Fatalf("third batch not cancelled: %s", batch.results[0])
	}

	// Recovery face: cancelled calls never ran, so nothing side-effecting
	// entered the recovery maps — the batch has no replay handle.
	if len(a.turn.writeRecovery) != 0 || len(a.turn.unknownRecovery) != 0 {
		t.Fatalf("recovery maps not empty after cancellation: write=%d unknown=%d",
			len(a.turn.writeRecovery), len(a.turn.unknownRecovery))
	}

	// A fresh turn (as beginRunTurn zeroes the runtime) starts at zero: the
	// model continues in the new turn without the old batch ever replaying.
	a.turn = turnRuntime{}
	batch = a.executeBatch(context.Background(), &a.turn, []provider.ToolCall{{Name: "a"}})
	if calls != 17 {
		t.Fatalf("executed %d after fresh turn, want 17 (fresh counter admits)", calls)
	}
	if strings.Contains(batch.results[0], "tool_flood_cancelled") {
		t.Fatalf("fresh turn wrongly capped: %s", batch.results[0])
	}
}

// TestToolFloodCapOffByDefault: the gate off means no counting behavior at
// all — an overflow batch executes like upstream (fork rule 2 default).
func TestToolFloodCapOffByDefault(t *testing.T) {
	reg := tool.NewRegistry()
	var calls int32
	// A non-read-only fakeTool serializes the batch (the parallel partition is
	// read-only by construction): this test is about the gate, and running it
	// through the parallel read-only path would trip the PRE-EXISTING
	// concurrent-map race in recordToolErrorStats (tool_error_stats.go:89,
	// unrelated to 243 A1 — shared baseline red list).
	reg.Add(fakeTool{name: "b", calls: &calls})

	a := New(nil, reg, NewSession(""), Options{}, event.Discard) // gate not set
	if a.toolFloodLimit {
		t.Fatal("toolFloodLimit defaults on, want off (fork rule 2)")
	}
	for i := 0; i < 3; i++ {
		overflow := make([]provider.ToolCall, 17)
		for j := range overflow {
			overflow[j] = provider.ToolCall{Name: "b"}
		}
		batch := a.executeBatch(context.Background(), &a.turn, overflow)
		for _, r := range batch.results {
			if strings.Contains(r, "tool_flood_cancelled") {
				t.Fatalf("gate off but batch cancelled: %s", r)
			}
		}
	}
	if calls != 51 {
		t.Fatalf("executed %d calls with gate off, want 51 (all admitted)", calls)
	}
}
