package control

import (
	"context"
	"log/slog"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/extension"
)

// turnInterruptStaleTimeout bounds how long an interrupted turn may ignore
// cancellation before admission treats it as deadlocked (Task 303). Normal
// interrupts finish in seconds; the 11:57-interrupt sample never returned,
// leaving running=true and silently dropping every later submit.
const turnInterruptStaleTimeout = 45 * time.Second

// FinishingStuckTimeout is the 任务796 bound on the TurnDone fan-out window:
// a healthy fan-out (sink subscribers, runtime emit, receipt bookkeeping)
// completes in well under a second, so a finishing-only window older than
// this means the finisher goroutine is wedged inside Emit and will never run
// its deferred cleanup — the gate stays shut (finishing=true, running=false)
// while the inbox keeps queueing. The desktop idle-turn bridge (579) calls
// HealStuckFinishing with this value from its 4s sweep; there is no internal
// ticker, because a wedged fan-out produces no further events to hook.
const FinishingStuckTimeout = 90 * time.Second

// RunningStallQuietTimeout is the tl1288 bound on a RUNNING turn's silence:
// every sign of life appends to the turn event ledger (tool dispatch/result,
// tokens, status changes, retries), so a running turn whose ledger has been
// quiet this long is wedged inside its body — no TurnDone will ever arrive,
// c.running stays true, and every later observer (TabBar projection, 579
// bridge) reads a phantom busy state (20261031 01:07-01:18 现场：579 桥开的
// collab 轮体卡死，sweep 连续 11 分钟 skip:running，tab 挂绿点+已工作计数残
// 影，实体在盘所以 1266b 不拦). Deliberately far above FinishingStuckTimeout
// and the 303 interrupt-stale 45s: the false-positive cost is abandoning a
// genuinely slow foreground tool, so only silence no honest tool run can
// produce (background jobs keep their own aliveness guard in the heal) may
// clear the bar. Same trigger discipline as 796: no internal ticker, the
// desktop idle-turn bridge calls HealStalledRunning from its sweep.
const RunningStallQuietTimeout = 10 * time.Minute

// admissionResult classifies what runGuarded did with a turn body.
type admissionResult int

const (
	turnStarted admissionResult = iota
	turnParked
	turnDroppedRunning
	turnDroppedRotating
	turnDroppedClosed
	turnDroppedDraining // generation no longer published after rebuild
	turnDroppedWriteAuthority
)

// runGuarded runs body under a fresh context, guarding concurrent turns.
// Finishing-window arrivals park instead of dropping (see admissionResult).
func (c *Controller) runGuarded(body func(ctx context.Context) error) admissionResult {
	return c.admitGuardedTurn(body, false, true, nil)
}

// runGuardedOrPark admits like runGuarded but parks the body while another
// turn is running instead of using the deliberately-silent running drop.
// Reserved for inputs that are the user's own words (the steer fallback):
// the FIFO drain in finishGuardedTurn delivers them the moment the current
// turn finishes.
func (c *Controller) runGuardedOrPark(body func(ctx context.Context) error) admissionResult {
	return c.admitGuardedTurn(body, true, true, nil)
}

// runGuardedInbox admits a durable item without parking it in volatile memory.
// onStart runs after admission is reserved and before its goroutine can finish.
func (c *Controller) runGuardedInbox(body func(ctx context.Context) error, onStart func()) admissionResult {
	return c.admitGuardedTurn(body, false, false, onStart)
}

// turnStalledLocked reports whether the running turn is an abandoned interrupt
// (Task 303 deadlock precondition): cancel was requested, the body never
// returned, and the interrupt is now older than the stale threshold. Caller
// must hold c.mu.
func (c *Controller) turnStalledLocked(now time.Time) bool {
	return c.running && !c.interruptRequestedAt.IsZero() &&
		now.Sub(c.interruptRequestedAt) > turnInterruptStaleTimeout
}

