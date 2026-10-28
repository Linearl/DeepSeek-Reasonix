package control

import (
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/sessioninbox"
)

// turnStallThreshold is the silence after which a running turn is reported as
// possibly stuck. It only warns: the user decides whether to stop, because a
// legitimately long tool and a wedged one look identical from here.
var turnStallThreshold atomic.Int64

// turnPreflightStallAbortThreshold (tl-1213) is the total event silence after
// which a running turn whose LAST tool outcome was a preflight (not_run)
// failure is auto-terminated instead of hanging in "running" forever. The
// 2026-10-28 field incident: a bash call whose arguments carried a duplicated
// "command" field (second copy empty) failed preflight with state=not_run, and
// the turn never reached any terminal state — 40 minutes of dead air with
// steer/followup delivery fully blocked. The default is 15 minutes:
//
//   - every live path re-arms the silence clock (stream attempts, retry
//     frames, tool progress, text) — a provider still cycling events can never
//     look silent, so the threshold only judges turns that produce NOTHING;
//   - the provider layer's own ResponseHeaderTimeout is 300s (all three
//     provider families); 15 minutes is 3x that guard, so by the time the
//     watchdog fires the provider-side recovery machinery has itself wedged
//     rather than merely being slow;
//   - the stall WARN fires at 10 minutes (turnStallThreshold), leaving the
//     user a 5-minute window to press Stop before the automatic abort (A4);
//   - the incident ran 40 minutes; 15 minutes cuts the worst-case session
//     loss to about a third while staying far above any legitimate round.
var turnPreflightStallAbortThreshold atomic.Int64

func init() {
	turnStallThreshold.Store(int64(10 * time.Minute))
	turnPreflightStallAbortThreshold.Store(int64(15 * time.Minute))
}

// preflightFailureInfo identifies the tool call whose preflight failure armed
// the stall marker, so the abort notice and the terminal error can name it.
type preflightFailureInfo struct {
	toolName string
	callID   string
}

// turnLiveness remembers the last event a running turn produced so a silent
// stretch can be surfaced instead of leaving "working" unexplained.
type turnLiveness struct {
	lastEvent atomic.Int64
	warned    atomic.Bool
	// preflightFailure (tl-1213) marks a turn whose most recent tool outcome
	// was a preflight (not_run) failure. It is cleared by any later event that
	// proves work beyond the failure (a new dispatch, progress from a sibling
	// call, another result, a prompt). Stream attempts and retry frames do NOT
	// clear it: they are provider-side churn, and "preflight failure persisted
	// while nothing else happened" is exactly the wedged shape the watchdog
	// owns. Events also keep the silence clock alive, so a provider retry loop
	// still never trips the threshold.
	preflightFailure atomic.Pointer[preflightFailureInfo]
}

func (l *turnLiveness) reset(now time.Time) {
	l.lastEvent.Store(now.UnixNano())
	l.warned.Store(false)
	l.preflightFailure.Store(nil)
}

func (l *turnLiveness) observe(e event.Event, now time.Time) {
	if e.Kind == event.Notice && e.Code == event.NoticeCodeTurnStalled {
		return
	}
	l.lastEvent.Store(now.UnixNano())
	l.warned.Store(false)
	l.observePreflightMarker(e)
}

// observePreflightMarker maintains the tl-1213 preflight-stall marker. See the
// field comment for the exact semantics.
func (l *turnLiveness) observePreflightMarker(e event.Event) {
	switch e.Kind {
	case event.ToolResult:
		if isPreflightFailureToolResult(e.Tool) {
			l.preflightFailure.Store(&preflightFailureInfo{toolName: e.Tool.Name, callID: e.Tool.ID})
			return
		}
		l.preflightFailure.Store(nil)
	case event.ToolDispatch:
		// Speculative partial dispatches are stream churn, not committed work.
		if !e.Tool.Partial {
			l.preflightFailure.Store(nil)
		}
	case event.ToolStarted, event.ToolProgress, event.ToolResultPreview, event.Steer,
		event.AskRequest, event.ApprovalRequest, event.MCPInteractionRequest:
		l.preflightFailure.Store(nil)
	}
}

