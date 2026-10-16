package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"reasonix/internal/event"
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
