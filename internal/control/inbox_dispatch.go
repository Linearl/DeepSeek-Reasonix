package control

import (
	"errors"
	"log/slog"
	"time"

	"reasonix/internal/sessioninbox"
)

const maxInboxDispatchRetryAttempts = 3

// ErrInboxRuntimeUnpublished means admission could not resolve a published
// runtime owner (detached runtime, or the runtime was replaced mid-admission).
// 任务579 root-cause note: the old contract comment claimed "the host owns the
// next dispatch kick", but for a detached runtime no host kick ever comes —
// the controller therefore leaves its own bounded trail (log + 5s/15s/45s
// retries + exhaustion report) instead of dropping the item silently.
var ErrInboxRuntimeUnpublished = errors.New("inbox runtime is not published")

// NotifyInboxRuntimeReady is called after a host publishes a complete runtime.
func (c *Controller) NotifyInboxRuntimeReady() { c.maybeDispatchInbox() }

func (c *Controller) SetBeforeInboxDispatch(before func(*Controller) (func(), error)) {
	c.mu.Lock()
	c.modelSettings.beforeInboxDispatch = before
	c.mu.Unlock()
}

type inboxDispatchResult int

const (
	inboxDispatchIdle inboxDispatchResult = iota
	inboxDispatchStarted
	inboxDispatchRetry
	// inboxDispatchRuntimePending marks the 任务579 dead path: admission says
	// the runtime owner is not published (detached runtime, replaced runtime),
	// so nothing consumed the item and NOBODY else owns the next kick. Unlike
	// ErrTurnRunning (the running turn's completion re-kicks) this condition
	// used to be silent and permanent; it now leaves a log trail and arms a
	// bounded host-independent retry.
	inboxDispatchRuntimePending
)

// InboxDispatchExhausted is what the host learns when the bounded opening
// retry budget for one stuck item is spent (任务579 验收④: exhaustion must be
// observable, never a silent drop). The item STAYS queued — this is a status
// report, not an eviction; the idle-turn bridge or a later runtime publish
// can still consume it.
type InboxDispatchExhausted struct {
	ItemID string
	// Source / CollabMsgID / CollabMailTo carry the task-309 receipt
	// coordinates when the item arrived over the collaboration mail pump;
	// empty for local queue entries.
	Source       string
	CollabMsgID  string
	CollabMailTo string
	// Attempts counts the retries that were made after the initial kick.
	Attempts int
	// Err is the last admission error (ErrInboxRuntimeUnpublished).
	Err error
}

// endRotation releases the admission gate and republishes durable queue work.
func (c *Controller) endRotation() {
	c.mu.Lock()
	c.rotating = false
	c.mu.Unlock()
	c.maybeDispatchInbox()
}

// maybeDispatchInbox is a level-triggered kick, not a one-shot edge. Every
// caller publishes pending work before checking whether a dispatcher is live.
// The active dispatcher clears dispatching only while holding the same lock
// after observing no pending kick, so a completion, rotation release, or steer
// rejection can never disappear in the handoff window.
func (c *Controller) maybeDispatchInbox() {
	c.inbox.mu.Lock()
	if c.inbox.closed {
		c.inbox.mu.Unlock()
		return
	}
	c.inbox.dispatchPending = true
	if c.inbox.dispatching {
		c.inbox.mu.Unlock()
		return
	}
	c.inbox.dispatching = true
	c.inbox.mu.Unlock()
	c.mu.Lock()
	hostAdmission := c.modelSettings.beforeInboxDispatch != nil
	c.mu.Unlock()
	if hostAdmission {
		// Enqueue/resume can be called with the host's publication lock held.
		// Never synchronously reenter that lock through its admission callback.
		c.autosaveWG.Go(c.drainInboxDispatch)
		return
	}
	c.drainInboxDispatch()
}

func (c *Controller) drainInboxDispatch() {
	for {
		c.inbox.mu.Lock()
		if !c.inbox.dispatchPending {
			c.inbox.dispatching = false
			c.inbox.mu.Unlock()
			return
		}
		c.inbox.dispatchPending = false
		c.inbox.mu.Unlock()

		switch c.dispatchInboxOnce() {
		case inboxDispatchRetry:
			c.scheduleInboxDispatchRetry()
		case inboxDispatchRuntimePending:
			c.scheduleInboxRuntimeRetry()
		case inboxDispatchStarted, inboxDispatchIdle:
			c.resetInboxDispatchRetries()
			c.resetInboxRuntimeRetry()
		}
	}
}

