package agent

import (
	"fmt"
	"log/slog"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/i18n"
	"reasonix/internal/provider"
)

// headerTimeoutStopStreak (任务927): consecutive RESPONSE-HEADER timeouts on
// the same frozen request that stop the retry loop. Two, not 929 S2's three:
// that streak counted SERVER-sent stream resets whose duration the gateway
// chose — this timeout is OUR client-side guard firing (ResponseHeaderTimeout
// ), so every occurrence costs the full ~300s wait by construction and the
// duration carries no extra evidence. Two burned waits (≈10 minutes) already
// prove the gateway will not answer this request; a third replay only re-pays
// the whole prefill. A package var so tests can shrink it.
var headerTimeoutStopStreak = 2

// emitHeaderTimeoutRetryNotice fires before the loop re-pays a full prefill
// after a header timeout: the user learns the timeout shape, that each retry
// restarts the whole context, and what to do instead of burning the remaining
// attempts. Info-level because the loop is still recovering on its own.
func (a *Agent) emitHeaderTimeoutRetryNotice(attempt int) {
	a.svc.sink.Emit(event.Event{
		Kind:   event.Notice,
		Level:  event.LevelInfo,
		Code:   event.NoticeCodeHeaderTimeoutRetry,
		Text:   fmt.Sprintf(i18n.M.HeaderTimeoutRetryFmt, attempt, maxSamplingAttempts),
		Detail: fmt.Sprintf("response-header timeout streak=%d; each retry reprocesses the full context", attempt),
	})
}

// stopHeaderTimeoutRetry gives up BEFORE the next pointless replay and says
// why. Mirrors929 S2's stopDeterministicStreamRetry shape — terminal retry
// frame so the strip settles, plus the actionable notice — but keys on the
// response-header timeout face (see headerTimeoutStopStreak for why the
// streak is shorter). Returns false: the loop ends with the original error.
func (a *Agent) stopHeaderTimeoutRetry(s *samplingRecoveryState, result *streamedTurn, attempt int, failure provider.RecoveryFailure) bool {
	slog.Warn("agent: repeated response-header timeouts — stopping the retry loop (task 927)",
		"streak", s.headerTimeoutStreak, "attempt", attempt, "max_attempts", maxSamplingAttempts,
		"elapsed_ms", time.Since(s.attemptStartedAt).Milliseconds(), "err", result.err)
	a.svc.sink.Emit(event.Event{Kind: event.Retrying, RetryAttempt: attempt, RetryMax: maxStreamRecoveries, RetryScope: event.RetryScopeStream, Recovery: &event.RecoveryStatus{
		Phase:           failure.Phase,
		Reason:          "response header timeout",
		BudgetUsed:      a.streamRetryBudget.Count(failure.Phase, time.Now()),
		BudgetLimit:     a.streamRetryBudget.Limit(),
		BudgetExhausted: true,
	}})
	a.svc.sink.Emit(event.Event{
		Kind:   event.Notice,
		Level:  event.LevelWarn,
		Code:   event.NoticeCodeHeaderTimeoutGiveup,
		Text:   fmt.Sprintf(i18n.M.HeaderTimeoutGiveupFmt, s.headerTimeoutStreak),
		Detail: fmt.Sprintf("response-header timeout streak=%d; each attempt burned the full header wait", s.headerTimeoutStreak),
	})
	return false
}
