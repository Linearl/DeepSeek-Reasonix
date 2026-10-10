package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"reasonix/internal/provider"
)

// compactionProgress is how compaction is faring in this session: whether a
// fold stopped reducing, how many ran back to back, and which retries already
// ran in the active turn. The fields are cleared together on lineage resets.
type compactionProgress struct {
	stuck          bool   // a fold landed above the trigger, so the same-view pressure retry is pointless
	stuckInputHash string // provider-visible view covered by stuck; changed input may retry
	consecutive    int    // back-to-back folds since one last helped
	// failedTurn backs off changed-view retries within one active tool loop.
	// A later user turn may retry, while hard-ceiling recovery bypasses it.
	failedTurn atomic.Int64
	// lastTurn stops the post-turn observer and the pre-send preflight from
	// paying for two summaries during one active tool loop.
	lastTurn atomic.Int64
	// 任务461-P13② growth watch: lastPrepareEst is the previous maintenance
	// check's context estimate; growthWarnedTurn/lastGrowthWarnAt dedupe the
	// sharp-growth warning. All three are only touched under compactionRunMu
	// (prepareOnce → observeContextGrowth), so plain fields suffice.
	lastPrepareEst   int64
	growthWarnedTurn int64
	lastGrowthWarnAt time.Time
	// 任务516④ fallback watch: fallbackWarnAt throttles the "view fell back to
	// the full canonical transcript" warning to one line per cooldown. Same
	// compactionRunMu discipline as the growth-watch fields.
	fallbackWarnAt time.Time
}

// ContextManager is the sole owner of provider-visible context maintenance.
// Canonical session messages are immutable inputs; Prepare evolves only the
// durable projection and returns the exact visible view for one sampling round.
type ContextManager struct {
	agent *Agent
}

// ContextPreparePolicy describes one maintenance transaction.
type ContextPreparePolicy struct {
	Trigger      string
	Instructions string
	Force        bool
	// ObservedInputTokens is used by compatibility harnesses that invoke the
	// old post-turn shim directly. Production Prepare estimates the current view
	// from its calibrated final request shape.
	ObservedInputTokens int
	// AllowChunkedFallback enables fragment/tree-reduce recovery after a single
	// summary fails. Ordinary pressure/overflow leave this false.
	AllowChunkedFallback bool
}

// PreparedContext is the frozen result of a successful Prepare transaction.
type PreparedContext struct {
	Messages          []provider.Message
	InputTokens       int
	ProjectionVersion uint64
}

func (a *Agent) contextManager() ContextManager { return ContextManager{agent: a} }

// PrepareContext is the public automatic-maintenance entry used by smoke tools
// and controllers that need a one-shot Prepare without sampling.
func (a *Agent) PrepareContext(ctx context.Context) error {
	_, err := a.contextManager().Prepare(ctx, ContextPreparePolicy{Trigger: CompactionTriggerPressure})
	return err
}

// ObserveUsage is retained as a compatibility hook. Usage observations never
// mutate the provider-visible checkpoint.
func (m ContextManager) ObserveUsage(u *provider.Usage) {
	_ = u
}

// Prepare is the sole automatic maintenance entry. Below compact_ratio it does
// nothing. At or above the trigger it runs one single-flight prune/summary
// transaction, with at most two successful summary attempts under pressure.
func (m ContextManager) Prepare(ctx context.Context, policy ContextPreparePolicy) (PreparedContext, error) {
	// Legacy desktop callers can compact before their runtime context is installed.
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return PreparedContext{}, err
	}
	if policy.Trigger == "" {
		policy.Trigger = CompactionTriggerPressure
	}
	if m.agent == nil {
		return PreparedContext{}, nil
	}
	m.agent.sess.compactionRunMu.Lock()
	defer m.agent.sess.compactionRunMu.Unlock()
	// Cancellation may have arrived while another maintenance transaction held the lock.
	// Reject it before any fast path or projection maintenance can run.
	if err := ctx.Err(); err != nil {
		return PreparedContext{}, err
	}
	return m.prepareOnce(ctx, policy)
}

