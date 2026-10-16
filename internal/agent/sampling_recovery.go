package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/i18n"
	"reasonix/internal/provider"
)

// 任务929 S2 knobs for the deterministic-reject breaker. A package var (the
// recovery suite's established pattern) keeps the production thresholds
// reviewable in one place while tests shrink them to milliseconds.
var (
	// deterministicBreakStreak: consecutive same-reason stream failures that
	// prove the pattern (the incident's 3 killed attempts → the 4th is refused).
	deterministicBreakStreak = 3
	// deterministicBreakMinAttempt: each attempt must have burned real prefill
	// time — fast failures (bad request shapes the provider refuses instantly)
	// keep the historical retry loop instead of tripping a "slow gateway"
	// breaker they never earned.
	deterministicBreakMinAttempt = 30 * time.Second
	// deterministicBreakJitter: max-min spread of those attempt durations
	// (task wording: ±5s around the mean) — 303.9/303.8/304.4s spread 0.7s,
	// a jittering network does not stay that tight.
	deterministicBreakJitter = 10 * time.Second
)

// noteDeterministicStreamFailure folds one stream-interrupt failure into the
// breaker's run and reports whether the deterministic pattern is proven.
func (a *Agent) noteDeterministicStreamFailure(s *samplingRecoveryState, reason string, elapsed time.Duration) (bool, time.Duration) {
	if reason == "" || s.attemptStartedAt.IsZero() || elapsed < 0 {
		s.resetDeterministicStreamFailure()
		return false, 0
	}
	if reason != s.detReason {
		s.detReason, s.detStreak, s.detMin, s.detMax = reason, 1, elapsed, elapsed
		return false, elapsed
	}
	s.detStreak++
	if elapsed < s.detMin {
		s.detMin = elapsed
	}
	if elapsed > s.detMax {
		s.detMax = elapsed
	}
	if s.detStreak < deterministicBreakStreak || s.detMin < deterministicBreakMinAttempt || s.detMax-s.detMin > deterministicBreakJitter {
		return false, elapsed
	}
	return true, elapsed
}

func (s *samplingRecoveryState) resetDeterministicStreamFailure() {
	s.detReason, s.detStreak, s.detMin, s.detMax = "", 0, 0, 0
}

// stopDeterministicStreamRetry gives up BEFORE the next pointless replay and
// tells the user why. It mirrors the existing "retry loop ended" terminal
// frame (same stream-scope give-up shape, so the retry strip settles) and adds
// the actionable notice the incident lacked: the gateway is rejecting this
// request, not flaking.
func (a *Agent) stopDeterministicStreamRetry(s *samplingRecoveryState, result *streamedTurn, attempt int, failure provider.RecoveryFailure, reason string, elapsed time.Duration) bool {
	slog.Warn("agent: deterministic stream failure — stopping the retry loop (task 929)",
		"reason", reason, "streak", s.detStreak,
		"attempt_ms", elapsed.Milliseconds(),
		"min_ms", s.detMin.Milliseconds(), "max_ms", s.detMax.Milliseconds(),
		"attempt", attempt, "max_attempts", maxSamplingAttempts, "err", result.err)
	a.svc.sink.Emit(event.Event{Kind: event.Retrying, RetryAttempt: attempt, RetryMax: maxStreamRecoveries, RetryScope: event.RetryScopeStream, Recovery: &event.RecoveryStatus{
		Phase:           failure.Phase,
		Reason:          "deterministic reject",
		BudgetUsed:      a.streamRetryBudget.Count(failure.Phase, time.Now()),
		BudgetLimit:     a.streamRetryBudget.Limit(),
		BudgetExhausted: true,
	}})
	a.svc.sink.Emit(event.Event{
		Kind:  event.Notice,
		Level: event.LevelWarn,
		Code:  event.NoticeCodeProviderDeterministicReject,
		Text:  fmt.Sprintf(i18n.M.ProviderDeterministicReject, attempt),
		Detail: fmt.Sprintf("reason=%s streak=%d attempt_ms=%d window_ms=%d..%d",
			reason, s.detStreak, elapsed.Milliseconds(), s.detMin.Milliseconds(), s.detMax.Milliseconds()),
	})
	return false
}

// defaultRecoveryWaitBudget bounds continuous waiting on an unreachable
// provider (#9889): mainstream agents stop after ~10 attempts or ~10 minutes.
const defaultRecoveryWaitBudget = 10 * time.Minute

