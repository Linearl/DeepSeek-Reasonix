package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// Task 117 P1: the opt-in delivery catch-up cap clamps to [1, hardCap].
func TestNormalizeReadinessCatchUpLimit(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, maxReadinessCatchUps},
		{-3, maxReadinessCatchUps},
		{1, 1},
		{2, 2},
		{3, maxReadinessCatchUpHardCap},
		{50, maxReadinessCatchUpHardCap},
	}
	for _, c := range cases {
		if got := normalizeReadinessCatchUpLimit(c.in); got != c.want {
			t.Errorf("normalizeReadinessCatchUpLimit(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func catchUpFinalText() []provider.Chunk {
	return []provider.Chunk{{Type: provider.ChunkText, Text: "done, all fixed"}, {Type: provider.ChunkDone}}
}

// catchUpRegistry mirrors the delivery gate fixture: a writer keeps mutation
// expected, so a text-only final answer is genuinely unsatisfied.
func catchUpRegistry() *tool.Registry {
	reg := tool.NewRegistry()
	reg.Add(fakeReadFileTool{})
	reg.Add(fakeWriterTool{})
	return reg
}

func hostCatchUpRounds(a *Agent) []string {
	var out []string
	for _, m := range a.sess.conversation.Snapshot() {
		if m.Origin == provider.MessageOriginHost && strings.Contains(m.Content, "The host could not settle") {
			out = append(out, m.Content)
		}
	}
	return out
}

// Task 117 P1: off by default the run still ends on the FIRST unsatisfied
// delivery answer (the upstream contract); turning the switch on buys exactly
// one visible catch-up round, and a gap that survives it still stops the run.
func TestDeliveryCatchUpSpendsVisibleRoundBeforePausing(t *testing.T) {
	script := func() [][]provider.Chunk {
		return [][]provider.Chunk{
			{toolCallChunk("w", "fake_write", `{"path":"a.go","content":"package a"}`), {Type: provider.ChunkDone}},
			catchUpFinalText(),
			catchUpFinalText(), // the catch-up round did not close the gap
		}
	}

	// Default: pause on the first unsatisfied answer, no extra provider call.
	prov := &scriptedProvider{name: "p", turns: script()}
	def := New(prov, catchUpRegistry(), NewSession("sys"), Options{}, event.Discard)
	var defErr *FinalReadinessError
	if err := def.Run(withClosedLoopContext(context.Background()), "fix the crash in a.go"); !errors.As(err, &defErr) {
		t.Fatalf("default run error = %v, want FinalReadinessError", err)
	}
	if prov.call != 2 {
		t.Fatalf("default provider calls = %d, want write + one final answer", prov.call)
	}
	if rounds := hostCatchUpRounds(def); len(rounds) != 0 {
		t.Fatalf("default run must not spend catch-up rounds, got %d", len(rounds))
	}

	// Opt-in: one catch-up round naming the gap, then the same pause.
	prov2 := &scriptedProvider{name: "p", turns: script()}
	a := New(prov2, catchUpRegistry(), NewSession("sys"), Options{ReadinessCatchUp: true}, event.Discard)
	var catchErr *FinalReadinessError
	if err := a.Run(withClosedLoopContext(context.Background()), "fix the crash in a.go"); !errors.As(err, &catchErr) {
		t.Fatalf("catch-up run error = %v, want FinalReadinessError", err)
	}
	if prov2.call != 3 {
		t.Fatalf("catch-up provider calls = %d, want write + final + one catch-up round", prov2.call)
	}
	rounds := hostCatchUpRounds(a)
	if len(rounds) != 1 {
		t.Fatalf("catch-up rounds = %d, want exactly 1", len(rounds))
	}
	if !strings.Contains(rounds[0], "verification") {
		t.Fatalf("catch-up round must name the missing evidence, got %q", rounds[0])
	}
	// The pause still lands, and it is the ordinary recovery card - the
	// catch-up never replaces the host's decision, only defers it.
	if !a.pending.finalReadinessRecovery {
		t.Fatal("an unsatisfied contract must still leave delivery recovery pending")
	}
}

// Task 117 P1: catch-up rounds honor the configured limit.
func TestDeliveryCatchUpHonorsConfiguredLimit(t *testing.T) {
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{toolCallChunk("w", "fake_write", `{"path":"a.go","content":"package a"}`), {Type: provider.ChunkDone}},
		catchUpFinalText(),
		catchUpFinalText(),
		catchUpFinalText(),
	}}
	a := New(prov, catchUpRegistry(), NewSession("sys"), Options{
		ReadinessCatchUp:      true,
		ReadinessCatchUpLimit: 2,
	}, event.Discard)
	var err *FinalReadinessError
	if runErr := a.Run(withClosedLoopContext(context.Background()), "fix the crash in a.go"); !errors.As(runErr, &err) {
		t.Fatalf("run error = %v, want FinalReadinessError", runErr)
	}
	if rounds := hostCatchUpRounds(a); len(rounds) != 2 {
		t.Fatalf("catch-up rounds = %d, want 2 (configured limit)", len(rounds))
	}
	if prov.call != 4 {
		t.Fatalf("provider calls = %d, want write + final + 2 catch-up rounds", prov.call)
	}
}

