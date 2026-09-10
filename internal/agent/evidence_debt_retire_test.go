package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// flippingEvidenceWriter declares a target successfully, then starts failing -
// the shape of an edit whose old_string the model removed by rewriting the file
// wholesale (which is what the evidence gate asked for).
type flippingEvidenceWriter struct {
	target tool.EvidenceTargetInfo
	err    error
}

func (w *flippingEvidenceWriter) Name() string        { return "write_file" }
func (w *flippingEvidenceWriter) Description() string { return "fake writer" }
func (w *flippingEvidenceWriter) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (w *flippingEvidenceWriter) ReadOnly() bool { return false }
func (w *flippingEvidenceWriter) Execute(context.Context, json.RawMessage) (string, error) {
	return "", nil
}
func (w *flippingEvidenceWriter) DeclareEvidenceTarget(context.Context, json.RawMessage) (tool.EvidenceTargetInfo, error) {
	return w.target, w.err
}

// Task 42: once a blocked writer can no longer declare its target, the debt can
// never be replayed to satisfaction. It must retire instead of locking every
// target-less write (bash python/sed, out-of-workspace writes) for the turn.
func TestEvidenceGateRetiresDebtWhoseTargetBecameInvalid(t *testing.T) {
	writer := &flippingEvidenceWriter{target: tool.EvidenceTargetInfo{
		Path: "/w/a.go", WholeFile: true, Hashes: hashesFor("alpha"),
	}}
	a, _ := newEvidenceAgent(t, writer, true)
	a.svc.tools.Add(undeclaredWriter{name: "bash"})

	if out, blocked := runEvidenceGate(a, "/w/a.go"); !blocked {
		t.Fatalf("the first write must still be blocked: %+v", out)
	}

	// The model rewrote the file, so the original target no longer resolves.
	writer.err = errors.New("old_string not found in /w/a.go")

	// Re-evaluating the debt must retire it rather than keep it forever.
	if outstanding := a.outstandingReadEvidence(context.Background(), 0); len(outstanding) != 0 {
		t.Fatalf("an invalid target must retire the debt, got %v", outstanding)
	}

	plan := &toolCallPlan{call: provider.ToolCall{Name: "bash", Arguments: `{"command":"python -B x.py"}`}}
	if out, blocked := a.applyEvidenceGates(context.Background(), plan); blocked {
		t.Fatalf("a retired debt must not lock target-less writes: %+v", out)
	}
}

// Task 42 P1-1: a write that landed proves the model saw the file, so the debt
// for those paths retires even while the blocked call itself still resolves.
func TestRetireReadEvidenceClearsLandedPaths(t *testing.T) {
	a, _ := newEvidenceAgent(t, evidenceWriter{target: tool.EvidenceTargetInfo{
		Path: "/w/a.go", WholeFile: true, Hashes: hashesFor("alpha"),
	}}, true)
	a.svc.tools.Add(undeclaredWriter{name: "bash"})

	if _, blocked := runEvidenceGate(a, "/w/a.go"); !blocked {
		t.Fatal("the first write must be blocked")
	}
	a.retireReadEvidence([]string{"/w/a.go"})

	if outstanding := a.outstandingReadEvidence(context.Background(), 0); len(outstanding) != 0 {
		t.Fatalf("a landed write must retire its debt, got %v", outstanding)
	}
	if strings.Contains(strings.Join(a.turn.evidenceBlocked.snapshot(), ","), "/w/a.go") {
		t.Fatal("the retired path must leave the blocked table")
	}
}