// dispatchInboxOnce admits one FIFO item when every runtime gate is open. A
// started turn owns the next kick through finishGuardedTurn; this method never
// loops over multiple items while that turn is active.
func (c *Controller) dispatchInboxOnce() inboxDispatchResult {
	if c.PendingPrompt() {
		return inboxDispatchIdle
	}
	c.mu.Lock()
	busy := c.running || c.finishing || c.rotating || c.closed
	c.mu.Unlock()
	// 任务920: an in-flight compaction is busy for durable dispatch too —
	// admitting an item would runGuardedInbox-drop it (turnDroppedRunning)
	// and burn a claim cycle; the settle hook's maybeDispatchInbox is the
	// kick that owns this window (same level-triggered contract as rotation).
	if !busy && c.executor != nil && c.executor.CompactionInFlight() {
		busy = true
	}
	if busy {
		return inboxDispatchIdle
	}
	// Controllers without persistence cannot own a durable inbox. Rotation and
	// turn-completion hooks are shared with those controllers, so treat the
	// missing path as an empty queue instead of retrying a permanent condition.
	if c.SessionPath() == "" {
		return inboxDispatchIdle
	}
	meta, ok, err := c.nextInboxDispatchItem()
	if err != nil {
		c.setDispatchRetryFailure("", err)
		slog.Warn("controller: open inbox for dispatch", "err", err)
		return inboxDispatchRetry
	}
	// Task 221: with a merge mode armed, the picked item may become the carrier
	// of its whole drain group (off mode returns it untouched at near-zero cost).
	if ok {
		meta = c.maybeMergeInboxDispatchGroup(meta)
	}
	c.inbox.mu.Lock()
	beforeSubmit := c.inbox.beforeDispatchSubmit
	c.inbox.mu.Unlock()
	if !ok {
		// tl1339 刀①: "empty" and "paused/readonly hides queued work" used to
		// fold into the same silent idle. Leave a trail (zero behavior) when
		// the queue was actually gate-held.
		c.traceInboxGateHeld()
		return inboxDispatchIdle
	}
	c.clearInboxDispatchGateHeld()
	if beforeSubmit != nil {
		if err := beforeSubmit(meta.ID); err != nil {
			c.setDispatchRetryFailure(meta.ID, err)
			slog.Warn("controller: inbox dispatch hook", "err", err, "id", meta.ID)
			return inboxDispatchRetry
		}
	}
	receipt, err := c.TrySubmitInboxItem(meta.ID)
	if err != nil {
		if errors.Is(err, ErrTurnRunning) {
			// The running turn owns the next kick: its completion re-runs the
			// dispatcher, so staying silent here is by design.
			return inboxDispatchIdle
		}
		if errors.Is(err, ErrInboxRuntimeUnpublished) {
			// 任务579②: the host does NOT own a kick for this state (detached
			// runtime; the host that could publish one is gone or never
			// comes). Record the trail and arm the bounded retry instead of
			// dropping the kick as idle — that silence was the 2026-10-07
			// five-hour backlog.
			c.inbox.mu.Lock()
			// 任务808: fresh budget per stuck head. The retry bookkeeping is a
			// single slot; without this reset a new head inherits the previous
			// item's burned attempts — its first re-drive lands at the tail of
			// someone else's backoff and the exhaustion report names it with
			// attempts it never made (the 796 报告 §6.2 消费竞争的覆盖面).
			if c.inbox.runtimeRetryItem.ID != meta.ID {
				c.inbox.runtimeRetryAttempts = 0
			}
			c.inbox.runtimeRetryItem = meta
			c.inbox.mu.Unlock()
			slog.Warn("controller: inbox dispatch deferred, runtime unpublished (item stays queued; bounded retry armed)",
				"id", meta.ID, "source", meta.Source)
			return inboxDispatchRuntimePending
		}
		c.setDispatchRetryFailure(meta.ID, err)
		slog.Warn("controller: dispatch inbox item", "err", err, "id", meta.ID)
		return inboxDispatchRetry
	}
	if receipt.Disposition == sessioninbox.DispositionStarted {
		return inboxDispatchStarted
	}
	// A competing turn or rotation owns the next kick when its gate releases.
	return inboxDispatchIdle
}

func (c *Controller) nextInboxDispatchItem() (sessioninbox.InboxItemMeta, bool, error) {
	c.inbox.scanMu.Lock()
	defer c.inbox.scanMu.Unlock()
	c.inbox.mu.Lock()
	closed := c.inbox.closed
	afterScan := c.inbox.afterDispatchScan
	c.inbox.mu.Unlock()
	if closed {
		return sessioninbox.InboxItemMeta{}, false, nil
	}
	st, err := c.ensureInbox()
	if err != nil {
		return sessioninbox.InboxItemMeta{}, false, err
	}
	// NextQueued refreshes disk state and may create its transaction-lock
	// directory. Keep that access inside the same shutdown boundary as Open.
	meta, ok := st.NextQueued()
	if afterScan != nil {
		afterScan(ok)
	}
	return meta, ok, nil
}