// Task 117 P1: an out-of-range limit is clamped, so a real gap cannot spin.
func TestDeliveryCatchUpClampsUnreasonableLimit(t *testing.T) {
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{toolCallChunk("w", "fake_write", `{"path":"a.go","content":"package a"}`), {Type: provider.ChunkDone}},
		catchUpFinalText(),
		catchUpFinalText(),
		catchUpFinalText(),
		catchUpFinalText(),
	}}
	a := New(prov, catchUpRegistry(), NewSession("sys"), Options{
		ReadinessCatchUp:      true,
		ReadinessCatchUpLimit: 99,
	}, event.Discard)
	var err *FinalReadinessError
	if runErr := a.Run(withClosedLoopContext(context.Background()), "fix the crash in a.go"); !errors.As(runErr, &err) {
		t.Fatalf("run error = %v, want FinalReadinessError", runErr)
	}
	if rounds := hostCatchUpRounds(a); len(rounds) != maxReadinessCatchUpHardCap {
		t.Fatalf("catch-up rounds = %d, want the hard cap %d", len(rounds), maxReadinessCatchUpHardCap)
	}
}

// Task 117 P1: when the catch-up round closes the gap, the turn ends normally
// and the recovery card is withdrawn - a recovered turn must not look like the
// paused turn the catch-up exists to avoid.
func TestDeliveryCatchUpClosingGapClearsRecovery(t *testing.T) {
	reg := evidenceRegistry()
	reg.Add(fakeReadFileTool{})
	finalText := []provider.Chunk{{Type: provider.ChunkText, Text: "premature"}, {Type: provider.ChunkDone}}
	prov := &scriptedProvider{name: "delivery", turns: [][]provider.Chunk{
		{toolCallChunk("todo", "todo_write", `{"todos":[{"content":"Ship main","status":"in_progress"}]}`), {Type: provider.ChunkDone}},
		{toolCallChunk("write", "write_file", `{"path":"main.go"}`), {Type: provider.ChunkDone}},
		finalText,
		// Catch-up round: the evidence the host asked for, then a real finish.
		{toolCallChunk("review", "read_file", `{"path":"main.go"}`), {Type: provider.ChunkDone}},
		{toolCallChunk("verify", "bash", `{"command":"go test ./..."}`), {Type: provider.ChunkDone}},
		{toolCallChunk("signoff", "complete_step", `{"step":"Ship main","result":"done","evidence":[{"kind":"verification","summary":"tests pass","command":"go test ./..."}]}`), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "delivered"}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, reg, NewSession("sys"), Options{ReadinessCatchUp: true}, event.Discard)
	if err := a.Run(withClosedLoopContext(context.Background()), "implement main"); err != nil {
		t.Fatalf("catch-up run must finish once the evidence lands, got %v", err)
	}
	if rounds := hostCatchUpRounds(a); len(rounds) != 1 {
		t.Fatalf("catch-up rounds = %d, want exactly 1", len(rounds))
	}
	if a.pending.finalReadinessRecovery {
		t.Fatal("a satisfied contract must not leave delivery recovery pending")
	}
	if a.PrepareDeliveryRecovery() {
		t.Fatal("a satisfied contract must not authorize a recovery card")
	}
}
