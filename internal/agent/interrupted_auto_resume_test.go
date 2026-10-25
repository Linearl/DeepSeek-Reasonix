package agent

import (
	"context"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// tl 1105 返修 (2026-10-24): an outcome-unknown interruption that the agent's
// retry machinery already exhausted must still carry the stream-interrupt
// identity when it finally stops. The wait-lane wraps it in
// *provider.RecoveryWaitExhaustedError — errors.As sees through the Cause
// chain, so interruptionLike matches. But a plain terminal stream error (the
// non-wait path) returns the raw error unchanged, and the controller's gate
// (interrupted_auto_resume.go: interruptionLike) also matches
// provider.IsStreamInterrupted(err) directly, so both faces are covered.
//
// What was NOT covered: the run loop's err-from-streamWithSamplingRecovery
// return (run_loop.go: streamed.err from the ChunkError case) can also be a
// bare context.DeadlineExceeded when the run's own wall clock fired — and
// that shape carries NO StreamInterruptedError at all. The controller gate's
// errors.Is(turnErr, context.DeadlineExceeded) branch claims it; this test
// pins that a deadline-exceeded turn does not get misfiled as an ordinary
// provider failure by the notice layer (streamInterruptNotice must stay
// silent for it — the deadline is the run's own budget, not a broken stream).
func TestStreamInterruptNoticeStaysSilentForDeadlineExceeded(t *testing.T) {
	prov := &breakerScriptProvider{err: context.DeadlineExceeded}
	sink := &recordingSink{}
	a := newBreakerAgent(t, prov, sink)

	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err == nil {
		t.Fatal("expected the turn to fail on the deadline")
	}
	for _, code := range noticeCodes(sink) {
		if code == event.NoticeCodeStreamInterruptedPrematureEOF ||
			code == event.NoticeCodeStreamInterruptedConnectionReset ||
			code == event.NoticeCodeStreamInterruptedIdleTimeout ||
			code == event.NoticeCodeStreamInterruptedServerError {
			t.Fatalf("notice code %s emitted for a deadline-exceeded stop; the run's own budget is not a broken-stream notice", code)
		}
	}
}

// tl 1105 返修: the controller's interruptionLike gate reads through the
// errors.As chain, so an exhausted stream interruption that arrived wrapped
// in RecoveryWaitExhaustedError must still be recognized as a genuine
// interruption — not a plain terminal failure. This test pins the wrap
// preserves provider.IsStreamInterrupted(err) == true so the gate's
// interruptionLike sees it.
func TestExhaustedStreamInterruptKeepsInterruptIdentity(t *testing.T) {
	prov := &breakerScriptProvider{err: http2RSTError()}
	sink := &recordingSink{}
	a := newBreakerAgent(t, prov, sink)

	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err == nil {
		t.Fatal("expected the turn to fail at the attempt cap")
	}
	if !provider.IsStreamInterrupted(got.err) {
		t.Fatalf("terminal err %v lost the stream-interrupt identity; the tl-1105 gate (interruptionLike) will not claim it", got.err)
	}
}