// selfHealStalledLocked abandons a turn that outlived the stale threshold and
// reopens the admission gate (Task 303 core). Returns how long the turn had
// been stalled, or 0 when there was nothing to heal. Caller must hold c.mu.
func (c *Controller) selfHealStalledLocked(now time.Time) time.Duration {
	if !c.turnStalledLocked(now) {
		return 0
	}
	stalledFor := now.Sub(c.interruptRequestedAt)
	if c.cancel != nil {
		c.cancel()
	}
	// Bump the generation so the dead turn's late completion is recognized as
	// stale (finishGuardedTurn ignores it) instead of clearing this new turn's
	// gate or emitting TurnDone over the replacement stream.
	c.turnGeneration++
	c.running = false
	c.finishing = false
	c.interrupting = false
	c.canceling = false
	c.cancel = nil
	c.interruptRequestedAt = time.Time{}
	// 任务920: a deferred park-interrupt dies with the abandoned turn — its
	// late finishGuardedTurn is recognized stale and would never clear it.
	c.interruptDeferred = false
	slog.Warn("control: turn deadlock detected — self-heal reset the admission gate",
		"stalled_for", stalledFor, "threshold", turnInterruptStaleTimeout,
		"parked", len(c.parkedTurns), "generation", c.turnGeneration,
		"action", "abandon_stalled_turn_and_admit_current_submit")
	return stalledFor
}

// HealStuckFinishing is the 任务796 unattended-side sibling of the Task 303
// self-heal: 303 recovers a RUNNING turn that ignored cancellation, but only
// when a new submit arrives to run the check — an unattended session has no
// submitter, and a fan-out wedged inside Emit produces no further events for
// any internal hook. What it leaves behind is the third 579-family shape:
// running=false + finishing=true forever, so RuntimeStatus().Running reports
// busy, the desktop idle-turn bridge skips the target, and a queued follow-up
// sits unconsumed (20261007 合并线 146 分钟 / 20261011 合并线 75+ 分钟实测).
//
// The heal only fires on the unambiguous leak shape — no running turn, no
// parked bodies (a live FIFO drain keeps finishing true legitimately), window
// older than maxAge — and does exactly what the wedged finisher's own defer
// would have done, plus the generation bump that makes that finisher's late
// cleanup skip the gate entirely (stale-finisher guard in finishGuardedTurn).
// The caller (bridge sweep) re-evaluates on its next tick, so the queued item
// flows through the normal 579 opening path — this method never opens a turn
// itself.
func (c *Controller) HealStuckFinishing(maxAge time.Duration) (healed bool, stalledFor time.Duration) {
	if c == nil || maxAge <= 0 {
		return false, 0
	}
	c.mu.Lock()
	if c.closed || c.rotating || c.running || !c.finishing || len(c.parkedTurns) > 0 {
		c.mu.Unlock()
		return false, 0
	}
	now := time.Now()
	if c.finishingSince.IsZero() || now.Sub(c.finishingSince) < maxAge {
		c.mu.Unlock()
		return false, 0
	}
	stalledFor = now.Sub(c.finishingSince)
	c.turnGeneration++
	c.finishing = false
	c.finishingSince = time.Time{}
	c.canceling = false
	// The wedged finisher registered this boundary and will never reach its
	// end(); close it here so asynchronous frontends waiting for the TurnDone
	// delivery are released.
	c.finishingBoundary.end()
	c.mu.Unlock()
	slog.Warn("control: finishing window wedged — self-heal reopened the admission gate (任务796)",
		"finishing_for", stalledFor, "threshold", maxAge, "generation", c.turnGeneration,
		"action", "reset_stuck_finishing_and_rearm_inbox_dispatch")
	// 任务807: the reopen boundary reconciles window-era unowned claims before
	// re-arming dispatch — same pass as the normal close, so a stray whose
	// claiming pipeline died during the wedge requeues instead of stranding.
	c.releaseUnownedInboxClaims("heal_reopen")
	c.refreshRuntimeState(event.Event{})
	// Same re-arms the normal exit path performs: durable-host consumers get
	// their dispatch pass; the desktop bridge re-opens on its own next sweep.
	c.maybeDispatchInbox()
	c.wakeKickAfterTurnFinished()
	return true, stalledFor
}

