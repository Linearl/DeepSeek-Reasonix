package agent

// 任务912 P1-c: 永不全量 —— compaction that keeps failing must never hand the
// turn the full canonical transcript.
//
// Before this, a failed summary below the hard ceiling was the "recoverable"
// case: rescueOrFail returned currentPrepared() (the whole transcript) and the
// turn went out with it. That is fine ONCE — the view still fits, and task 719
// clamps anything past the ceiling anyway. It is not fine on repeat: the
// desktop.log.8 incident sat at 1.8M source tokens across three back-to-back
// failed folds, and every one of them left the session carrying the entire
// transcript, so the provider kept receiving a full-prefill request that the
// summarizer could not shrink.
//
// The user's rule is absolute ("任何时候不回退全量"), so after
// slidingWindowFailureStreak consecutive failures the failure path no longer
// returns the view: it installs a sliding-window truncation capped at
// slidingWindowCapRatio of the window. Same mechanism overflow recovery already
// uses (truncateToProjectionLocked): system message and pinned revisions
// survive, the recent tail is protected verbatim, oldest replay units are
// dropped behind a marker. Canonical storage keeps every byte — this is a
// projection, not a deletion, so a later successful fold still sees the whole
// history.

import (
	"errors"
	"fmt"
	"log/slog"

	"reasonix/internal/event"
	"reasonix/internal/i18n"
)

// slidingWindowFailureStreak is N: how many consecutive failed compaction
// passes before the fallback stops being "the turn proceeds unfolded" and
// becomes a capped sliding window. Two keeps the ordinary transient blip from
// costing cache; three is the point where "the summarizer cannot shrink this"
// is a pattern, not an incident.
const slidingWindowFailureStreak = 3

// slidingWindowCapRatio is the share of the model window the fallback view may
// occupy. 0.85 leaves real headroom for the turn's own reply, tool results and
// the provider framing that never shows up in the message estimate — while
// still carrying far more recent rounds than the 16% tail budget a healthy
// compaction would have left.
const slidingWindowCapRatio = 0.85

// slidingWindowCapTarget is the token ceiling the fallback truncation aims at.
// Zero means no window is configured, in which case there is nothing to cap.
func slidingWindowCapTarget(window int) int {
	if window <= 0 {
		return 0
	}
	return int(float64(window) * slidingWindowCapRatio)
}

// slidingWindowCapEligible reports whether the fallback applies right now:
// the failure streak has reached N AND the view is actually over the cap (a
// view that already fits must not be truncated — that would only lose content).
func (m ContextManager) slidingWindowCapEligible() bool {
	a := m.agent
	if a == nil || a.sess.conversation == nil {
		return false
	}
	if a.sess.compaction.failureStreak.Load() < slidingWindowFailureStreak {
		return false
	}
	return m.currentPrepared().InputTokens >= slidingWindowCapTarget(a.effectiveContextWindow())
}

// slidingWindowCap installs the capped sliding window and returns the view the
// turn should leave with. It never returns an error: the caller is already on a
// failure path and its summary is unrecoverable, so the cap either reclaims
// headroom (the good case) or the current view stands and the warning says why.
// The truncation is announced through the task-867 failure card family — a
// LevelWarn notice naming before/after/target — so "compaction failed AND the
// context was clipped" is one visible story instead of a silent shrink.
func (m ContextManager) slidingWindowCap(policy ContextPreparePolicy, cause error) (PreparedContext, error) {
	a := m.agent
	window := a.effectiveContextWindow()
	target := slidingWindowCapTarget(window)
	streak := a.sess.compaction.failureStreak.Load()
	before := m.currentPrepared().InputTokens
	if target <= 0 || before < target {
		return m.currentPrepared(), nil
	}
	applied, err := a.truncateToProjectionLocked(policy.Trigger, target)
	latest := m.currentPrepared()
	if err != nil {
		slog.Error("agent: sliding-window cap failed after repeated compaction failures",
			"trigger", policy.Trigger, "consecutive_failures", streak,
			"before_tokens", before, "target_tokens", target, "window", window,
			"cause", cause, "err", err)
		a.emitSlidingWindowCapNotice(before, before, target, int(streak), err)
		return latest, nil
	}
	if !applied || latest.InputTokens >= target {
		slog.Warn("agent: sliding-window cap could not reclaim headroom",
			"trigger", policy.Trigger, "consecutive_failures", streak,
			"before_tokens", before, "after_tokens", latest.InputTokens,
			"target_tokens", target, "window", window, "cause", cause)
		a.emitSlidingWindowCapNotice(before, latest.InputTokens, target, int(streak), cause)
		return latest, nil
	}
	slog.Warn("agent: sliding-window cap applied after repeated compaction failures",
		"trigger", policy.Trigger, "consecutive_failures", streak,
		"before_tokens", before, "after_tokens", latest.InputTokens,
		"target_tokens", target, "window", window, "cause", cause)
	a.emitSlidingWindowCapNotice(before, latest.InputTokens, target, int(streak), cause)
	return latest, nil
}

// slidingWindowFallback is the guarded entry the failure paths call: it caps
// when eligible and otherwise leaves the caller's existing behavior untouched.
// cause names the summary failure that led here; it never reaches the model.
func (m ContextManager) slidingWindowFallback(policy ContextPreparePolicy, cause error) (PreparedContext, bool) {
	if !m.slidingWindowCapEligible() {
		return PreparedContext{}, false
	}
	capped, err := m.slidingWindowCap(policy, cause)
	if err != nil {
		return PreparedContext{}, false
	}
	return capped, true
}

// emitSlidingWindowCapNotice is the user-facing half of the fallback. The
// ContextMaintenance "truncate" receipt already carries the machine-readable
// record; this is the warning a person sees next to the failed compaction card,
// and it must never grow into a place where the cap silently eats context.
func (a *Agent) emitSlidingWindowCapNotice(before, after, target, streak int, cause error) {
	if a == nil || a.svc.sink == nil {
		return
	}
	detail := fmt.Sprintf("consecutive_failures=%d before=%d after=%d target=%d", streak, before, after, target)
	if cause != nil {
		detail += " cause=" + compactionFailureReason(cause)
	}
	a.svc.sink.Emit(event.Event{
		Kind:   event.Notice,
		Level:  event.LevelWarn,
		Code:   event.NoticeCodeSlidingWindowCap,
		Text:   i18n.M.SlidingWindowCap,
		Detail: detail,
	})
}

// slidingWindowCapCause is the wrapper the fallback paths use so the warning
// names a compaction failure rather than a bare provider error.
func slidingWindowCapCause(err error) error {
	if err == nil {
		return errors.New("compaction kept failing")
	}
	return err
}
