package control

import (
	"context"
	"log/slog"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
)

// 任务461-P7 三级终止交互: the stop escalation state machine.
//
// The user-facing stop button escalates across three levels, and each press of
// the button advances exactly one level (the UI mirrors level + deadline from
// RuntimeStatus and simply calls CancelStop again — the kill semantics live
// here, not in the webview, so they survive a closed window):
//
//	L1 (1st press): the ordinary graceful cancel — the tool/turn keeps its
//	    normal exit window (acceptance: ≤1s when tools cooperate). A fallback
//	    timer opens the grace automatically when nothing responds (ruling ④).
//	L2 (2nd press): the bounded force grace — a countdown (default 15s, ruling
//	    ②) after which the executor force-abandons whatever is still running.
//	L3 (3rd press, or any press during the grace): immediate force — the
//	    force context fires the executor's abandon watchdog (goroutine 类工具
//	    记录+隔离，执行器不等它、UI 立即释放，ruling ⑤) alongside the turn
//	    cancel, which kills process-backed tools via their existing
//	    exec.Cancel/process-tree paths.
//
// The thresholds are vars so tests can shrink them; production runs on the
// defaults. 阈值可配置（ruling ⑦）：跑 build/test 这类长任务的会话可以把两个
// 窗口调大再停止，避免长任务被误升级。

var (
	// stopAutoGraceAfter bounds how long a normal (L1) stop may go without
	// the turn ending before the grace opens on its own (ruling ④'s 保底).
	stopAutoGraceAfter = 30 * time.Second
	// stopForceGrace is the L2 countdown: how long the force grace waits
	// before the executor force-abandons (ruling ②).
	stopForceGrace = 15 * time.Second
)

// Stop escalation levels (mirrored to the frontend via RuntimeStatus /
// RuntimeStateSnapshot.StopLevel).
const (
	StopLevelNone   = 0 // no stop in flight
	StopLevelNormal = 1 // L1: graceful cancel requested
	StopLevelGrace  = 2 // L2: force grace running (countdown to force)
	StopLevelForce  = 3 // L3: executor force-abandon fired
)

// CancelStop is the stop button's escalation entry. Each call advances one
// level; a controller with no live turn behaves like the ordinary Cancel.
func (c *Controller) CancelStop() {
	c.mu.Lock()
	level := c.stopLevel
	live := c.cancel != nil
	c.mu.Unlock()
	switch {
	case level == StopLevelGrace:
		// Any press during the countdown jumps straight to L3 (ruling ②).
		c.fireStopForce()
		return
	case level == StopLevelForce:
		// Force already fired; an extra press just re-asserts the cancel.
		c.Cancel()
		return
	case level == StopLevelNormal && live:
		// 2nd press: open the force grace.
		c.openStopGrace()
		return
	}
	// L1: the ordinary graceful cancel + the no-response fallback timer.
	c.promptResolveMu.Lock()
	turnID, cancelled := c.cancelTurnLocked()
	c.promptResolveMu.Unlock()
	c.finishCancel(turnID, cancelled)
	if cancelled {
		c.armStopEscalation()
	}
}

// armStopEscalation records L1 and starts the 保底 timer (ruling ④): a stop
// the turn never acknowledges opens the grace on its own after
// stopAutoGraceAfter.
func (c *Controller) armStopEscalation() {
	c.mu.Lock()
	if c.cancel == nil {
		c.mu.Unlock()
		return
	}
	c.stopLevel = StopLevelNormal
	c.stopDeadline = time.Time{}
	c.stopTimers = append(c.stopTimers, time.AfterFunc(stopAutoGraceAfter, func() {
		c.mu.Lock()
		stale := c.stopLevel != StopLevelNormal || c.cancel == nil
		c.mu.Unlock()
		if stale {
			return
		}
		slog.Info("control: stop unacknowledged; opening the force grace automatically (任务461-P7 ④)")
		c.openStopGrace()
	}))
	// refreshRuntimeState takes c.mu itself — unlock first (never defer it
	// across this call).
	c.mu.Unlock()
	c.refreshRuntimeState(event.Event{})
}

