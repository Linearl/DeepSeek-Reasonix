package agent

import (
	"log/slog"
	"strings"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// tl1562 observability helpers. The 20261112 incident (fork开发-新12: a
// session died on "无人值守自动续轮已停止" and the log could not close the
// loop single-threaded) named three gaps: projection invalidation carried no
// drift detail, provider empty responses carried no local reconciliation or
// session attribution, and output truncation reached the UI but never the
// log. The helpers in this file feed the structured lines that close those
// gaps; each log site stays grep-able and names the session.

// sessionLogRef names this conversation in a structured log line. The bound
// session path is stable across resume (desktop sessions); ephemeral and
// sub-agent sessions fall back to the transport identity, minted lazily on
// the same terms withProviderCacheSession uses, so a log line that fires
// before the first sampling request still carries attribution. Empty only
// when there is no conversation at all. Acceptance contract (tl1562 ①):
// every one of the three incident-form lines carries at least one of
// session/topic/tab — this is the "session" leg.
func (a *Agent) sessionLogRef() string {
	if a == nil {
		return ""
	}
	if path := strings.TrimSpace(a.SessionPath()); path != "" {
		return path
	}
	s := a.Session()
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cacheSessionID == "" {
		s.cacheSessionID = provider.NewCacheSessionID()
	}
	return s.cacheSessionID
}

// localViewTokens estimates the provider-visible request size exactly the way
// maintenance measures it (model-visible view + role projection + tool
// schemas), so a provider-side usage reading can be reconciled against the
// local view on the same line. The 20261112 incident showed local ~492K
// against provider cache_miss_tokens=1048570 and the log could not even name
// the local side of that pair. Zero when no view exists yet.
func (a *Agent) localViewTokens() int {
	if a == nil || a.sess.conversation == nil {
		return 0
	}
	visible := a.modelVisibleMessages()
	if len(visible) == 0 {
		return 0
	}
	return a.estimatedVisibleRequestTokens(visible)
}

// usageTokenFields extracts the provider-reported token counts shared by the
// tl1562 failure/truncation lines. A nil usage — the round that never got a
// billable response — yields zeros and an empty finish reason; the caller's
// usage_present flag stays the discriminator, mirroring the existing
// cache_hit_tokens/cache_miss_tokens convention.
func usageTokenFields(u *provider.Usage) (prompt, completion, total int, finishReason string) {
	if u == nil {
		return 0, 0, 0, ""
	}
	return u.PromptTokens, u.CompletionTokens, u.TotalTokens, u.FinishReason
}

// emitFinishReasonNotice is the single abnormal-finish_reason surface shared
// by the run loop's error and success turns (tl1562 G3): it renders the
// existing user-facing notice AND leaves the matching structured log line.
// The 20261112 incident showed the truncation on screen while grep found no
// finish_reason/max_tokens/truncated record anywhere — the sink event was the
// only trace. Normal terminations ("stop", "tool_calls") log nothing.
func (a *Agent) emitFinishReasonNotice(u *provider.Usage) {
	msg, ok := finishReasonMessage(u)
	if !ok {
		return
	}
	a.svc.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: msg})
	prompt, completion, total, _ := usageTokenFields(u)
	slog.Warn("agent: turn output truncated by provider finish reason",
		"finish_reason", u.FinishReason,
		"session", a.sessionLogRef(),
		"model_ref", a.destinationModelRef(),
		"max_output_tokens", a.destinationMaxOutputTokens(),
		"prompt_tokens", prompt, "completion_tokens", completion, "total_tokens", total)
}

// logSamplingAttemptFailure is the per-attempt structured failure trail (task
// 317's searchable line, extended by tl1562 G2): the failure classification
// plus the local reconciliation surface — the session it belongs to, the
// local view tokens, and the provider usage side by side — so "local 492K vs
// provider cache_miss 1048570" is one grep away instead of an inference.
func (a *Agent) logSamplingAttemptFailure(attempt int, failure provider.RecoveryFailure, reason string, usage *provider.Usage, err error) {
	var cacheHit, cacheMiss int64
	usagePresent := usage != nil
	if usage != nil {
		cacheHit, cacheMiss = int64(usage.CacheHitTokens), int64(usage.CacheMissTokens)
	}
	prompt, completion, total, finishReason := usageTokenFields(usage)
	slog.Info("agent: sampling attempt failed",
		"attempt", attempt, "max_attempts", maxSamplingAttempts,
		"phase", failure.Phase, "status", failure.Status, "code", failure.Code,
		"reason", reason, "retryable", failure.Retryable,
		"cache_hit_tokens", cacheHit, "cache_miss_tokens", cacheMiss,
		"usage_present", usagePresent,
		"session", a.sessionLogRef(),
		"view_tokens", a.localViewTokens(),
		"prompt_tokens", prompt, "completion_tokens", completion,
		"total_tokens", total, "finish_reason", finishReason,
		"session_hit_total", a.sess.cacheHit.Load(), "session_miss_total", a.sess.cacheMiss.Load(),
		"err", err)
}

// logTurnProviderFailure is the turn-level closing line for an exhausted
// sampling loop (run loop's provider-failure branch, extended by tl1562 G2):
// same reconciliation surface as the per-attempt trail, so an incident's
// final failure names the session and both sides of the token ledger.
func (a *Agent) logTurnProviderFailure(err error, usage *provider.Usage) {
	failure := provider.ClassifyRecovery(err)
	var cacheHit, cacheMiss int64
	usagePresent := usage != nil
	if usage != nil {
		cacheHit, cacheMiss = int64(usage.CacheHitTokens), int64(usage.CacheMissTokens)
	}
	prompt, completion, total, finishReason := usageTokenFields(usage)
	slog.Warn("agent: turn ended with provider failure",
		"phase", failure.Phase, "status", failure.Status, "code", failure.Code,
		"usage_present", usagePresent,
		"session", a.sessionLogRef(),
		"view_tokens", a.localViewTokens(),
		"prompt_tokens", prompt, "completion_tokens", completion,
		"total_tokens", total, "finish_reason", finishReason,
		"cache_hit_tokens", cacheHit, "cache_miss_tokens", cacheMiss,
		"session_hit_total", a.sess.cacheHit.Load(),
		"session_miss_total", a.sess.cacheMiss.Load(),
		"err", err)
}