var recoveryWaitBudget = defaultRecoveryWaitBudget

// Task 845 (user-ratified quota semantics): an exhausted quota is no longer
// an immediate terminal failure — the lane waits out the quota window and
// resumes automatically. Without a provider-stated Retry-After the lane
// probes at defaultQuotaWaitInterval, bounded by defaultQuotaWaitBudget;
// both are config-overridable (agent.retry_wait_* / quota_wait_*).
const (
	defaultQuotaWaitBudget   = 30 * time.Minute
	defaultQuotaWaitInterval = 5 * time.Minute
)

type samplingRecoveryState struct {
	frozen                             samplingRequest
	context                            contextRecoveryBudget
	replay                             reasoningReplayRecoveryBudget
	output, protocol, partial, missing bool
	billable                           *provider.Usage
	waited                             time.Duration
	// quotaWaited accumulates the task-845 quota long-wait lane separately
	// from `waited`: the two lanes have independent budgets (wait vs quota
	// wait) and must not erode each other's allowance.
	quotaWaited time.Duration
	// 任务929 S2: deterministic-reject breaker bookkeeping. attemptStartedAt
	// stamps one attempt's wall clock (excludes retry backoff) so the breaker
	// compares attempt-to-attempt durations; detReason/detStreak track the
	// current run of same-reason failures and detMin/detMax its spread.
	attemptStartedAt time.Time
	detReason        string
	detStreak        int
	detMin, detMax   time.Duration
	// 任务927: consecutive RESPONSE-HEADER timeouts on this round's frozen
	// request. Independent of the 929 S2 streak above — that one tracks
	// server-sent stream resets (phase=stream); this one tracks the client's
	// own ResponseHeaderTimeout guard (phase=connect), the 2026-10-16
	// anthropic-endpoint loop face. Reset by any other failure shape.
	headerTimeoutStreak int
}

func (a *Agent) samplingDeadline(ctx context.Context) (context.Context, context.CancelFunc, TaskBudget) {
	limit := a.taskBudgetLimit(ctx)
	if a.turn.graceRound {
		limit = TaskBudget{}
	}
	if limit.Wall <= 0 {
		return ctx, func() {}, limit
	}
	started := a.task.budget.started
	if started.IsZero() {
		started = a.turn.budget.started
	}
	if started.IsZero() {
		started = time.Now()
	}
	next, cancel := context.WithDeadline(ctx, started.Add(limit.Wall))
	return next, cancel, limit
}

func (a *Agent) streamWithSamplingRecovery(parent context.Context, turn int) (terminal streamedTurn) {
	ctx, cancel, limit := a.samplingDeadline(parent)
	defer cancel()
	state := samplingRecoveryState{}
	defer func() {
		if limit.Wall > 0 && errors.Is(terminal.err, context.DeadlineExceeded) && parent.Err() == nil {
			terminal.err = &taskBudgetPause{axis: "time", detail: "recovery reached the task deadline"}
		}
		if terminal.err == nil && state.replay.retries > 0 {
			a.activateReasoningReplayStrongProjection(state.replay)
		}
	}()
	var err error
	state.frozen, err = a.prepareSamplingRequest(ctx)
	if err != nil {
		return streamedTurn{err: err}
	}
	if err := a.consumeManualProtocolRecovery(ctx, &state); err != nil {
		return streamedTurn{err: err}
	}
	ctx = provider.WithManagedRecovery(provider.WithRequestAttemptCounter(ctx))
	for attempt := 1; ; attempt++ {
		if err := a.samplingRecoveryStop(ctx, limit, state.billable, attempt); err != nil {
			return streamedTurn{err: err, usage: state.billable}
		}
		if state.protocol && !state.replay.persisted {
			record := a.protocolRecord(state.frozen, "consumed")
			if state.replay.cutoff > 0 {
				record.Projected = true
				record.Prefix, record.Anchor = state.replay.cutoff, state.replay.anchor
			}
			if err := a.saveProtocolRecord(record); err != nil {
				return streamedTurn{err: err, usage: state.billable}
			}
			state.replay.persisted = true
		}
		id := newStreamAttemptID(attempt)
		a.emitStreamAttempt(id, event.StreamAttemptBegin, attempt, "", nil)
		sink, attemptSink := a.samplingAttemptSinks()
		a.freezeVisibleReads(state.frozen.req.Messages)
		// 任务929 S2: stamp the attempt so the deterministic breaker can
		// compare attempt-to-attempt durations (backoff excluded).
		state.attemptStartedAt = time.Now()
		result := a.runSamplingAttempt(ctx, turn, attemptSink, &state.frozen, id)
		state.billable, _ = a.recordSamplingAttempt(state.billable, result)
		if ctx.Err() != nil {
			sink.Discard()
			return streamedTurn{err: ctx.Err(), interrupted: true, usage: state.billable}
		}
		if result.err == nil {
			retry, done := a.handleSamplingCandidate(&state, result, sink, attempt, id)
			if retry {
				continue
			}
			return done
		}
		state.partial = state.partial || sawSpeculativeSamplingOutput(result) || len(result.responsesItems) > 0 || len(result.serverSearch) > 0
		if attempt < maxSamplingAttempts && a.trySamplingRepair(ctx, &state, result, sink, attempt, id) {
			continue
		}
		if a.waitSamplingRetry(ctx, &state, &result, sink, attempt, id) {
			continue
		}
		sink.Flush()
		if !state.protocol {
			if err := a.offerProtocolRecovery(state.frozen, result.err); err != nil {
				result.err = err
			}
		}
		if provider.AsContextLimitError(result.err) != nil {
			a.setLastRecovery(contextRecoveryFailed)
		}
		result.usage = finalizeSamplingUsage(state.billable, result.usage)
		if ctx.Err() != nil {
			result.err = ctx.Err()
			result.interrupted = true
		}
		return result
	}
}

