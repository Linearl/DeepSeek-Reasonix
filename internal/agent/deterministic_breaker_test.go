package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// breakerScriptProvider fails every call with the same stream interruption,
// counting calls so a test can prove WHERE the retry loop stopped.
type breakerScriptProvider struct {
	mu     sync.Mutex
	calls  int
	err    error
	delays []time.Duration // popped one per call; empty = no delay
}

func (p *breakerScriptProvider) Name() string { return "breaker-script" }

func (p *breakerScriptProvider) ContextBudgetPolicy() provider.ContextBudgetPolicy {
	return provider.ContextBudgetPolicy{WindowMode: provider.ContextWindowIndependent}
}

func (p *breakerScriptProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	p.mu.Lock()
	p.calls++
	var delay time.Duration
	if len(p.delays) > 0 {
		delay = p.delays[0]
		p.delays = p.delays[1:]
	}
	err := p.err
	p.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	ch := make(chan provider.Chunk, 1)
	ch <- provider.Chunk{Type: provider.ChunkError, Err: err}
	close(ch)
	return ch, nil
}

func (p *breakerScriptProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// newBreakerAgent wires a recording sink so the test can assert the
// user-visible frames the breaker emits.
func newBreakerAgent(t *testing.T, prov provider.Provider, sink event.Sink) *Agent {
	t.Helper()
	sess := NewSession("")
	sess.Replace([]provider.Message{{Role: provider.RoleUser, Content: "summarize this huge context"}})
	return New(prov, tool.NewRegistry(), sess, Options{
		ContextWindow: 1_048_576, CompactRatio: 2, MaxOutputTokens: 0, Temperature: 0.25,
	}, sink)
}

func http2RSTError() error {
	return provider.StreamInterrupt(
		fmt.Errorf("mimo-token-plan: read stream: %w",
			errors.New("stream error: stream ID 5; INTERNAL_ERROR; received from peer")),
		provider.StreamInterruptServerError)
}

func shrinkDeterministicBreakerForTest(t *testing.T, jitter time.Duration) {
	t.Helper()
	min, j, streak := deterministicBreakMinAttempt, deterministicBreakJitter, deterministicBreakStreak
	deterministicBreakMinAttempt = 0
	deterministicBreakJitter = jitter
	t.Cleanup(func() {
		deterministicBreakMinAttempt, deterministicBreakJitter, deterministicBreakStreak = min, j, streak
	})
}

func noticeCodes(sink *recordingSink) []string {
	var codes []string
	for _, e := range sink.events {
		if e.Kind == event.Notice && e.Code != "" {
			codes = append(codes, e.Code)
		}
	}
	return codes
}

// 任务929 S2 acceptance ①: three same-reason stream failures with near-identical
// durations stop the loop BEFORE the 4th full replay (the incident: 4 × ~304s
// prefill = 18.5 minutes of silence) and the user gets the actionable notice.
func TestDeterministicStreamFailureStopsAfterThree(t *testing.T) {
	shrinkDeterministicBreakerForTest(t, 10*time.Second) // production jitter window; streak stays 3
	prov := &breakerScriptProvider{err: http2RSTError()}
	sink := &recordingSink{}
	a := newBreakerAgent(t, prov, sink)

	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err == nil {
		t.Fatal("expected the turn to fail on the deterministic rejection")
	}
	if calls := prov.callCount(); calls != 3 {
		t.Fatalf("provider calls = %d, want 3 — the breaker must refuse the 4th replay", calls)
	}
	codes := noticeCodes(sink)
	found := false
	for _, c := range codes {
		if c == event.NoticeCodeProviderDeterministicReject {
			found = true
		}
	}
	if !found {
		t.Fatalf("notice codes = %v, want %s", codes, event.NoticeCodeProviderDeterministicReject)
	}
	// The terminal frame must settle the UI's retry strip with the give-up shape.
	settled := false
	for _, e := range sink.events {
		if e.Kind == event.Retrying && e.Recovery != nil && e.Recovery.Reason == "deterministic reject" {
			settled = true
		}
	}
	if !settled {
		t.Fatal("missing the terminal retrying frame for the deterministic give-up")
	}
}

// 任务929 S2 acceptance ②: jittered failures (durations spread past the
// window) are NOT mistaken for a deterministic rejection — the loop keeps its
// historical behaviour and burns the full attempt cap.
func TestJitteredStreamFailuresKeepRetrying(t *testing.T) {
	shrinkDeterministicBreakerForTest(t, time.Millisecond) // spread below this = deterministic
	prov := &breakerScriptProvider{
		err:    http2RSTError(),
		delays: []time.Duration{0, 5 * time.Millisecond, 10 * time.Millisecond, 0},
	}
	sink := &recordingSink{}
	a := newBreakerAgent(t, prov, sink)

	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err == nil {
		t.Fatal("expected the turn to fail at the attempt cap")
	}
	if calls := prov.callCount(); calls != 4 {
		t.Fatalf("provider calls = %d, want 4 — jitter must not trip the breaker", calls)
	}
	for _, c := range noticeCodes(sink) {
		if c == event.NoticeCodeProviderDeterministicReject {
			t.Fatalf("jittered failures emitted the deterministic-reject notice (code %s)", c)
		}
	}
}

// The pure pattern logic: streak, reason-reset, min-duration floor, jitter
// window — synthetic durations, production thresholds.
func TestDeterministicFailurePatternLogic(t *testing.T) {
	a := &Agent{}
	newRun := func() *samplingRecoveryState {
		return &samplingRecoveryState{attemptStartedAt: time.Now()}
	}

	// 304.0 / 303.8 / 304.4s — the incident's fingerprints — prove the pattern
	// on the third failure.
	s := newRun()
	for i, d := range []time.Duration{304 * time.Second, 303800 * time.Millisecond, 304400 * time.Millisecond} {
		trip, _ := a.noteDeterministicStreamFailure(s, provider.StreamInterruptServerError, d)
		want := i == 2
		if trip != want {
			t.Fatalf("failure %d: trip=%t, want %t (streak=%d min=%s max=%s)", i+1, trip, want, s.detStreak, s.detMin, s.detMax)
		}
	}

	// A different reason restarts the run.
	s = newRun()
	a.noteDeterministicStreamFailure(s, provider.StreamInterruptServerError, 304*time.Second)
	a.noteDeterministicStreamFailure(s, provider.StreamInterruptServerError, 304*time.Second)
	if trip, _ := a.noteDeterministicStreamFailure(s, provider.StreamInterruptIdleTimeout, 304*time.Second); trip {
		t.Fatal("a reason change must restart the run, not prove it")
	}
	if s.detStreak != 1 || s.detReason != provider.StreamInterruptIdleTimeout {
		t.Fatalf("run after reason change = (%q, %d), want a fresh single-failure run", s.detReason, s.detStreak)
	}
	// …and the new reason proves on its own third failure.
	if trip, _ := a.noteDeterministicStreamFailure(s, provider.StreamInterruptIdleTimeout, 304*time.Second); trip {
		t.Fatal("streak must count from 1 after the reason changed")
	}
	if trip, _ := a.noteDeterministicStreamFailure(s, provider.StreamInterruptIdleTimeout, 304*time.Second); !trip {
		t.Fatal("third same-reason failure must prove the pattern")
	}

	// Fast failures stay below the production floor: an instantly-refused
	// request keeps the historical retry loop.
	s = newRun()
	for range deterministicBreakStreak {
		if trip, _ := a.noteDeterministicStreamFailure(s, provider.StreamInterruptServerError, 50*time.Millisecond); trip {
			t.Fatal("sub-floor durations must never trip the deterministic breaker")
		}
	}
	if s.detStreak != deterministicBreakStreak {
		t.Fatalf("streak = %d, want %d — fast failures still count, they just never trip", s.detStreak, deterministicBreakStreak)
	}

	// Wide jitter (a network that varies) never proves determinism.
	s = newRun()
	for _, d := range []time.Duration{100 * time.Second, 150 * time.Second, 120 * time.Second} {
		if trip, _ := a.noteDeterministicStreamFailure(s, provider.StreamInterruptServerError, d); trip {
			t.Fatalf("duration %s: jitter past the window must not trip", d)
		}
	}

	// A non-stream failure resets the run (the caller passes reason "").
	s = newRun()
	a.noteDeterministicStreamFailure(s, provider.StreamInterruptServerError, 304*time.Second)
	s.resetDeterministicStreamFailure()
	if s.detStreak != 0 || s.detReason != "" {
		t.Fatalf("state after reset = (%q, %d), want cleared", s.detReason, s.detStreak)
	}
}
