package control

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/jobs"
)

// 任务 553：后台任务完成 → 唤起空闲父会话续轮。
//
// 触发链：jobs.Manager 在完成摘要入队后回调本控制器（AddCompletionObserver）→
// 合并窗口（coalesce）→ 到期后逐闸判定（开关/归属/去重/预算/节流/让位）→
// runGuarded 准入一回合成的「自动续轮」。模型可见的结果摘要完全复用 compose()
// 既有 DrainCompletedNoteForSession 通道（<background-jobs> 块），不另造结果
// 通道。全部唤醒状态挂 c.mu，零新锁；观察者回调在 m.mu 外触发，且回调与
// fireWakeTurn 都不持 c.mu 调任何 jobs 方法——c.mu 与 m.mu 从不嵌套。

const (
	// defaultWakeCoalesceWindow batches completions that land close together
	// into one wake turn (design requirement d). Not config-exposed: it is a
	// latency/cost trade, not a user-facing policy.
	defaultWakeCoalesceWindow = 2 * time.Second

	defaultWakeMaxTurnsPerWindow = 3
	defaultWakeWindowSeconds     = 600
	defaultWakeThrottleSeconds   = 60
)

// BackgroundJobWakeOptions opts a controller into waking its idle session when
// a background job it owns finishes (task 553). Iron rule 2: default off; the
// values are a boot snapshot (restart to apply), like the other boot options.
type BackgroundJobWakeOptions struct {
	Enabled bool
	// MaxTurnsPerWindow caps wake turns per rolling WindowSeconds (the
	// self-drive budget: wake → model spawns another job → completes → wake …
	// is bounded). <=0 keeps the default.
	MaxTurnsPerWindow int
	// WindowSeconds is the rolling budget window. <=0 keeps the default.
	WindowSeconds int
	// ThrottleSeconds is the minimum spacing between admitted wake turns.
	// <=0 keeps the default.
	ThrottleSeconds int
}

// jobWakeState is the controller-side wake bookkeeping. Everything is guarded
// by c.mu; the coalesce timer fires on its own goroutine and re-enters the
// guards.
type jobWakeState struct {
	opts           BackgroundJobWakeOptions
	coalesceWindow time.Duration
	removeObserver func()

	timer            *time.Timer
	wokenJobs        map[string]struct{} // per-job dedupe: one wake contribution per job, ever
	budget           []time.Time         // admitted wake turns inside the rolling window
	lastWakeAdmitted time.Time
	// yieldNoticed dedupes the user-facing yield notice (让位提示)：每次让位只
	// 发一条「将自动续轮」提示，下一批被接受的完成将其复位，让位提示不刷屏
	// 也不被永久吞掉。
	yieldNoticed bool
}

// initBackgroundJobWake snapshots the boot options and, when enabled,
// registers the completion observer on the (possibly shared) jobs manager.
func (c *Controller) initBackgroundJobWake(opts BackgroundJobWakeOptions) {
	c.wake.opts = opts
	c.wake.coalesceWindow = defaultWakeCoalesceWindow
	if !opts.Enabled || c.jobs == nil {
		return
	}
	c.wake.wokenJobs = map[string]struct{}{}
	c.wake.removeObserver = c.jobs.AddCompletionObserver(c.noteJobCompletionForWake)
}

// noteJobCompletionForWake runs on the job's own goroutine (outside m.mu).
// Trigger surface (design requirement a): only this session's own jobs wake
// this session, and only Done/Failed — Killed and Interrupted are
// user-initiated or teardown states that never need a model round.
func (c *Controller) noteJobCompletionForWake(sessionID, jobID string, st jobs.Status) {
	if !c.wake.opts.Enabled {
		return
	}
	if st != jobs.Done && st != jobs.Failed {
		return
	}
	if sessionID != c.parentSessionID() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	if _, seen := c.wake.wokenJobs[jobID]; seen {
		slog.Info("control: background job wake skipped", "reason", "job_already_woken", "job", jobID)
		return
	}
	if c.wake.wokenJobs == nil {
		c.wake.wokenJobs = map[string]struct{}{}
	}
	c.wake.wokenJobs[jobID] = struct{}{}
	// 新完成的批次重新获得一次让位提示资格：上一批的提示已随重踢/真实轮消化。
	c.wake.yieldNoticed = false
	c.scheduleWakeTurnLocked()
}