// RunningQuietFor reports how long the running turn has produced no ledger
// event (the tl1288 liveness probe). ok=false when no turn is running, the
// controller is closed, or there is no ledger stamp to read — callers must
// treat a missing stamp as "cannot judge", never as "quiet forever".
func (c *Controller) RunningQuietFor(now time.Time) (quiet time.Duration, ok bool) {
	if c == nil {
		return 0, false
	}
	c.mu.Lock()
	running, closed := c.running, c.closed
	c.mu.Unlock()
	if !running || closed {
		return 0, false
	}
	last := c.turnEventLedger().LastActivityAt()
	if last <= 0 {
		return 0, false
	}
	quiet = now.Sub(time.UnixMilli(last))
	if quiet < 0 {
		quiet = 0
	}
	return quiet, true
}

// HealStalledRunning is the tl1288 unattended-side heal for the fourth
// 579-family shape: a RUNNING turn whose body wedged (provider stream died
// mid-tool, executor stuck) never runs finishGuardedTurn, so c.running stays
// true forever. The 303 self-heal cannot reach it — that check rides
// admitGuardedTurn and an unattended session has no submitter — and the 579
// bridge's sweep reads skip:running and moves on, which is exactly the
// 20261031 production scene (tab green dot + work counter on a transcript
// with no events for 11+ minutes, entity on disk so 1266b does not engage).
//
// The heal fires only on the unambiguous shape — running, gate not rotating,
// no parked bodies (a live FIFO drain keeps the gate busy legitimately), no
// in-flight compaction, no background jobs (a running job is the turn's
// aliveness evidence: foreground tools wait on them routinely), ledger quiet
// past maxQuiet — and does what the wedged turn's own abandon path would do:
// cancel the body, bump the generation so its late completion is recognized
// stale (finishGuardedTurn's 303 stale-completion guard), reopen the gate,
// notify, and re-arm the durable consumers. The caller re-evaluates on its
// next sweep tick; this method never opens a turn itself.
func (c *Controller) HealStalledRunning(maxQuiet time.Duration) (healed bool, stalledFor time.Duration) {
	if c == nil || maxQuiet <= 0 {
		return false, 0
	}
	now := time.Now()
	c.mu.Lock()
	if c.closed || c.rotating || !c.running || len(c.parkedTurns) > 0 {
		c.mu.Unlock()
		return false, 0
	}
	if c.executor != nil && c.executor.CompactionInFlight() {
		c.mu.Unlock()
		return false, 0
	}
	path := c.sessionPath
	if c.jobs != nil && len(c.jobs.RunningForSession(agent.BranchID(path))) > 0 {
		c.mu.Unlock()
		return false, 0
	}
	last := c.turnEventLedger().LastActivityAt()
	if last <= 0 {
		c.mu.Unlock()
		return false, 0
	}
	stalledFor = now.Sub(time.UnixMilli(last))
	if stalledFor < maxQuiet {
		c.mu.Unlock()
		return false, 0
	}
	if c.cancel != nil {
		c.cancel()
	}
	// Terminate the ledger's active turn now, while still under c.mu (the
	// c.mu→ledger.mu order is the only one in this file): otherwise the next
	// real Submit's Begin is refused with "turn is still active" and the
	// reopened gate is unusable for exactly the session that needed the heal.
	if ledger := c.turnEventLedger(); ledger != nil {
		_ = ledger.AbandonActiveTurn("runtime stall self-heal abandoned the wedged turn (tl1288)")
	}
	c.turnGeneration++
	c.running = false
	c.finishing = false
	c.finishingSince = time.Time{}
	c.canceling = false
	c.interrupting = false
	c.interruptRequestedAt = time.Time{}
	c.interruptDeferred = false
	c.resetStopEscalationLocked()
	c.mu.Unlock()
	slog.Warn("control: running turn wedged past quiet threshold — self-heal abandoned it and reopened the admission gate (tl1288)",
		"quiet_for", stalledFor.Round(time.Second), "threshold", maxQuiet,
		"generation", c.turnGeneration,
		"action", "cancel_wedged_turn_and_rearm_consumers")
	c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn,
		Text: "the running turn stopped responding and was reset automatically; you can resend your message"})
	c.releaseUnownedInboxClaims("heal_stalled_running")
	c.refreshRuntimeState(event.Event{})
	c.maybeDispatchInbox()
	c.wakeKickAfterTurnFinished()
	return true, stalledFor
}

