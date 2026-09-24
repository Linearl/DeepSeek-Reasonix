package agent

import (
	"strings"
	"testing"
	"time"

	"reasonix/internal/evidence"
	"reasonix/internal/tool"
)

func TestSoftBudgetUsesRollingMedianElapsedTime(t *testing.T) {
	a := &Agent{}
	a.modelRef = t.Name()
	key := a.softBudgetHistoryKey()
	t.Cleanup(func() {
		readonlySoftBudgetHistory.Lock()
		delete(readonlySoftBudgetHistory.byKey, key)
		readonlySoftBudgetHistory.Unlock()
	})
	for _, sample := range []time.Duration{90, 95, 100, 105, 110} {
		recordReadonlySoftBudgetDuration(key, sample*time.Millisecond)
	}
	a.turn.budget = runBudget{started: time.Now().Add(-250 * time.Millisecond), rounds: 2}
	first := a.applySoftBudget(nil)
	if first.verdict != verdictRedirect || !strings.Contains(first.notice.Detail, "2x rolling median") {
		t.Fatalf("elapsed-time nudge = %+v", first)
	}

	a.turn.budget.rounds = 4
	second := a.applySoftBudget(nil)
	if second.verdict != verdictRedirect || !strings.Contains(second.guidance, "two further rounds") {
		t.Fatalf("hard follow-up = %+v", second)
	}
}

func TestExtendResearchBudgetDoublesGatesAndCapsAtThree(t *testing.T) {
	// Before the nudge the tool is refused: the model must not pre-emptively
	// raise its own budget.
	fresh := &Agent{}
	fresh.turn.budget = runBudget{started: time.Now(), rounds: 1}
	if _, _, err := fresh.ExtendResearchBudget("curious"); err == nil {
		t.Fatal("extension before the nudge should be refused")
	}
	if _, _, err := fresh.ExtendResearchBudget("   "); err == nil {
		t.Fatal("extension without a reason should be refused")
	}

	// researchBudget on: the nudge offers the extension (task 240 direction 2
	// made this conditional — with the feature off the old text pointed at a
	// tool that could not run). researchBudget lives on the embedded
	// agentConfig, so it is assigned after construction, not set in the literal.
	a := &Agent{}
	a.researchBudget = true
	a.modelRef = t.Name()
	a.turn.budget = runBudget{started: time.Now(), rounds: readonlySoftBudgetRounds}

	nudge := a.applySoftBudget(nil)
	if nudge.verdict != verdictRedirect || !strings.Contains(nudge.guidance, "extend_research_budget") {
		t.Fatalf("first nudge should offer the extension: %+v", nudge)
	}

	ext, text, err := a.ExtendResearchBudget("the conflict surface needs another pass")
	if err != nil || ext.Rounds != 20 || ext.Remaining != 2 {
		t.Fatalf("first extension = %+v %q %v", ext, text, err)
	}
	if !strings.Contains(text, "20 rounds") || !strings.Contains(text, "2 extension") {
		t.Fatalf("extension text = %q", text)
	}

	// Inside the doubled budget no further nudge fires.
	a.turn.budget.rounds = 15
	if got := a.applySoftBudget(nil); got.verdict != verdictContinue {
		t.Fatalf("round 15 under the doubled budget should not nudge: %+v", got)
	}
	// The extension re-armed the nudge: it fires again at the new limit.
	a.turn.budget.rounds = 20
	if got := a.applySoftBudget(nil); got.verdict != verdictRedirect {
		t.Fatalf("round 20 should nudge again: %+v", got)
	}

	for _, want := range []int{40, 80} {
		ext, _, err := a.ExtendResearchBudget("still digging")
		if err != nil || ext.Rounds != want {
			t.Fatalf("extension to %d = %+v %v", want, ext, err)
		}
	}
	if _, _, err := a.ExtendResearchBudget("one more"); err == nil {
		t.Fatal("a fourth extension should be refused")
	}

	// The time gate doubles with the same counter: 3 extensions mean 16x the
	// rolling median instead of 2x.
	key := a.softBudgetHistoryKey()
	t.Cleanup(func() {
		readonlySoftBudgetHistory.Lock()
		delete(readonlySoftBudgetHistory.byKey, key)
		readonlySoftBudgetHistory.Unlock()
	})
	for _, sample := range []time.Duration{100, 100, 100, 100, 100} {
		recordReadonlySoftBudgetDuration(key, sample*time.Millisecond)
	}
	a.turn.budget = runBudget{started: time.Now().Add(-1 * time.Second), rounds: 1}
	if got := a.applySoftBudget(nil); got.verdict != verdictContinue {
		t.Fatalf("1s elapsed is inside 16x100ms after three extensions: %+v", got)
	}
	a.turn.budget = runBudget{started: time.Now().Add(-2 * time.Second), rounds: 1}
	if got := a.applySoftBudget(nil); got.verdict != verdictRedirect {
		t.Fatalf("2s elapsed should exceed 16x100ms: %+v", got)
	}
}