func (m ContextManager) prepareOnce(ctx context.Context, policy ContextPreparePolicy) (PreparedContext, error) {
	a := m.agent
	if a == nil || a.sess.conversation == nil {
		return PreparedContext{}, nil
	}
	visible := a.modelVisibleMessages()
	// Threshold uses the stable pre-interceptor request shape (messages + tools
	// + role projection). Extension interceptors run only on the real sampling
	// request so side-effecting plugins are not double-invoked; if they expand
	// the prompt past the hard ceiling, overflow recovery still fires.
	est := a.estimatedVisibleRequestTokens(visible)
	viewEst := est
	prepared := PreparedContext{
		Messages:          append([]provider.Message(nil), visible...),
		InputTokens:       est,
		ProjectionVersion: a.currentProjectionVersion(),
	}
	if a.effectiveContextWindow() <= 0 || len(visible) == 0 {
		return prepared, nil
	}
	fold := a.compactTrigger()
	hard := a.hardInputCeiling()
	if policy.ObservedInputTokens > 0 {
		est = policy.ObservedInputTokens
		prepared.InputTokens = est
	}
	inputHash := a.contextMaintenanceInputHash(visible)
	// 任务461-P13② growth watch: record the estimate delta since the previous
	// maintenance check BEFORE any early return, so a view that balloons inside
	// one tool loop (replayed guidance, repeated sub-agent payloads) leaves a
	// log trail even when it stays below the fold trigger.
	a.observeContextGrowth(est)
	a.observeProjectionFallback(est)
	// Receipts back off sub-critical retries only. A failed summary never
	// fabricates a digest; at the ceiling the lossy truncation rescue is the
	// last resort, so the turn still leaves with a view the provider accepts.
	if blocked, _ := a.contextMaintenanceBlocked(inputHash, viewEst); blocked && policy.Trigger != CompactionTriggerManual &&
		policy.Trigger != CompactionTriggerOverflow && est < hard {
		return prepared, nil
	}
	if est < fold {
		a.resetCompactionProgress()
	}
	if a.sess.compaction.stuck && a.sess.compaction.stuckInputHash != inputHash {
		// The previous projection could not reclaim enough from its exact view,
		// but newly appended messages create a new fold boundary and may retry.
		a.sess.compaction.stuck = false
		a.sess.compaction.stuckInputHash = ""
		a.sess.compaction.consecutive = 0
	}
	if a.sess.compaction.stuck && policy.Trigger == CompactionTriggerPressure && est < hard {
		return prepared, nil
	}
	// One user trigger. Overflow is a one-shot physical recovery path only.
	// 任务719: a degraded tail view sits below the fold trigger by design, so
	// without this flag the bounded window would suppress the very compaction
	// that rebuilds the projection — the fold is forced while the view is the
	// rolling tail.
	forceFold := policy.Force || policy.Trigger == CompactionTriggerManual || policy.Trigger == CompactionTriggerOverflow || est >= hard || a.sess.tailView.active()
	if est < fold && !forceFold {
		return prepared, nil
	}

	// A manual compact over the hard ceiling is a rescue, not a convenience:
	// prune first so the never-folded recent tail can shrink too.
	if shouldPruneBeforeFold(policy.Trigger, est >= hard) {
		applied, err := a.pruneToolResultsToProjectionLocked(policy.Trigger)
		if err != nil {
			return PreparedContext{}, err
		}
		if applied {
			prepared = m.currentPrepared()
			est = prepared.InputTokens
			inputHash = a.contextMaintenanceInputHash(prepared.Messages)
			if (policy.Trigger == CompactionTriggerPressure && est < fold) ||
				(policy.Trigger == CompactionTriggerOverflow && est < hard) {
				return prepared, nil
			}
		}
	}

	return m.foldContext(ctx, prepared, policy, inputHash, est, fold, hard, forceFold)
}

func shouldPruneBeforeFold(trigger string, overHardCeiling bool) bool {
	switch trigger {
	case CompactionTriggerPressure, CompactionTriggerOverflow:
		return true
	case CompactionTriggerManual:
		return overHardCeiling
	default:
		return false
	}
}

// manualRecoverySummaries bounds the rescue loop for a manual compact that
// starts at or above the hard input ceiling. Each batch folds the largest
// admissible prefix, so a handful of batches recovers even a view several
// times the window while capping summarizer spend on pathological input.
const manualRecoverySummaries = 4

func maxSummariesFor(policy ContextPreparePolicy, overCeiling bool) int {
	switch {
	case policy.Trigger == CompactionTriggerManual && overCeiling:
		return manualRecoverySummaries
	case policy.Trigger == CompactionTriggerPressure:
		return 2
	default:
		return 1
	}
}

