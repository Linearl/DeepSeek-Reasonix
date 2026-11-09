package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/evidence"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1514 面二钉: the in-running loop breaker. Same shape + zero new host
// evidence past the threshold → the turn interrupts with a visible
// loop_breaker cause; a steer tightens the window to ≤2 tool boundaries
// (验收④); any gain (command receipt / unique read / todo advance) resets the
// streak; poll-only rounds and the ≤0 kill switch never trip.

func shrinkLoopBreakerForTest(t *testing.T, threshold, steerThreshold int) {
	t.Helper()
	oldRound, oldSteer := loopBreakerRoundThreshold, loopBreakerSteerThreshold
	loopBreakerRoundThreshold, loopBreakerSteerThreshold = threshold, steerThreshold
	t.Cleanup(func() { loopBreakerRoundThreshold, loopBreakerSteerThreshold = oldRound, oldSteer })
}

// loopBreakerRepeatTool is a read-only stand-in for the field incident's
// repeated read call: it executes fine and returns the same output every time.
type loopBreakerRepeatTool struct{}

func (loopBreakerRepeatTool) Name() string        { return "read_file" }
func (loopBreakerRepeatTool) Description() string { return "test stand-in read" }
func (loopBreakerRepeatTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)
}
func (loopBreakerRepeatTool) ReadOnly() bool { return true }
func (loopBreakerRepeatTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "same bytes every time", nil
}

// loopBreakerCountingTool counts its own executions so the end-to-end钉 can
// pin exactly how many rounds ran before the breaker cut the turn.
type loopBreakerCountingTool struct{ calls int64 }

func (t *loopBreakerCountingTool) Name() string        { return "read_file" }
func (t *loopBreakerCountingTool) Description() string { return "test stand-in read" }
func (t *loopBreakerCountingTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)
}
func (t *loopBreakerCountingTool) ReadOnly() bool { return true }
func (t *loopBreakerCountingTool) Execute(context.Context, json.RawMessage) (string, error) {
	t.calls++
	return "same bytes every time", nil
}

func loopBreakerCalls() []provider.ToolCall {
	return []provider.ToolCall{{ID: "call-1", Name: "read_file", Arguments: `{"path":"confirm-image.txt"}`}}
}

func newLoopBreakerAgent(t *testing.T) *Agent {
	t.Helper()
	prov := testutil.NewMock("m", testutil.Turn{Text: "done"})
	reg := tool.NewRegistry()
	reg.Add(loopBreakerRepeatTool{})
	a := New(prov, reg, NewSession("sys"), Options{}, event.Discard)
	a.task.ledger = evidence.NewLedger()
	return a
}

// The core钉: same-shape zero-gain rounds trip at the threshold with a typed
// loop_breaker pause (the Run-level exit the controller maps to
// recovery_paused).
func TestLoopBreakerTripsAfterThresholdRounds(t *testing.T) {
	shrinkLoopBreakerForTest(t, 2, 2)
	a := newLoopBreakerAgent(t)
	state := &turnRuntime{loopBreakerSteerStep: -1}
	calls := loopBreakerCalls()

	if pause := a.loopBreakerCheck(context.Background(), state, 0, calls, 0); pause != nil {
		t.Fatalf("first same-shape round tripped early: %v", pause)
	}
	pause := a.loopBreakerCheck(context.Background(), state, 1, calls, 0)
	if pause == nil {
		t.Fatal("second same-shape zero-gain round did not trip the breaker")
	}
	if pause.StopReason != "loop_breaker" {
		t.Fatalf("stop reason = %q, want loop_breaker", pause.StopReason)
	}
	var rp *RecoveryPauseError
	if !errors.As(pause, &rp) || rp.Message == "" {
		t.Fatalf("pause must carry the user-facing message, got %#v", pause)
	}
	if !containsSubstring(pause.Detail, "cause=loop_breaker") {
		t.Fatalf("detail must carry the machine cause, got %q", pause.Detail)
	}
}

// 验收④: after a consumed steer the window tightens to ≤2 tool boundaries —
// the same shape persisting two rounds past the steer trips with the boost
// marker even though the general threshold never reached.
func TestLoopBreakerSteerBoostTripsWithinTwoBoundaries(t *testing.T) {
	shrinkLoopBreakerForTest(t, 50, 2) // general threshold far away
	a := newLoopBreakerAgent(t)
	// The steer was consumed at the gap before step 3.
	state := &turnRuntime{loopBreakerSteerStep: 3}
	calls := loopBreakerCalls()

	if pause := a.loopBreakerCheck(context.Background(), state, 3, calls, 0); pause != nil {
		t.Fatalf("first post-steer round tripped early: %v", pause)
	}
	pause := a.loopBreakerCheck(context.Background(), state, 4, calls, 0)
	if pause == nil {
		t.Fatal("the second same-shape round after a steer must trip the breaker (≤2 boundaries)")
	}
	if !containsSubstring(pause.Detail, "boost=steer_window") {
		t.Fatalf("boosted trip must name the steer window, got %q", pause.Detail)
	}
	// Outside the boost window the general threshold applies: a steer three
	// steps back no longer tightens the judgment.
	state2 := &turnRuntime{loopBreakerSteerStep: 0}
	if pause := a.loopBreakerCheck(context.Background(), state2, 4, calls, 0); pause != nil {
		t.Fatalf("round outside the steer window tripped early: %v", pause)
	}
}