func TestExtendResearchBudgetToolFailsClosedWithoutExtender(t *testing.T) {
	builtin, ok := tool.LookupBuiltin("extend_research_budget")
	if !ok {
		t.Fatal("extend_research_budget builtin not registered")
	}
	if _, err := builtin.Execute(t.Context(), []byte(`{"reason":"need more evidence"}`)); err == nil {
		t.Fatal("the tool must fail closed outside an agent turn")
	}
}

// Task 240, direction 1 / acceptance 1: once the session produced (or tried to
// produce) a write, later read-only-looking turns must not be nudged as
// "read-only planning/analysis" — including the cross-turn case where the
// per-turn ledger has reset, and the Mutation-only receipt whose tool is not
// on the writer-tool list (Write=false).
func TestSoftBudgetNeverNudgesASessionThatWrote(t *testing.T) {
	// Scenario A: the write happened as an outcome of an earlier turn; the
	// fresh turn now looks read-only and is over the round limit.
	a := &Agent{}
	a.modelRef = t.Name()
	a.turn.budget = runBudget{started: time.Now(), rounds: 1}
	if got := a.applySoftBudget([]toolOutcome{{resolved: true}}); got.verdict != verdictContinue {
		t.Fatalf("the writing turn itself must not nudge: %+v", got)
	}
	if !a.softBudgetMutationSeen {
		t.Fatal("a write must be remembered for the whole session")
	}
	// Fresh turn, read-only outcomes, budget blown: still no nudge.
	a.turn = turnRuntime{budget: runBudget{started: time.Now(), rounds: readonlySoftBudgetRounds}}
	if got := a.applySoftBudget(nil); got.verdict == verdictRedirect {
		t.Fatalf("a session that wrote must not be nudged as read-only: %+v", got)
	}

	// Scenario B: a Mutation-only receipt (Write=false because the tool is
	// not on the writer-tool list) still proves implementation work.
	b := &Agent{}
	b.modelRef = t.Name() + "/mutation-receipt"
	b.task.ledger = evidence.NewLedger()
	b.task.ledger.Record(evidence.Receipt{Mutation: true})
	b.turn = turnRuntime{budget: runBudget{started: time.Now(), rounds: readonlySoftBudgetRounds}}
	if got := b.applySoftBudget(nil); got.verdict == verdictRedirect {
		t.Fatalf("a Mutation receipt must exempt the session like a Write receipt: %+v", got)
	}
}

