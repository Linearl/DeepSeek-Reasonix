package control

import (
	"log/slog"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/goaleval"
)

// tl1459 P0-a: Goal loop observability. The 2026-11-06 early-stop incident
// (session 20260925-150958, five rounds in 22 minutes) left no Goal-side trace
// at all: the auto-continue loop ended five times and the only evidence was
// the save-conflict lines that happened to share a timestamp. Every place the
// loop can end now writes one greppable line carrying the real binding values,
// and every continuationEpoch change names its source. Lines are written only
// at exits and changes — never per model round — so one Goal turn costs at most
// one exit line plus one epoch line.
//
// Non-goals: no new state, no decision reads these lines, no per-round or
// per-tool logging, and ordinary (non-Goal) turns stay silent.
const (
	goalExitLogMessage  = "controller: goal loop exit"
	goalEpochLogMessage = "controller: goal epoch change"
)

// Goal loop exits: where one Goal turn's auto-continuation ended. The first
// four are the exits of advanceGoalAfterTurn; the rest sit in the loops that
// call it and were equally silent before tl1459.
const (
	// goalExitGate: advanceGoalAfterTurn's early return — no recorder, the
	// recorder's epoch is not the expected one, or the turn is no longer active.
	goalExitGate = "gate"
	// goalExitTerminalError: a turn error the FSM does not own. The FSM is not
	// touched, so the Goal stays running while auto-continue stops.
	goalExitTerminalError = "terminal_error"
	// goalExitFSM: goals.advance decided (continue, complete, blocked, paused,
	// stopped); goal_status/cont/notice_set say which.
	goalExitFSM = "fsm"
	// goalExitEvaluatorFailClosed: the completion evaluator failed, was
	// unavailable or uncertain, and the FSM failed closed on it.
	goalExitEvaluatorFailClosed = "evaluator_fail_closed"
	// goalExitFSMRefused: goals.advance refused the step (epoch moved or the
	// Goal stopped between the gate and the FSM lock).
	goalExitFSMRefused = "fsm_refused"
	// goalExitTurnCancelled: the turn context was cancelled and the loop
	// stopped the Goal (user stop, durability failure, or host abort).
	goalExitTurnCancelled = "turn_cancelled"
	// goalExitAdmissionRefused: a continue verdict was rejected when the next
	// synthetic turn tried to claim it.
	goalExitAdmissionRefused = "admission_refused"
	// goalExitNotAdmitted: the synthetic continuation turn was not admitted.
	goalExitNotAdmitted = "not_admitted"
)

// Goal loop exit sites: which loop reached the exit.
const (
	goalSiteFirstTurn    = "first_turn"
	goalSiteEditedTurn   = "edited_turn"
	goalSiteContinuation = "continuation"
	goalSiteSkill        = "skill"
	goalSiteAdvance      = "advance"
)

// Goal epoch change sources: the closed set of operations allowed to move
// continuationEpoch. Recovery forks and promotions are deliberately absent —
// they are save-path facilities and must not touch Goal business state
// (CONSTITUTION D2 refinement); tl1459 pins that with a test.
const (
	goalEpochInstall      = "install"
	goalEpochClear        = "clear"
	goalEpochStop         = "stop"
	goalEpochPause        = "pause"
	goalEpochResume       = "resume"
	goalEpochAdvance      = "advance"
	goalEpochRestore      = "restore_sidecar"
	goalEpochRollback     = "rollback"
	goalEpochLegacyBlock  = "legacy_block"
	goalEpochLegacyFill   = "legacy_fill"
	goalEpochLegacyResume = "legacy_resume"
)

// bumpEpochLocked is the only place continuationEpoch moves. Caller holds g.mu.
func (g *goalMachine) bumpEpochLocked(source string) {
	from := g.continuationEpoch
	g.continuationEpoch++
	slog.Info(goalEpochLogMessage,
		"source", source, "from", from, "to", g.continuationEpoch,
		"scope", shortGoalScope(g.scopeID))
}

// shortGoalScope keeps scope ids greppable without logging the full random id.
func shortGoalScope(scopeID string) string {
	scopeID = strings.TrimPrefix(strings.TrimSpace(scopeID), "goal-")
	if len(scopeID) > 12 {
		return scopeID[:12]
	}
	return scopeID
}

// goalGateProbe is the machine state behind turnActive, read in one critical
// section so an exit line reports the values the gate actually compared.
type goalGateProbe struct {
	goalPresent  bool
	status       string
	scopeMatch   bool
	machineEpoch uint64
	turnActive   bool
}

func (g *goalMachine) gateProbe(scopeID string, epoch uint64) goalGateProbe {
	g.mu.Lock()
	defer g.mu.Unlock()
	p := goalGateProbe{
		goalPresent:  strings.TrimSpace(g.goal) != "",
		status:       g.status,
		scopeMatch:   scopeID != "" && g.scopeID == scopeID,
		machineEpoch: g.continuationEpoch,
	}
	p.turnActive = p.goalPresent && p.status == GoalStatusRunning && p.scopeMatch && p.machineEpoch == epoch
	return p
}

// goalGateReason names which gate condition rejected the turn.
func goalGateReason(recorder *goalTurnRecorder, expectedEpoch uint64) string {
	switch {
	case recorder == nil:
		return "recorder_nil"
	case recorder.epoch != expectedEpoch:
		return "recorder_epoch_mismatch"
	default:
		return "turn_inactive"
	}
}

