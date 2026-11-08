package control

import (
	"fmt"
	"strings"

	"reasonix/internal/event"
)

// tl1459 stage 2: the Goal never leaves running in silence. Every transition
// out of running — FSM decision, host stop, clear — emits one goal_* notice
// whose Text is the English technical line and whose Detail carries
// cause=<cause> (dual track: frontends localize the human half by Code and keep
// Detail visible). Frontends also refresh the Goal state on any goal_* notice,
// which closes the 2026-11-06 desync where the FSM had stopped but the Goal
// control still showed "on".
//
// Non-goals: no notice for transitions INTO running (install/resume are user
// actions the UI already reflects), no new decision input, no retry policy.

// goalCauseText is the English technical half for a transition cause.
func goalCauseText(cause string) string {
	switch cause {
	case goalCauseDurability:
		return "the turn was cancelled because a durable save failed"
	case goalCauseStopRequested:
		return "a stop was requested"
	case goalCauseCancelled:
		return "the turn ended before the goal could continue"
	case goalCauseCleared:
		return "the goal was cleared"
	default:
		return strings.ReplaceAll(cause, "_", " ")
	}
}

func (c *Controller) emitGoalNotice(code string, level event.Level, text, detail string) {
	c.sink.Emit(event.Event{Kind: event.Notice, Level: level, Code: code, Text: text, Detail: detail})
}

// goalNoticeDetail renders the machine half of a goal_* notice:
// "cause=<id> status=<status> reason=<free text to end of line>". reason is
// last because it may contain spaces; parsers read key=value pairs up to it.
func goalNoticeDetail(cause, status, reason string) string {
	parts := []string{"cause=" + cause}
	if status != "" {
		parts = append(parts, "status="+status)
	}
	if reason = strings.Join(strings.Fields(reason), " "); reason != "" {
		parts = append(parts, "reason="+clipGoalReason(reason))
	}
	return strings.Join(parts, " ")
}

// goalSpendReport renders the same spend suffix appendGoalStopReport puts on
// FSM terminal notices, from the lock-taking runtime view.
func goalSpendReport(view GoalRuntimeView) string {
	parts := []string{fmt.Sprintf("turns %d", view.TurnsUsed)}
	if view.TokensUsed > 0 {
		parts = append(parts, fmt.Sprintf("tokens %d", view.TokensUsed))
	}
	if view.WorkDurationMs > 0 {
		parts = append(parts, fmt.Sprintf("work %s", formatGoalSpendDuration(view.WorkDurationMs)))
	}
	return " — " + strings.Join(parts, ", ")
}

// stopGoal stops a running Goal on behalf of the host (the turn context died).
// cause is required so no host stop can be silent; reason carries the
// technical detail (for a durability failure, the failing write's error).
func (c *Controller) stopGoal(status, cause, reason string) {
	path, data, ok, left := c.goals.stop(status, cause, c.goalTodos())
	c.persistGoalState(path, data, ok)
	if !left {
		return
	}
	text := "goal stopped: " + goalCauseText(cause)
	if reason = strings.TrimSpace(reason); reason != "" {
		text += ": " + clipGoalReason(reason)
	}
	text += goalSpendReport(c.goals.runtimeView())
	c.emitGoalNotice(event.NoticeCodeGoalStopped, event.LevelWarn, text, goalNoticeDetail(cause, status, reason))
}

// stopGoalForCancelledTurn is the loop-side stop: it classifies why the turn
// context died (durability failure before a generic stop request — the ledger
// failure also raises the canceling flag) and stops with that cause.
func (c *Controller) stopGoalForCancelledTurn() {
	cancelCause, ledgerErr := c.goalCancelCause()
	cause, reason := goalCauseCancelled, ""
	switch cancelCause {
	case goalCancelDurability:
		cause, reason = goalCauseDurability, goalTraceErr(ledgerErr)
	case goalCancelRequested:
		cause = goalCauseStopRequested
	}
	c.stopGoal(GoalStatusStopped, cause, reason)
}

// goalNoticeCodeFor maps a post-advance status (and stop cause) to its code.
func goalNoticeCodeFor(status, stopCause string) (string, event.Level) {
	switch status {
	case GoalStatusComplete:
		return event.NoticeCodeGoalComplete, event.LevelInfo
	case GoalStatusBlocked:
		if stopCause != "" {
			return event.NoticeCodeGoalPaused, event.LevelWarn
		}
		return event.NoticeCodeGoalBlocked, event.LevelWarn
	default:
		return event.NoticeCodeGoalStopped, event.LevelWarn
	}
}

