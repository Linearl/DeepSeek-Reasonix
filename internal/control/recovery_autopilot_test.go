package control

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/recovery"
	"reasonix/internal/tool"
)

func newRecoveryGateController(t *testing.T, autopilot bool) *Controller {
	t.Helper()
	ag := agent.New(nil, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	opts := Options{Runner: ag, Executor: ag}
	if autopilot {
		opts.Autopilot = true
		opts.AutopilotMaxRuntime = time.Minute
	}
	c := New(opts)
	c.SetToolApprovalMode(ToolApprovalAuto)
	c.mu.Lock()
	gate := c.recoveryGate
	c.mu.Unlock()
	if gate == nil {
		t.Fatal("recovery gate not initialised")
	}
	return c
}

// unattendedController builds an autopilot session whose grace period is short
// enough to test the unattended decisions without sleeping for the default 15s.
func unattendedController(t *testing.T) *Controller {
	t.Helper()
	ag := agent.New(nil, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	return New(Options{
		Runner:                 ag,
		Executor:               ag,
		Sink:                   event.Discard,
		Autopilot:              true,
		AutopilotMaxRuntime:    time.Minute,
		AutopilotApprovalGrace: 20 * time.Millisecond,
	})
}

// Task 109 B2 (option B): Auto Guard shows a card a human must answer, and it
// never drains on a mode switch, so an unattended run would hang on it forever.
// An autopilot session reports a non-auto mode to the gate, which makes it
// bypass itself; the unattended approval path stays the A5 guardian.
func TestAutoGuardBypassedForUnattendedRun(t *testing.T) {
	obs := recovery.Observation{
		TaskID:     "task",
		Tool:       "bash",
		Args:       json.RawMessage(`{"command":"go test ./..."}`),
		Mutates:    true,
		ErrSummary: "exit status 1",
	}

	interactive := newRecoveryGateController(t, false)
	if got := interactive.recoveryGateMode(); got != ToolApprovalAuto {
		t.Fatalf("interactive gate mode = %q, want %q", got, ToolApprovalAuto)
	}
	if guidance := interactive.recoveryGate.ObserveResult(context.Background(), obs); guidance == "" {
		t.Fatal("Auto Guard did not arm under an interactive Auto session")
	}

	unattended := newRecoveryGateController(t, true)
	if got := unattended.recoveryGateMode(); got != ToolApprovalYolo {
		t.Fatalf("unattended gate mode = %q, want the gate to bypass itself", got)
	}
	if guidance := unattended.recoveryGate.ObserveResult(context.Background(), obs); guidance != "" {
		t.Fatalf("Auto Guard armed for an unattended run: %q", guidance)
	}
}

// Task 109 B4: a question only a human may answer cannot be answered by an
// unattended run, so it stops when the grace runs out instead of waiting for a
// user who is not there.
func TestUnattendedQuestionStopsInsteadOfWaiting(t *testing.T) {
	c := unattendedController(t)
	started := time.Now()
	answers, err := c.Ask(context.Background(), []event.AskQuestion{{
		ID:     "q1",
		Prompt: "Delete the release branch and push the result, or keep it?",
	}})
	if err == nil {
		t.Fatal("unattended run waited for a human instead of stopping")
	}
	if !errors.Is(err, errUnattendedQuestionNeedsHuman) {
		t.Fatalf("ask err = %v, want the unattended stop", err)
	}
	if len(answers) != 0 {
		t.Fatalf("answers = %+v, want none: the run must not answer this itself", answers)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("ask took %s; the point of the grace is to stop promptly", elapsed)
	}
}

// The same question stays a plain wait for an interactive session: only an
// unattended run trades the wait for a terminal stop.
func TestInteractiveQuestionStillWaits(t *testing.T) {
	ag := agent.New(nil, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	c := New(Options{Runner: ag, Executor: ag, Sink: event.Discard})
	if c.autopilotApprovalGrace != 0 {
		t.Fatalf("interactive grace = %v, want none", c.autopilotApprovalGrace)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := c.Ask(ctx, []event.AskQuestion{{ID: "q1", Prompt: "Delete the release branch and push the result, or keep it?"}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("interactive ask err = %v, want it to keep waiting until the caller's own deadline", err)
	}
}

// Task 109 B6: with nothing able to decide on the absent user's behalf, an
// unattended approval is refused rather than left waiting for a human who never
// arrives.
func TestUnattendedApprovalRefusesWhenNothingCanDecide(t *testing.T) {
	c := unattendedController(t)
	started := time.Now()
	allow, _, err := c.requestApprovalWithReason(context.Background(), "write_file", "main.go",
		json.RawMessage(`{"path":"main.go"}`), "edit the entry point")
	if err != nil {
		t.Fatalf("approval err = %v, want a refusal", err)
	}
	if allow {
		t.Fatal("unattended approval was granted with no reviewer to decide it")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("approval took %s; an unattended run must not wait for a human", elapsed)
	}
}

// The unattended stop reaches the Goal FSM as a terminal state, while the same
// error in an interactive session stays outside it.
func TestUnattendedAskPauseIsTerminalForTheGoal(t *testing.T) {
	cause, reason, ok := goalPauseFromRunError(errUnattendedQuestionNeedsHuman, true)
	if !ok || cause != stopCauseAskNeedsHuman {
		t.Fatalf("unattended pause = (%q, %q, %v), want cause %q", cause, reason, ok, stopCauseAskNeedsHuman)
	}
	if reason == "" {
		t.Fatal("unattended stop carried no reason to report")
	}
	if _, _, ok := goalPauseFromRunError(errUnattendedQuestionNeedsHuman, false); ok {
		t.Fatal("an interactive Goal would absorb a question it must wait for")
	}
}