// bindAgentToolRoundGap wires the agent's tool-round gap (任务461-P9) to the
// durable queue: at each gap the head queued item is attempted with the SAME
// mid-turn admission the enqueue-time steer uses (TrySteerInboxItem), so user
// guidance lands within one tool cycle instead of waiting for the turn
// boundary. Rejections keep the item queued exactly as before (queue
// semantics, dedup and the follow-up fallback unchanged), and the
// turn-boundary pump stays the owner of idle-session delivery.
func (c *Controller) bindAgentToolRoundGap() {
	if c == nil || c.executor == nil {
		return
	}
	c.executor.SetToolRoundGapHook(c.dispatchQueuedAtToolGap)
}

// dispatchQueuedAtToolGap is the 任务461-P9 injection attempt, fired by the
// agent between tool rounds. Every gate mirrors dispatchInboxOnce (busy /
// pending prompt / closed / rotating), the task-221 drain merge rides along,
// and delivery goes through TrySteerInboxItem: an accepted item is consumed in
// this very gap, a rejected one stays queued for the turn-boundary pump.
func (c *Controller) dispatchQueuedAtToolGap() {
	if c.SessionPath() == "" {
		return
	}
	c.mu.Lock()
	rotating, closed := c.rotating, c.closed
	c.mu.Unlock()
	// The live-run signal is the executor's own (任务461-P9): precise across
	// every admission path (guarded, synchronous, orchestrator), not just the
	// ones that set c.running.
	if rotating || closed || c.executor == nil || !c.executor.SteerRunActive() {
		return
	}
	if c.PendingPrompt() {
		return
	}
	meta, ok, err := c.nextInboxDispatchItem()
	if err != nil || !ok {
		return
	}
	meta = c.maybeMergeInboxDispatchGroup(meta)
	// Never blocks the loop for long: the body loads at consume, not here. A
	// rejection is by design (paused / images / stale turn) and keeps the item.
	_, _ = c.TrySteerInboxItem(meta.ID)
}

func (c *Controller) scheduleInboxDispatchRetry() {
	c.inbox.mu.Lock()
	if c.inbox.dispatchRetryScheduled {
		c.inbox.mu.Unlock()
		return
	}
	if c.inbox.dispatchRetryAttempts >= maxInboxDispatchRetryAttempts {
		// tl1339 刀①（报告 §2.1 #6/#9）: after the 3 bounded retries this
		// boundary used to stop dead silent — the gate-held condition persisted
		// with no exhaustion trail at all (the per-attempt WARNs above end at
		// 3; unlike the 579④ runtime path there was no budget-spent report).
		// One WARN per episode + counter, zero behavior: the budget still
		// stands down, the item stays queued, the gate owner still owns the
		// next kick.
		lastID, lastErr := c.inbox.dispatchRetryLastID, c.inbox.dispatchRetryLastErr
		first := !c.inbox.dispatchRetryExhaustedLogged
		c.inbox.dispatchRetryExhaustedLogged = true
		c.inbox.mu.Unlock()
		sessioninbox.NoteRetryExhausted()
		if first {
			slog.Warn("controller: inbox dispatch retries exhausted; gate-held condition persists (item stays queued; budget stands down until a kick stops failing)",
				"id", lastID, "attempts", maxInboxDispatchRetryAttempts, "last_err", lastErr)
		}
		return
	}
	attempt := c.inbox.dispatchRetryAttempts
	c.inbox.dispatchRetryAttempts++
	c.inbox.dispatchRetryScheduled = true
	schedule := c.inbox.scheduleDispatchRetry
	c.inbox.mu.Unlock()

	delay := [...]time.Duration{50 * time.Millisecond, 200 * time.Millisecond, 500 * time.Millisecond}[attempt]
	retry := func() {
		c.inbox.mu.Lock()
		c.inbox.dispatchRetryScheduled = false
		c.inbox.mu.Unlock()
		c.maybeDispatchInbox()
	}
	if schedule != nil {
		schedule(delay, retry)
		return
	}
	time.AfterFunc(delay, retry)
}