// noticeGoalAdvance surfaces an FSM decision that left running. The FSM's own
// notice text is kept verbatim; the code and cause make it machine-readable.
func (c *Controller) noticeGoalAdvance(res goalAdvanceResult) {
	if res.notice == "" {
		return
	}
	status := c.goals.statusForDisplay()
	view := c.goals.runtimeView()
	code, level := goalNoticeCodeFor(status, view.StopCause)
	cause := res.cause
	if cause == "" {
		// Unreachable while TestGoalStatusTransitionsDeclareACause holds; keep
		// the notice honest instead of empty if a future branch regresses.
		cause = "unclassified"
	}
	c.emitGoalNotice(code, level, res.notice, goalNoticeDetail(cause, status, ""))
}

// noticeGoalCleared reports a running Goal that a set("") just cleared.
func (c *Controller) noticeGoalCleared() {
	c.emitGoalNotice(event.NoticeCodeGoalCleared, event.LevelInfo,
		"goal cleared: the goal will not continue", goalNoticeDetail(goalCauseCleared, GoalStatusStopped, ""))
}

// noticeGoalAutoContinueHalted reports exit ②: a Goal turn ended on an error
// the FSM does not own. The Goal stays running — the next message keeps the
// scope — but its auto-continue stopped, which before tl1459 looked like a
// Goal control that was "on" and did nothing. tl1472: the halt now also
// withdraws the armed permit (打点+persist), so the sidebar's armed/disarmed
// state mirrors reality instead of the 1459 mirror ("显示 running 其实早已
// disarmed"). /goal resume re-arms.
func (o *turnOrchestrator) noticeGoalAutoContinueHalted(recorder *goalTurnRecorder, err error) {
	if o == nil || o.c == nil || recorder == nil || !o.c.goals.active() {
		return
	}
	o.c.emitGoalNotice(event.NoticeCodeGoalAutoContinueHalted, event.LevelWarn,
		"goal auto-continue stopped after a turn error; the goal stays active but disarmed (/goal resume re-arms it)",
		goalNoticeDetail("turn_error", GoalStatusRunning, goalTraceErr(err)))
	path, data, ok := o.c.goals.disarm(goalActivationActorHost, "turn_error", o.c.goalTodos())
	o.c.persistGoalState(path, data, ok)
}

// turnInactiveReason explains why a recorder bound to (scopeID, epoch) is no
// longer active — the detail update_goal used to collapse into "the active
// goal changed".
func (g *goalMachine) turnInactiveReason(scopeID string, epoch uint64) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case strings.TrimSpace(g.goal) == "":
		if g.lastStopCause != "" {
			return fmt.Sprintf("the goal is no longer set (%s)", goalCauseText(g.lastStopCause))
		}
		return "the goal is no longer set"
	case g.status != GoalStatusRunning:
		reason := fmt.Sprintf("the goal is %s", goalStatusLabel(g.status))
		if g.lastStopCause != "" {
			reason += fmt.Sprintf(" (cause: %s)", g.lastStopCause)
		}
		return reason
	case g.scopeID != scopeID:
		return "the goal was replaced by a new goal during this turn"
	case g.continuationEpoch != epoch:
		return fmt.Sprintf("the goal moved on during this turn (turn epoch %d, goal epoch %d)", epoch, g.continuationEpoch)
	default:
		return "the goal turn is no longer active"
	}
}

// goalTurnUnavailableReason explains, for a turn that bound no recorder, why
// it is not a Goal turn. Empty when no Goal was ever involved (plain chat).
func (g *goalMachine) goalTurnUnavailableReason() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	goal := strings.TrimSpace(g.goal)
	if goal == "" && g.lastStopCause == "" {
		return ""
	}
	status := goalStatusLabel(g.status)
	if goal == "" {
		return fmt.Sprintf("this turn is not a goal turn: the goal is no longer set (%s); ask the user to start or restart the goal", goalCauseText(g.lastStopCause))
	}
	if g.status == GoalStatusRunning {
		return ""
	}
	reason := fmt.Sprintf("this turn is not a goal turn: the goal is %s", status)
	if g.lastStopCause != "" {
		reason += fmt.Sprintf(" (cause: %s)", g.lastStopCause)
	}
	return reason + "; ask the user to resume or restart the goal"
}