// wakeKickAfterTurnFinished re-arms the wake check when the admission gate
// reopens with no parked user turn to hand the gate to. It closes the gap
// "job finished while another turn was still running": the queued completion
// notes could otherwise wait for a user who left. All guards still apply at
// fire time, so this stays inside the self-drive budget.
func (c *Controller) wakeKickAfterTurnFinished() {
	if !c.wake.opts.Enabled {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.scheduleWakeTurnLocked()
}

// scheduleWakeTurnLocked arms the coalesce timer once; completions and finish
// kicks that land inside the window collapse into this single firing
// (design requirement d).
func (c *Controller) scheduleWakeTurnLocked() {
	if c.wake.timer != nil {
		return
	}
	c.wake.timer = time.AfterFunc(c.wake.coalesceWindow, c.fireWakeTurn)
}

// fireWakeTurn runs the full guard chain on the timer goroutine and, when
// every gate opens, admits one synthetic wake turn through the normal
// runGuarded admission (existing turn lifecycle, existing locks).
func (c *Controller) fireWakeTurn() {
	c.mu.Lock()
	c.wake.timer = nil
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return
	}
	// Peek without draining (and without holding c.mu — no lock nesting).
	if c.jobs == nil || !c.jobs.HasCompletedNotesForSession(c.parentSessionID()) {
		return
	}

	// 让位 (design requirement c): a live or finishing turn keeps the gate; the
	// notes stay queued and the finish hook re-kicks when the gate reopens.
	if c.Running() {
		c.noticeWakeYield("turn_running")
		return
	}
	// 让位: pending user work (parked turns, unapplied steer, queued durable
	// inbox items) always wins; the next real turn drains the notes itself.
	if c.hasPendingUserWork() {
		c.noticeWakeYield("pending_user_work")
		return
	}

	// Self-drive budget (design requirement b): rolling window of admitted
	// wake turns plus a session-level throttle. Over either → degrade to
	// today's behavior (Notice only, the note still rides the next turn).
	opts := c.wake.opts
	c.mu.Lock()
	now := time.Now()
	window := time.Duration(opts.windowSeconds()) * time.Second
	kept := c.wake.budget[:0]
	for _, at := range c.wake.budget {
		if now.Sub(at) < window {
			kept = append(kept, at)
		}
	}
	c.wake.budget = kept
	if len(c.wake.budget) >= opts.maxTurnsPerWindow() {
		c.mu.Unlock()
		slog.Info("control: background job wake degraded", "reason", "budget_exhausted",
			"max_turns_per_window", opts.maxTurnsPerWindow(), "window_seconds", opts.windowSeconds())
		return
	}
	if !c.wake.lastWakeAdmitted.IsZero() {
		if wait := c.wake.lastWakeAdmitted.Add(time.Duration(opts.throttleSeconds()) * time.Second).Sub(now); wait > 0 {
			c.mu.Unlock()
			slog.Info("control: background job wake degraded", "reason", "throttled",
				"retry_in", wait.Round(time.Millisecond).String())
			return
		}
	}
	c.mu.Unlock()

	parent := c.parentSessionID()
	// Task 394: an autopilot session's wake round is a batch round opener —
	// it carries the compact batch-context header when the dial is on. The
	// header is a PREFIX, so IsBackgroundJobWakeTurnContent (a Contains check
	// on the fixed prompt) still recognizes the turn; non-autopilot sessions
	// get the prompt byte-for-byte as before.
	wakePrompt := backgroundWakeTurnPrompt
	if header := c.autopilotBatchContextHeader(); header != "" {
		wakePrompt = header + "\n\n" + wakePrompt
	}
	admitted := c.runGuarded(func(ctx context.Context) error {
		// Task 299 fence: bound inside the closure over the guard's fresh ctx
		// (idempotent). The wake turn runs unattended — it must never reach the
		// orchestrator with an unbound barrier.
		ctx = c.withRecoveryFenceBindings(ctx)
		return newTurnOrchestrator(c).runSyntheticTurnWithRawDisplay(
			ctx, wakePrompt, wakePrompt, backgroundWakeTurnDisplay)
	})
	switch admitted {
	case turnStarted:
		c.mu.Lock()
		c.wake.budget = append(c.wake.budget, time.Now())
		c.wake.lastWakeAdmitted = time.Now()
		c.wake.yieldNoticed = false
		c.mu.Unlock()
		slog.Info("control: background job wake admitted", "session", parent, "auto_turn", true)
	default:
		slog.Info("control: background job wake degraded", "reason", "admission_refused",
			"outcome", wakeAdmissionOutcome(admitted))
	}
}