func (c *Controller) resetInboxDispatchRetries() {
	c.inbox.mu.Lock()
	c.inbox.dispatchRetryAttempts = 0
	// tl1339 刀①: a kick that stops failing (started turn or benign empty
	// scan) ends the retry-exhaustion episode — a later re-exhaustion earns a
	// fresh exhaustion WARN. The gate-held WARN flag (gateHeldLogged) is
	// intentionally untouched here: every idle scan lands in this reset, and
	// only the gate itself lifting ends that episode (cleared in
	// traceInboxGateHeld / clearInboxDispatchGateHeld).
	c.inbox.dispatchRetryExhaustedLogged = false
	c.inbox.dispatchRetryLastID = ""
	c.inbox.dispatchRetryLastErr = nil
	c.inbox.mu.Unlock()
}

// traceInboxGateHeld is the tl1339 刀① visibility patch for the pump's silent
// gate-held stop (调研报告 §2.1 #5). NextQueued answers "empty" both for a
// truly empty queue and when the manifest gate (Paused / schema readonly)
// hides queued follow-ups — the two used to fold into one unobservable idle,
// so a multi-hour parked backlog looked identical to a quiet session and
// burned no budget anywhere. This trace changes NO dispatch behavior (the
// gate owner — user resume or the 709 auto-resume — still owns the next
// kick): it leaves one transition-gated WARN per gate-held episode plus a
// per-cycle counter, with paused provenance (user vs automatic) in the line.
func (c *Controller) traceInboxGateHeld() {
	st, err := c.ensureInbox()
	if err != nil {
		return
	}
	// CachedSnapshot: owner-local in-memory read, no cross-process disk lock —
	// the dispatch loop must not grow a lock-wait because of a log line.
	snap := st.CachedSnapshot()
	queued := 0
	for _, item := range snap.Items {
		if item.State == sessioninbox.StateQueued {
			queued++
		}
	}
	c.inbox.mu.Lock()
	if queued == 0 || (!snap.Paused && !snap.Readonly) {
		// Benign empty scan, or the gate lifted between NextQueued and this
		// snapshot: the episode is over — re-arm the transition WARN.
		c.inbox.gateHeldLogged = false
		c.inbox.mu.Unlock()
		return
	}
	first := !c.inbox.gateHeldLogged
	c.inbox.gateHeldLogged = true
	c.inbox.mu.Unlock()
	sessioninbox.NoteGateHeldSkip()
	if first {
		slog.Warn("controller: inbox dispatch gate-held; queued followups parked (item stays queued; no retry armed — the gate owner owns the next kick)",
			"queued", queued, "paused", snap.Paused, "user_paused", snap.UserPaused, "readonly", snap.Readonly)
	}
}

// clearInboxDispatchGateHeld ends a gate-held episode from the success side: a
// scan admitted real work, so nothing is parked behind the gate anymore.
func (c *Controller) clearInboxDispatchGateHeld() {
	c.inbox.mu.Lock()
	c.inbox.gateHeldLogged = false
	c.inbox.mu.Unlock()
}

// setDispatchRetryFailure pins the retry-class failure for the budget-
// exhaustion WARN (tl1339 刀①): the spent-budget line must name the item and
// error that kept failing, not just the fact of standing down.
func (c *Controller) setDispatchRetryFailure(id string, err error) {
	c.inbox.mu.Lock()
	c.inbox.dispatchRetryLastID = id
	c.inbox.dispatchRetryLastErr = err
	c.inbox.mu.Unlock()
}

// inboxRuntimeRetryBackoff is the 任务579 schedule for the runtime-unpublished
// dead path: 3 retries after the initial kick (5s / 15s / 45s, the task book's
// prescription). Bounded by construction — after the last attempt the item is
// reported through the exhaustion hook and the budget re-arms for the next
// episode, so a permanently unpublished runtime costs at most one timer at a
// time and one report per stuck item, never a hot loop.
var inboxRuntimeRetryBackoff = [...]time.Duration{5 * time.Second, 15 * time.Second, 45 * time.Second}

// SetOnInboxDispatchExhausted registers the host-visible sink for the bounded
// retry budget running out (任务579 验收④). Production wiring normally comes
// through Options.OnInboxDispatchExhausted; this setter exists for hosts that
// wire after construction. Nil keeps the log-only behaviour.
func (c *Controller) SetOnInboxDispatchExhausted(fn func(InboxDispatchExhausted)) {
	c.mu.Lock()
	c.modelSettings.onInboxDispatchExhausted = fn
	c.mu.Unlock()
}