func (a *Agent) samplingRecoveryStop(ctx context.Context, limit TaskBudget, usage *provider.Usage, attempt int) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if attempt <= 1 {
		return nil
	}
	shadow := a.task.budget
	if usage != nil {
		shadow.observe(usage, a.svc.pricing)
	}
	if axis, detail := shadow.exceeded(limit); axis != "" {
		return &taskBudgetPause{axis: axis, detail: detail}
	}
	return nil
}

func (a *Agent) handleSamplingCandidate(s *samplingRecoveryState, result streamedTurn, sink *deferredStreamSink, attempt int, id string) (bool, streamedTurn) {
	issue := a.reasoningReplayIssue(result)
	if issue == "" {
		a.observeMissingAssistantReasoning(result.assistantMessage(), result.reasoningComplete)
		if s.missing {
			a.recordRecoveredCandidate(result)
		}
		sink.Flush()
		a.emitStreamAttempt(id, event.StreamAttemptCommit, attempt, "", nil)
		result.usage = finalizeSamplingUsage(s.billable, result.usage)
		return false, result
	}
	s.partial = true
	_, claimed := a.observeMissingAssistantReasoning(result.assistantMessage(), result.reasoningComplete)
	if (issue != ReasoningReplayMissing && issue != ReasoningReplayIncomplete) || s.protocol || a.protocolRecoverySpent() || !claimed || attempt >= maxSamplingAttempts {
		return false, a.finishReasoningReplayOverflow(result, sink, issue, s.billable, id, attempt)
	}
	s.protocol, s.missing = true, true
	event.RecordProtocolRecovery(a.svc.sink, event.ProtocolRecoveryAudit{Kind: event.ProtocolRecoveryMissingReasoningRetryAttempted})
	if next, ok := a.recoverReasoningReplayHistory(s.frozen, &s.replay); ok {
		s.frozen = next
		s.replay.local = true
	}
	sink.Discard()
	a.emitStreamAttempt(id, event.StreamAttemptDiscard, attempt, "reasoning_replay", nil)
	a.emitProtocolRetry(attempt, false)
	return true, streamedTurn{}
}

func (a *Agent) recordRecoveredCandidate(result streamedTurn) {
	kind := event.ProtocolRecoveryMissingReasoningRetryRecovered
	if len(result.calls) == 0 && len(result.serverSearch) == 0 {
		kind = event.ProtocolRecoveryMissingReasoningRetryReplaced
	}
	event.RecordProtocolRecovery(a.svc.sink, event.ProtocolRecoveryAudit{Kind: kind})
}

