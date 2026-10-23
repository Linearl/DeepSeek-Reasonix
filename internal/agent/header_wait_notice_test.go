package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

func shrinkHeaderWaitForTest(t *testing.T, after, repeat time.Duration) {
	t.Helper()
	oldAfter, oldRepeat := headerWaitNoticeAfter, headerWaitNoticeRepeat
	headerWaitNoticeAfter, headerWaitNoticeRepeat = after, repeat
	t.Cleanup(func() { headerWaitNoticeAfter, headerWaitNoticeRepeat = oldAfter, oldRepeat })
}

// shrinkHeaderWaitLightForTest shrinks ONLY the 任务1080 light-tier threshold
// so a test can drive the 3~5s mark in milliseconds without touching the 30s
// slow tier (the two knobs are independent by design).
func shrinkHeaderWaitLightForTest(t *testing.T, after time.Duration) {
	t.Helper()
	old := headerWaitLightNoticeAfter
	headerWaitLightNoticeAfter = after
	t.Cleanup(func() { headerWaitLightNoticeAfter = old })
}

// slowStartProvider delays returning its stream channel — simulating the
// response-header wait — then either replies or hangs. The delay honours ctx
// so a cancelled turn unblocks the start goroutine (goleak-clean).
type slowStartProvider struct {
	delay time.Duration
	reply string
	hang  bool
}

func (p *slowStartProvider) Name() string { return "slow-start" }

func (p *slowStartProvider) ContextBudgetPolicy() provider.ContextBudgetPolicy {
	return provider.ContextBudgetPolicy{WindowMode: provider.ContextWindowIndependent}
}

