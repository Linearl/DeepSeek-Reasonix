package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// blockedTodoRefusal is the refusal the first-writer contract guard returns; it
// is one constraint read through any writing tool.
const blockedTodoRefusal = "blocked: establish a concrete todo and acceptance criteria before this class of write"

func newStormAgent(t *testing.T) (*Agent, *[]string) {
	t.Helper()
	reg := tool.NewRegistry()
	reg.Add(failTool{name: "write_file"})
	sink, notices := noticeRecorder()
	return New(nil, reg, NewSession(""), Options{}, sink), notices
}

// TestStormBreakerStillBlocksARepeatingCall is the "keep the guard" half of task
// 171: the same call failing the same way must still be caught, and it must NOT
// be described as a constraint surface.
func TestStormBreakerStillBlocksARepeatingCall(t *testing.T) {
	a, _ := newStormAgent(t)
	calls := []provider.ToolCall{{Name: "write_file"}}
	outcomes := []toolOutcome{{errMsg: "unexpected end of JSON input"}}
	var last intervention
	for i := 0; i < stormBreakThreshold; i++ {
		last = a.applyStormBreaker(calls, outcomes, 0)
	}
	if !strings.Contains(last.guidance, "[loop guard]") {
		t.Fatalf("a repeated identical failure must still be guarded, got %q", last.guidance)
	}
	if strings.Contains(last.guidance, "constraint surface") {
		t.Fatal("a repeated call is not a constraint surface")
	}
	if last.verdict != verdictRedirect {
		t.Fatalf("verdict = %v, want redirect for a repeated call", last.verdict)
	}
}

// TestStormBreakerTreatsAUniformConstraintRefusalAsSurface is the "stop the
// mis-fire" half: rounds that were each refused by the SAME host constraint are
// a surface, not a loop. The agent cannot change approach its way out, so the
// guard must name the recovery path (the next user message) and land the turn.
func TestStormBreakerTreatsAUniformConstraintRefusalAsSurface(t *testing.T) {
	a, _ := newStormAgent(t)
	var last intervention
	for i := 0; i < stormBreakThreshold; i++ {
		// A different tool every round: the constraint, not the tool, is what
		// stays in the way.
		calls := []provider.ToolCall{{Name: fmt.Sprintf("tool_%d", i)}}
		last = a.applyStormBreaker(calls, []toolOutcome{{blocked: true, errMsg: blockedTodoRefusal}}, 0)
	}
	if last.verdict != verdictLand {
		t.Fatalf("verdict = %v, want land: a constraint surface must stop the retry loop", last.verdict)
	}
	if !strings.Contains(last.guidance, "constraint surface") || !strings.Contains(last.guidance, "next message") {
		t.Fatalf("guidance must name the constraint surface and the recovery path, got %q", last.guidance)
	}
}

// TestStormBreakerKeepsMixedBlockedRoundsAsALoop guards the boundary: blocked
// rounds refused for DIFFERENT reasons are not one surface, so the existing
// change-approach redirect stays.
func TestStormBreakerKeepsMixedBlockedRoundsAsALoop(t *testing.T) {
	a, _ := newStormAgent(t)
	reasons := []string{
		"blocked: [evidence required] edit_file targets a.go, but the model has not seen its current content",
		"blocked: plan mode is read-only",
		"blocked: the permission hook denied bash",
	}
	var last intervention
	for i, reason := range reasons {
		calls := []provider.ToolCall{{Name: fmt.Sprintf("tool_%d", i)}}
		last = a.applyStormBreaker(calls, []toolOutcome{{blocked: true, errMsg: reason}}, 0)
	}
	if strings.Contains(last.guidance, "constraint surface") {
		t.Fatalf("three different refusals are not one surface, got %q", last.guidance)
	}
	if !strings.Contains(last.guidance, "[loop guard]") || last.verdict != verdictRedirect {
		t.Fatalf("mixed blocked rounds must keep the existing redirect, got verdict=%v %q", last.verdict, last.guidance)
	}
}

// TestRefusedWriteCountsAsANonReadOnlySignal pins task 171's second fix: a write
// the host refused still proves the turn is not a read-only investigation, so
// the read-only budget must not keep tightening (that pairing was the loop).
func TestRefusedWriteCountsAsANonReadOnlySignal(t *testing.T) {
	a, _ := newStormAgent(t)
	blockedWrite := []toolOutcome{{blocked: true, diagnostic: &tool.OperationDiagnostic{Code: tool.WriteEvidenceMissing}}}
	if a.readonlySoftBudgetApplies(blockedWrite) {
		t.Fatal("a refused write must not keep the round classified as read-only")
	}
	if !a.turn.softBudgetMutation {
		t.Fatal("the refusal must set the non-read-only signal")
	}

	b, _ := newStormAgent(t)
	blockedRead := []toolOutcome{{blocked: true, resolved: true, resolvedReadOnly: true}}
	if !b.readonlySoftBudgetApplies(blockedRead) {
		t.Fatal("a blocked read-only call is still a read-only round")
	}
}

// TestLoopGuardWritesAPendingHandoff pins task 171's third fix: before a guard
// stops the turn, the model's latest text reaches the session directory so a
// crash cannot lose the handoff.
func TestLoopGuardWritesAPendingHandoff(t *testing.T) {
	dir := t.TempDir()
	a, _ := newStormAgent(t)
	a.sess.path = filepath.Join(dir, "20260101-000000.000000000-test.jsonl")
	a.Session().Add(provider.Message{Role: provider.RoleAssistant, Content: "Handoff: 12 tests green, commit pending."})

	for i := 0; i < stormBreakThreshold; i++ {
		a.applyStormBreaker([]provider.ToolCall{{Name: "write_file"}},
			[]toolOutcome{{blocked: true, errMsg: blockedTodoRefusal}}, 0)
	}
	data, err := os.ReadFile(filepath.Join(dir, pendingHandoffFile))
	if err != nil {
		t.Fatalf("pending handoff not written: %v", err)
	}
	if !strings.Contains(string(data), "Handoff: 12 tests green") {
		t.Fatalf("handoff must carry the latest model text, got %q", string(data))
	}
}
