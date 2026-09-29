package agent

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// defaultRecoveryWaitBudget bounds continuous waiting on an unreachable
// provider (#9889): mainstream agents stop after ~10 attempts or ~10 minutes.
const defaultRecoveryWaitBudget = 10 * time.Minute

var recoveryWaitBudget = defaultRecoveryWaitBudget

type samplingRecoveryState struct {
	frozen                             samplingRequest
	context                            contextRecoveryBudget
	replay                             reasoningReplayRecoveryBudget
	output, protocol, partial, missing bool
	billable                           *provider.Usage
	waited                             time.Duration
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
	waiting := attempt >= maxSamplingAttempts && a.canWaitSampling(ctx, s, failure)
	// Task 242: quota-class failures join the ordinary fast retry loop ONLY
	// when a fallback target is configured — the retries exist to absorb
	// transient quota jitter, and the exhausted loop hands the error up so the
	// controller can switch models. With the switch off (default) quota keeps
	// its pre-242 immediate failure: zero regression. Quota never enters the
	// minute-scale `waiting` lane (canWaitSampling unchanged): waiting out a
	// 5h window is pointless.
	quotaFallback := failure.Phase == "quota" && ModelFallbackTarget(ctx) != ""
	// Task 243 A4: the kind-aware bounded budget gates the fast lane BEFORE
	// the attempt-cap logic: a terminal 4xx shape is refused by the matrix
	// (the provider will refuse it again), and transient/unknown shapes
	// consume a sliding 8/15min window shared across turns — one turn's
	// attempt cap alone let failure storms restart the loop every turn. A
	// refusal keeps the ORIGINAL error string as the phase's refused
	// identity (no re-encoding). Quota keeps its task-242 lane untouched.
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
	if waiting && s.waited+delay > recoveryWaitBudget {
		slog.Warn("agent: recovery wait budget exhausted",
			"phase", failure.Phase, "status", failure.Status, "code", failure.Code,
			"attempts", attempt, "waited_ms", s.waited.Milliseconds(),
			"budget_ms", recoveryWaitBudget.Milliseconds(),
			"cache_hit_tokens", cacheHit, "cache_miss_tokens", cacheMiss,
			"usage_present", usagePresent, "err", result.err)
		result.err = &provider.RecoveryWaitExhaustedError{Phase: failure.Phase, Code: failure.Code, Status: failure.Status, Waited: s.waited, Attempts: attempt, Cause: result.err}
		return false
	}
	sink.Discard()
	a.emitStreamAttempt(id, event.StreamAttemptDiscard, attempt, reason, result.err)
	status := &event.RecoveryStatus{Phase: failure.Phase, Reason: failure.Code, NextAttemptAt: time.Now().Add(delay).UnixMilli(), WaitedMs: s.waited.Milliseconds(), Waiting: waiting}
	if waiting {
		status.WaitBudgetMs = recoveryWaitBudget.Milliseconds()
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
