package agent

import (
	"context"
	"fmt"

	"reasonix/internal/event"
	"reasonix/internal/i18n"
	"reasonix/internal/provider"
)

// tl 1190 recovery budgets (2026-10-27 deepseek incident: 4×400, zero
// recoveries — the parsed overflow was stripped before the agent saw it, and
// once that is fixed every retry must stay a SAME-REQUEST parameter
// correction; never an agent/turn rerun).
const (
	// maxPreciseContextRetries bounds the L1 precise corrections per turn: each
	// resend is the same frozen request with a recomputed max_tokens
	// (limit − messagesReal − margin), so a second correction can only be
	// needed when the first retry's rebuilt view still overflowed.
	maxPreciseContextRetries = 2
	// minPreciseRetryOutputTokens: a corrected cap below this cannot carry a
	// useful agent turn — the request is not resent, overflow compaction owns
	// the recovery instead.
	minPreciseRetryOutputTokens = 4096
	// The 兜底 ladder for overflows that carry NO token numbers (Zhipu GLM
	// 1261): shrink the completion cap by 10% per retry, at most five times,
	// before handing the recovery to overflow compaction. It only helps when
	// the completion side of the request overflowed a smaller-than-configured
	// gateway window; a prompt-side overflow walks into compaction after the
	// bounded burns.
	unnumberedRetryFactor        = 0.9
	maxUnnumberedFallbackRetries = 5
)

type contextRecoveryBudget struct {
	// retries counts L1 precise corrections spent this turn.
	retries int
	// fallbackRetries counts unnumbered-overflow cap shrinks spent this turn.
	fallbackRetries int
}

func (a *Agent) recoverContextLimit(ctx context.Context, frozen samplingRequest, err error, budget *contextRecoveryBudget) (samplingRequest, bool, string) {
	limit := provider.AsContextLimitError(err)
	if a == nil || limit == nil || budget == nil {
		return samplingRequest{}, false, contextRecoveryFailed
	}
	omitted := frozen.req.MaxTokens == 0
	// An overflow is "numbered" when the provider's reply carried token counts
	// (Window/Prompt>0); an unnumbered one (all zero, e.g. GLM 1261) cannot
	// size a precise correction and walks the 兜底 ladder instead.
	numbered := limit.PromptTokens > 0 || limit.WindowTokens > 0
	if limit.PromptTokens > 0 {
		// L2 feeds on the estimate BEFORE the session recalibration below
		// rewrites it — afterwards est would equal the real count and the
		// ratio would always read 1.
		if est := a.estimatedRequestTokens(frozen.req); est > 0 {
			noteProviderPromptCorrection(a.requestProviderName(), limit.PromptTokens, est)
		}
		a.setPromptTokenCalibrationFromActive(limit.PromptTokens)
	}
	a.learnContextBudget(limit.WindowTokens, limit.CompletionTokens, omitted)
	adm := a.lastAdmission()
	adm.ObservedWindow = limit.WindowTokens
	adm.ObservedPrompt = limit.PromptTokens
	adm.ObservedCompletion = limit.CompletionTokens
	a.storeAdmission(adm)

	if numbered {
		return a.retryNumberedContextLimit(ctx, frozen, limit, budget, omitted)
	}
	return a.retryUnnumberedContextLimit(ctx, frozen, limit, budget)
}

// retryNumberedContextLimit is the L1 precise lane: one same-request resend
// with a provider-arithmetic max_tokens (limit − messagesReal − margin), at
// most maxPreciseContextRetries per turn. Below the minimum useful output the
// recovery escalates to overflow compaction (once per turn, as before).
func (a *Agent) retryNumberedContextLimit(ctx context.Context, frozen samplingRequest, limit *provider.ContextLimitError, budget *contextRecoveryBudget, omitted bool) (samplingRequest, bool, string) {
	window := a.effectiveContextWindow()
	prompt := limit.PromptTokens
	if prompt <= 0 {
		prompt = a.estimatedRequestTokens(frozen.req)
	}
	physical := window - prompt - contextSafetyMargin(prompt)
	if physical >= minPreciseRetryOutputTokens && budget.retries < maxPreciseContextRetries {
		next := freezeProviderRequest(frozen.req)
		next.MaxTokens = physical
		if frozen.req.MaxTokens > 0 && frozen.req.MaxTokens < physical {
			next.MaxTokens = frozen.req.MaxTokens
		}
		budget.retries++
		// Publish the request that will actually be retried, not the stale
		// pre-error admission. The Context Panel reads this atomic snapshot
		// while the turn is still active and after it completes.
		adm := a.lastAdmission()
		adm.WindowMode = provider.ContextWindowShared.String()
		adm.Source = provider.ContextBudgetSourceLearned
		adm.WindowTokens = window
		adm.PromptTokens = prompt
		adm.PhysicalRemaining = physical
		if adm.RequestedOutputTokens <= 0 {
			adm.RequestedOutputTokens = limit.CompletionTokens
		}
		if omitted && adm.AutoOutputTokens <= 0 {
			adm.AutoOutputTokens = limit.CompletionTokens
		}
		adm.EffectiveOutputTokens = next.MaxTokens
		adm.Clipped = adm.RequestedOutputTokens > 0 && next.MaxTokens < adm.RequestedOutputTokens
		adm.ApplyMaxTokens = next.MaxTokens > 0
		adm.LastRecovery = contextRecoveryLearnedRetry
		a.storeAdmission(adm)
		a.emitContextRecoveryNotice(contextRecoveryLearnedRetry, limit, next.MaxTokens)
		shape := a.requestCalibrationShape(next)
		a.sess.output.activeReqShape.Store(&shape)
		return samplingRequest{req: next, prov: frozen.prov}, true, contextRecoveryLearnedRetry
	}
	return a.retryContextLimitByCompaction(ctx, frozen, limit, budget)
}

