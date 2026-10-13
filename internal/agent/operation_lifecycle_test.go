package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/evidence"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

func writeOperationID(path string) string {
	return evidence.OperationID("write_file", json.RawMessage(`{"path":"`+path+`"}`))
}

func writeOperationPlan(path string) *toolCallPlan {
	return &toolCallPlan{call: provider.ToolCall{Name: "write_file", Arguments: `{"path":"` + path + `"}`}}
}

func TestOperationRejectionOffersMachineExecutableRecovery(t *testing.T) {
	writer := evidenceWriter{target: tool.EvidenceTargetInfo{
		Path: "/w/a.go", WholeFile: true, Hashes: hashesFor("alpha", "beta"),
	}}
	a, ledger := newEvidenceAgent(t, writer, true)
	ledger.Record(evidence.Receipt{ToolName: "bash", Command: "go test ./...", Success: true})

	out, blocked := runEvidenceGate(a, "/w/a.go")
	if !blocked || out.diagnostic == nil {
		t.Fatalf("expected a diagnosed block, got %+v", out)
	}
	d := out.diagnostic
	if !d.Retryable || d.RetryBudget != 2 {
		t.Fatalf("first rejection = retryable %v budget %d, want two offered recoveries", d.Retryable, d.RetryBudget)
	}
	if len(d.AllowedRecovery) == 0 {
		t.Fatal("rejection must name the actions the host accepts")
	}
	if len(d.AvailableReceipts) == 0 {
		t.Fatal("rejection must list citable receipt ids instead of asking for a command string")
	}
	if !strings.Contains(out.output, "recovery: {") {
		t.Fatalf("model-facing output must carry the recovery block: %q", out.output)
	}
}

func TestOperationStopsAutomaticRetryAfterTheSameRejectionThreeTimes(t *testing.T) {
	executions := 0
	writer := evidenceWriter{target: tool.EvidenceTargetInfo{
		Path: "/w/a.go", WholeFile: true, Hashes: hashesFor("alpha", "beta"),
	}, executions: &executions}
	a, _ := newEvidenceAgent(t, writer, true)

	if _, blocked := runEvidenceGate(a, "/w/a.go"); !blocked {
		t.Fatal("first attempt must be rejected")
	}
	// Task 859: the second identical rejection still carries a guided retry,
	// and its receipt names both the failure reason and the compliant path.
	// This fixture reads as WRITE_EVIDENCE_MISSING (no eligible prior-round
	// read); the STALE wording itself is pinned in
	// TestFailureGuidanceNamesReasonAndCompliantPath.
	second, blocked := runEvidenceGate(a, "/w/a.go")
	if !blocked {
		t.Fatal("second attempt must be rejected")
	}
	if !second.diagnostic.Retryable {
		t.Fatalf("second identical rejection lost its guided retry: %+v", second.diagnostic)
	}
	if !strings.Contains(second.output, "not seen its current content") || !strings.Contains(second.output, "then retry") {
		t.Fatalf("rejection receipt must name the failure reason and the compliant path, got %q", second.output)
	}
	third, blocked := runEvidenceGate(a, "/w/a.go")
	if !blocked {
		t.Fatal("third attempt must be rejected")
	}
	if third.diagnostic.Retryable {
		t.Fatalf("third identical rejection stayed retryable: %+v", third.diagnostic)
	}
	op, ok := a.operations().Get(writeOperationID("/w/a.go"))
	if !ok || op.State != evidence.OperationNeedsUser {
		t.Fatalf("operation state = %+v, want needs_user", op)
	}

	// The fourth attempt never reaches the evidence check, so it costs no tool
	// execution and no further host analysis — the loop simply ends.
	fourth, gated := a.applyOperationGate(writeOperationPlan("/w/a.go"))
	if !gated || !fourth.blocked {
		t.Fatal("a paused operation must be refused before it runs again")
	}
	if fourth.diagnostic == nil || fourth.diagnostic.Code != tool.OperationNeedsUser {
		t.Fatalf("fourth attempt diagnostic = %+v, want OPERATION_NEEDS_USER", fourth.diagnostic)
	}
	if executions != 0 {
		t.Fatalf("writer executed %d times while blocked", executions)
	}
}