// isPreflightFailureToolResult reports a ToolResult whose execution metadata
// proves the call was refused at preflight and never ran (state=not_run).
// Both markers come from the bash/shell preflight path (tool.ShellPhasePreflight /
// tool.ShellStateNotRun, mirrored into event.ShellExecution); the error text
// guard keeps a phase-stamped-but-successful execution from arming the marker.
func isPreflightFailureToolResult(t event.Tool) bool {
	ex := t.Execution
	return ex != nil && ex.FailurePhase == "preflight" && ex.State == "not_run" && t.Err != ""
}

// silenceFor returns how long the turn has produced no events.
func (l *turnLiveness) silenceFor(now time.Time) time.Duration {
	last := l.lastEvent.Load()
	if last == 0 {
		return 0
	}
	return now.Sub(time.Unix(0, last))
}

// stalledFor claims the single warning for the current silence.
func (l *turnLiveness) stalledFor(now time.Time) (time.Duration, bool) {
	silence := l.silenceFor(now)
	if silence < time.Duration(turnStallThreshold.Load()) {
		return 0, false
	}
	return silence, l.warned.CompareAndSwap(false, true)
}

// TurnPreflightStallAbortError is the terminal cause recorded when the
// turn-level watchdog aborts a preflight-stalled turn (tl-1213). It
// deliberately does not wrap context.Canceled: the abort arrives through the
// cancel machinery, but the honest terminal state is failed-with-cause, not
// interrupted-by-user.
type TurnPreflightStallAbortError struct {
	ToolName string
	CallID   string
	Silence  time.Duration
}

func (e *TurnPreflightStallAbortError) Error() string {
	return fmt.Sprintf("turn aborted by the stall watchdog: the last tool call %q (id %s) failed preflight without executing and the turn produced no further events for %s. Nothing was executed and no data was changed; queued messages were kept — resend to continue.",
		e.ToolName, e.CallID, e.Silence.Round(time.Second))
}

// preflightStallAbort is the controller-side marker binding one watchdog fire
// to the turn generation it aborted, consumed by finishGuardedTurn to rewrite
// the terminal state.
type preflightStallAbort struct {
	generation uint64
	cause      preflightFailureInfo
	silence    time.Duration
}

func (c *Controller) warnIfTurnStalled(now time.Time) {
	c.mu.Lock()
	running := c.running
	executor := c.executor
	c.mu.Unlock()
	if !running {
		return
	}
	silence, ok := c.liveness.stalledFor(now)
	if ok {
		// 任务1147: a silent stretch while a compaction pass is in flight is the
		// summarizer request still generating, not a wedged turn. The 2026-10-24
		// field report had a healthy chunked compaction (1.3M→250K) read as a
		// failure because this warning kept urging Stop during it — surface the
		// same silence as a neutral "compaction running" notice instead. The
		// warned CAS above already claimed this stretch, so the cadence (one
		// notice per silent stretch, re-armed by progress) is unchanged.
		if executor.CompactionRunning() {
			c.sink.Emit(event.Event{
				Kind:  event.Notice,
				Code:  event.NoticeCodeTurnStalledCompacting,
				Level: event.LevelInfo,
				Text:  fmt.Sprintf("No progress for %s because a context compaction is running; the turn is waiting on its result.", silence.Round(time.Minute)),
			})
		} else {
			c.sink.Emit(event.Event{
				Kind:  event.Notice,
				Code:  event.NoticeCodeTurnStalled,
				Level: event.LevelWarn,
				Text:  fmt.Sprintf("No progress for %s. The turn is still running; press Stop if it looks stuck.", silence.Round(time.Minute)),
			})
		}
	}
	c.maybeAbortPreflightStalledTurn(now)
}