func (a *Agent) trySamplingRepair(ctx context.Context, s *samplingRecoveryState, result streamedTurn, sink *deferredStreamSink, attempt int, id string) bool {
	if limit := provider.AsOutputLimitError(result.err); !s.output && limit != nil && s.frozen.req.MaxTokens > limit.MaxOutputTokens {
		s.output = true
		a.learnOutputBudget(limit.MaxOutputTokens)
		s.frozen.req.MaxTokens = limit.MaxOutputTokens
		sink.Discard()
		a.emitStreamAttempt(id, event.StreamAttemptDiscard, attempt, "output_limit", result.err)
		return true
	}
	if next, ok, _ := a.recoverContextLimit(ctx, s.frozen, result.err, &s.context); ok {
		sink.Discard()
		a.emitStreamAttempt(id, event.StreamAttemptDiscard, attempt, "context_limit", result.err)
		s.frozen = next
		return true
	}
	if s.protocol {
		return false
	}
	next, ok := a.tryRecoverReasoningReplay400(sink, s.frozen, id, attempt, result.err, &s.replay)
	if ok {
		s.protocol = true
		s.frozen = next
	}
	return ok
}

func (a *Agent) canWaitSampling(ctx context.Context, s *samplingRecoveryState, f provider.RecoveryFailure) bool {
	role, _ := ctx.Value(turnContextRoleKey{}).(turnContextRole)
	if role == turnContextPlanner {
		return false
	}
	if SubagentDepth(ctx) != 0 || a.turn.graceRound || a.turn.recoveryGraceRound || s.partial || len(a.turn.writeRecovery) > 0 || len(a.turn.unknownRecovery) > 0 {
		return false
	}
	return f.Retryable && (f.Phase == "connect" || (f.Phase == "headers" && (f.Status == 408 || f.Status == 429 || f.Status >= 500)))
}

