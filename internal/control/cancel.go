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
	// tl1357 形态②（cancel 不丢引导）：在 cancel() 信号发出【之前】快照本回合
	// 已受理（steer_accepted）的持久 steer 行。取消后的 unwind 会跑 agent 的
	// flushSteerQueue，把未消费行的 loader 再拉一遍（把行翻成 steer_consumed）
	// 并发 unapplied 通知——若在快照之后再读状态，窗口内翻动的行会被漏掉。
	// 快照集合由 TurnDone 的 tl-1213 requeue 通道消费（SetState queued +
	// ConvertIntent follow-up），这些消息回到重放队列，按 FIFO 作为下一轮
	// 输入被派发，不再依赖 desktop 侧任何内存队列存活。用户 Stop / 停止升级 /
	// 跨会话 stop 钩子都汇入本入口，语义自动对齐；watchdog abort 路径
	// （maybeAbortPreflightStalledTurn）在更早处已自带同款武装，canceling 位
	// 保证两者不会双写。正常完成不经过本入口，「steer 迟到」既有语义
	// （Uncertain 人工检视，防 #7045 过期指令重放）保持不变。
	requeueGeneration := c.turnGeneration
	c.mu.Unlock()
	if cancel == nil {
		outcome := cancelTurnIdle
		if ledgerRef != nil && ledgerRef.LastActivityAt() > 0 {
			outcome = cancelTurnFinished
		}
		return "", false, outcome
	}
	c.armSteerRequeue(requeueGeneration)
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
		// tl1514 面一: every LIVE cancel opens the escalation ladder, not just
		// the CancelStop entry. A plain Cancel() (cli/acp/bot/serve transports,
		// and any host surface that predates the escalation entry) used to sit
		// at L1 forever when the turn ignored the graceful cancel: no 30s 保底
		// timer existed on this entry, so the automatic grace→force escalation
		// never came (the tl1514 field shape: 点停止后回合永不收敛). The guard
		// inside armStopEscalation keeps the CancelStop L1 path single-armed.
		// finishGuardedTurn resets the ladder at turn end (controller.go 任务
		// 461-P7 block), so a turn that ends normally drops the armed state.
		c.armStopEscalation()
		return
	}
	// tl1304 ③: the no-turn stop is an honest, coded receipt + mirror refresh
	// (the defer above) instead of the old silent return. Still deliberately
	// NO TurnCancelling event — faking turn traffic for a turn that is not
	// there would be its own dishonesty and could wedge the next turn's UI.
	c.logNoTurnStopReceipt("cancel", outcome)
	if c.goals.active() {
		c.stopGoal(GoalStatusStopped, goalCauseStopRequested, "stop requested while no turn was running")
	}
}
