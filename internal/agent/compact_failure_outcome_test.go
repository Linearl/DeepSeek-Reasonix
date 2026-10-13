package agent

// 任务 867 验收: a failed compaction pass reaches the UI as a CompactionDone
// event with outcome="failed" and the consecutive-failure counter, instead of
// the summary-less Done the frontend silently dropped.

import (
	"context"
	"errors"
	"testing"

	"reasonix/internal/event"
)

// A non-transient summarizer failure ends the pass with a visible failed
// outcome. Below the hard ceiling the pressure failure keeps the current view
// by design (rescueOrFail), so the visible failure is the event, not a
// returned error.
func TestFailedCompactionEmitsFailedOutcomeWithAttemptCounter(t *testing.T) {
	sess := foldableSessionOverForce(6)
	// "boom" is not transient-retryable, so one stream error ends the pass.
	a := agentOverForce(t, &fakeProvider{streamErr: errors.New("boom")}, sess)
	var failed []event.Compaction
	a.svc.sink = event.FuncSink(func(e event.Event) {
		if e.Kind == event.CompactionDone && e.Compaction.Outcome == "failed" {
			failed = append(failed, e.Compaction)
		}
	})

	_ = prepareContext(context.Background(), a, CompactionTriggerPressure)
	if len(failed) != 1 {
		t.Fatalf("failed outcomes = %d, want exactly one visible failure", len(failed))
	}
	if failed[0].Attempt != 1 {
		t.Fatalf("first failure attempt = %d, want 1", failed[0].Attempt)
	}
	if failed[0].Reason == "" {
		t.Fatalf("failed reason = empty, want a display-safe classification")
	}
	if failed[0].Summary != "" {
		t.Fatalf("failed outcome summary = %q, want empty", failed[0].Summary)
	}

	// The unchanged view is backed off: the retry gate blocks a second pass on
	// the same input, so no second failure event may fire from the same view.
	_ = prepareContext(context.Background(), a, CompactionTriggerPressure)
	if len(failed) != 1 {
		t.Fatalf("failed outcomes after same-view retry = %d, want still 1 (backoff holds)", len(failed))
	}
}

// Consecutive failures count up 1, 2, ...; emitCompactionSucceeded stamps the
// success outcome and resets the streak, so the next failure reads 1 again.
func TestFailureStreakCountsUpAndResetsOnSuccess(t *testing.T) {
	sess := foldableSessionOverForce(6)
	a := agentOverForce(t, &fakeProvider{reply: "- kept: alpha constraint"}, sess)
	var done []event.Compaction
	a.svc.sink = event.FuncSink(func(e event.Event) {
		if e.Kind == event.CompactionDone && e.Compaction.Outcome != "" {
			done = append(done, e.Compaction)
		}
	})

	a.emitCompactionFailed(CompactionTriggerPressure, errors.New("boom"))
	a.emitCompactionFailed(CompactionTriggerPressure, errCompressStaleContext)
	a.emitCompactionSucceeded(CompactionTriggerPressure, 3, "digest body", "")
	a.emitCompactionFailed(CompactionTriggerPressure, errors.New("boom"))

	var attempts []int
	okSeen := false
	for _, c := range done {
		switch c.Outcome {
		case "failed":
			attempts = append(attempts, c.Attempt)
		case "ok":
			okSeen = c.Summary == "digest body" && c.Messages == 3
		}
	}
	if len(attempts) != 3 || attempts[0] != 1 || attempts[1] != 2 || attempts[2] != 1 {
		t.Fatalf("failure attempts = %v, want [1 2 1] — counting up, reset by success", attempts)
	}
	if !okSeen {
		t.Fatalf("success outcome missing or malformed among %+v", done)
	}
	if got := a.sess.compaction.failureStreak.Load(); got != 1 {
		t.Fatalf("final failure streak = %d, want 1", got)
	}
}

// The stale-context CAS rejection — the incident's root cause — classifies as
// a failure whose reason names the discard, not a generic provider error.
func TestStaleContextFailureReasonNamesTheDiscard(t *testing.T) {
	if got := compactionFailureReason(errCompressStaleContext); got == "" || got == "summarizer request failed" {
		t.Fatalf("stale reason = %q, want the dedicated context-changed classification", got)
	}
}