// traceGoalExit writes the one exit line for a Goal turn. recorder may be nil
// (the gate's own failure mode); the probe then reports machine-only values.
func (c *Controller) traceGoalExit(exit, site string, expectedEpoch uint64, recorder *goalTurnRecorder, extra ...any) {
	if c == nil {
		return
	}
	attrs := []any{
		"goal_exit", exit,
		"site", site,
		"session", agent.BranchID(c.SessionPath()),
		"expected_epoch", expectedEpoch,
	}
	if recorder != nil {
		probe := c.goals.gateProbe(recorder.scopeID, recorder.epoch)
		attrs = append(attrs,
			"recorder", true,
			"recorder_epoch", recorder.epoch,
			"machine_epoch", probe.machineEpoch,
			"turn_active", probe.turnActive,
			"scope_match", probe.scopeMatch,
			"goal_present", probe.goalPresent,
			"goal_status", goalStatusLabel(probe.status))
	} else {
		probe := c.goals.gateProbe("", 0)
		attrs = append(attrs,
			"recorder", false,
			"recorder_epoch", -1,
			"machine_epoch", probe.machineEpoch,
			"turn_active", false,
			"goal_present", probe.goalPresent,
			"goal_status", goalStatusLabel(probe.status))
	}
	attrs = append(attrs, extra...)
	slog.Info(goalExitLogMessage, attrs...)
}

// traceGoalAdvance records the FSM decision exit (③/④/refused).
func (c *Controller) traceGoalAdvance(expectedEpoch uint64, recorder *goalTurnRecorder, res goalAdvanceResult, report *goalTurnReport, evaluator *goalEvaluatorVerdict, evaluatorFailed, pauseCause string) {
	exit := goalExitFSM
	switch {
	case res.continuationEpoch == 0:
		exit = goalExitFSMRefused
	case evaluatorFailed != "" || (evaluator != nil && evaluator.outcome == goaleval.OutcomeUncertain):
		exit = goalExitEvaluatorFailClosed
	}
	reportStatus := "none"
	if report != nil {
		reportStatus = report.status
	}
	evaluatorOutcome := "not_run"
	if evaluator != nil {
		evaluatorOutcome = string(evaluator.outcome)
	} else if evaluatorFailed != "" {
		evaluatorOutcome = "failed"
	}
	view := c.goals.runtimeView()
	extra := []any{
		"cont", res.cont,
		"epoch_after", res.continuationEpoch,
		"notice_set", res.notice != "",
		"stop_cause", view.StopCause,
		"report", reportStatus,
		"evaluator", evaluatorOutcome,
	}
	if evaluatorFailed != "" {
		extra = append(extra, "evaluator_err", clipGoalReason(evaluatorFailed))
	}
	if pauseCause != "" {
		extra = append(extra, "pause_cause", pauseCause)
	}
	c.traceGoalExit(exit, goalSiteAdvance, expectedEpoch, recorder, extra...)
}

func goalStatusLabel(status string) string {
	if status == "" {
		return "none"
	}
	return status
}

// goalTraceErr renders an error for an exit line, bounded like goal reasons.
func goalTraceErr(err error) string {
	if err == nil {
		return ""
	}
	return clipGoalReason(err.Error())
}

// Cancel causes for a turn_cancelled exit. failTurnEventLedger also raises the
// controller's canceling flag, so a durability failure must be classified
// before the generic stop request or it reads as a user stop.
const (
	goalCancelDurability = "durability_failure"
	goalCancelRequested  = "stop_requested"
	goalCancelContext    = "context_cancelled"
)

// goalCancelCause classifies why the turn context died under a Goal.
func (c *Controller) goalCancelCause() (cause string, ledgerErr error) {
	if ledgerErr = c.turnEventLedgerError(); ledgerErr != nil {
		return goalCancelDurability, ledgerErr
	}
	if c.CancelRequested() {
		return goalCancelRequested, nil
	}
	return goalCancelContext, nil
}

// goalCancelAttrs describes a cancelled Goal loop exit: the turn context died
// while the loop was still deciding (user stop, durability failure, host
// abort). The durability cause is the one the 2026-11-06 incident hid — a
// poisoned turn-event ledger cancels the turn and the old code stopped the
// Goal without a line.
func (o *turnOrchestrator) goalCancelAttrs(err error) []any {
	cause, ledgerErr := o.c.goalCancelCause()
	attrs := []any{"cancel_cause", cause, "ctx_err", goalTraceErr(err)}
	if ledgerErr != nil {
		attrs = append(attrs, "durability_err", goalTraceErr(ledgerErr))
	}
	return attrs
}

// traceGoalTurnExit is the orchestrator-side exit line. It is silent for an
// ordinary (recorder-less) turn, so chat stays out of the goal log.
func (o *turnOrchestrator) traceGoalTurnExit(recorder *goalTurnRecorder, exit, site string, expectedEpoch uint64, turnErr error) {
	if o == nil || o.c == nil || recorder == nil {
		return
	}
	extra := []any{"err", goalTraceErr(turnErr)}
	if exit == goalExitTurnCancelled {
		extra = o.goalCancelAttrs(turnErr)
	}
	o.c.traceGoalExit(exit, site, expectedEpoch, recorder, extra...)
}