// retryUnnumberedContextLimit is the 兜底 lane: without token numbers the
// only sized lever is shrinking the completion cap (max_tokens×0.9, at most
// maxUnnumberedFallbackRetries per turn, floored at the minimum useful
// output). A cap of zero (omitted output) cannot shrink — compaction owns it
// directly, matching the pre-1190 behavior.
func (a *Agent) retryUnnumberedContextLimit(ctx context.Context, frozen samplingRequest, limit *provider.ContextLimitError, budget *contextRecoveryBudget) (samplingRequest, bool, string) {
	if frozen.req.MaxTokens > 0 && budget.fallbackRetries < maxUnnumberedFallbackRetries {
		shrunk := int(float64(frozen.req.MaxTokens) * unnumberedRetryFactor)
		if shrunk >= minPreciseRetryOutputTokens {
			budget.fallbackRetries++
			next := freezeProviderRequest(frozen.req)
			next.MaxTokens = shrunk
			adm := a.lastAdmission()
			adm.WindowMode = provider.ContextWindowShared.String()
			adm.Source = provider.ContextBudgetSourceLearned
			adm.EffectiveOutputTokens = shrunk
			adm.Clipped = true
			adm.ApplyMaxTokens = true
			adm.LastRecovery = contextRecoveryLearnedRetry
			a.storeAdmission(adm)
			a.emitContextRecoveryNotice(contextRecoveryLearnedRetry, limit, shrunk)
			shape := a.requestCalibrationShape(next)
			a.sess.output.activeReqShape.Store(&shape)
			return samplingRequest{req: next, prov: frozen.prov}, true, contextRecoveryLearnedRetry
		}
	}
	return a.retryContextLimitByCompaction(ctx, frozen, limit, budget)
}

// retryContextLimitByCompaction rebuilds the request on a freshly compacted
// view. It runs once per turn (the retries gate) so a compacted view that
// still overflows fails the turn instead of looping.
func (a *Agent) retryContextLimitByCompaction(ctx context.Context, frozen samplingRequest, limit *provider.ContextLimitError, budget *contextRecoveryBudget) (samplingRequest, bool, string) {
	if budget.retries > 0 {
		a.setLastRecovery(contextRecoveryFailed)
		return samplingRequest{}, false, contextRecoveryFailed
	}
	startProjectionVersion := a.currentProjectionVersion()
	if _, perr := a.contextManager().Prepare(ctx, ContextPreparePolicy{
		Trigger: CompactionTriggerOverflow,
		Force:   true,
	}); perr != nil {
		a.setLastRecovery(contextRecoveryFailed)
		return samplingRequest{}, false, contextRecoveryFailed
	}
	if a.currentProjectionVersion() <= startProjectionVersion {
		a.setLastRecovery(contextRecoveryFailed)
		return samplingRequest{}, false, contextRecoveryFailed
	}
	rebuilt, rerr := a.buildSamplingRequest(ctx, CompactionTriggerPressure)
	if rerr != nil {
		a.setLastRecovery(contextRecoveryFailed)
		return samplingRequest{}, false, contextRecoveryFailed
	}
	if aerr := a.applyAdmissionToRequest(&rebuilt.req); aerr != nil {
		a.setLastRecovery(contextRecoveryFailed)
		return samplingRequest{}, false, contextRecoveryFailed
	}
	budget.retries++
	a.setLastRecovery(contextRecoveryCompacted)
	a.emitContextRecoveryNotice(contextRecoveryCompacted, limit, rebuilt.req.MaxTokens)
	shape := a.requestCalibrationShape(rebuilt.req)
	a.sess.output.activeReqShape.Store(&shape)
	return samplingRequest{req: freezeProviderRequest(rebuilt.req), prov: rebuilt.prov}, true, contextRecoveryCompacted
}

func (a *Agent) emitContextRecoveryNotice(kind string, limit *provider.ContextLimitError, nextOutput int) {
	if a == nil || a.svc.sink == nil {
		return
	}
	text := i18n.M.ContextRecoveryAdjustBudget
	if kind == contextRecoveryCompacted {
		text = i18n.M.ContextRecoveryCompacted
	}
	detail := fmt.Sprintf("recovery=%s next_output=%d", kind, nextOutput)
	if limit != nil {
		detail = fmt.Sprintf("%s window=%d prompt=%d completion=%d requested=%d",
			detail, limit.WindowTokens, limit.PromptTokens, limit.CompletionTokens, limit.RequestedTokens)
	}
	a.svc.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: text, Detail: detail})
}