// onCompactionSettled (任务920 压缩单飞门放行) runs on the agent goroutine the
// moment a real maintenance transaction releases its slot. It does the same
// work finishGuardedTurn's cleanup does, under the same critical section
// discipline: honor a park-path interrupt deferred during the pass, then
// start the oldest parked body (if any) while flipping running under c.mu —
// opening the gate first and re-admitting after would let an unrelated submit
// jump the queue. With nothing parked it re-arms the durable inbox, mirroring
// endRotation, so 579 items rejected busy during the window get their kick.
func (c *Controller) onCompactionSettled() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	if c.interruptDeferred {
		c.interruptDeferred = false
		// The compaction this interrupt waited out is over; fire it now and
		// start the stall clock together (see interruptDeferred field note).
		c.interrupting = true
		c.interruptRequestedAt = time.Now()
		if c.cancel != nil {
			c.cancel()
		}
	}
	if c.rotating || c.running || c.finishing || len(c.parkedTurns) == 0 {
		c.mu.Unlock()
		// Same re-arms the normal turn-close path performs: durable inbox and
		// idle-session wake both had their admission refused during the pass.
		c.maybeDispatchInbox()
		c.wakeKickAfterTurnFinished()
		return
	}
	// Re-check the gate on the agent's own signal: another pass may already
	// have started (manual compact right after a pressure fold), and parking
	// until THAT pass settles is the correct behavior.
	if c.executor != nil && c.executor.CompactionInFlight() {
		c.mu.Unlock()
		return
	}
	next := c.parkedTurns[0]
	c.parkedTurns = c.parkedTurns[1:]
	ctx, cancel := context.WithCancel(extension.ContextWithRuntimeOwner(context.Background(), c.runtimeOwner))
	forceCtx, forceCancel := context.WithCancel(extension.ContextWithRuntimeOwner(context.Background(), c.runtimeOwner))
	ctx = agent.WithStopForce(ctx, forceCtx.Done())
	c.cancel = cancel
	c.forceCancel = forceCancel
	c.running = true
	c.canceling = false
	parked := len(c.parkedTurns)
	c.mu.Unlock()
	slog.Info("control: parked turn drained after compaction settled", "parked_remaining", parked)
	c.releaseUnownedInboxClaims("compaction_settle")
	c.spawnGuardedTurn(ctx, cancel, next)
	c.refreshRuntimeState(event.Event{})
}

