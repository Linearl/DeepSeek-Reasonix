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
	c.refreshRuntimeState(event.Event{})
	// Same re-arms the normal exit path performs: durable-host consumers get
	// their dispatch pass; the desktop bridge re-opens on its own next sweep.
	c.maybeDispatchInbox()
	c.wakeKickAfterTurnFinished()
	return true, stalledFor
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
	if c.running {
		if parkWhileRunning {
			// Claude Code-style interrupt: if the current turn is executing
			// tools, signal it to end gracefully (bash keeps running in
			// background). This lets the parked turn start sooner.
			c.interrupting = true
			c.interruptRequestedAt = time.Now()
			if c.cancel != nil {
				c.cancel()
			}
			c.parkedTurns = append(c.parkedTurns, body)
			c.mu.Unlock()
			slog.Info("control: submit parked behind running turn", "interrupt_requested", true)
			return turnParked
		}
		c.mu.Unlock()
		slog.Warn("control: submit dropped", "reason", "turn_running", "interrupting", true)
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
