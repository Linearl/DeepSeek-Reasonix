package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/i18n"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

func shrinkPrefillNoticeForTest(t *testing.T, d time.Duration) {
	t.Helper()
	old := prefillNoticeAfter
	prefillNoticeAfter = d
	t.Cleanup(func() { prefillNoticeAfter = old })
}

func hasNoticeCode(sink *recordingSink, code string) bool {
	for _, c := range noticeCodes(sink) {
		if c == code {
			return true
		}
	}
	return false
}

// 任务929 S3 acceptance ①: an attempt that produces zero provider output for
// the threshold gets the user-visible "processing a very large context"
// notice — the 18.5-minute silent hang (issue #59) must no longer be
// indistinguishable from a dead session. The turn is then cancelled by the
// user exactly as before; only the observability changed.
func TestPrefillNoticeFiresWhenNoProviderOutput(t *testing.T) {
	shrinkPrefillNoticeForTest(t, 50*time.Millisecond)
	prov := &fakeProvider{hang: true}
	sink := &recordingSink{}
	a := newBreakerAgent(t, prov, sink)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	got := a.streamWithSamplingRecovery(ctx, 1)
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled (the test's own cancel)", got.err)
	}
	if !hasNoticeCode(sink, event.NoticeCodePrefillSlow) {
		t.Fatalf("notice codes = %v, want %s after a silent %s wait", noticeCodes(sink), event.NoticeCodePrefillSlow, prefillNoticeAfter)
	}
}

// 任务929 S3 acceptance ②: a stream that starts answering promptly never
// shows the notice — no noise on ordinary turns.
func TestPrefillNoticeStaysQuietOnFastOutput(t *testing.T) {
	shrinkPrefillNoticeForTest(t, 50*time.Millisecond)
	prov := &fakeProvider{reply: "streamed answer"}
	sink := &recordingSink{}
	a := newBreakerAgent(t, prov, sink)

	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err != nil {
		t.Fatalf("recovery failed: %v", got.err)
	}
	if hasNoticeCode(sink, event.NoticeCodePrefillSlow) {
		t.Fatalf("notice codes = %v, a fast turn must not announce slow prefill", noticeCodes(sink))
	}
}

// Per TURN, not per attempt: retry attempts re-arm the watch, but the same
// sentence repeated per replay is noise.
func TestPrefillNoticeEmittedOncePerTurn(t *testing.T) {
	shrinkPrefillNoticeForTest(t, 50*time.Millisecond)
	sink := &recordingSink{}
	a := newBreakerAgent(t, &fakeProvider{reply: "x"}, sink)

	a.emitPrefillSlowNotice(sink)
	a.emitPrefillSlowNotice(sink)

	count := 0
	for _, c := range noticeCodes(sink) {
		if c == event.NoticeCodePrefillSlow {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("prefill notices = %d, want exactly 1 per turn", count)
	}
	if !a.turn.prefillNoticeShown {
		t.Fatal("the turn flag must be set so retry attempts stay quiet")
	}
}

// tl1608 分档①: at or above the compact trigger the original prefill
// attribution stays — there the context size actually supports the strong
// reading ("normal prefill phase"), so the wording must not downgrade.
func TestPrefillNoticeUsesPrefillWordingAtOrAboveCompactTrigger(t *testing.T) {
	sess := NewSession("")
	sess.Replace([]provider.Message{{Role: provider.RoleUser, Content: strings.Repeat("filler ", 400)}})
	sink := &recordingSink{}
	a := New(&fakeProvider{reply: "x"}, tool.NewRegistry(), sess, Options{
		ContextWindow: 400, CompactRatio: 0.5, MaxOutputTokens: 0, Temperature: 0.25,
	}, sink)
	trigger := a.compactTrigger()
	if trigger <= 0 {
		t.Fatalf("compactTrigger = %d, the tier fixture needs a configured window", trigger)
	}
	used := a.ContextUsedTokens()
	if used < trigger {
		t.Fatalf("tier-① fixture is broken: used=%d sits below trigger=%d — fix the fixture, not the assertion", used, trigger)
	}
	if got, want := a.prefillSlowNoticeText(), fmt.Sprintf(i18n.M.PrefillSlowNoticeFmt, used); got != want {
		t.Fatalf("at/above-trigger wording = %q, want the prefill attribution %q", got, want)
	}
}

// tl1608 分档②: below the compact trigger the attribution must downgrade to
// the neutral wording — the same 60s silence may be a slow upstream or a
// connection problem, so claiming "the normal prefill phase" over-claims.
func TestPrefillNoticeDowngradesToNeutralBelowCompactTrigger(t *testing.T) {
	a := newBreakerAgent(t, &fakeProvider{reply: "x"}, &recordingSink{})
	trigger := a.compactTrigger()
	if trigger <= 0 {
		t.Fatalf("compactTrigger = %d, want a configured window from the breaker fixture", trigger)
	}
	if used := a.ContextUsedTokens(); used >= trigger {
		t.Fatalf("tier-② fixture is broken: used=%d sits at/above trigger=%d — fix the fixture, not the assertion", used, trigger)
	}
	if got, want := a.prefillSlowNoticeText(), fmt.Sprintf(i18n.M.PrefillSlowNoticeNeutralFmt, int(prefillNoticeAfter.Seconds())); got != want {
		t.Fatalf("below-trigger wording = %q, want the neutral attribution %q", got, want)
	}
}

// tl1608: the emit path carries the tiered text, not a hardcoded line —
// pinned on the neutral tier, the one the standard breaker fixture exercises.
func TestPrefillNoticeEmitCarriesNeutralTierText(t *testing.T) {
	sink := &recordingSink{}
	a := newBreakerAgent(t, &fakeProvider{reply: "x"}, sink)
	a.emitPrefillSlowNotice(sink)
	want := fmt.Sprintf(i18n.M.PrefillSlowNoticeNeutralFmt, int(prefillNoticeAfter.Seconds()))
	for _, e := range sink.events {
		if e.Kind == event.Notice && e.Code == event.NoticeCodePrefillSlow {
			if e.Text != want {
				t.Fatalf("emitted text = %q, want the neutral tier text %q", e.Text, want)
			}
			return
		}
	}
	t.Fatal("prefill notice not emitted through the emit path")
}