func (m ContextManager) foldContext(ctx context.Context, prepared PreparedContext, policy ContextPreparePolicy, inputHash string, est, fold, hard int, forceFold bool) (PreparedContext, error) {
	a := m.agent
	maxSummaries := maxSummariesFor(policy, est >= hard)
	ladder := newSummaryLadder(maxSummaries)
	result := prepared
	// Task 303: tracks whether an earlier ladder round already installed a
	// fold, so a later round's summary failure is reported as a summary-only
	// problem (compaction kept) instead of "the compaction failed".
	foldInstalled := false
	for ladder.next() {
		mustFree := policy.Trigger == CompactionTriggerOverflow || result.InputTokens >= hard
		outcome, err := a.compactToProjectionLocked(ctx, policy.Trigger, policy.Instructions,
			ladder.request(forceFold, mustFree, policy.AllowChunkedFallback))
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return PreparedContext{}, err
			}
			if ladder.absorbOverflow(err) {
				continue
			}
			return m.summaryFailed(policy, inputHash, hard, foldInstalled, err)
		}
		if outcome == CompactionNoop {
			return m.summaryNoop(policy, inputHash, hard)
		}
		foldInstalled = true

		result = m.currentPrepared()
		if foldLanded(policy, result.InputTokens, fold, hard) {
			a.resetCompactionProgress()
			return result, nil
		}
		forceFold = false
		inputHash = a.contextMaintenanceInputHash(result.Messages)
	}

	reason := fmt.Sprintf("summary result remains above fold trigger after %d attempts (%d >= %d)", maxSummaries, result.InputTokens, fold)
	blockedInputHash := a.contextMaintenanceInputHash(result.Messages)
	a.recordContextMaintenanceBlocked(blockedInputHash, policy.Trigger, "summary", reason)
	a.sess.compaction.stuck = true
	a.sess.compaction.stuckInputHash = blockedInputHash
	a.sess.compaction.consecutive += maxSummaries
	if policy.Trigger == CompactionTriggerOverflow || result.InputTokens >= hard {
		return m.rescueByTruncation(policy, hard, errors.New(reason))
	}
	slog.Info("agent: context maintenance paused below hard ceiling", "reason", reason)
	return result, nil
}

func foldLanded(policy ContextPreparePolicy, tokens, fold, hard int) bool {
	switch policy.Trigger {
	case CompactionTriggerManual, CompactionTriggerOverflow:
		return tokens < hard || tokens < fold
	default:
		return tokens < fold
	}
}

func (m ContextManager) summaryFailed(policy ContextPreparePolicy, inputHash string, hard int, foldInstalled bool, err error) (PreparedContext, error) {
	a := m.agent
	if errors.Is(err, errCompressStaleContext) && policy.Trigger != CompactionTriggerManual {
		reason := "context changed during summary; automatic retry blocked for this generation"
		slog.Warn("agent: context summary blocked", "reason", reason, "trigger", policy.Trigger, "fold_installed", foldInstalled)
		a.recordContextMaintenanceBlocked(inputHash, policy.Trigger, "summary", reason)
		return m.rescueOrFail(policy, hard, errors.New(reason))
	}
	status := "failed"
	if errors.Is(err, errSummaryOutputTruncated) || errors.Is(err, errCheckpointRejected) {
		status = "blocked"
	}
	// Task 303: compaction result and summary result are separate outcomes.
	// When an earlier ladder round already installed the fold, this round's
	// failure (typically a provider stream error mid-summary — the mimo
	// INTERNAL_ERROR sample) must not present as the compaction failing: the
	// projection is live, only the summary refresh is missing, and it stays
	// retryable on the next generation.
	reason := fmt.Sprintf("context summary failed: %v", err)
	if foldInstalled {
		reason = fmt.Sprintf("summary refresh failed after fold was applied (compaction kept): %v", err)
	}
	slog.Warn("agent: context summary failed",
		"status", status, "trigger", policy.Trigger, "fold_installed", foldInstalled,
		"model_ref", a.destinationModelRef(),
		"transient_stream", summaryTransientRetryable(err), "err", err)
	a.recordContextMaintenanceOutcome(inputHash, policy.Trigger, "summary", status, reason, foldInstalled)
	// Task 307: a candidate at or above the physical ceiling would be rejected
	// again with the very same oversized projection, so waiting for the view
	// itself to cross the ceiling (rescueOrFail's below-hard path) only lets
	// the context keep growing - the 17:50 event did exactly that: three
	// back-to-back rejections and no rescue, on to 3.8M tokens. End the loop
	// on the first rejection: truncation is the same recovery overflow uses.
	// Manual compaction never reaches this branch (the ceiling check skips
	// manual triggers) and keeps its fail-visible semantics.
	// Task 516: the acceptance path now installs partial progress instead of
	// producing this rejection, so the branch is defensive only — it stays so a
	// re-introduced ceiling rejection can never regress into the 17:50 loop.
	if errors.Is(err, errCheckpointCeiling) && policy.Trigger != CompactionTriggerManual {
		return m.rescueByTruncation(policy, hard, err)
	}
	return m.rescueOrFail(policy, hard, err)
}

