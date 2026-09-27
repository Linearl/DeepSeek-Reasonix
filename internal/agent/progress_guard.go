package agent

import (
	"context"

	"reasonix/internal/event"
	"reasonix/internal/evidence"
	"reasonix/internal/provider"
)

// applyBatchGuards collects this round's observational signals — outcome
// shadow, soft budget, operation breaker — and lets the arbiter deliver them
// as one tail. The two no-progress guards (the zero-gain progress ladder and
// the storm breaker) were retired with upstream #10223 (v1.38.8): they counted
// conversation rounds and could stop a turn after a long run of read-only tool
// rounds (upstream #9766). The shadow trackers observe the same receipts
// without influencing any verdict.
func (a *Agent) applyBatchGuards(ctx context.Context, cancelled bool, calls []provider.ToolCall, outcomes []toolOutcome, results []string, receiptMark int) {
	if cancelled {
		return
	}
	a.observeBlockedBatch(outcomes, receiptMark)
	shadow := a.observeOutcomeShadow(receiptMark, outcomes)
	budget := a.applySoftBudget(outcomes)
	operation := a.applyOperationBreaker(receiptMark)
	a.applyInterventions(results, outcomes, shadow, budget, operation)
	a.observeDelegationAdmission(calls)
}

// observeBlockedBatch keeps only what the retired storm breaker's report pass
// owed the model: after stormBreakThreshold consecutive fully-blocked batches
// the final-readiness loop-guard pass is armed so a blocked model can report
// the blocker and end the turn. It counts rounds but never stops one — the
// two no-progress guards that could (upstream #9766) retired with #10223.
func (a *Agent) observeBlockedBatch(outcomes []toolOutcome, receiptMark int) {
	for _, outcome := range outcomes {
		if outcome.blocked && outcome.errMsg == loopGuardBlockErrMsg {
			a.armLoopGuardPass(receiptMark)
			break
		}
	}
	allBlocked := len(outcomes) > 0
	for _, outcome := range outcomes {
		if !outcome.blocked {
			allBlocked = false
			break
		}
	}
	if allBlocked {
		a.turn.blockedTurnStreak++
		if a.turn.blockedTurnStreak >= stormBreakThreshold {
			a.armLoopGuardPass(receiptMark)
		}
		return
	}
	a.turn.blockedTurnStreak = 0
}

// resetTurnEvidence clears the ledger together with the retired guard counters
// that still live on the turn runtime for history compatibility. A fresh
// ledger is what "a new task" means here, and a continuation keeps it.
func (a *Agent) resetTurnEvidence() {
	a.task.restartLedger()
	a.turn.stormSig, a.turn.stormCount, a.turn.blockedTurnStreak = "", 0, 0
}

// observeOutcomeShadow scores the round's receipts through the shadow outcome
// tracker, lets the EBM policy stamp (and under its arm, act on) the sample,
// then records it. Unlike the guards it observes every round.
func (a *Agent) observeOutcomeShadow(receiptMark int, outcomes []toolOutcome) intervention {
	if a.task.ledger == nil {
		return intervention{}
	}
	if a.task.outcome == nil {
		a.task.outcome = evidence.NewOutcomeTracker()
	}
	sample := a.task.outcome.ScoreRound(a.task.ledger.ReceiptsSince(receiptMark))
	iv := a.applyEBM(&sample, outcomes)
	a.applyGovernor(&sample)
	a.armGovernorCapture(sample)
	event.RecordOutcomeProgress(a.svc.sink, sample)
	a.observeContractRound()
	return iv
}

// armLoopGuardPass records that a stop-adjacent guard fired this user turn.
// receiptMark is the evidence-ledger receipt count from just before the
// guarded batch ran, so a successful write or command receipt recorded after
// it counts as real progress and revokes the pass (see loopGuardAllowsFinal).
func (a *Agent) armLoopGuardPass(receiptMark int) {
	a.turn.loopGuardArmed = true
	a.turn.loopGuardReceiptMark = receiptMark
}

// loopGuardAllowsFinal reports whether final readiness should stand down: a
// guard fired this user turn and no successful write or command receipt has
// landed since. The missing receipts are exactly what the blocker prevents —
// demanding them would restart the loop the guard broke — while bookkeeping
// (ask, todo_write, complete_step) keeps the pass and real progress revokes it.
func (a *Agent) loopGuardAllowsFinal() bool {
	if a == nil || !a.turn.loopGuardArmed {
		return false
	}
	if a.task.ledger == nil {
		return true
	}
	return !a.task.ledger.HasWriteOrCommandSince(a.turn.loopGuardReceiptMark)
}