func (c *Controller) admitGuardedTurn(body func(ctx context.Context) error, parkWhileRunning, parkWhileFinishing bool, onStart func()) admissionResult {
	if err := c.ensureWriteAuthorityReady(); err != nil {
		slog.Warn("control: submit dropped", "reason", "write_authority_not_ready", "err", err)
		c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: "input was not accepted: this session is no longer writable — reopen it and try again"})
		return turnDroppedWriteAuthority
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		slog.Warn("control: submit dropped", "reason", "controller_closed")
		return turnDroppedClosed
	}
	if c.rejectDrainingGenerationLocked() {
		c.mu.Unlock()
		slog.Warn("control: submit dropped", "reason", "draining_generation")
		c.emitDrainingNotice()
		return turnDroppedDraining
	}
	if c.rotating {
		c.mu.Unlock()
		slog.Warn("control: submit dropped", "reason", "session_rotating")
		c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: "input was not accepted: the session is being switched — please resend"})
		return turnDroppedRotating
	}
	// Task 303: turn-deadlock self-heal. A park-path interrupt that outlives
	// the stale threshold means the body ignored cancellation and will never
	// run finishGuardedTurn — without this reset every later submit is
	// silently dropped (running gate stuck) and only a session rebuild
	// (compaction side effect) ever unlocked the conversation. Abandon the
	// stalled turn under the same lock: bump the generation so its late
	// completion is treated as stale, then reopen the gate for THIS body.
	healedStalledFor := c.selfHealStalledLocked(time.Now())
	// 任务920 压缩单飞门: a real maintenance transaction (prune/fold/rescue)
	// is in flight. Admitting a turn now would inject its messages into the
	// summary's snapshot→install window — either invalidating the whole pass
	// (errCompressStaleContext, the burned-summary family) or re-inflating the
	// view right after the fold lands (压完又胀). Park instead; the settle
	// hook (onCompactionSettled) drains the FIFO when the pass ends.
	compacting := c.executor != nil && c.executor.CompactionInFlight()
	if c.running {
		if parkWhileRunning {
			// Claude Code-style interrupt: if the current turn is executing
			// tools, signal it to end gracefully (bash keeps running in
			// background). This lets the parked turn start sooner.
			c.interrupting = true
			if compacting {
				// 让路不打断: cancelling the turn now would cancel the in-flight
				// summary with it (the context-canceled burn 912 repaired).
				// Record the intent and fire the cancel when the pass settles;
				// the stall clock (interruptRequestedAt) only starts then, so a
				// minutes-long compaction can never trip the 303 self-heal.
				c.interruptDeferred = true
			} else {
				c.interruptRequestedAt = time.Now()
				if c.cancel != nil {
					c.cancel()
				}
			}
			c.parkedTurns = append(c.parkedTurns, body)
			c.mu.Unlock()
			slog.Info("control: submit parked behind running turn", "interrupt_requested", !compacting, "compaction_in_flight", compacting)
			return turnParked
		}
		c.mu.Unlock()
		slog.Warn("control: submit dropped", "reason", "turn_running", "interrupting", true)
		return turnDroppedRunning
	}
	if compacting {
		if parkWhileRunning {
			c.parkedTurns = append(c.parkedTurns, body)
			parked := len(c.parkedTurns)
			c.mu.Unlock()
			slog.Info("control: submit parked behind in-flight compaction", "parked", parked)
			return turnParked
		}
		c.mu.Unlock()
		slog.Warn("control: submit dropped", "reason", "compaction_in_flight")
		return turnDroppedRunning
	}
	if c.finishing {
		if !parkWhileFinishing {
			c.mu.Unlock()
			slog.Warn("control: submit dropped", "reason", "turn_finishing_window")
			return turnDroppedRunning
		}
		c.parkedTurns = append(c.parkedTurns, body)
		// 748 race-A: len must be evaluated while holding c.mu — evaluating it
		// in the slog call below (after Unlock) races with finishGuardedTurn's
		// locked write to c.parkedTurns.
		parked := len(c.parkedTurns)
		c.mu.Unlock()
		slog.Info("control: submit parked behind finishing turn", "parked", parked)
		return turnParked
	}
	// 任务920 FIFO 自愈: the gate is open but the queue is not empty — the
	// bodies parked behind the compaction window lost their settle kick (hook
	// rebind, controller rebuild). This submit joins the tail and the oldest
	// parked body starts first, so order is preserved and nothing strands.
	if len(c.parkedTurns) > 0 {
		c.parkedTurns = append(c.parkedTurns, body)
		body = c.parkedTurns[0]
		c.parkedTurns = c.parkedTurns[1:]
	}
	ctx, cancel := context.WithCancel(extension.ContextWithRuntimeOwner(context.Background(), c.runtimeOwner))
	// 任务461-P7 三级终止: the force half is an INDEPENDENT context (same base
	// as the turn context). Firing it arms the executor's abandon watchdog
	// without touching the tool's graceful-exit window; the force path fires
	// both. Its Done channel travels on the turn context for the watchdog.
	forceCtx, forceCancel := context.WithCancel(extension.ContextWithRuntimeOwner(context.Background(), c.runtimeOwner))
	ctx = agent.WithStopForce(ctx, forceCtx.Done())
	c.cancel = cancel
	c.forceCancel = forceCancel
	c.running = true
	c.canceling = false
	c.mu.Unlock()
	if healedStalledFor > 0 {
		// Surface the self-heal to the user: their previous reply is gone and
		// this submit now owns the reopened gate (tell, don't silently swap).
		c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn,
			Text: "the previous reply stalled after an interrupt and was reset automatically — your message is running now"})
	}
	if onStart != nil {
		onStart()
	}
	c.refreshRuntimeState(event.Event{})
	c.spawnGuardedTurn(ctx, cancel, body)
	return turnStarted
}