// scheduleInboxRuntimeRetry arms one bounded retry for the runtime-unpublished
// dead path, or reports exhaustion once the 3-retry budget is spent. The
// timer is one-shot and single-instance: a second caller while one is armed
// is a no-op, the callback is a no-op on a closed inbox, and a successful or
// empty dispatch stops and clears it — the goroutine/timer lifecycle always
// terminates (交付纪律: 重试必须有终止条件).
//
// 任务808: single-consumer boundary against every other head consumer (the
// desktop idle-turn bridge's RunInboxTurn, a manual retry, a drain merge).
// The chain re-drives only the item it was pinned to, and only while that
// item is still the unclaimed FIFO head: when the head moved between the
// failed attempt and this arm — someone else claimed and consumed it —
// re-driving would race the new owner, burn retries against finished work,
// and the exhaustion report would name an item nobody is stuck on. The chain
// stands down instead; a new stuck head re-arms a fresh chain with its own
// budget (the pin change resets attempts in dispatchInboxOnce).
func (c *Controller) scheduleInboxRuntimeRetry() {
	c.inbox.mu.Lock()
	pinned := c.inbox.runtimeRetryItem.ID
	c.inbox.mu.Unlock()
	if pinned != "" {
		// Store read outside inbox.mu (nextInboxDispatchItem takes it
		// internally). The pin is re-validated on every timer fire via the
		// same head pick, so a TOCTOU here self-corrects on the next kick.
		meta, ok, err := c.nextInboxDispatchItem()
		if err != nil || !ok || meta.ID != pinned {
			c.resetInboxRuntimeRetry()
			return
		}
	}
	c.inbox.mu.Lock()
	if c.inbox.closed || c.inbox.runtimeRetryScheduled {
		c.inbox.mu.Unlock()
		return
	}
	item := c.inbox.runtimeRetryItem
	attempt := c.inbox.runtimeRetryAttempts
	if attempt >= len(inboxRuntimeRetryBackoff) {
		// Budget spent: surface it once, then re-arm the counter so the NEXT
		// stuck item (or a manually retried one) gets its own bounded budget
		// instead of inheriting a burned one. The hook is read under c.mu
		// (same home as beforeInboxDispatch) with inbox.mu already released —
		// the hook must never run under any controller lock.
		c.inbox.runtimeRetryAttempts = 0
		c.inbox.mu.Unlock()
		c.mu.Lock()
		hook := c.modelSettings.onInboxDispatchExhausted
		c.mu.Unlock()
		slog.Error("controller: inbox open retries exhausted; item stays queued, not dropped",
			"id", item.ID, "source", item.Source, "collab_msg_id", item.CollabMsgID,
			"attempts", len(inboxRuntimeRetryBackoff), "last_err", ErrInboxRuntimeUnpublished.Error())
		if hook != nil {
			hook(InboxDispatchExhausted{
				ItemID:       item.ID,
				Source:       item.Source,
				CollabMsgID:  item.CollabMsgID,
				CollabMailTo: item.CollabMailTo,
				Attempts:     len(inboxRuntimeRetryBackoff),
				Err:          ErrInboxRuntimeUnpublished,
			})
		}
		return
	}
	delay := inboxRuntimeRetryBackoff[attempt]
	c.inbox.runtimeRetryAttempts++
	c.inbox.runtimeRetryScheduled = true
	retry := func() {
		c.inbox.mu.Lock()
		c.inbox.runtimeRetryScheduled = false
		if c.inbox.runtimeRetryTimer != nil {
			c.inbox.runtimeRetryTimer.Stop()
			c.inbox.runtimeRetryTimer = nil
		}
		c.inbox.mu.Unlock()
		c.maybeDispatchInbox()
	}
	if schedule := c.inbox.scheduleRuntimeRetry; schedule != nil {
		// Deterministic tests own the timer; the single-instance guard above
		// is the scheduled flag, which covers this path too.
		c.inbox.mu.Unlock()
		schedule(delay, retry)
		return
	}
	c.inbox.runtimeRetryTimer = time.AfterFunc(delay, retry)
	c.inbox.mu.Unlock()
	slog.Warn("controller: inbox open retry armed",
		"id", item.ID, "attempt", attempt+1, "max", len(inboxRuntimeRetryBackoff), "delay", delay)
}

// resetInboxRuntimeRetry stops a pending runtime-unpublished retry and clears
// the budget: a started turn or an empty queue means the dead path is over.
func (c *Controller) resetInboxRuntimeRetry() {
	c.inbox.mu.Lock()
	if c.inbox.runtimeRetryTimer != nil {
		c.inbox.runtimeRetryTimer.Stop()
		c.inbox.runtimeRetryTimer = nil
	}
	c.inbox.runtimeRetryScheduled = false
	c.inbox.runtimeRetryAttempts = 0
	c.inbox.runtimeRetryItem = sessioninbox.InboxItemMeta{}
	c.inbox.mu.Unlock()
}