func (a *Agent) waitSamplingRetry(ctx context.Context, s *samplingRecoveryState, result *streamedTurn, sink *deferredStreamSink, attempt int, id string) bool {
	failure := provider.ClassifyRecovery(result.err)
	reason := failure.Phase
	if provider.IsStreamInterrupted(result.err) {
		reason = provider.StreamInterruptReason(result.err)
	}
	// Task 317: every failed attempt leaves a searchable slog trail carrying
	// this turn's cache surface — events alone left desktop.log blind to the
	// error round (304 error-path rule). A nil usage is the zero-usage round
	// cache-查证 candidate A predicts (the server never persisted the prefix
	// of a request that never completed), logged explicitly as
	// usage_present=false instead of an indistinguishable hit=0. The stamp
	// feeds compaction miss attribution (the error→compact gap vs TTL).
	var cacheHit, cacheMiss int64
	usagePresent := result.usage != nil
	if result.usage != nil {
		cacheHit, cacheMiss = int64(result.usage.CacheHitTokens), int64(result.usage.CacheMissTokens)
	}
	a.sess.lastProviderErrorAt.Store(time.Now().UnixMilli())
	slog.Info("agent: sampling attempt failed",
		"attempt", attempt, "max_attempts", maxSamplingAttempts,
		"phase", failure.Phase, "status", failure.Status, "code", failure.Code,
		"reason", reason, "retryable", failure.Retryable,
		"cache_hit_tokens", cacheHit, "cache_miss_tokens", cacheMiss,
		"usage_present", usagePresent,
		"session_hit_total", a.sess.cacheHit.Load(), "session_miss_total", a.sess.cacheMiss.Load(),
		"err", result.err)
	// 任务912 P1-d: a rejected key is the one failure the user has to fix.
	// Raise the global banner here (throttled per credential) so a dead key is
	// announced by the turn that hit it, not by whichever path notices first.
	if failure.Phase == "auth" {
		a.alertCredentialExpired(result.err)
	}
	// 任务929 S2: deterministic-reject breaker. A gateway that cuts the SAME
	// stream reason after NEARLY IDENTICAL durations (the 2026-10-16 incident:
	// 303.9 / 303.8 / 304.4s, range 0.7s) is rejecting the request itself, not
	// flaking — replaying it only re-pays the full prefill (~5 min per attempt,
	// 18.5 min of zero output for the user). Three such failures in a row stop
	// the loop with an actionable notice instead of gambling on the 4th.
	// Fast failures stay under deterministicBreakMinAttempt and jittered ones
	// widen past deterministicBreakJitter: both keep the historical loop. The
	// Phase=="stream" gate is the shape ClassifyRecovery reaches only when no
	// special lane owns the failure — quota / 429 / auth / header lanes keep
	// their task-845 / task-242 semantics untouched.
	if failure.Phase == "stream" && provider.IsStreamInterrupted(result.err) {
		if deterministic, elapsed := a.noteDeterministicStreamFailure(s, reason, time.Since(s.attemptStartedAt)); deterministic {
			return a.stopDeterministicStreamRetry(s, result, attempt, failure, reason, elapsed)
		}
	} else {
		s.resetDeterministicStreamFailure()
	}
	// 任务927: response-header timeout loop governance. This failure is OUR
	// client-side guard (ResponseHeaderTimeout) firing — the gateway accepted
	// the request and then stayed silent past the window — so every replay
	// re-pays the full prefill for a request the gateway has already shown it
	// cannot answer in time (the 2026-10-16 anthropic-endpoint incident: 4 ×
	// 300s ≈ 20 minutes of zero output). Independent of the 929 S2 streak
	// above: that one keys on server-sent stream resets (phase=stream); this
	// one keys on the client's own timeout (phase=connect). One timeout earns
	// a retry with an advisory; a second consecutive one stops the loop.
	if provider.IsResponseHeaderTimeout(result.err) {
		s.headerTimeoutStreak++
		if s.headerTimeoutStreak >= headerTimeoutStopStreak {
			return a.stopHeaderTimeoutRetry(s, result, attempt, failure)
		}
		// Tell the user the loop is about to burn another full prefill —
		// with the actionable alternative — before the retry runs.
		a.emitHeaderTimeoutRetryNotice(attempt)
	} else {
		s.headerTimeoutStreak = 0
	}
	waiting := attempt >= maxSamplingAttempts && a.canWaitSampling(ctx, s, failure)
	// Task 242: quota-class failures join the ordinary fast retry loop ONLY
	// when a fallback target is configured — the retries exist to absorb
	// transient quota jitter, and the exhausted loop hands the error up so the
	// controller can switch models. With the switch off (default) quota used
	// to keep its pre-242 immediate failure; task 845 (user-ratified) rerouted
	// that default into the quota long-wait lane below — "exhausted quota"
	// waits out the window and resumes automatically instead of dying on the
	// first response. The 242 fallback path itself is untouched.
	quotaFallback := failure.Phase == "quota" && ModelFallbackTarget(ctx) != ""
	// Task 845: quota without a fallback target enters the dedicated long-wait
	// lane BEFORE the budget gate — quota never belonged to the sliding-window
	// budget, and waiting out a plan window is the whole point of the lane.
	if failure.Phase == "quota" && !quotaFallback {
		return a.waitQuotaRetry(ctx, s, result, sink, attempt, id, failure)
	}
	// Task 243 A4: the kind-aware bounded budget gates the fast lane BEFORE
	// the attempt-cap logic: a terminal 4xx shape is refused by the matrix
	// (the provider will refuse it again), and transient/unknown shapes
	// consume a sliding 8/15min window shared across turns — one turn's
	// attempt cap alone let failure storms restart the loop every turn. A
	// refusal keeps the ORIGINAL error string as the phase's refused
	// identity (no re-encoding). Quota keeps its task-242 lane untouched.
	// Task 845: rate-limit shapes never reach a refusal here any more — Allow
	// exempts them (the queue lane is bounded by the wait budget below).
	if !quotaFallback {
		if allowed, kind, why := a.streamRetryBudget.Allow(failure.Phase, result.err, time.Now()); !allowed {
			slog.Warn("agent: retry budget refused stream attempt (task 243 A4)",
				"phase", failure.Phase, "kind", string(kind), "reason", why,
				"identity", a.streamRetryBudget.RefusedIdentity(failure.Phase),
				"attempt", attempt, "err", result.err)
			// Task 372 (visibility only): a window/shape refusal is the
			// give-up frame — emit it so the UI can show "terminated after
			// N attempts" instead of flipping to an ambiguous idle state.
			// Admission itself is unchanged (Allow already returned false).
			a.svc.sink.Emit(event.Event{Kind: event.Retrying, RetryAttempt: attempt, RetryMax: maxStreamRecoveries, RetryScope: event.RetryScopeStream, Recovery: &event.RecoveryStatus{
				Phase:           failure.Phase,
				Reason:          why,
				BudgetUsed:      a.streamRetryBudget.Count(failure.Phase, time.Now()),
				BudgetLimit:     a.streamRetryBudget.Limit(),
				BudgetExhausted: true,
			}})
			return false
		}
	}
	if (!failure.Retryable && !quotaFallback) || (attempt >= maxSamplingAttempts && !waiting) {
		// Task 372 (visibility only): the loop gives up here (attempt cap or
		// non-retryable shape) — emit the terminal frame BEFORE returning so
		// the UI can show "terminated (gave up after N attempts)" with a
		// manual-continue cue instead of a silent status flip. The decision
		// itself (`return false`) is unchanged.
		a.svc.sink.Emit(event.Event{Kind: event.Retrying, RetryAttempt: attempt, RetryMax: maxStreamRecoveries, RetryScope: event.RetryScopeStream, Recovery: &event.RecoveryStatus{
			Phase:           failure.Phase,
			Reason:          "retry loop ended",
			BudgetUsed:      a.streamRetryBudget.Count(failure.Phase, time.Now()),
			BudgetLimit:     a.streamRetryBudget.Limit(),
			BudgetExhausted: true,
		}})
		return false
	}
	base := time.Duration(1<<min(attempt-1, 2)) * 2 * time.Second
	delay := base
	if waiting {
		delay = time.Minute + time.Duration(rand.Intn(6001))*time.Millisecond
	}
	delay = max(delay, failure.RetryAfter)
	// Task 845: the wait budget is config-overridable per agent; the package
	// var stays the default (and the tests' knob). A 429 in the waiting lane
	// is the QUEUE face — the UI renders it as "service busy, queued", and
	// retryQueueMax (0 = uncapped) bounds the queue rounds when set.
	waitBudget := a.retryWaitBudget
	if waitBudget <= 0 {
		waitBudget = recoveryWaitBudget
	}
	queue := waiting && failure.Phase == "headers" && failure.Status == 429
	if queue && a.retryQueueMax > 0 && attempt-maxSamplingAttempts >= a.retryQueueMax {
		slog.Warn("agent: rate-limit queue cap reached",
			"phase", failure.Phase, "status", failure.Status,
			"queue_rounds", attempt-maxSamplingAttempts, "queue_max", a.retryQueueMax,
			"attempts", attempt, "waited_ms", s.waited.Milliseconds(), "err", result.err)
		result.err = &provider.RecoveryWaitExhaustedError{Phase: failure.Phase, Code: failure.Code, Status: failure.Status, Waited: s.waited, Attempts: attempt, Cause: result.err}
		return false
	}
	if waiting && s.waited+delay > waitBudget {
		slog.Warn("agent: recovery wait budget exhausted",
			"phase", failure.Phase, "status", failure.Status, "code", failure.Code,
			"attempts", attempt, "waited_ms", s.waited.Milliseconds(),
			"budget_ms", waitBudget.Milliseconds(),
			"cache_hit_tokens", cacheHit, "cache_miss_tokens", cacheMiss,
			"usage_present", usagePresent, "err", result.err)
		result.err = &provider.RecoveryWaitExhaustedError{Phase: failure.Phase, Code: failure.Code, Status: failure.Status, Waited: s.waited, Attempts: attempt, Cause: result.err}
		return false
	}
	sink.Discard()
	a.emitStreamAttempt(id, event.StreamAttemptDiscard, attempt, reason, result.err)
	status := &event.RecoveryStatus{Phase: failure.Phase, Reason: failure.Code, NextAttemptAt: time.Now().Add(delay).UnixMilli(), WaitedMs: s.waited.Milliseconds(), Waiting: waiting, Queue: queue}
	if waiting {
		status.WaitBudgetMs = waitBudget.Milliseconds()
	}
	// Task 372 (visibility only): mirror the task-243 sliding-window counts so
	// the UI can say "auto-retried N (N/limit) times, recovering". Allow
	// already admitted this round — Count is a read-only observation of the
	// window and never changes admission.
	status.BudgetUsed = a.streamRetryBudget.Count(failure.Phase, time.Now())
	status.BudgetLimit = a.streamRetryBudget.Limit()
	a.svc.sink.Emit(event.Event{Kind: event.Retrying, RetryAttempt: attempt, RetryMax: maxStreamRecoveries, RetryScope: event.RetryScopeStream, Recovery: status})
	s.waited += delay
	if !waiting && failure.RetryAfter <= base {
		return streamRetrySleep(ctx, attempt)
	}
	return recoverySleep(ctx, delay)
}