// Task 240, direction 3 / acceptance 3: rounds whose tools failed or were
// blocked never advance the convergence gate — the failed-round counter grows,
// the effective round count stays discounted, and no nudge fires from errors.
func TestSoftBudgetFailedRoundsDoNotTightenGate(t *testing.T) {
	a := &Agent{}
	a.modelRef = t.Name()
	failed := toolOutcome{errMsg: "edit_file: WRITE_EVIDENCE_MISSING", blocked: true}
	for i := 1; i <= readonlySoftBudgetRounds; i++ {
		a.turn.budget = runBudget{started: time.Now(), rounds: i}
		if got := a.applySoftBudget([]toolOutcome{failed}); got.verdict == verdictRedirect {
			t.Fatalf("failed round %d must not tighten the gate: %+v", i, got)
		}
	}
	if a.turn.softBudgetFailedRounds != readonlySoftBudgetRounds {
		t.Fatalf("failed rounds counted = %d, want %d", a.turn.softBudgetFailedRounds, readonlySoftBudgetRounds)
	}
	// A later successful round adds to the cost axis but not the failed
	// counter: effective rounds = rounds - failed = 11 - 10 = 1 < 10.
	a.turn.budget = runBudget{started: time.Now(), rounds: readonlySoftBudgetRounds + 1}
	if got := a.applySoftBudget(nil); got.verdict == verdictRedirect {
		t.Fatalf("one successful round after ten failures is not ten rounds of progress: %+v", got)
	}
	if a.turn.softBudgetFailedRounds != readonlySoftBudgetRounds {
		t.Fatalf("a successful round must not increment the failed counter: %d", a.turn.softBudgetFailedRounds)
	}
}

// Task 240, direction 4 + direction 2 / acceptance 2 and 4: the nudge states
// its scope (per-turn read-only convergence gate, not a session-wide cap —
// the session has no fixed total limit), names the shaped capability id the
// proxy accepts, and degrades to converge-only instructions when the extension
// feature is off instead of pointing at a tool that cannot run.
func TestSoftBudgetGuidanceScopesTurnBudgetAndNamesShapedID(t *testing.T) {
	on := &Agent{}
	on.researchBudget = true
	on.modelRef = t.Name()
	on.turn = turnRuntime{budget: runBudget{started: time.Now(), rounds: readonlySoftBudgetRounds}}
	n := on.applySoftBudget(nil)
	if n.verdict != verdictRedirect {
		t.Fatalf("nudge expected: %+v", n)
	}
	for _, want := range []string{
		"tool:extend_research_budget",
		"per-turn convergence budget",
		"not a session-wide resource cap",
		"no fixed total limit",
	} {
		if !strings.Contains(n.guidance, want) {
			t.Fatalf("guidance missing %q:\n%s", want, n.guidance)
		}
	}

	off := &Agent{}
	off.modelRef = t.Name() + "/extension-off"
	off.turn = turnRuntime{budget: runBudget{started: time.Now(), rounds: readonlySoftBudgetRounds}}
	o := off.applySoftBudget(nil)
	if o.verdict != verdictRedirect {
		t.Fatalf("nudge expected with the feature off too: %+v", o)
	}
	if strings.Contains(o.guidance, "extend_research_budget") {
		t.Fatalf("feature-off guidance must not point at the tool:\n%s", o.guidance)
	}
	if !strings.Contains(o.guidance, "not enabled") {
		t.Fatalf("feature-off guidance must say extensions are off:\n%s", o.guidance)
	}

	// Acceptance 2, live channel: on a nudged turn with the extender stamped
	// (what Run does when the feature is on), the tool the guidance names
	// actually doubles the budget.
	builtin, ok := tool.LookupBuiltin("extend_research_budget")
	if !ok {
		t.Fatal("extend_research_budget builtin not registered")
	}
	out, err := builtin.Execute(tool.WithResearchBudgetExtender(t.Context(), on), []byte(`{"reason":"the conflict surface needs another pass"}`))
	if err != nil {
		t.Fatalf("nudged turn must be able to extend: %v", err)
	}
	if !strings.Contains(out, "20 rounds") {
		t.Fatalf("extension output = %q", out)
	}
}

// Task 240, direction 2 / acceptance 2: the bare-id dead end is gone — the
// error names both id shapes and the concrete budget-nudge id, instead of the
// bare "requires an mcp-tool capability id" with no way out.
func TestUseCapabilityBareIDErrorNamesShapedIDs(t *testing.T) {
	_, _, err := parseMCPCapabilityID("extend_research_budget")
	if err == nil {
		t.Fatal("a bare id must still be rejected")
	}
	msg := err.Error()
	for _, want := range []string{"tool:<name>", "tool:extend_research_budget", "mcp-tool:<server>/<tool>"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error must point at %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "requires an mcp-tool capability id") {
		t.Fatalf("old dead-end text survived:\n%s", msg)
	}
}
