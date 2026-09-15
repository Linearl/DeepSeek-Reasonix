package control

import (
	"context"
	"errors"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/goaleval"
	"reasonix/internal/permission"
	"reasonix/internal/provider"
)

// TestAutopilotDisablesAutoGuardArming pins task 109 B2: under autopilot the
// recovery gate must not arm, even when the interactive posture is auto. It
// asserts the mode provider the gate actually reads - constructing a controller
// without an executor leaves the gate nil, and a t.Skip there silently retired
// the only coverage of this task.
func TestAutopilotDisablesAutoGuardArming(t *testing.T) {
	c := New(Options{Policy: permission.Policy{Mode: permission.Allow}})
	c.SetToolApprovalMode(ToolApprovalAuto)
	c.autopilot = true
	if got := c.recoveryGateMode(); got != ToolApprovalYolo {
		t.Fatalf("autopilot gate mode = %q, want %q: the gate must bypass itself", got, ToolApprovalYolo)
	}
	c.autopilot = false
	if got := c.recoveryGateMode(); got != ToolApprovalAuto {
		t.Fatalf("interactive gate mode = %q, want %q", got, ToolApprovalAuto)
	}
}

// TestInspectRunPauseRecoveryPaused pins task 109 B5's error classification.
func TestInspectRunPauseRecoveryPaused(t *testing.T) {
	err := &agent.RecoveryPauseError{Message: "Automatic retries paused."}
	info, ok := agent.InspectRunPause(err)
	if !ok || info.Kind != "recovery_paused" || !info.HostOwned {
		t.Fatalf("InspectRunPause = %+v, %v; want recovery_paused host-owned", info, ok)
	}
	// Interactive goalPauseFromRunError does not absorb it — the orchestrator
	// only maps it under autopilot (TestRecoveryPauseKeepsGoalRunningAndDeliveryScope).
	if _, _, ok := goalPauseFromRunError(err); ok {
		t.Fatal("interactive path must not absorb RecoveryPauseError via goalPauseFromRunError")
	}
}

// TestGoalAutopilotRecoveryPauseIsTerminal pins task 109 B5: an unattended
// RecoveryPauseError becomes GoalStatusFailed, not a fake-Running pause.
func TestGoalAutopilotRecoveryPauseIsTerminal(t *testing.T) {
	g := &goalMachine{autopilot: true, goal: "ship it", status: GoalStatusRunning}
	res := g.advance(goalAdvanceInput{
		pauseCause:  stopCauseRecoveryPaused,
		pauseReason: "Automatic retries paused.",
	})
	if res.cont {
		t.Fatalf("autopilot recovery pause must not continue: %+v", res)
	}
	if g.status != GoalStatusFailed {
		t.Fatalf("status = %q, want %q", g.status, GoalStatusFailed)
	}
	if g.stopCause != stopCauseRecoveryPaused {
		t.Fatalf("stopCause = %q, want %q", g.stopCause, stopCauseRecoveryPaused)
	}
}

// TestGoalInteractiveRecoveryPauseStillBlocks keeps the interactive contract
// of a resumable Blocked pause (user can send continue).
func TestGoalInteractiveRecoveryPauseStillBlocks(t *testing.T) {
	g := &goalMachine{goal: "ship it", status: GoalStatusRunning}
	res := g.advance(goalAdvanceInput{
		pauseCause:  stopCauseRecoveryPaused,
		pauseReason: "Automatic retries paused.",
	})
	if res.cont {
		t.Fatalf("interactive recovery pause must not continue: %+v", res)
	}
	if g.status != GoalStatusBlocked {
		t.Fatalf("status = %q, want %q (user can send continue)", g.status, GoalStatusBlocked)
	}
}

// TestGoalAutopilotAskUnansweredIsTerminal pins task 109 B4.
func TestGoalAutopilotAskUnansweredIsTerminal(t *testing.T) {
	cause, reason, ok := goalPauseFromRunError(ErrAutopilotAskUnanswered)
	if !ok || cause != stopCauseAskUnanswered {
		t.Fatalf("goalPauseFromRunError ask = %q, %q, %v", cause, reason, ok)
	}
	g := &goalMachine{autopilot: true, goal: "ship it", status: GoalStatusRunning}
	res := g.advance(goalAdvanceInput{pauseCause: cause, pauseReason: reason})
	if res.cont || g.status != GoalStatusFailed {
		t.Fatalf("status=%q cont=%v want Failed/stopped", g.status, res.cont)
	}
}

