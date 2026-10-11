package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/evidence"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// newRelaxedEvidenceAgent is newEvidenceAgent with the task-789 relaxed edit
// evidence gate under test: relaxed controls the switch, gates stays on.
func newRelaxedEvidenceAgent(t *testing.T, relaxed bool, writers ...tool.Tool) (*Agent, *evidence.Ledger) {
	t.Helper()
	reg := tool.NewRegistry()
	for _, w := range writers {
		reg.Add(w)
	}
	a := New(&userInputCaptureProvider{}, reg, NewSession("system"), Options{EditEvidenceRelaxed: relaxed}, event.Discard)
	ledger := evidence.NewLedger()
	a.task.ledger = ledger
	return a, ledger
}

// relaxedEvidenceRound runs one provider batch through executeBatch, mirroring
// evidenceRound so the preflight + execution-time gates both participate.
func relaxedEvidenceRound(t *testing.T, a *Agent, calls []provider.ToolCall) []string {
	t.Helper()
	a.freezeVisibleReads(a.Session().Snapshot())
	a.sess.conversation.Add(provider.Message{Role: provider.RoleAssistant, ToolCalls: calls})
	b := a.executeBatch(context.Background(), &a.turn, calls)
	if len(b.results) != len(calls) {
		t.Fatalf("results: %v", b.results)
	}
	return b.results
}

// TestRelaxedGateCountsSameBatchRead pins scenario 1 (task 789): with the
// relaxed switch on, a read that already completed earlier in the same
// provider batch satisfies the write evidence check. The strict counterpart —
// the same ledger state with the switch off — stays blocked, so the switch
// itself is the only difference between the two verdicts.
func TestRelaxedGateCountsSameBatchRead(t *testing.T) {
	writer := evidenceWriter{target: tool.EvidenceTargetInfo{
		Path: "/w/a.go", WholeFile: true, Hashes: hashesFor("alpha"),
	}}
	a, ledger := newRelaxedEvidenceAgent(t, true, writer)
	boundary := ledger.ObservationBoundary()
	ledger.RecordTextObservation(evidence.TextObservation{
		Path: "/w/a.go", StartLine: 1, Snapshot: "ss2:1", LineHashes: hashesFor("alpha"),
	})
	ctx := withObservationBoundary(context.Background(), boundary)

	plan := &toolCallPlan{call: provider.ToolCall{Name: "write_file", Arguments: `{"path":"/w/a.go"}`}}
	if out, blocked := a.applyEvidenceGates(ctx, plan); blocked {
		t.Fatalf("relaxed mode: a completed same-batch read must count as evidence: %+v", out)
	}

	b, strictLedger := newRelaxedEvidenceAgent(t, false, writer)
	boundary = strictLedger.ObservationBoundary()
	strictLedger.RecordTextObservation(evidence.TextObservation{
		Path: "/w/a.go", StartLine: 1, Snapshot: "ss2:1", LineHashes: hashesFor("alpha"),
	})
	plan = &toolCallPlan{call: provider.ToolCall{Name: "write_file", Arguments: `{"path":"/w/a.go"}`}}
	if out, blocked := b.applyEvidenceGates(withObservationBoundary(context.Background(), boundary), plan); !blocked {
		t.Fatalf("strict mode must keep ignoring the same-batch read (byte-identical baseline): %+v", out)
	}
}

// TestRelaxedGateIsNotAWaiver pins the boundary of scenario 1: relaxed mode
// widens WHICH reads count, it never waives the requirement. Without any read
// the overwrite stays blocked.
func TestRelaxedGateIsNotAWaiver(t *testing.T) {
	writer := evidenceWriter{target: tool.EvidenceTargetInfo{
		Path: "/w/a.go", WholeFile: true, Hashes: hashesFor("alpha"),
	}}
	a, _ := newRelaxedEvidenceAgent(t, true, writer)
	out, blocked := a.applyEvidenceGates(context.Background(), &toolCallPlan{
		call: provider.ToolCall{Name: "write_file", Arguments: `{"path":"/w/a.go"}`},
	})
	if !blocked || !strings.Contains(out.output, "evidence required") {
		t.Fatalf("relaxed mode without any read must still block: %+v (blocked=%v)", out, blocked)
	}
}

