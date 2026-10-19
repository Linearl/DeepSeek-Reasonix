package control

import (
	"context"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/tool"

	"reasonix/internal/agent"
)

// Task 849 tests: the ask panel's takeover button is the explicit "the user is
// here" declaration — stronger than the task-844 hover, and unlike it allowed
// to last forever. Holding the takeover must park the auto-decide timer AND
// the absolute hover budget (a user who needs more time to think is not forced
// to keep moving the mouse); releasing it must resume the countdown with a
// fresh window and a fresh budget. The 806/844 acceptance files
// (autopilot_ask_timeout_test.go / autopilot_ask_activity_test.go) keep their
// own scenarios unchanged.

// askTakeoverController builds an autopilot controller with the reversible
// tier armed at the given (short) wait and the task-477 sub-option off, so an
// expiry after the release takes the task-806 decide-for-yourself path — the
// same builder the 844 tests use.
func askTakeoverController(t *testing.T, wait time.Duration) (*Controller, *askTimeoutSink) {
	t.Helper()
	ag := agent.New(nil, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	sink := &askTimeoutSink{}
	c := New(Options{
		Runner:              ag,
		Executor:            ag,
		Sink:                sink,
		Autopilot:           true,
		AutopilotMaxRuntime: time.Minute,
		AutopilotAskWait:    wait,
	})
	return c, sink
}

// 验收②: holding the takeover parks the auto-decide entirely — timer and
// absolute hover budget suspended — so the ask waits far past the point where
// any unattended semantics (plain expiry at askWait, budget at
// askAutoDecideHoverFactor×askWait, continuous hover at any rate) would have
// decided. Only the user's own answer ends it, and the auto-decide anchor
// never fires.
func TestAutopilotReversibleAskTakeoverStopsTimerAndSuspendsBudget(t *testing.T) {
	const askWait = 50 * time.Millisecond
	budget := askAutoDecideHoverFactor * askWait
	c, sink := askTakeoverController(t, askWait)
	done := make(chan struct{})
	var answers []event.AskAnswer
	var err error
	go func() {
		answers, err = c.Ask(context.Background(), askActivityQuestion())
		close(done)
	}()
	askID := waitPanel(t, sink)

	if !c.NotifyAskTakeover(askID, true) {
		t.Fatal("NotifyAskTakeover(hold) = false on a reversible timed ask; the takeover channel must be armed")
	}

	// Hold far past every unattended deadline (5× budget) while hovering
	// continuously: neither the parked timers nor the hover may decide. The
	// multiplier is 5, not the old 20, because task 991 raised the factor to
	// 10 — 5× that budget is already far past both deadlines without making
	// the test ten seconds long.
	start := time.Now()
	stop := time.After(5 * budget)
hoverLoop:
	for {
		select {
		case <-stop:
			break hoverLoop
		case <-time.After(5 * time.Millisecond):
			c.NotifyAskActivity(askID)
		case <-done:
			t.Fatalf("ask self-answered at %v although the takeover was held (wait=%v budget=%v); the park is not real", time.Since(start).Round(time.Millisecond), askWait, budget)
		}
	}
	select {
	case <-done:
		t.Fatalf("ask returned at %v right after the hover loop; the takeover must keep the ask waiting", time.Since(start).Round(time.Millisecond))
	case <-time.After(100 * time.Millisecond):
	}

	// The user's own answer ends the held wait, never a decide-for-you.
	c.AnswerQuestion(askID, []event.AskAnswer{{QuestionID: "q1", Selected: []string{"table"}}})
	select {
	case <-done:
		if err != nil {
			t.Fatalf("ask err = %v, want the user's own answer", err)
		}
		if len(answers) != 1 || answers[0].Selected[0] != "table" {
			t.Fatalf("answers = %+v, want the user's selection to win", answers)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ask did not return after the user answered")
	}
	for _, n := range sink.notices() {
		if n.Code == "autopilot_ask_auto_decide" {
			t.Fatal("auto-decide fired although the takeover was held; the parked timer or budget leaked")
		}
	}
}

// 验收③: releasing the takeover resumes the countdown — the ask must then
// decide at the fresh window's expiry with the unchanged task-806 decision
// trail. Also pins that hover spent under the hold is not charged against the
// re-armed budget: the release restarts from a clean arming posture.
func TestAutopilotReversibleAskTakeoverReleaseResumesWindow(t *testing.T) {
	const askWait = 80 * time.Millisecond
	c, sink := askTakeoverController(t, askWait)
	done := make(chan struct{})
	var answers []event.AskAnswer
	var err error
	go func() {
		answers, err = c.Ask(context.Background(), askActivityQuestion())
		close(done)
	}()
	askID := waitPanel(t, sink)

	if !c.NotifyAskTakeover(askID, true) {
		t.Fatal("NotifyAskTakeover(hold) = false on a reversible timed ask")
	}
	// Sit under the hold for several windows: nothing may fire.
	time.Sleep(6 * askWait)
	select {
	case <-done:
		t.Fatal("ask self-answered although the takeover was held; the window did not park")
	default:
	}
	// Hover under the hold: swallowed, and must not pre-spend the new budget.
	c.NotifyAskActivity(askID)

	start := time.Now()
	if !c.NotifyAskTakeover(askID, false) {
		t.Fatal("NotifyAskTakeover(release) = false on a held reversible timed ask")
	}
	// No further hover: the fresh window expires on its own and the original
	// 806 semantics decide for the user.
	select {
	case <-done:
		elapsed := time.Since(start)
		if elapsed < askWait/2 {
			t.Fatalf("ask decided %v after the release, before the re-armed window (%v) could expire; the release decided instantly", elapsed.Round(time.Millisecond), askWait)
		}
		if elapsed > askWait+300*time.Millisecond {
			t.Fatalf("ask decided %v after the release, long past the re-armed window %v; the release did not resume the countdown", elapsed.Round(time.Millisecond), askWait)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ask never returned after the takeover was released; the countdown did not resume")
	}
	if err != nil {
		t.Fatalf("ask err = %v, want the auto-decide answers with no error", err)
	}
	if len(answers) != 1 || answers[0].Selected[0] != autopilotNoHumanAnswer {
		t.Fatalf("answers = %+v, want the decide-for-yourself answer", answers)
	}
	// The decision trail survives the takeover round-trip: the task-544 marker
	// plus the same grep-able notice the plain expiry emits.
	if c.consumeTurnAskAnswered() == "" {
		t.Fatal("turn-ask-answered marker empty; the post-release decide must keep the decision trail")
	}
	notices := sink.notices()
	if len(notices) != 1 || notices[0].Code != "autopilot_ask_auto_decide" {
		t.Fatalf("notices = %+v, want exactly the autopilot_ask_auto_decide anchor", notices)
	}
}

// The toggle is inert outside the reversible-timed class: an attended ask
// reports false for both directions and keeps answering only from the user —
// the 109 B4 safety valve stays real on prompts without an auto-decide.
func TestAttendedAskRejectsTakeover(t *testing.T) {
	ag := agent.New(nil, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	sink := &askTimeoutSink{}
	c := New(Options{
		Runner:              ag,
		Executor:            ag,
		Sink:                sink,
		AutopilotMaxRuntime: time.Minute,
	})
	done := make(chan struct{})
	var err error
	go func() {
		_, err = c.Ask(context.Background(), askActivityQuestion())
		close(done)
	}()
	askID := waitPanel(t, sink)
	if c.NotifyAskTakeover(askID, true) {
		t.Fatal("NotifyAskTakeover(hold) = true on an attended ask; attended asks have no auto-decide to park")
	}
	if c.NotifyAskTakeover(askID, false) {
		t.Fatal("NotifyAskTakeover(release) = true on an attended ask")
	}
	select {
	case <-done:
		t.Fatal("attended ask returned from the toggle alone; the takeover must never answer")
	case <-time.After(100 * time.Millisecond):
	}
	c.AnswerQuestion(askID, []event.AskAnswer{{QuestionID: "q1", Selected: []string{"table"}}})
	select {
	case <-done:
		if err != nil {
			t.Fatalf("ask err = %v, want a clean user answer", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ask did not return after the user answered")
	}
}

// Rapid toggling collapses to the last click: hold then release with no
// consumer in between must land the release, not lose it behind the hold —
// the panel button is a toggle, so double-clicks are an expected gesture.
func TestAutopilotReversibleAskTakeoverRapidToggleLandsLastClick(t *testing.T) {
	const askWait = 60 * time.Millisecond
	c, sink := askTakeoverController(t, askWait)
	done := make(chan struct{})
	go func() {
		_, _ = c.Ask(context.Background(), askActivityQuestion())
		close(done)
	}()
	askID := waitPanel(t, sink)

	// Hold+release back-to-back must end released: the ask then behaves as if
	// never taken over, i.e. the fresh window decides at ~askWait. Both
	// signals race the Ask loop's first select, so the assertion is on the
	// observable outcome (decides at all, promptly), not on internal timing.
	if !c.NotifyAskTakeover(askID, true) || !c.NotifyAskTakeover(askID, false) {
		t.Fatal("rapid toggle dropped a signal on a reversible timed ask")
	}
	start := time.Now()
	select {
	case <-done:
		if elapsed := time.Since(start); elapsed > askWait+500*time.Millisecond {
			t.Fatalf("ask decided %v after a hold+release toggle; the release must resume the window (%v)", elapsed.Round(time.Millisecond), askWait)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ask never returned after hold+release; the last click was lost")
	}
}