// Any new host evidence resets the streak: a canonical todo advancing to
// completed between the rounds keeps the turn alive (this is what separates a
// loop from a long chain of real work).
func TestLoopBreakerGainResetsStreak(t *testing.T) {
	shrinkLoopBreakerForTest(t, 2, 2)
	a := newLoopBreakerAgent(t)
	state := &turnRuntime{loopBreakerSteerStep: -1}
	calls := loopBreakerCalls()

	if pause := a.loopBreakerCheck(context.Background(), state, 0, calls, 0); pause != nil {
		t.Fatalf("round 1 tripped early: %v", pause)
	}
	// Host-visible gain: a todo advanced to completed → no trip even at the
	// threshold, and the streak restarts.
	a.setTodoState([]evidence.TodoItem{{Content: "confirm the path", Status: "completed"}})
	if pause := a.loopBreakerCheck(context.Background(), state, 1, calls, 0); pause != nil {
		t.Fatalf("a round with fresh host evidence must not trip: %v", pause)
	}
	// Shaped differently → fresh streak too.
	other := []provider.ToolCall{{ID: "call-2", Name: "read_file", Arguments: `{"path":"other.txt"}`}}
	if pause := a.loopBreakerCheck(context.Background(), state, 2, other, 0); pause != nil {
		t.Fatalf("a changed call shape must reset the streak, tripped early: %v", pause)
	}
}

// Poll-only rounds (wait/bash_output/event_wait) are the legitimate
// same-shape face: a background job is the progress, so the breaker never
// judges them.
func TestLoopBreakerPollRoundsExempt(t *testing.T) {
	shrinkLoopBreakerForTest(t, 2, 2)
	a := newLoopBreakerAgent(t)
	state := &turnRuntime{loopBreakerSteerStep: -1}
	polls := []provider.ToolCall{{ID: "call-1", Name: "bash_output", Arguments: `{"job":"j1"}`}}
	for step := 0; step < 4; step++ {
		if pause := a.loopBreakerCheck(context.Background(), state, step, polls, 0); pause != nil {
			t.Fatalf("poll-only round %d must never trip, got %v", step, pause)
		}
	}
}

// Kill switch: threshold ≤0 disables the breaker entirely (the documented
// rollback face — set the var to 0 to get the pre-tl1514 behavior verbatim).
func TestLoopBreakerKillSwitch(t *testing.T) {
	shrinkLoopBreakerForTest(t, 0, 2)
	a := newLoopBreakerAgent(t)
	state := &turnRuntime{loopBreakerSteerStep: -1}
	calls := loopBreakerCalls()
	for step := 0; step < 6; step++ {
		if pause := a.loopBreakerCheck(context.Background(), state, step, calls, 0); pause != nil {
			t.Fatalf("disabled breaker must never trip, got %v", pause)
		}
	}
}

// The end-to-end Run钉 (任务书验收④的循环场景钉): a provider that answers every
// round with the SAME tool call drives the real loop into the breaker; Run
// returns the typed pause after exactly threshold rounds instead of spinning
// for the rest of the turn budget.
func TestRunLoopBreakerEndsRepeatingToolTurn(t *testing.T) {
	shrinkLoopBreakerForTest(t, 3, 2)
	turns := []testutil.Turn{
		{ToolCalls: loopBreakerCalls()},
		{ToolCalls: loopBreakerCalls()},
		{ToolCalls: loopBreakerCalls()},
		{ToolCalls: loopBreakerCalls()},
		{Text: "never reached"},
	}
	prov := testutil.NewMock("m", turns...)
	counting := &loopBreakerCountingTool{}
	reg := tool.NewRegistry()
	reg.Add(counting)
	a := New(prov, reg, NewSession("sys"), Options{}, event.Discard)
	a.task.ledger = evidence.NewLedger()

	err := a.Run(withNoClosedLoop(context.Background()), "confirm the image path and read it")
	var rp *RecoveryPauseError
	if !errors.As(err, &rp) {
		t.Fatalf("Run = %v, want a RecoveryPauseError from the loop breaker", err)
	}
	if rp.StopReason != "loop_breaker" {
		t.Fatalf("stop reason = %q, want loop_breaker", rp.StopReason)
	}
	// Rounds 0..2 executed (threshold 3 trips at the end of the third
	// same-shape round), so the tool ran exactly 3 times; the fourth tool turn
	// and the text tail never did.
	if got := counting.calls; got != 3 {
		t.Fatalf("tool executions = %d, want 3 (the breaker must stop the loop at the threshold)", got)
	}
}

func containsSubstring(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
