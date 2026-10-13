package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// Task 845 (user-ratified three-lane retry semantics):
//  1. a 429 is a QUEUE — exempt from the cross-turn 8/15min window, bounded
//     by the wait budget / queue cap instead, cancellable, UI-visible;
//  2. an exhausted quota LONG-WAITS and resumes automatically (no fallback
//     needed), including GLM's Chinese quota bodies upstream;
//  3. the task-242 fallback lane stays byte-for-byte (model_fallback_test.go).
//
// The pins below cover lane 1 at both the unit (budget) and the wired-lane
// face, and lane 2's scheduling/budget/cancellation faces.

// Lane 1, unit face: twenty 429s in the same phase inside one window never
// refuse and never consume a slot; the same phase then still refuses the 9th
// 5xx — the storm breaker stays armed for non-queue kinds.
func TestRateLimitExemptFromCrossTurnWindow(t *testing.T) {
	b := NewRetryBudget()
	base := time.Unix(1_700_000_000, 0)
	for i := 0; i < 20; i++ {
		ok, kind, why := b.Allow("stream", errors.New("status 429 too many requests"), base.Add(time.Duration(i)*time.Second))
		if !ok || kind != KindRateLimit {
			t.Fatalf("429 attempt %d = (%v, %q, %s), want budget-exempt admission", i+1, ok, kind, why)
		}
	}
	if n := b.Count("stream", base.Add(time.Minute)); n != 0 {
		t.Fatalf("429 consumed %d budget slots, want zero", n)
	}
	for i := 0; i < retryBudgetLimit; i++ {
		if ok, _, _ := b.Allow("stream", errors.New("status 503"), base.Add(time.Duration(i)*time.Second)); !ok {
			t.Fatalf("5xx attempt %d refused, want the storm breaker intact", i+1)
		}
	}
	if ok, _, why := b.Allow("stream", errors.New("status 503"), base); ok {
		t.Fatalf("9th 5xx admitted (%q), want the window refusal", why)
	}
}

// Lane 1, wired face: a provider that answers nothing but 429 keeps queueing
// past the old cross-turn budget (8) — the loop ends with the wait-budget
// exhaustion (the lane's own bound), never with a budget refusal, and the
// retry strip carries the queue marker.
func Test429QueueSurvivesBeyondSharedBudgetWindow(t *testing.T) {
	p := &retryAfterProvider{after: time.Second}
	oldBudget, oldSleep := recoveryWaitBudget, recoverySleep
	defer func() { recoveryWaitBudget, recoverySleep = oldBudget, oldSleep }()
	// 12 minutes of queue room: at the 60s-per-round waiting pace this keeps
	// the lane running well past the pre-845 budget cap of 8 attempts (the
	// old lane died on attempt 9), which is the regression this pin guards.
	recoveryWaitBudget = 12 * time.Minute
	recoverySleep = func(ctx context.Context, d time.Duration) bool { return ctx.Err() == nil }
	sink := &recordSink{}
	a := New(p, tool.NewRegistry(), NewSession(""), Options{}, sink)
	err := a.Run(withNoClosedLoop(context.Background()), "go")
	var exhausted *provider.RecoveryWaitExhaustedError
	if !errors.As(err, &exhausted) {
		t.Fatalf("calls=%d err=%v", p.calls, err)
	}
	if p.calls < 12 {
		t.Fatalf("calls=%d, want the queue to run past the pre-845 budget cap of %d", p.calls, retryBudgetLimit)
	}
	queued := 0
	for _, e := range sink.kinds(event.Retrying) {
		if e.Recovery == nil {
			continue
		}
		if e.Recovery.Reason == "window budget exhausted" || e.Recovery.Reason == "terminal kind: "+string(KindTerminal4xx) {
			t.Fatalf("budget refusal leaked into the queue lane: %+v", e.Recovery)
		}
		if e.Recovery.Queue {
			queued++
			if !e.Recovery.Waiting || e.Recovery.Phase != "headers" {
				t.Fatalf("queue frame missing the waiting face: %+v", e.Recovery)
			}
		}
	}
	if queued == 0 {
		t.Fatal("no queue-marked retry frame emitted")
	}
}

// Lane 1, cap face: retryQueueMax bounds the queue rounds — after the four
// fast-lane attempts the configured number of queue rounds run, then the lane
// terminates with the wait-exhausted error.
func TestQueueCapTerminates(t *testing.T) {
	p := &retryAfterProvider{after: time.Second}
	oldSleep := recoverySleep
	defer func() { recoverySleep = oldSleep }()
	recoverySleep = func(ctx context.Context, d time.Duration) bool { return ctx.Err() == nil }
	a := New(p, tool.NewRegistry(), NewSession(""), Options{RetryQueueMax: 2}, event.Discard)
	err := a.Run(withNoClosedLoop(context.Background()), "go")
	var exhausted *provider.RecoveryWaitExhaustedError
	if !errors.As(err, &exhausted) || exhausted.Attempts != maxSamplingAttempts+2 || p.calls != maxSamplingAttempts+2 {
		t.Fatalf("calls=%d err=%v, want the queue to stop at the cap (2 rounds)", p.calls, err)
	}
}