// noticeWakeYield records the yield (让位) log line and, once per arriving
// completion batch, emits the user-facing 「将自动续轮」 notice so a user
// watching an in-flight turn knows the finished result will be picked up
// automatically (task 553 前端提示：让位时的提示). The flag resets on the next
// accepted completion and on admission, so the hint can never spam nor be
// permanently swallowed. Emit runs outside c.mu; sinks are concurrency-safe.
func (c *Controller) noticeWakeYield(reason string) {
	c.mu.Lock()
	first := !c.wake.yieldNoticed
	c.wake.yieldNoticed = true
	c.mu.Unlock()
	slog.Info("control: background job wake yielded", "reason", reason)
	if !first {
		return
	}
	c.sink.Emit(event.Event{
		Kind:  event.Notice,
		Code:  event.NoticeCodeBackgroundJobWakeYielded,
		Level: event.LevelInfo,
		Text:  "后台任务已完成；等当前回合结束后将自动续轮处理其结果",
	})
}

func wakeAdmissionOutcome(r admissionResult) string {
	switch r {
	case turnParked:
		return "parked"
	case turnDroppedRunning:
		return "turn_running"
	case turnDroppedRotating:
		return "session_rotating"
	case turnDroppedClosed:
		return "controller_closed"
	case turnDroppedDraining:
		return "draining_generation"
	case turnDroppedWriteAuthority:
		return "write_authority"
	default:
		return "unknown"
	}
}

// shutdownBackgroundJobWake unregisters the observer and disarms the timer.
// Called from close() outside the c.mu critical section; both calls are
// goroutine-safe, and fireWakeTurn's closed check covers any in-flight timer.
func (c *Controller) shutdownBackgroundJobWake() {
	c.mu.Lock()
	timer := c.wake.timer
	c.wake.timer = nil
	c.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
	if c.wake.removeObserver != nil {
		c.wake.removeObserver()
		c.wake.removeObserver = nil
	}
}

// BackgroundWakeTurnMarker is the transcript label for an automatic
// background-job wake turn (task 553). Host transcript projections use it to
// render the wake turn as a labeled auto-continuation row instead of quoting
// the model-facing prompt.
const BackgroundWakeTurnMarker = "〔自动续轮〕后台任务已完成"

// IsBackgroundJobWakeTurnContent reports whether a persisted user-role message
// carries the task-553 wake turn input (the marker-bearing synthetic prompt).
// Host projections call it before the generic host-guidance fallback so the
// auto turn is labeled rather than quoted.
func IsBackgroundJobWakeTurnContent(content string) bool {
	return strings.Contains(content, backgroundWakeTurnPrompt)
}

// backgroundWakeTurnPrompt is the model-facing body of an automatic wake turn.
// The <background-jobs> block (drained completion summaries) is prepended by
// compose(); the prompt tells the model what the turn is and bounds its
// behavior. The self-identifying text also makes the auto turn recognizable in
// the transcript (design requirement f).
const backgroundWakeTurnPrompt = "Automatic background-job wake turn: background job(s) you started finished while the session was idle. Their completion summaries are in the <background-jobs> block above. Continue the work that depended on those results, report anything the user must decide, and avoid starting new background jobs unless the original request still requires them."

// backgroundWakeTurnDisplay is the turn's display label, single-sourced from
// BackgroundWakeTurnMarker. recordDisplayForNewUser never records it (host
// messages are not user-authored turns); the visible labeling lives in the
// host transcript projections, which match on IsBackgroundJobWakeTurnContent.
const backgroundWakeTurnDisplay = BackgroundWakeTurnMarker

// windowSeconds/maxTurnsPerWindow/throttleSeconds resolve the configured
// values against the defaults so the guards never see zero knobs.
func (o BackgroundJobWakeOptions) maxTurnsPerWindow() int {
	if o.MaxTurnsPerWindow <= 0 {
		return defaultWakeMaxTurnsPerWindow
	}
	return o.MaxTurnsPerWindow
}

func (o BackgroundJobWakeOptions) windowSeconds() int {
	if o.WindowSeconds <= 0 {
		return defaultWakeWindowSeconds
	}
	return o.WindowSeconds
}

func (o BackgroundJobWakeOptions) throttleSeconds() int {
	if o.ThrottleSeconds <= 0 {
		return defaultWakeThrottleSeconds
	}
	return o.ThrottleSeconds
}
