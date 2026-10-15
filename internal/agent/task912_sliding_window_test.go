package agent

// 任务912 P1-c: 永不全量 —— once compaction has failed N times in a row the turn
// must leave with a capped sliding window, never with the full transcript.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// growIntoCapBand appends until the canonical transcript sits over the
// sliding-window cap but still under the hard ceiling. 任务822: past the
// ceiling the VISIBLE view degrades to a bounded tail, so the growth loop must
// watch the canonical estimate — tracking the view would never terminate.
func growIntoCapBand(t *testing.T, a *Agent, sess *Session) {
	t.Helper()
	target := slidingWindowCapTarget(a.effectiveContextWindow())
	hard := a.hardInputCeiling()
	filler := strings.Repeat("word ", 100) // ~50 tokens per append
	for range 4000 {
		if canonicalEstimate(a, sess) >= target {
			break
		}
		if canonicalEstimate(a, sess) >= hard {
			t.Fatalf("fixture crossed the hard ceiling %d before reaching the cap band %d", hard, target)
		}
		sess.Add(provider.Message{Role: provider.RoleAssistant, Content: filler})
	}
	if got := a.estimatedVisibleRequestTokens(a.modelVisibleMessages()); got < target {
		t.Fatalf("visible view = %d tokens, under the cap band %d — the fixture never got there", got, target)
	}
}

// 验收④的前提: the cap is 85% of the window, and it always sits below the
// physical ceiling — a "cap" that could exceed the ceiling would be no cap.
func TestTask912SlidingWindowCapTargetIsEightyFivePercentOfWindow(t *testing.T) {
	if got := slidingWindowCapTarget(1_000_000); got != 850_000 {
		t.Fatalf("cap target(1M) = %d, want 850000 (0.85 × window)", got)
	}
	if got := slidingWindowCapTarget(0); got != 0 {
		t.Fatalf("cap target without a window = %d, want 0 (nothing to cap)", got)
	}

	sess := foldableSessionOverForce(6)
	a := agentOverForceWindow(t, &fakeProvider{reply: "digest"}, sess, 4000)
	target := slidingWindowCapTarget(a.effectiveContextWindow())
	if target <= 0 || target >= a.hardInputCeiling() {
		t.Fatalf("cap target %d is not strictly under the hard ceiling %d (window %d)",
			target, a.hardInputCeiling(), a.effectiveContextWindow())
	}
}

// The gate is a streak, not a single failure: a first blip keeps the ordinary
// "the turn proceeds unfolded" behavior (its view is still worth the cache);
// the third one in a row is a pattern.
func TestTask912SlidingWindowCapNeedsThreeConsecutiveFailures(t *testing.T) {
	sess := foldableSessionOverForce(3)
	a := agentOverForceWindow(t, &fakeProvider{streamErr: errors.New("boom")}, sess, 4000)
	m := a.contextManager()
	growIntoCapBand(t, a, sess)

	for _, tc := range []struct {
		streak int64
		want   bool
	}{{1, false}, {2, false}, {3, true}, {4, true}} {
		a.sess.compaction.failureStreak.Store(tc.streak)
		if got := m.slidingWindowCapEligible(); got != tc.want {
			t.Errorf("failureStreak=%d eligible = %t, want %t", tc.streak, got, tc.want)
		}
	}

	// A view already inside the cap is left alone even at streak 3: truncating
	// it would only lose content.
	small := foldableSessionOverForce(1)
	b := agentOverForceWindow(t, &fakeProvider{streamErr: errors.New("boom")}, small, 200_000)
	b.sess.compaction.failureStreak.Store(3)
	if est, target := b.contextManager().currentPrepared().InputTokens, slidingWindowCapTarget(b.effectiveContextWindow()); est >= target {
		t.Fatalf("small fixture estimates %d tokens, at or over the cap %d — it cannot prove the no-op", est, target)
	}
	if b.contextManager().slidingWindowCapEligible() {
		t.Fatal("a view already inside the cap must not be eligible for truncation")
	}
}

// 验收④（正向钉）: three failed pressure passes in a row, and the input the turn
// actually sees is under the cap — never the full transcript. Canonical storage
// keeps every byte: this is a projection, so nothing is deleted.
func TestTask912RepeatedFailuresCapInputNeverFull(t *testing.T) {
	sess := foldableSessionOverForce(3)
	a := agentOverForceWindow(t, &fakeProvider{streamErr: errors.New("boom")}, sess, 4000)
	target := slidingWindowCapTarget(a.effectiveContextWindow())
	growIntoCapBand(t, a, sess)

	var notices []event.Event
	a.svc.sink = event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && e.Code == event.NoticeCodeSlidingWindowCap {
			notices = append(notices, e)
		}
	})

	for i := range 4 {
		if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
			t.Fatalf("pressure prepare %d = %v, want an absorbed failure with a capped view", i, err)
		}
		if a.sess.compaction.failureStreak.Load() >= slidingWindowFailureStreak {
			break
		}
		// Shift the view so the failed receipt's hash cannot hold the next
		// attempt hostage (a real session grows between turns).
		sess.Add(provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("nudge %d", i)})
	}

	if got := a.sess.compaction.failureStreak.Load(); got < slidingWindowFailureStreak {
		t.Fatalf("failure streak = %d, want at least %d before the cap may fire", got, slidingWindowFailureStreak)
	}

	canonicalTokens := canonicalEstimate(a, sess)
	if canonicalTokens < target {
		t.Fatalf("canonical transcript = %d tokens, under the cap band %d — the cap was never exercised", canonicalTokens, target)
	}
	viewTokens := a.estimatedVisibleRequestTokens(a.modelVisibleMessages())
	if viewTokens >= target {
		t.Fatalf("view = %d tokens, want under the sliding-window cap %d (永不全量)", viewTokens, target)
	}
	if viewTokens >= canonicalTokens {
		t.Fatalf("view = %d tokens equals the full transcript %d — the turn fell back to 全量", viewTokens, canonicalTokens)
	}
	if len(notices) == 0 {
		t.Fatal("no sliding-window-cap notice was raised — the clip must be visible, not silent")
	}
	if !strings.Contains(notices[0].Detail, "consecutive_failures=3") {
		t.Fatalf("notice detail = %q, want the consecutive-failure count", notices[0].Detail)
	}
	// The cap is a projection: canonical storage keeps the whole history.
	if got := len(sess.Messages); got < 3 {
		t.Fatalf("canonical messages = %d — the cap must never delete, only project", got)
	}
	if !truncatedRescue(a) {
		t.Fatalf("receipt = %+v, want an applied truncation with no fabricated digest", a.sess.compactionState.LastReceipt)
	}
}
