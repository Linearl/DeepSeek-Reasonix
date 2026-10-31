package control

import (
	"log/slog"

	"reasonix/internal/event"
)

// cancelTurnOutcome tells the caller what a cancel actually did (tl1304 ③
// 后端诚实化). A stop pressed against a controller whose turn context is
// already gone (finishGuardedTurn cleared c.cancel at body return) used to be
// a silent no-op — no event, no receipt, no state change — so neither the
// desktop bridge nor the escalation ladder could tell "cancel delivered" from
// "nothing to cancel", and N presses were N unacknowledged writes. The
// receipt names the shape; every no-turn path now also refreshes the runtime
// mirror so a stale projection reconciles instead of echoing a dead turn.
type cancelTurnOutcome int

const (
	// cancelTurnLive: a live turn context existed and the cancel signal fired.
	cancelTurnLive cancelTurnOutcome = iota
	// cancelTurnIdle: no context and this controller never produced turn
	// activity (fresh runtime / session-level stop on an idle tab).
	cancelTurnIdle
	// cancelTurnFinished: no context because the turn already ended — the
	// context was torn down at finish (or by a stall self-heal). This is the
	// wedge-adjacent shape the 诊断报告 called out: RuntimeStatus still reports
	// busy (finishing merge) while the stop finds nothing to cancel.
	cancelTurnFinished
)

func (o cancelTurnOutcome) String() string {
	switch o {
	case cancelTurnLive:
		return "live"
	case cancelTurnIdle:
		return "idle"
	default:
		return "finished"
	}
}

// noTurnOutcome classifies why a stop found no live turn context: a ledger
// with any recorded activity means a turn existed and ended (finished); no
// activity at all means this controller never admitted a turn (idle). Same
// liveness source tl1288's heal reads. Call without holding c.mu (the c.mu →
// ledger.mu order is fine too; this helper simply never takes c.mu).
func (c *Controller) noTurnOutcome() cancelTurnOutcome {
	if ledger := c.turnEventLedger(); ledger != nil && ledger.LastActivityAt() > 0 {
		return cancelTurnFinished
	}
	return cancelTurnIdle
}

// Cancel aborts the in-flight turn. A goroutine blocked awaiting approval
// unblocks via the cancelled context. Re-entry is idempotent — tl1304 ① keeps
// the stop button enabled through the cancelling window, so presses may land
// while an earlier cancel is still unwinding: each re-run takes the same
// fast path (context cancel is a no-op the second time) and, at worst, emits
// one redundant TurnCancelling status.
func (c *Controller) Cancel() {
	c.promptResolveMu.Lock()
	turnID, cancelled, outcome := c.cancelTurnLocked()
	c.promptResolveMu.Unlock()
	c.finishCancel(turnID, cancelled, outcome)
}

// cancelLocked is Cancel for callers that already hold promptResolveMu.
func (c *Controller) cancelLocked() {
	turnID, cancelled, outcome := c.cancelTurnLocked()
	c.finishCancel(turnID, cancelled, outcome)
}

// cancelTurnLocked signals the turn before any observable work: the status
// emit that follows is a synchronous event barrier, and a stalled event lane
// must never keep the provider stream or a tool process alive after Stop.
// A torn-down context returns the coded no-turn receipt (tl1304 ③) instead
// of silently pretending a cancel happened.
func (c *Controller) cancelTurnLocked() (string, bool, cancelTurnOutcome) {
	c.mu.Lock()
	cancel := c.cancel
	ledgerRef := c.turnEventLedger() // c.mu → ledger-holding-registry order (see admission_guard.go)
	if cancel != nil {
		c.canceling = true
	}
	c.mu.Unlock()
	if cancel == nil {
		outcome := cancelTurnIdle
		if ledgerRef != nil && ledgerRef.LastActivityAt() > 0 {
			outcome = cancelTurnFinished
		}
		return "", false, outcome
	}
	turnID := ""
	if ledgerRef != nil {
		turnID = ledgerRef.ActiveTurnID()
	}
	cancel()
	c.promptOwner.CancelAll()
	c.approval.clearAll()
	return turnID, true, cancelTurnLive
}

// logNoTurnStopReceipt is the tl1304 ③ coded receipt for a stop that found no
// live turn context: one log line naming the shape (idle vs finished). Call
// without holding any controller lock; the runtime mirror refresh is the
// caller's job (every no-turn site refreshes right after).
func (c *Controller) logNoTurnStopReceipt(site string, outcome cancelTurnOutcome) {
	slog.Info("control: stop found no live turn context (tl1304 ③ coded receipt)",
		"site", site, "outcome", outcome.String(),
		"action", "no_op_receipt_and_runtime_mirror_refresh")
}

func (c *Controller) finishCancel(turnID string, cancelled bool, outcome cancelTurnOutcome) {
	defer c.refreshRuntimeState(event.Event{})
	if cancelled {
		c.emitTurnStatus(event.TurnCancelling, turnID)
		return
	}
	// tl1304 ③: the no-turn stop is an honest, coded receipt + mirror refresh
	// (the defer above) instead of the old silent return. Still deliberately
	// NO TurnCancelling event — faking turn traffic for a turn that is not
	// there would be its own dishonesty and could wedge the next turn's UI.
	c.logNoTurnStopReceipt("cancel", outcome)
	if c.goals.active() {
		c.stopGoal(GoalStatusStopped)
	}
}