func (m ContextManager) summaryNoop(policy ContextPreparePolicy, inputHash string, hard int) (PreparedContext, error) {
	// Task 424: the reason doubles as the ErrNoFoldableRegion sentinel so
	// failure consumers recognise the terminal "nothing foldable left" class
	// without string matching (the desktop cold-cache loop parks on it).
	m.agent.recordContextMaintenanceBlocked(inputHash, policy.Trigger, "summary", ErrNoFoldableRegion.Error())
	latest := m.currentPrepared()
	switch {
	case policy.Trigger == CompactionTriggerOverflow || latest.InputTokens >= hard:
		return m.rescueByTruncation(policy, hard, ErrNoFoldableRegion)
	case policy.Force:
		return PreparedContext{}, fmt.Errorf("%w: %w", ErrCompactionRequired, ErrNoFoldableRegion)
	default:
		return latest, nil
	}
}

// rescueOrFail decides what a failed summary means: below the ceiling
// automatic maintenance waits for the next view and a manual compact reports
// the error; at or above the ceiling only the lossy truncation rescue is left.
func (m ContextManager) rescueOrFail(policy ContextPreparePolicy, hard int, cause error) (PreparedContext, error) {
	latest := m.currentPrepared()
	if policy.Trigger != CompactionTriggerOverflow && latest.InputTokens < hard {
		if policy.Trigger == CompactionTriggerManual {
			return PreparedContext{}, cause
		}
		return latest, nil
	}
	return m.rescueByTruncation(policy, hard, cause)
}

// rescueByTruncation installs the lossy truncation projection aimed at the
// fold trigger so the turn leaves the ceiling with headroom. cause is the
// summary failure it stands in for and stays in the error when even that fails.
func (m ContextManager) rescueByTruncation(policy ContextPreparePolicy, hard int, cause error) (PreparedContext, error) {
	a := m.agent
	applied, err := a.truncateToProjectionLocked(policy.Trigger, a.compactTrigger())
	if err != nil {
		return PreparedContext{}, fmt.Errorf("%w: %w (truncation: %w)", ErrCompactionRequired, cause, err)
	}
	if !applied {
		return PreparedContext{}, fmt.Errorf("%w: %w", ErrCompactionRequired, cause)
	}
	latest := m.currentPrepared()
	if latest.InputTokens >= hard {
		return PreparedContext{}, fmt.Errorf("%w: truncated view still %d >= %d", ErrCompactionRequired, latest.InputTokens, hard)
	}
	a.resetCompactionProgress()
	return latest, nil
}

func (a *Agent) resetCompactionProgress() {
	a.sess.compaction.stuck = false
	a.sess.compaction.stuckInputHash = ""
	a.sess.compaction.consecutive = 0
	a.sess.compaction.failedTurn.Store(0)
}

func (m ContextManager) currentPrepared() PreparedContext {
	if m.agent == nil {
		return PreparedContext{}
	}
	visible := m.agent.modelVisibleMessages()
	return PreparedContext{
		Messages:          append([]provider.Message(nil), visible...),
		InputTokens:       m.agent.estimatedVisibleRequestTokens(visible),
		ProjectionVersion: m.agent.currentProjectionVersion(),
	}
}

// estimatedVisibleRequestTokens sizes the pre-interceptor sampling shape:
// ModelMessages + role projection + tool schemas. Extension interceptors are
// intentionally omitted here (see prepareOnce) to avoid double side effects.
func (a *Agent) estimatedVisibleRequestTokens(visible []provider.Message) int {
	if a == nil {
		return 0
	}
	msgs := a.normalizeModelRequestMessages(visible)
	tools := a.providerToolSchemas()
	return a.estimatedRequestTokens(provider.Request{
		Messages:    msgs,
		Tools:       tools,
		MaxTokens:   a.destinationMaxOutputTokens(),
		Temperature: provider.OptionalTemperature(a.temperature),
	})
}

// contextGrowthWarnRatio is the share of the model window one maintenance
// interval may add before the growth watch speaks up (任务461-P13②). The
// 0.80 compaction trigger already bounds steady growth; this catches the
// pathological shape where a single tool loop injects a large fraction of the
// window between two checks — exactly the replay-storm feeding the user saw.
const contextGrowthWarnRatio = 0.40