// waitQuotaRetry is the task-845 quota long-wait lane (user-ratified
// semantics ②): an exhausted quota waits out the provider's window and
// resumes the SAME request automatically — the turn no longer dies on the
// first "insufficient balance / 额度已用完" answer. Scheduling: the
// provider's Retry-After when stated, otherwise defaultQuotaWaitInterval;
// the lane is bounded by the quota wait budget (both config-overridable) and
// fully cancellable (the sleep rides ctx). UI: a QuotaWait recovery frame —
// "quota exhausted, resuming in ~N" — plus the retry strip the user can stop.
// The lane never touches the sliding-window budget (quota is deterministic,
// not a storm) and never merges with the 429 queue lane's `waited` total.
func (a *Agent) waitQuotaRetry(ctx context.Context, s *samplingRecoveryState, result *streamedTurn, sink *deferredStreamSink, attempt int, id string, failure provider.RecoveryFailure) bool {
	budget := a.quotaWaitBudget
	if budget <= 0 {
		budget = defaultQuotaWaitBudget
	}
	interval := a.quotaWaitInterval
	if interval <= 0 {
		interval = defaultQuotaWaitInterval
	}
	// Task 317 trail: same rationale as the generic lane — a searchable slog
	// line per failed attempt with the turn's cache surface.
	var cacheHit, cacheMiss int64
	usagePresent := result.usage != nil
	if result.usage != nil {
		cacheHit, cacheMiss = int64(result.usage.CacheHitTokens), int64(result.usage.CacheMissTokens)
	}
	a.sess.lastProviderErrorAt.Store(time.Now().UnixMilli())
	slog.Info("agent: quota exhausted — long-wait lane engaged (task 845)",
		"attempt", attempt, "retry_after_s", int(failure.RetryAfter/time.Second),
		"interval_s", int(interval/time.Second), "budget_s", int(budget/time.Second),
		"quota_waited_ms", s.quotaWaited.Milliseconds(),
		"cache_hit_tokens", cacheHit, "cache_miss_tokens", cacheMiss,
		"usage_present", usagePresent, "err", result.err)
	delay := max(interval, failure.RetryAfter)
	if s.quotaWaited+delay > budget {
		slog.Warn("agent: quota wait budget exhausted",
			"attempt", attempt, "quota_waited_ms", s.quotaWaited.Milliseconds(),
			"budget_ms", budget.Milliseconds(),
			"cache_hit_tokens", cacheHit, "cache_miss_tokens", cacheMiss,
			"usage_present", usagePresent, "err", result.err)
		result.err = &provider.RecoveryWaitExhaustedError{Phase: "quota", Code: failure.Code, Status: failure.Status, Waited: s.quotaWaited, Attempts: attempt, Cause: result.err}
		return false
	}
	sink.Discard()
	a.emitStreamAttempt(id, event.StreamAttemptDiscard, attempt, "quota", result.err)
	// Waiting=true keeps older clients on the generic "waiting to recover"
	// strip; QuotaWait lets the current UI name the quota face explicitly.
	status := &event.RecoveryStatus{
		Phase:         "quota",
		Reason:        failure.Code,
		NextAttemptAt: time.Now().Add(delay).UnixMilli(),
		WaitedMs:      s.quotaWaited.Milliseconds(),
		WaitBudgetMs:  budget.Milliseconds(),
		Waiting:       true,
		QuotaWait:     true,
	}
	a.svc.sink.Emit(event.Event{Kind: event.Retrying, RetryAttempt: attempt, RetryMax: maxStreamRecoveries, RetryScope: event.RetryScopeStream, Recovery: status})
	s.quotaWaited += delay
	return recoverySleep(ctx, delay)
}

func unmeteredHeaderFailure(result streamedTurn, httpRequests int) bool {
	if httpRequests <= 0 || sawSpeculativeSamplingOutput(result) {
		return false
	}
	failure := provider.ClassifyRecovery(result.err)
	return failure.Phase == "headers" || failure.Phase == "connect"
}

func unmeteredUsage(usage *provider.Usage, result streamedTurn, httpRequests int) *provider.Usage {
	if usage == nil && unmeteredHeaderFailure(result, httpRequests) {
		return &provider.Usage{Unknown: true, RequestCount: httpRequests}
	}
	return usage
}
