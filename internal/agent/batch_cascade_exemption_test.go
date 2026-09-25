package agent

import (
	"encoding/json"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// Task 244 B8: pin the four fail-cascade classification rules documented on
// batchCallMutationFailureCause (Reasonix's side of MiMo #2463's exemption
// face). The existing effect-matrix test drives bash commands; this table
// covers the classification axes themselves.

func b8Call(t *testing.T, name string, args map[string]string) provider.ToolCall {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return provider.ToolCall{ID: "call-1", Name: name, Arguments: string(raw)}
}

func TestBatchCascadeExemptionFace(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "ro_file", readOnly: true})
	reg.Add(fakeTool{name: "write_file", readOnly: false})
	reg.Add(fakeTool{name: "bash", readOnly: false})
	a := New(nil, reg, NewSession(""), Options{}, event.Discard)

	// Rule 1: an effective read/search failure exempts — non-mutation
	// classification returns nil regardless of the tool's own failure.
	readCall := b8Call(t, "ro_file", map[string]string{"path": "a.txt"})
	if cause := batchCallMutationFailureCause(a, readCall, toolOutcome{errMsg: "failed"}); cause != nil {
		t.Fatalf("rule 1: read failure must exempt the cascade, got %+v", cause)
	}

	// Rule 2: every side-effect failure closes the batch, both phases.
	writeCall := b8Call(t, "write_file", map[string]string{"path": "a.txt", "content": "x"})
	cause := batchCallMutationFailureCause(a, writeCall, toolOutcome{errMsg: "failed"})
	if cause == nil {
		t.Fatal("rule 2: side-effect failure must close the batch")
	}
	if cause.blockingPhase != "failed" {
		t.Fatalf("rule 2: phase = %q, want failed", cause.blockingPhase)
	}
	cause = batchCallMutationFailureCause(a, writeCall, toolOutcome{blocked: true, errMsg: "blocked"})
	if cause == nil {
		t.Fatal("rule 2: a BLOCKED write is still a write — it must close the batch too")
	}
	if cause.blockingPhase != "blocked" {
		t.Fatalf("rule 2: phase = %q, want blocked", cause.blockingPhase)
	}

	// Rule 3: read-only verification commands exempt like rule 1.
	verifyCall := b8Call(t, "bash", map[string]string{"command": "node --check app.js"})
	if cause := batchCallMutationFailureCause(a, verifyCall, toolOutcome{errMsg: "failed"}); cause != nil {
		t.Fatalf("rule 3: verification failure must exempt, got %+v", cause)
	}

	// Rule 4: unknown classification fails closed — an unproven call never
	// reads as safe.
	unknownCall := b8Call(t, "vanished_tool", map[string]string{"x": "y"})
	if cause := batchCallMutationFailureCause(a, unknownCall, toolOutcome{errMsg: "failed"}); cause == nil {
		t.Fatal("rule 4: unknown classification must fail closed (writer side)")
	}
}