// contextGrowthWarnCooldown throttles repeat warnings inside one turn so a
// continuously ballooning loop stays visible without spamming the log.
const contextGrowthWarnCooldown = 5 * time.Minute

// observeContextGrowth compares this maintenance check's context estimate with
// the previous one. A delta of at least contextGrowthWarnRatio × window logs a
// sharp-growth warning (deduped per turn, re-armed after the cooldown). It is
// pure observation: no behavior changes, it only gives the diagnosis a named
// entry point in the log alongside the existing maintenance receipts.
func (a *Agent) observeContextGrowth(est int) bool {
	if a == nil || est <= 0 {
		return false
	}
	window := a.effectiveContextWindow()
	if window <= 0 {
		return false
	}
	prev := a.sess.compaction.lastPrepareEst
	a.sess.compaction.lastPrepareEst = int64(est)
	if prev <= 0 || est <= int(prev) {
		return false
	}
	delta := est - int(prev)
	if delta < int(float64(window)*contextGrowthWarnRatio) {
		return false
	}
	now := time.Now()
	turn := a.activeTurnCreatedAt.Load()
	sameTurn := turn != 0 && turn == a.sess.compaction.growthWarnedTurn
	if sameTurn && now.Sub(a.sess.compaction.lastGrowthWarnAt) < contextGrowthWarnCooldown {
		return false
	}
	a.sess.compaction.growthWarnedTurn = turn
	a.sess.compaction.lastGrowthWarnAt = now
	slog.Warn("agent: context estimate grew sharply between maintenance checks",
		"prev_tokens", prev, "now_tokens", est, "delta_tokens", delta,
		"window", window, "turn", turn)
	return true
}

// projectionFallbackWarnCooldown throttles the invalid-projection warning so a
// stuck episode stays visible without spamming every maintenance check.
const projectionFallbackWarnCooldown = 5 * time.Minute

// 任务516④ fallback watch: after a lineage change (model switch, resume
// rebuild) or a history rewrite, a stale-or-missing projection legitimately
// serves the whole canonical transcript; the view can then dwarf the window
// while compaction looks healthy, and diagnosis had to infer the fallback from
// compaction source spikes. observeProjectionFallback names the reason once per
// cooldown. Pure observation: no behavior reads it.
//
// Gates keep normal states quiet: a body-less projection below the hard input
// ceiling is the ordinary fresh-session shape, and a valid projection re-arms
// the throttle so the next episode warns immediately.
func (a *Agent) observeProjectionFallback(est int) {
	if a == nil || a.sess.conversation == nil || est <= 0 {
		return
	}
	a.sess.compactionMu.Lock()
	st := a.sess.compactionState
	a.sess.compactionMu.Unlock()
	window := a.effectiveContextWindow()
	if len(st.Projection.Messages) == 0 {
		// Fresh or invalidated session: only worth a line once the full
		// canonical view itself is past the physical ceiling (the 2.7M/1M shape).
		if est < a.hardInputCeiling() {
			return
		}
		a.warnProjectionFallback("no_projection", st, est, window)
		return
	}
	msgs, _ := a.sess.conversation.snapshotMessagesVersion()
	key := a.currentPromptCacheKeyLocked()
	if projectionContentValid(st, msgs) {
		// 任务638: a lineage-key change rebinds the cache namespace; it is not a
		// view fallback, so a content-valid projection stays quiet. Re-arm the
		// throttle so a later episode warns on its first check.
		a.sess.compaction.fallbackWarnAt = time.Time{}
		return
	}
	// Content drift: name whether the lineage key also moved.
	if key != "" {
		if _, ok := lineageKeyCompatible(st.PromptCacheKey, key); !ok {
			a.warnProjectionFallback("lineage_key_mismatch", st, est, window)
			return
		}
	}
	a.warnProjectionFallback("content_mismatch", st, est, window)
}

// warnProjectionFallback emits the throttled fallback warning.
func (a *Agent) warnProjectionFallback(reason string, st CompactionState, est, window int) {
	if !a.sess.compaction.fallbackWarnAt.IsZero() && time.Since(a.sess.compaction.fallbackWarnAt) < projectionFallbackWarnCooldown {
		return
	}
	a.sess.compaction.fallbackWarnAt = time.Now()
	slog.Warn("agent: context view fell back to the full canonical transcript",
		"reason", reason, "view_tokens", est, "window", window,
		"projection_version", st.Projection.ProjectionVersion,
		"prompt_cache_key", st.PromptCacheKey)
}