func (p *slowStartProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	if p.delay > 0 {
		select {
		case <-time.After(p.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p.hang {
		return make(chan provider.Chunk), nil
	}
	ch := make(chan provider.Chunk, 2)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: p.reply}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

// 任务927 acceptance ① (phase distinction, half 1): when the gateway is slow
// to send RESPONSE HEADERS, the header-wait watch speaks — and the 929 S3
// prefill_slow must NOT fire, because the body-prefill phase never began.
// Before this change the wait ran inline above the select, so neither notice
// could exist during it.
func TestHeaderWaitNoticeFiresDuringResponseHeaderWait(t *testing.T) {
	shrinkHeaderWaitForTest(t, 40*time.Millisecond, 40*time.Millisecond)
	prov := &slowStartProvider{delay: 150 * time.Millisecond, reply: "ok"}
	sink := &recordingSink{}
	a := newBreakerAgent(t, prov, sink)

	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err != nil {
		t.Fatalf("turn failed: %v", got.err)
	}
	if !hasNoticeCode(sink, event.NoticeCodeHeaderWaitSlow) {
		t.Fatalf("notice codes = %v, want %s during a slow header wait", noticeCodes(sink), event.NoticeCodeHeaderWaitSlow)
	}
	if hasNoticeCode(sink, event.NoticeCodePrefillSlow) {
		t.Fatalf("notice codes = %v: prefill_slow must not fire while still awaiting response headers (phase confusion)", noticeCodes(sink))
	}
}

// 任务927 acceptance ① (phase distinction, half 2): headers arrive promptly
// but the BODY stays silent — the929 S3 prefill_slow fires and the
// header-wait watch stays quiet, because phase 1 already ended. The two
// watches are structurally exclusive: the prefill timer only arms when the
// start result lands.
func TestPrefillSlowNotConfusedWithHeaderWaitOnSilentBody(t *testing.T) {
	shrinkPrefillNoticeForTest(t, 40*time.Millisecond)
	prov := &slowStartProvider{hang: true}
	sink := &recordingSink{}
	a := newBreakerAgent(t, prov, sink)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	got := a.streamWithSamplingRecovery(ctx, 1)
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled (the test's own cancel)", got.err)
	}
	if !hasNoticeCode(sink, event.NoticeCodePrefillSlow) {
		t.Fatalf("notice codes = %v, want %s for a silent body after fast headers", noticeCodes(sink), event.NoticeCodePrefillSlow)
	}
	if hasNoticeCode(sink, event.NoticeCodeHeaderWaitSlow) {
		t.Fatalf("notice codes = %v: header_wait_slow must not fire after headers already arrived (phase confusion)", noticeCodes(sink))
	}
}

// The header-wait notice repeats on its cadence while the wait continues, and
// every emission names the phase and the elapsed wait so a multi-minute stall
// reads as progress instead of one stale line.
func TestHeaderWaitNoticeRepeatsWithElapsedDetail(t *testing.T) {
	shrinkHeaderWaitForTest(t, 30*time.Millisecond, 30*time.Millisecond)
	prov := &slowStartProvider{delay: 120 * time.Millisecond, reply: "ok"}
	sink := &recordingSink{}
	a := newBreakerAgent(t, prov, sink)

	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err != nil {
		t.Fatalf("turn failed: %v", got.err)
	}
	count := 0
	for _, e := range sink.events {
		if e.Kind == event.Notice && e.Code == event.NoticeCodeHeaderWaitSlow {
			count++
			if !strings.Contains(e.Detail, "awaiting response headers") {
				t.Errorf("header-wait notice detail = %q, want the phase marker", e.Detail)
			}
		}
	}
	if count < 2 {
		t.Fatalf("header_wait_slow notices = %d, want >= 2 (initial + repeat) during a %s wait", count, prov.delay)
	}
}

// Fast headers and a fast reply: neither silence notice may fire — the
// watches are for named silences, not for turns that answer promptly.
func TestSilenceNoticesStayQuietOnFastTurn(t *testing.T) {
	shrinkHeaderWaitForTest(t, 40*time.Millisecond, 40*time.Millisecond)
	shrinkPrefillNoticeForTest(t, 40*time.Millisecond)
	prov := &slowStartProvider{reply: "instant"}
	sink := &recordingSink{}
	a := newBreakerAgent(t, prov, sink)

	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err != nil {
		t.Fatalf("turn failed: %v", got.err)
	}
	if codes := noticeCodes(sink); len(codes) != 0 {
		t.Fatalf("notice codes = %v, want none on a fast turn", codes)
	}
}

// Cancelling mid header-wait surfaces the cancellation exactly as before —
// the async start must not swallow or reshape it.
func TestCancelDuringHeaderWaitReturnsContextCanceled(t *testing.T) {
	shrinkHeaderWaitForTest(t, 10*time.Second, 10*time.Second)
	prov := &slowStartProvider{delay: 10 * time.Second, hang: true}
	sink := &recordingSink{}
	a := newBreakerAgent(t, prov, sink)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	got := a.streamWithSamplingRecovery(ctx, 1)
	if !errors.Is(got.err, context.DeadlineExceeded) && !errors.Is(got.err, context.Canceled) {
		t.Fatalf("err = %v, want the test's own deadline", got.err)
	}
}

// 任务1080 ①: the light tier fires during the header wait (well before the
// 30s slow tier) and names the wait with the light code — not the slow code.
func TestHeaderWaitLightNoticeFiresDuringHeaderWait(t *testing.T) {
	shrinkHeaderWaitForTest(t, 300*time.Millisecond, 300*time.Millisecond) // slow tier far away
	shrinkHeaderWaitLightForTest(t, 50*time.Millisecond)
	prov := &slowStartProvider{delay: 250 * time.Millisecond, reply: "ok"}
	sink := &recordingSink{}
	a := newBreakerAgent(t, prov, sink)

	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err != nil {
		t.Fatalf("turn failed: %v", got.err)
	}
	if !hasNoticeCode(sink, event.NoticeCodeHeaderWaitLight) {
		t.Fatalf("notice codes = %v, want %s during a %s header wait", noticeCodes(sink), event.NoticeCodeHeaderWaitLight, prov.delay)
	}
	// The slow tier's threshold was NOT reached in this wait — it must stay
	// quiet, so the two tiers cannot double-fire on the same stretch.
	if hasNoticeCode(sink, event.NoticeCodeHeaderWaitSlow) {
		t.Fatalf("notice codes = %v: header_wait_slow must not fire below its threshold", noticeCodes(sink))
	}
}

// 任务1080 ②: fast headers + fast reply → zero light-tier notice (不打扰).
func TestHeaderWaitLightNoticeStaysQuietOnFastTurn(t *testing.T) {
	shrinkHeaderWaitForTest(t, 40*time.Millisecond, 40*time.Millisecond)
	shrinkHeaderWaitLightForTest(t, 40*time.Millisecond)
	prov := &slowStartProvider{reply: "instant"}
	sink := &recordingSink{}
	a := newBreakerAgent(t, prov, sink)

	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err != nil {
		t.Fatalf("turn failed: %v", got.err)
	}
	if hasNoticeCode(sink, event.NoticeCodeHeaderWaitLight) {
		t.Fatalf("notice codes = %v: a fast turn must not emit the light wait notice", noticeCodes(sink))
	}
}

// 任务1080 ③: once per TURN — a wait long enough to cross the threshold twice
// (via the repeat cadence) still emits the light notice exactly once.
func TestHeaderWaitLightNoticeEmittedOncePerTurn(t *testing.T) {
	shrinkHeaderWaitForTest(t, 40*time.Millisecond, 40*time.Millisecond) // slow tier repeats fast
	shrinkHeaderWaitLightForTest(t, 30*time.Millisecond)
	prov := &slowStartProvider{delay: 200 * time.Millisecond, reply: "ok"}
	sink := &recordingSink{}
	a := newBreakerAgent(t, prov, sink)

	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err != nil {
		t.Fatalf("turn failed: %v", got.err)
	}
	count := 0
	for _, c := range noticeCodes(sink) {
		if c == event.NoticeCodeHeaderWaitLight {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("header_wait_light notices = %d, want exactly 1 per turn during a %s wait", count, prov.delay)
	}
	if !a.turn.headerWaitLightShown {
		t.Fatal("the turn flag must be set so the light notice stays at one per turn")
	}
}