// TestRelaxedGateDoesNotBlockShellWrite pins scenario 2 (task 789): with the
// relaxed switch on, an outstanding read requirement no longer hard-blocks a
// bash write — the block degrades to a host-side warning and the call
// proceeds. Strict mode keeps the block.
func TestRelaxedGateDoesNotBlockShellWrite(t *testing.T) {
	build := func(relaxed bool) *Agent {
		reg := tool.NewRegistry()
		reg.Add(evidenceWriter{target: tool.EvidenceTargetInfo{
			Path: "/w/a.go", WholeFile: true, Hashes: hashesFor("alpha"),
		}})
		reg.Add(undeclaredWriter{name: "bash"})
		a := New(&userInputCaptureProvider{}, reg, NewSession("system"), Options{EditEvidenceRelaxed: relaxed}, event.Discard)
		a.task.ledger = evidence.NewLedger()
		return a
	}

	a := build(true)
	if out, blocked := runEvidenceGate(a, "/w/a.go"); !blocked {
		t.Fatalf("fixture: the declarer must be blocked first even relaxed: %+v", out)
	}
	plan := &toolCallPlan{call: provider.ToolCall{Name: "bash", Arguments: `{"command":"echo x > /w/a.go"}`}}
	if out, blocked := a.applyEvidenceGates(context.Background(), plan); blocked {
		t.Fatalf("relaxed mode: an outstanding requirement must not hard-block the shell write: %+v", out)
	}

	b := build(false)
	if out, blocked := runEvidenceGate(b, "/w/a.go"); !blocked {
		t.Fatalf("fixture: the declarer must be blocked first: %+v", out)
	}
	plan = &toolCallPlan{call: provider.ToolCall{Name: "bash", Arguments: `{"command":"echo x > /w/a.go"}`}}
	out, blocked := b.applyEvidenceGates(context.Background(), plan)
	if !blocked || !strings.Contains(out.output, "cannot declare which files it changes") {
		t.Fatalf("strict mode must keep blocking the unknown-scope writer: %+v (blocked=%v)", out, blocked)
	}
}

// TestRelaxedPreflightLeavesTheVerdictToExecution pins the batch-face change:
// strict mode reports the missing evidence before anything runs; relaxed mode
// defers to the execution-time gate, where a same-batch read that already ran
// can pay for the write.
func TestRelaxedPreflightLeavesTheVerdictToExecution(t *testing.T) {
	writer := evidenceWriter{target: tool.EvidenceTargetInfo{
		Path: "/w/a.go", WholeFile: true, Hashes: hashesFor("alpha"),
	}}
	relaxed, _ := newRelaxedEvidenceAgent(t, true, writer)
	calls := []provider.ToolCall{{ID: "c1", Name: "write_file", Arguments: `{"path":"/w/a.go"}`}}
	if blocked := relaxed.preflightEvidenceBatch(context.Background(), calls); len(blocked) != 0 {
		t.Fatalf("relaxed preflight must not hard-block: %v", blocked)
	}

	strict, _ := newRelaxedEvidenceAgent(t, false, writer)
	if blocked := strict.preflightEvidenceBatch(context.Background(), calls); len(blocked) != 1 {
		t.Fatalf("strict preflight must keep blocking before the batch runs: %v", blocked)
	}
}

// TestRelaxedGateSameBatchReadPaysForEdit is the end-to-end scenario 1: one
// provider batch [read_file, edit_file]. Relaxed mode lets the read that ran
// earlier in the batch pay for the edit; strict mode blocks the edit before it
// starts (the pre-789 baseline) and leaves the file untouched.
func TestRelaxedGateSameBatchReadPaysForEdit(t *testing.T) {
	build := func(relaxed bool) (*Agent, string) {
		path := filepath.Join(t.TempDir(), "script.sh")
		if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		read, ok := tool.LookupBuiltin("read_file")
		if !ok {
			t.Fatal("read_file builtin is not registered")
		}
		edit, ok := tool.LookupBuiltin("edit_file")
		if !ok {
			t.Fatal("edit_file builtin is not registered")
		}
		a, _ := newRelaxedEvidenceAgent(t, relaxed, read, edit)
		return a, path
	}
	args := func(path string) []provider.ToolCall {
		read, err := json.Marshal(map[string]any{"path": path})
		if err != nil {
			t.Fatal(err)
		}
		edit, err := json.Marshal(map[string]any{"path": path, "old_string": "beta", "new_string": "changed"})
		if err != nil {
			t.Fatal(err)
		}
		return []provider.ToolCall{
			{ID: "r1", Name: "read_file", Arguments: string(read)},
			{ID: "e1", Name: "edit_file", Arguments: string(edit)},
		}
	}

	a, path := build(true)
	results := relaxedEvidenceRound(t, a, args(path))
	if strings.Contains(results[1], "blocked") {
		t.Fatalf("relaxed mode: the same-batch read must pay for the edit: %q", results[1])
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "alpha\nchanged\ngamma\n" {
		t.Fatalf("relaxed mode: the edit must have applied, file = %q", content)
	}

	b, strictPath := build(false)
	results = relaxedEvidenceRound(t, b, args(strictPath))
	if !strings.Contains(results[1], "evidence required") {
		t.Fatalf("strict mode: the edit must stay blocked before it starts (baseline): %q", results[1])
	}
	content, err = os.ReadFile(strictPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "alpha\nbeta\ngamma\n" {
		t.Fatalf("strict mode: the file must stay untouched, file = %q", content)
	}
}
