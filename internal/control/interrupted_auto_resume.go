package control

import (
	"context"
	"errors"
	"log"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// tl 1105 — interrupted-turn auto-resume (lab switch interrupted_auto_resume,
// default off, 铁律 2).
//
// 现场实锤 (2026-10-23 装机链): the coordinating line ran
// restart_update execute (which restarts the desktop process). The in-flight
// verification call riding the same turn was killed by the restart; the host
// marked the outcome unknown and surfaced the "本轮已中断" recovery panel. The
// user manually dismissed it and the session went dead — the panel carries the
// bounded recovery summary but nothing consumes it, so the next round only
// starts when the user manually sends a new message (the incident's 3-minute
// gap).
//
// This sub-option (mirrors task-544's ask-auto-continue shape) adds the missing
// trigger: when on, a turn that stops with a genuine interruption — a provider
// stream interruption whose outcome is unknown, or a context.DeadlineExceeded
// that did not also cancel the parent context — gets exactly ONE host
// continuation turn. The prompt is a fixed product string (see
// interruptedAutoResumeTurnPrompt); the agent-side recovery machinery picks up
// the already-persisted bounded handoff (outcome_unknown_tools / not_started
// / write_postcondition checks) via its normal path, so no raw tool arguments,
// assistant text, or provider error bodies are duplicated here.
//
// Self-limiting: the one-shot gate fires once per eligible stop. If the
// continuation turn itself dies with the same interruption (e.g. the provider
// stays unreachable), the resumed turn sees the gate already consumed (the
// continuation runs under a fresh orchestrated turn whose marker is cleared at
// start) and stops without chaining another one.
//
// Safety exclusions (a stop that is someone else's contract keeps it verbatim):
//   - user cancellation (context.Canceled) — deliberate, never resurrected;
//   - the gate-owned pauses (RecoveryPauseError, FinalReadinessError,
//     taskBudgetPause, maxStepsPause, CompletionUncertainError) — each already
//     owns its own "send continue" contract;
//   - ErrAutopilotAskUnanswered — the 477-off terminal stop is its own
//     contract; the task-544 lane already handles the answered-ask shape.

// interruptedAutoResumeNoticeCode is the grep-able notice code (paired with the
// [interrupted-autoresume] slog line, the same判据锚 shape task-477 established).
const interruptedAutoResumeNoticeCode = "interrupted_auto_resume"

// interruptedAutoResumeTurnPrompt is the host-owned continuation prompt. It is
// deliberately free of provider error text or tool arguments: the agent-side
// recovery handoff (already persisted) carries the bounded facts, and this
// prompt only tells the model that a bounded handoff is waiting and where the
// turn actually stopped.
const interruptedAutoResumeTurnPrompt = "A previous turn was interrupted by an unexpected host or network event, and its outcome is unknown. The bounded recovery handoff for that turn is already in the transcript — inspect it (completed / interrupted / outcome-unknown / not-started tools, write postconditions) and resume from where the turn stopped. Do not repeat writes that are already marked satisfied; re-plan the ones that are not started."

// interruptionLike stops the current turn with an outcome-unknown interruption
// (as opposed to a clean stop, a deliberate cancellation, or a gate-owned
// pause). It reports true only when the error shape matches a genuine
// interruption; false for anything else.
func interruptionLike(err error) bool {
	if err == nil {
		return false
	}
	// Genuine interruption: an interrupted provider stream (outcome unknown —
	// the provider may or may not have processed the request) whose parent
	// context was NOT deliberately cancelled (context.Canceled is the
	// user-pressed-Stop path, checked by the caller). A context.DeadlineExceeded
	// also lands here when it did not come from the run's own task budget (the
	// budget pause carries its own type and is excluded upstream).
	if provider.IsStreamInterrupted(err) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// maybeInterruptedAutoContinueTurn fires the tl-1105 one-shot continuation after
// a terminal turn stop that was a genuine interruption. It reports whether the
// continuation turn ran; err is the continuation's own result when it did.
func (o *turnOrchestrator) maybeInterruptedAutoContinueTurn(ctx context.Context, turnErr error) (bool, error) {
	c := o.c
	if c == nil || !c.InterruptedAutoResume() {
		return false, nil
	}
	if turnErr == nil || ctx.Err() != nil {
		return false, nil
	}
	switch {
	case errors.Is(turnErr, context.Canceled):
		// The user (or the run's own wall clock) ended this turn on purpose.
		return false, nil
	case errors.Is(turnErr, ErrAutopilotAskUnanswered):
		// 477-off terminal stop: its own contract, not an interruption.
		return false, nil
	}
	var recoveryPause *agent.RecoveryPauseError
	if errors.As(turnErr, &recoveryPause) {
		return false, nil
	}
	var readinessErr *agent.FinalReadinessError
	if errors.As(turnErr, &readinessErr) {
		return false, nil
	}
	if _, paused := agent.InspectRunPause(turnErr); paused {
		// A deliberate run boundary (task budget / max_steps / incomplete
		// read / recovery pause) owns its own "send continue" contract —
		// never resurrect it.
		return false, nil
	}
	var uncertain *agent.CompletionUncertainError
	if errors.As(turnErr, &uncertain) {
		return false, nil
	}
	if !interruptionLike(turnErr) {
		return false, nil
	}
	turnID, _, _, _ := c.turnEventRuntimeStatus()
	log.Printf("[interrupted-autoresume] interrupted stop; one continuation turn submitted (tl 1105) turn=%s err=%v", turnID, turnErr)
	c.sink.Emit(event.Event{
		Kind:   event.Notice,
		Level:  event.LevelInfo,
		Code:   interruptedAutoResumeNoticeCode,
		Text:   "interrupted turn auto-resume — the outcome was unknown; resuming from the persisted recovery handoff (tl 1105)",
		Detail: "one host continuation turn carries the bounded recovery summary so the user does not have to send a follow-up message",
	})
	err := o.runOrchestratedTurn(ctx, orchestratedTurn{
		input: interruptedAutoResumeTurnPrompt, raw: interruptedAutoResumeTurnPrompt, display: "", synthetic: true,
	})
	return true, err
}