// TestReviewUnattendedApprovalRefusesWithoutReviewer pins task 109 B6: after
// grace, a missing reviewer refuses instead of parking the run.
func TestReviewUnattendedApprovalRefusesWithoutReviewer(t *testing.T) {
	c := New(Options{Policy: permission.Policy{Mode: permission.Allow}})
	c.guardianSess = nil
	dec, decided := c.reviewUnattendedApproval(context.Background(), "write_file", "a.txt", "local edit", nil)
	if !decided {
		t.Fatal("unattended approval must decide (refuse) when no reviewer is configured")
	}
	if dec.allow {
		t.Fatal("missing reviewer must refuse, not allow")
	}
}

// TestReviewUnattendedApprovalKeywordScreenStillRefuses keeps the hard screen.
func TestReviewUnattendedApprovalKeywordScreenStillRefuses(t *testing.T) {
	c := New(Options{Policy: permission.Policy{Mode: permission.Allow}})
	dec, decided := c.reviewUnattendedApproval(context.Background(), "bash", "force push origin main", "publish", nil)
	if !decided || dec.allow {
		t.Fatalf("keyword screen must refuse, decided=%v allow=%v", decided, dec.allow)
	}
}

// TestAutopilotAskReversibleStillSelfAnswers keeps A3 behaviour.
func TestAutopilotAskReversibleStillSelfAnswers(t *testing.T) {
	c := New(Options{Policy: permission.Policy{Mode: permission.Allow}, Autopilot: true, AutopilotMaxRuntime: time.Hour})
	answers, err := c.Ask(context.Background(), []event.AskQuestion{{
		ID: "q1", Prompt: "Should I edit README in the workspace?", Options: []event.AskOption{{Label: "yes"}},
	}})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(answers) != 1 || len(answers[0].Selected) == 0 {
		t.Fatalf("reversible ask under autopilot must self-answer, got %+v", answers)
	}
}

// TestGoalAutopilotEvaluatorUncertainContinuesThenFails pins task 109 B7.
func TestGoalAutopilotEvaluatorUncertainContinuesThenFails(t *testing.T) {
	g := &goalMachine{autopilot: true, goal: "ship it", status: GoalStatusRunning}
	res := g.advance(goalAdvanceInput{
		evaluator: &goalEvaluatorVerdict{outcome: goaleval.OutcomeUncertain},
	})
	if g.status == GoalStatusFailed {
		t.Fatalf("first uncertain evaluator must not fail the goal, status=%q res=%+v", g.status, res)
	}
	if g.evaluatorFailTurns != 1 {
		t.Fatalf("evaluatorFailTurns = %d, want 1", g.evaluatorFailTurns)
	}
	g.advance(goalAdvanceInput{evaluator: &goalEvaluatorVerdict{outcome: goaleval.OutcomeUncertain}})
	if g.evaluatorFailTurns != 2 || g.status == GoalStatusFailed {
		t.Fatalf("second uncertain still continues, turns=%d status=%q", g.evaluatorFailTurns, g.status)
	}
	g.advance(goalAdvanceInput{evaluator: &goalEvaluatorVerdict{outcome: goaleval.OutcomeUncertain}})
	if g.status != GoalStatusFailed || g.stopCause != stopCauseEvaluator {
		t.Fatalf("third uncertain must Failed, status=%q cause=%q", g.status, g.stopCause)
	}
}

// TestGoalInteractiveEvaluatorUncertainStillBlocked keeps interactive fail-closed.
func TestGoalInteractiveEvaluatorUncertainStillBlocked(t *testing.T) {
	g := &goalMachine{goal: "ship it", status: GoalStatusRunning}
	g.advance(goalAdvanceInput{evaluator: &goalEvaluatorVerdict{outcome: goaleval.OutcomeUncertain}})
	if g.status != GoalStatusBlocked || g.stopCause != stopCauseEvaluator {
		t.Fatalf("interactive uncertain must Blocked, status=%q cause=%q", g.status, g.stopCause)
	}
}

type noopProv struct{}

func (noopProv) Name() string { return "noop" }
func (noopProv) Stream(context.Context, provider.Request) (<-chan provider.Chunk, error) {
	return nil, errors.New("unused")
}