// openStopGrace records L2 and starts the force countdown (ruling ②).
func (c *Controller) openStopGrace() {
	c.mu.Lock()
	if c.stopLevel >= StopLevelGrace || c.cancel == nil {
		c.mu.Unlock()
		return
	}
	c.stopLevel = StopLevelGrace
	c.stopDeadline = time.Now().Add(stopForceGrace)
	c.stopTimers = append(c.stopTimers, time.AfterFunc(stopForceGrace, func() {
		c.mu.Lock()
		stale := c.stopLevel != StopLevelGrace || c.cancel == nil
		c.mu.Unlock()
		if stale {
			return
		}
		// The countdown ran out: force (ruling ②'s automatic escalation).
		slog.Info("control: force grace expired; firing the executor force-abandon (任务461-P7)")
		c.fireStopForce()
	}))
	c.mu.Unlock()
	slog.Info("control: force grace opened (任务461-P7 L2)", "grace_ms", stopForceGrace.Milliseconds())
	c.emitStopEscalationTurnStatus()
	c.refreshRuntimeState(event.Event{})
}

// fireStopForce records L3 and fires both halves: the force context (arms the
// executor's abandon watchdog — a wedged goroutine tool is quarantined, the
// UI releases immediately) and the turn cancel (process-backed tools die via
// their exec.Cancel/process-tree paths; the provider stream unwinds).
func (c *Controller) fireStopForce() {
	c.mu.Lock()
	if c.cancel == nil {
		// The turn ended between the timer tick and the fire; nothing to force.
		c.stopLevel = StopLevelNone
		c.stopDeadline = time.Time{}
		c.mu.Unlock()
		return
	}
	if c.stopLevel >= StopLevelForce {
		c.mu.Unlock()
		return
	}
	c.stopLevel = StopLevelForce
	c.stopDeadline = time.Time{}
	c.clearStopTimersLocked()
	forceCancel := c.forceCancel
	c.mu.Unlock()
	if forceCancel != nil {
		forceCancel()
		slog.Warn("control: stop escalated to force (任务461-P7 L3); the executor will abandon tools that ignore termination")
	}
	// The ordinary cancel rides along so every other wait keying off the turn
	// context unwinds with it.
	c.promptResolveMu.Lock()
	turnID, cancelled := c.cancelTurnLocked()
	c.promptResolveMu.Unlock()
	c.finishCancel(turnID, cancelled)
	c.emitStopEscalationTurnStatus()
	c.refreshRuntimeState(event.Event{})
}

// resetStopEscalationLocked drops the escalation state once the turn is gone
// (completed, failed, or force-unwound): the next stop starts fresh at L1.
// Callers hold c.mu.
func (c *Controller) resetStopEscalationLocked() {
	c.stopLevel = StopLevelNone
	c.stopDeadline = time.Time{}
	c.clearStopTimersLocked()
}

func (c *Controller) clearStopTimersLocked() {
	for _, t := range c.stopTimers {
		t.Stop()
	}
	c.stopTimers = nil
}

// emitStopEscalationTurnStatus refreshes the runtime-state mirror; the
// snapshot carries StopLevel/StopDeadlineUnix for the frontend countdown.
func (c *Controller) emitStopEscalationTurnStatus() {
	if ledger := c.turnEventLedger(); ledger != nil {
		if turnID := ledger.ActiveTurnID(); turnID != "" {
			c.emitTurnStatus(event.TurnCancelling, turnID)
		}
	}
	c.refreshRuntimeState(event.Event{})
}

// stopForceContext is a small helper for tests: it mirrors the admission
// wiring (independent force context + stamped Done channel).
func stopForceContext(base context.Context) (context.Context, context.CancelFunc, context.CancelFunc) {
	ctx, cancel := context.WithCancel(base)
	forceCtx, forceCancel := context.WithCancel(base)
	return agent.WithStopForce(ctx, forceCtx.Done()), cancel, forceCancel
}
