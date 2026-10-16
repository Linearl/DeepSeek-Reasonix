package agent

import (
	"context"
	"net/url"
	"sync"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// timeoutErr is the production shape of a ResponseHeaderTimeout fire: a
// net.Error timeout whose message carries the shared transport phrase.
type timeoutErr struct{ msg string }

func (e timeoutErr) Error() string   { return e.msg }
func (e timeoutErr) Timeout() bool   { return true }
func (e timeoutErr) Temporary() bool { return true }

// headerTimeoutError mirrors the exact chain SendWithRetry hands the agent:
// RequestFailure → url.Error → the transport's header-timeout net.Error.
func headerTimeoutError() error {
	return &provider.RequestFailure{
		Identity:  provider.RequestIdentity{Provider: "gw", Protocol: "anthropic"},
		Operation: "request failed",
		Err: &url.Error{
			Op:  "Post",
			URL: "https://gw.example/anthropic/v1/messages",
			Err: timeoutErr{msg: "http2: timeout awaiting response headers"},
		},
	}
}

// headerTimeoutScriptProvider fails Stream() itself with the queued errors —
// the realistic face of a header timeout (the request dies in httpClient.Do,
// before any chunk channel exists).
type headerTimeoutScriptProvider struct {
	mu    sync.Mutex
	errs  []error
	calls int
}

func (p *headerTimeoutScriptProvider) Name() string { return "header-timeout-script" }

func (p *headerTimeoutScriptProvider) ContextBudgetPolicy() provider.ContextBudgetPolicy {
	return provider.ContextBudgetPolicy{WindowMode: provider.ContextWindowIndependent}
}

func (p *headerTimeoutScriptProvider) Stream(_ context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if len(p.errs) == 0 {
		// Buffered to hold every chunk before the reader starts: Stream must
		// return the channel, and the run loop only reads after that.
		ch := make(chan provider.Chunk, 3)
		ch <- provider.Chunk{Type: provider.ChunkText, Text: "recovered"}
		ch <- provider.Chunk{Type: provider.ChunkDone}
		close(ch)
		return ch, nil
	}
	err := p.errs[0]
	p.errs = p.errs[1:]
	return nil, err
}

func (p *headerTimeoutScriptProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// countNotices counts a notice code in the recording sink.
func countNotices(sink *recordingSink, code string) int {
	n := 0
	for _, c := range noticeCodes(sink) {
		if c == code {
			n++
		}
	}
	return n
}

// 任务927 acceptance ②: one response-header timeout earns a single retry —
// with the actionable advisory notice fired BEFORE the replay — and a second
// consecutive timeout stops the loop instead of burning attempts 3 and 4. The
// incident's 4 × 300s ≈ 20-minute silent loop becomes 2 × 300s with a named
// give-up.
func TestHeaderTimeoutStopsRetryLoopAfterTwo(t *testing.T) {
	prov := &headerTimeoutScriptProvider{errs: []error{headerTimeoutError(), headerTimeoutError()}}
	sink := &recordingSink{}
	a := newBreakerAgent(t, prov, sink)

	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err == nil {
		t.Fatal("expected the turn to fail on the persistent header timeout")
	}
	if !provider.IsResponseHeaderTimeout(got.err) {
		t.Fatalf("turn error = %v, want the original response-header timeout", got.err)
	}
	if n := countNotices(sink, event.NoticeCodeHeaderTimeoutRetry); n != 1 {
		t.Fatalf("header_timeout_retry notices = %d, want exactly 1 (advisory before the one earned retry)", n)
	}
	if n := countNotices(sink, event.NoticeCodeHeaderTimeoutGiveup); n != 1 {
		t.Fatalf("header_timeout_giveup notices = %d, want exactly 1 (stop at streak 2)", n)
	}
	// The loop must stop BEFORE attempt 3: exactly two provider calls.
	if got := prov.callCount(); got != 2 {
		t.Fatalf("provider calls = %d, want 2 (stop at headerTimeoutStopStreak)", got)
	}
	// The terminal retry frame settles the strip (same shape as 929 S2's stop).
	found := false
	for _, e := range sink.events {
		if e.Kind == event.Retrying && e.Recovery != nil && e.Recovery.BudgetExhausted && e.Recovery.Reason == "response header timeout" {
			found = true
		}
	}
	if !found {
		t.Fatal("want a terminal Retrying frame with Reason='response header timeout' so the retry strip settles")
	}
}

// The streak is per-run: a different failure shape between two header
// timeouts resets it, so only CONSECUTIVE timeouts stop the loop. Sequence
// [timeout, RST, timeout, timeout] runs four attempts and stops on the
// second consecutive timeout.
func TestHeaderTimeoutStreakResetsOnOtherFailure(t *testing.T) {
	prov := &headerTimeoutScriptProvider{errs: []error{
		headerTimeoutError(),
		http2RSTError(),
		headerTimeoutError(),
		headerTimeoutError(),
	}}
	sink := &recordingSink{}
	a := newBreakerAgent(t, prov, sink)

	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err == nil {
		t.Fatal("expected the turn to fail")
	}
	if n := countNotices(sink, event.NoticeCodeHeaderTimeoutGiveup); n != 1 {
		t.Fatalf("header_timeout_giveup notices = %d, want exactly 1 (streak reset by the RST, stopped on the later consecutive pair)", n)
	}
	// Two separate streaks → two advisories (before retry #2 and retry #3).
	if n := countNotices(sink, event.NoticeCodeHeaderTimeoutRetry); n != 2 {
		t.Fatalf("header_timeout_retry notices = %d, want 2 (one per first-in-streak)", n)
	}
	if got := prov.callCount(); got != 4 {
		t.Fatalf("provider calls = %d, want 4 (reset streak runs the middle pair)", got)
	}
}

// A header timeout followed by a healthy answer: one advisory, no give-up,
// the turn succeeds. The governance must not punish a transient blip.
func TestHeaderTimeoutRecoversOnSecondAttempt(t *testing.T) {
	prov := &headerTimeoutScriptProvider{errs: []error{headerTimeoutError()}}
	sink := &recordingSink{}
	a := newBreakerAgent(t, prov, sink)

	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err != nil {
		t.Fatalf("turn failed after a single transient timeout recovered: %v", got.err)
	}
	if got.text != "recovered" {
		t.Fatalf("text = %q, want the scripted recovery", got.text)
	}
	if n := countNotices(sink, event.NoticeCodeHeaderTimeoutRetry); n != 1 {
		t.Fatalf("header_timeout_retry notices = %d, want 1", n)
	}
	if n := countNotices(sink, event.NoticeCodeHeaderTimeoutGiveup); n != 0 {
		t.Fatalf("header_timeout_giveup notices = %d, want 0 on a recovered turn", n)
	}
}

// The advisory must carry the attempt/max copy the i18n format promises, so
// the user sees which attempt the timeout landed on.
func TestHeaderTimeoutAdvisoryCarriesAttemptNumbers(t *testing.T) {
	prov := &headerTimeoutScriptProvider{errs: []error{headerTimeoutError(), headerTimeoutError()}}
	sink := &recordingSink{}
	a := newBreakerAgent(t, prov, sink)

	_ = a.streamWithSamplingRecovery(context.Background(), 1)
	for _, e := range sink.events {
		if e.Kind == event.Notice && e.Code == event.NoticeCodeHeaderTimeoutRetry {
			if e.Level != event.LevelInfo {
				t.Errorf("advisory level = %v, want info (the loop is still recovering)", e.Level)
			}
			if e.Text == "" {
				t.Error("advisory text must not be empty")
			}
			if e.Detail == "" {
				t.Error("advisory detail must name the streak")
			}
			return
		}
	}
	t.Fatal("no header_timeout_retry advisory found")
}