// Lane 2, scheduling face: a provider-stated Retry-After on a quota body
// schedules the probe (it wins over the default interval), each probe
// re-hits the same request, and the strip carries the quota_wait marker.
func TestQuotaLongWaitHonorsRetryAfter(t *testing.T) {
	p := &quotaRetryAfterProvider{after: 90 * time.Second}
	oldSleep := recoverySleep
	defer func() { recoverySleep = oldSleep }()
	slept := []time.Duration{}
	recoverySleep = func(ctx context.Context, d time.Duration) bool {
		slept = append(slept, d)
		return ctx.Err() == nil
	}
	sink := &recordSink{}
	a := New(p, tool.NewRegistry(), NewSession(""), Options{
		QuotaWaitInterval: time.Second,
		QuotaWaitBudget:   4 * time.Minute,
	}, sink)
	err := a.Run(withNoClosedLoop(context.Background()), "go")
	if err == nil {
		t.Fatal("quota lane must end once its budget is gone")
	}
	if len(slept) == 0 || slept[0] != 90*time.Second {
		t.Fatalf("first probe delay = %v, want the provider's Retry-After", slept)
	}
	quotaFrames := 0
	for _, e := range sink.kinds(event.Retrying) {
		if e.Recovery != nil && e.Recovery.QuotaWait {
			quotaFrames++
			if e.Recovery.Phase != "quota" || e.Recovery.NextAttemptAt == 0 {
				t.Fatalf("quota frame malformed: %+v", e.Recovery)
			}
		}
	}
	if quotaFrames == 0 {
		t.Fatal("no quota_wait frame emitted")
	}
	if p.calls <= 1 {
		t.Fatalf("calls=%d, want the lane to probe past the first failure", p.calls)
	}
}

// Lane 2, GLM face: a Chinese quota body (no English marker, no typed
// QuotaError — the wire-level APIError carries the raw text) must reach the
// quota lane, not the 429 queue. The provider returns the plain APIError the
// GLM gateway emits; ClassifyRecovery routes it via the body markers.
func TestChineseQuotaBodyEntersLongWaitLane(t *testing.T) {
	p := &chineseQuotaProvider{}
	oldSleep := recoverySleep
	defer func() { recoverySleep = oldSleep }()
	recoverySleep = func(ctx context.Context, d time.Duration) bool { return ctx.Err() == nil }
	sink := &recordSink{}
	a := New(p, tool.NewRegistry(), NewSession(""), Options{
		QuotaWaitInterval: time.Millisecond,
		QuotaWaitBudget:   3 * time.Millisecond,
	}, sink)
	err := a.Run(withNoClosedLoop(context.Background()), "go")
	var exhausted *provider.RecoveryWaitExhaustedError
	if !errors.As(err, &exhausted) || exhausted.Phase != "quota" {
		t.Fatalf("err=%v, want the chinese quota body routed into the quota lane", err)
	}
	if p.calls <= 1 {
		t.Fatalf("calls=%d, want probes, not an immediate failure", p.calls)
	}
}

// Lane 2, cancellation face: the quota wait rides ctx — a cancelled context
// surfaces immediately instead of finishing the sleep. The Retry-After stays
// under the default quota budget so the lane actually enters its sleep.
func TestQuotaLongWaitCancelsPromptly(t *testing.T) {
	p := &quotaRetryAfterProvider{after: 30 * time.Second}
	oldSleep := recoverySleep
	defer func() { recoverySleep = oldSleep }()
	recoverySleep = sleepRecovery
	ctx, cancel := context.WithCancel(context.Background())
	sink := &cancelOnQuotaWaitSink{cancel: cancel}
	a := New(p, tool.NewRegistry(), NewSession(""), Options{}, sink)
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	started := time.Now()
	err := a.Run(withNoClosedLoop(ctx), "go")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want cancellation to surface through the quota wait", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("cancel during the hour-long quota wait took %s", elapsed)
	}
}

// quotaRetryAfterProvider answers every attempt with a quota-class error
// carrying a provider-stated Retry-After (the task-845 scheduling input).
type quotaRetryAfterProvider struct {
	calls int
	after time.Duration
}

func (p *quotaRetryAfterProvider) Name() string { return "quota-retry-after" }

func (p *quotaRetryAfterProvider) Stream(context.Context, provider.Request) (<-chan provider.Chunk, error) {
	p.calls++
	return nil, &provider.QuotaError{Status: 429, Code: "insufficient_quota", RetryAfter: p.after}
}

// chineseQuotaProvider returns the raw APIError shape a GLM gateway emits on
// an exhausted plan: HTTP 429, Chinese body, no English marker anywhere.
type chineseQuotaProvider struct {
	calls int
}

func (p *chineseQuotaProvider) Name() string { return "glm-cn" }

func (p *chineseQuotaProvider) Stream(context.Context, provider.Request) (<-chan provider.Chunk, error) {
	p.calls++
	return nil, &provider.APIError{
		Status: 429,
		Body:   `{"error":{"code":"1302","message":"额度已用完，请购买资源包后重试"}}`,
	}
}

// cancelOnQuotaWaitSink cancels the run on the first quota_wait frame —
// the UI's Stop button rides the same ctx the lane sleeps on.
type cancelOnQuotaWaitSink struct {
	recordSink
	cancel context.CancelFunc
}

func (s *cancelOnQuotaWaitSink) Emit(e event.Event) {
	s.recordSink.Emit(e)
	if e.Kind == event.Retrying && e.Recovery != nil && e.Recovery.QuotaWait {
		s.cancel()
	}
}

// Guard the queue-marker contract on the event surface: the JSON tag stays
// stable because the desktop strip reads it across the wire.
func TestQueueMarkerJSONStable(t *testing.T) {
	blob, err := json.Marshal(&event.RecoveryStatus{Queue: true, QuotaWait: true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(blob), `"queue":true`) || !strings.Contains(string(blob), `"quota_wait":true`) {
		t.Fatalf("wire tags drifted: %s", blob)
	}
}