func TestOperationBreakerAnnouncesOnceAndLetsTheTurnLand(t *testing.T) {
	writer := evidenceWriter{target: tool.EvidenceTargetInfo{
		Path: "/w/a.go", WholeFile: true, Hashes: hashesFor("alpha", "beta"),
	}}
	a, _ := newEvidenceAgent(t, writer, true)
	runEvidenceGate(a, "/w/a.go")
	runEvidenceGate(a, "/w/a.go")
	runEvidenceGate(a, "/w/a.go")

	iv := a.applyOperationBreaker(0)
	if iv.verdict != verdictLand || !strings.Contains(iv.guidance, "operation paused") {
		t.Fatalf("breaker intervention = %+v, want a landing verdict naming the pause", iv)
	}
	if !a.turn.loopGuardArmed {
		t.Fatal("a paused operation must let final readiness stand down")
	}
	if again := a.applyOperationBreaker(0); again.fired() {
		t.Fatal("the same pause was announced twice")
	}
}

func TestOperationBlockDoesNotLeakToOtherOperations(t *testing.T) {
	writer := evidenceWriter{target: tool.EvidenceTargetInfo{
		Path: "/w/a.go", WholeFile: true, Hashes: hashesFor("alpha", "beta"),
	}}
	a, _ := newEvidenceAgent(t, writer, true)
	runEvidenceGate(a, "/w/a.go")
	runEvidenceGate(a, "/w/a.go")
	runEvidenceGate(a, "/w/a.go")

	if _, gated := a.applyOperationGate(writeOperationPlan("/w/other.go")); gated {
		t.Fatal("an unrelated operation inherited the block")
	}
}

func TestOperationSourceChangeOpensANewRecoveryEpoch(t *testing.T) {
	writer := evidenceWriter{target: tool.EvidenceTargetInfo{
		Path: "/w/a.go", Snapshot: "ss1", Ranges: []tool.ReadRange{{Start: 0, End: 2}}, Hashes: hashesFor("alpha", "beta"),
	}}
	a, ledger := newEvidenceAgent(t, writer, true)
	runEvidenceGate(a, "/w/a.go")
	runEvidenceGate(a, "/w/a.go")
	runEvidenceGate(a, "/w/a.go")
	if _, gated := a.applyOperationGate(writeOperationPlan("/w/a.go")); !gated {
		t.Fatal("fixture: the operation should be paused before the source changes")
	}

	ledger.RecordTextObservation(evidence.TextObservation{
		Path: "/w/a.go", StartLine: 1, Snapshot: "ss2", LineHashes: hashesFor("alpha", "changed"),
	})
	a.outstandingReadEvidence(context.Background(), ledger.ObservationBoundary())

	if _, gated := a.applyOperationGate(writeOperationPlan("/w/a.go")); gated {
		t.Fatal("a changed source must reopen automatic recovery for the operation")
	}
}

// TestFailureGuidanceNamesReasonAndCompliantPath pins the task 859 receipt
// wording: every guidance names why the rejection fired and what the host will
// accept, so the model's first reaction is the compliant path — not a reworded
// copy of the same call (the capability_id lesson).
func TestFailureGuidanceNamesReasonAndCompliantPath(t *testing.T) {
	stale := failureGuidance(tool.WriteEvidenceStale)
	if !strings.Contains(stale, "your own earlier edit") || !strings.Contains(stale, "re-read") || !strings.Contains(stale, "retry") {
		t.Fatalf("WRITE_EVIDENCE_STALE guidance must name the cause and the re-read path: %q", stale)
	}
	for _, code := range []string{
		tool.WriteEvidenceMissing, tool.ReadSourceChanged,
		tool.VerificationReceiptMissing, tool.VerificationReceiptMismatch,
		"SOME_UNMAPPED_CODE",
	} {
		g := failureGuidance(code)
		if !strings.Contains(g, "re-read") && !strings.Contains(g, "read ") && !strings.Contains(g, "run the verifier") && !strings.Contains(g, "cite") {
			t.Fatalf("%s guidance offers no actionable path: %q", code, g)
		}
	}
}

// stripReceiptCitation removes the host receipt trailer so a test can assert on
// the tool's own output.
func stripReceiptCitation(result string) string {
	for _, marker := range []string{"\n[receipt ", "\n[source_token "} {
		if i := strings.LastIndex(result, marker); i >= 0 && strings.HasSuffix(result, "]") {
			return result[:i]
		}
	}
	return result
}