// maybeAbortPreflightStalledTurn is the tl-1213 turn-level watchdog. When the
// running turn's last tool outcome was a preflight (not_run) failure and the
// turn has produced no events at all for the abort threshold, the turn has no
// self-recovery path left (the model never saw new work to react to and no
// provider round is producing events) — terminate it the same way the user's
// Stop does, with a visible notice, so the turn reaches a real terminal state
// instead of hanging in "running" forever. A plain stall without the
// preflight marker keeps the warn-only contract (a legitimately long tool and
// a wedged one look identical from here); the preflight marker is what makes
// automatic termination safe.
func (c *Controller) maybeAbortPreflightStalledTurn(now time.Time) {
	threshold := time.Duration(turnPreflightStallAbortThreshold.Load())
	if threshold <= 0 {
		return
	}
	silence := c.liveness.silenceFor(now)
	if silence < threshold {
		return
	}
	cause := c.liveness.preflightFailure.Load()
	if cause == nil {
		return
	}
	c.mu.Lock()
	running, canceling, closed := c.running, c.canceling, c.closed
	generation := c.turnGeneration
	c.mu.Unlock()
	if !running || canceling || closed {
		return
	}
	if c.executor != nil && c.executor.CompactionRunning() {
		// 任务1147 face: during a compaction pass the executor goroutine is the
		// summarizer; never race it with a forced unwind.
		return
	}
	// The cancel path is the user Stop path (cancelTurnLocked + finishCancel),
	// so A4 semantics are shared by construction: during the watchdog's wait
	// window a user Stop fires exactly the same machinery.
	slog.Warn("control: preflight-stall watchdog aborting the turn (tl-1213)",
		"tool", cause.toolName, "call", cause.callID,
		"silence", silence.Round(time.Second).String(),
		"threshold", threshold.String())
	c.sink.Emit(event.Event{
		Kind:  event.Notice,
		Code:  event.NoticeCodeTurnPreflightStallAbort,
		Level: event.LevelWarn,
		Text: fmt.Sprintf("The turn was aborted automatically: the last tool call %q failed preflight without executing and nothing has happened for %s. No command was run; resend your message to continue.",
			cause.toolName, silence.Round(time.Second)),
	})
	// ③: snapshot the steers accepted for this turn but never consumed BEFORE
	// the cancel unwinds the agent queue, so finishInFlightTurn requeues them
	// as follow-up turns instead of acknowledging them done (the messages must
	// not die with the collapsed turn).
	c.armSteerRequeue(generation)
	c.mu.Lock()
	c.preflightStallAbort = &preflightStallAbort{generation: generation, cause: *cause, silence: silence}
	c.mu.Unlock()
	c.promptResolveMu.Lock()
	turnID, cancelled := c.cancelTurnLocked()
	c.promptResolveMu.Unlock()
	c.finishCancel(turnID, cancelled)
}

// takePreflightStallAbort consumes the watchdog marker when the finishing
// completion is the exact turn the watchdog aborted. A generation mismatch
// (deadlock self-heal replaced the turn) drops the marker without rewriting.
func (c *Controller) takePreflightStallAbort(completion *guardedTurnCompletion) *preflightStallAbort {
	if c == nil || completion == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	armed := c.preflightStallAbort
	c.preflightStallAbort = nil
	if armed == nil || armed.generation != completion.generation {
		return nil
	}
	return armed
}

// armSteerRequeue records the active inbox items that are still sitting in
// steer_accepted (handed to the agent queue, loader not yet fired) so the
// collapsed turn's close requeues them instead of acknowledging them done.
func (c *Controller) armSteerRequeue(generation uint64) {
	st := c.inbox.store
	if st == nil {
		return
	}
	c.inbox.mu.Lock()
	ids := c.inbox.activeIDs()
	c.inbox.mu.Unlock()
	var requeue []string
	for _, id := range ids {
		if meta, _, err := st.ReadItem(id); err == nil && meta.State == sessioninbox.StateSteerAccepted {
			requeue = append(requeue, id)
		}
	}
	if len(requeue) == 0 {
		return
	}
	c.inbox.mu.Lock()
	c.inbox.stallRequeue = requeue
	c.inbox.stallRequeueGen = generation
	c.inbox.mu.Unlock()
}

// takeSteerRequeue returns the armed requeue set for a matching turn
// generation (and clears it in every case). The generation comparison uses the
// stamp captured at arm time, never a live read — turnGeneration is guarded by
// c.mu and this runs under the inbox lock.
func (c *Controller) takeSteerRequeue(generation uint64) []string {
	c.inbox.mu.Lock()
	defer c.inbox.mu.Unlock()
	armed := c.inbox.stallRequeue
	armedGen := c.inbox.stallRequeueGen
	c.inbox.stallRequeue = nil
	c.inbox.stallRequeueGen = 0
	if armed == nil || armedGen != generation {
		return nil
	}
	return armed
}
