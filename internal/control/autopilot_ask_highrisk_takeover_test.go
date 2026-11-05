package control

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/tool"
)

// tl1408 tests: the autopilot high-risk ask's takeover button must actually
// work. The 20261105 incident: the panel surfaced a 15s countdown with a
// takeover button, the user clicked it, the backend answered false (the
// high-risk registration carried no takeover channel) and the button rolled
// back; the window then expired and auto-refused every pending question —
// the user had to stop the turn to stop the bleed. The ruling (20261105):
// clicking takeover means the user is present, so the countdown must suspend
// (an infinite wait, not a single extension); the 15s safety valve stays real
// only while nobody has taken over. The reversible-tier 849 tests
// (autopilot_ask_takeover_test.go) keep their own scenarios unchanged.

// 验收a: holding the takeover suspends the high-risk window outright — the
// ask must not expire at the original wait, nor at any multiple of it, and
// only the user's own answer ends the wait (no refusal notice, no terminal
// stop). Also pins 验收④'s backend half: the toggle must be consumed — the
// old contract returned false here and the panel rolled the button back.
func TestAutopilotHighRiskAskTakeoverSuspendsTimeout(t *testing.T) {
	const askWait = 50 * time.Millisecond
	c, sink := askTimeoutController(t, true, askWait)
	done := make(chan struct{})
	var answers []event.AskAnswer
	var err error
	go func() {
		answers, err = c.Ask(context.Background(), askTimeoutHighRiskQuestion)
		close(done)
	}()
	askID := waitPanel(t, sink)

	if !c.NotifyAskTakeover(askID, true) {
		t.Fatal("NotifyAskTakeover(hold) = false on a high-risk timed ask; the takeover channel must be armed (tl1408)")
	}

	// Hold far past the original window (6×): a single extension would have
	// fired by now; the suspension must keep the ask waiting.
	start := time.Now()
	select {
	case <-done:
		t.Fatalf("high-risk ask returned at %v although the takeover was held (wait=%v); the suspension is not real", time.Since(start).Round(time.Millisecond), askWait)
	case <-time.After(6 * askWait):
	}

	c.AnswerQuestion(askID, []event.AskAnswer{{QuestionID: "q1", Selected: []string{"keep"}}})
	select {
	case <-done:
		if err != nil {
			t.Fatalf("ask err = %v, want the user's own answer with no error", err)
		}
		if len(answers) != 1 || answers[0].Selected[0] != "keep" {
			t.Fatalf("answers = %+v, want the user's selection to win", answers)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ask did not return after the user answered")
	}
	for _, n := range sink.notices() {
		if n.Code == "autopilot_ask_timeout" {
			t.Fatal("autopilot_ask_timeout fired although the takeover was held; the parked window leaked")
		}
	}
}

// 验收b (regression, lives beside the takeover tests so a future takeover
// change cannot silently break the valve): with nobody taking over, the
// high-risk window still expires exactly as before — the task-477 refusal
// answers at the configured wait, unchanged decision trail.
func TestAutopilotHighRiskAskWithoutTakeoverStillTimesOut(t *testing.T) {
	const askWait = 40 * time.Millisecond
	c, sink := askTimeoutController(t, true, askWait)
	started := time.Now()
	answers, err := c.Ask(context.Background(), askTimeoutHighRiskQuestion)
	if err != nil {
		t.Fatalf("ask err = %v, want the refusal answers with no error", err)
	}
	if elapsed := time.Since(started); elapsed < askWait/2 {
		t.Fatalf("ask returned %v after registration, before the %v window could expire; the valve fired early", elapsed.Round(time.Millisecond), askWait)
	}
	if len(answers) != 1 || !strings.Contains(strings.Join(answers[0].Selected, " "), "refused, not approved") {
		t.Fatalf("answers = %+v, want the explicit declined-not-approved refusal", answers)
	}
	notices := sink.notices()
	if len(notices) != 1 || notices[0].Code != "autopilot_ask_timeout" {
		t.Fatalf("notices = %+v, want exactly the autopilot_ask_timeout anchor", notices)
	}
}

// 验收d: releasing the takeover re-arms a fresh window — the refusal then
// arrives at ~one window after the release (not instantly, not never).
func TestAutopilotHighRiskAskTakeoverReleaseRearmsWindow(t *testing.T) {
	const askWait = 60 * time.Millisecond
	c, sink := askTimeoutController(t, true, askWait)
	done := make(chan struct{})
	var answers []event.AskAnswer
	var err error
	go func() {
		answers, err = c.Ask(context.Background(), askTimeoutHighRiskQuestion)
		close(done)
	}()
	askID := waitPanel(t, sink)

	if !c.NotifyAskTakeover(askID, true) {
		t.Fatal("NotifyAskTakeover(hold) = false on a high-risk timed ask")
	}
	// Several windows under the hold: nothing may fire.
	time.Sleep(5 * askWait)
	select {
	case <-done:
		t.Fatal("high-risk ask expired although the takeover was held; the window did not park")
	default:
	}

	start := time.Now()
	if !c.NotifyAskTakeover(askID, false) {
		t.Fatal("NotifyAskTakeover(release) = false on a held high-risk ask")
	}
	select {
	case <-done:
		elapsed := time.Since(start)
		if elapsed < askWait/2 {
			t.Fatalf("refusal landed %v after the release, before the re-armed window (%v) could expire; the release decided instantly", elapsed.Round(time.Millisecond), askWait)
		}
		if elapsed > askWait+400*time.Millisecond {
			t.Fatalf("refusal landed %v after the release, long past the re-armed window %v; the release did not re-arm", elapsed.Round(time.Millisecond), askWait)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ask never refused after the takeover was released; the window did not re-arm")
	}
	if err != nil {
		t.Fatalf("ask err = %v, want the refusal answers with no error", err)
	}
	if len(answers) != 1 || !strings.Contains(strings.Join(answers[0].Selected, " "), "refused, not approved") {
		t.Fatalf("answers = %+v, want the explicit declined-not-approved refusal", answers)
	}
	notices := sink.notices()
	if len(notices) != 1 || notices[0].Code != "autopilot_ask_timeout" {
		t.Fatalf("notices = %+v, want exactly the autopilot_ask_timeout anchor", notices)
	}
}

// The off state keeps the task-109 B4 terminal stop after a takeover
// round-trip: hold, sit out several windows, release — the fresh window then
// ends in ErrAutopilotAskUnanswered exactly like an untouched window would.
func TestAutopilotHighRiskAskTakeoverReleaseKeepsTerminalStop(t *testing.T) {
	const askWait = 40 * time.Millisecond
	c, sink := askTimeoutController(t, false, askWait)
	done := make(chan struct{})
	var err error
	go func() {
		_, err = c.Ask(context.Background(), askTimeoutHighRiskQuestion)
		close(done)
	}()
	askID := waitPanel(t, sink)

	if !c.NotifyAskTakeover(askID, true) {
		t.Fatal("NotifyAskTakeover(hold) = false on a high-risk timed ask")
	}
	time.Sleep(4 * askWait)
	select {
	case <-done:
		t.Fatal("terminal stop fired although the takeover was held")
	default:
	}
	if !c.NotifyAskTakeover(askID, false) {
		t.Fatal("NotifyAskTakeover(release) = false on a held high-risk ask")
	}
	start := time.Now()
	select {
	case <-done:
		if !errors.Is(err, ErrAutopilotAskUnanswered) {
			t.Fatalf("off-state ask err = %v after the release, want the task-109 B4 terminal stop", err)
		}
		if elapsed := time.Since(start); elapsed > askWait+400*time.Millisecond {
			t.Fatalf("terminal stop landed %v after the release; the window did not re-arm", elapsed.Round(time.Millisecond))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ask never returned after the takeover was released")
	}
	for _, n := range sink.notices() {
		if n.Code == "autopilot_ask_timeout" {
			t.Fatal("off state must not carry the task-477 notice code")
		}
	}
}

// 验收c: the attended path is untouched — a high-risk ask outside autopilot
// has no window to park, so the toggle is rejected in both directions and the
// ask keeps waiting for the user like any attended prompt.
func TestAttendedHighRiskAskRejectsTakeover(t *testing.T) {
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
		_, err = c.Ask(context.Background(), askTimeoutHighRiskQuestion)
		close(done)
	}()
	askID := waitPanel(t, sink)
	if c.NotifyAskTakeover(askID, true) {
		t.Fatal("NotifyAskTakeover(hold) = true on an attended high-risk ask; no window exists to park")
	}
	if c.NotifyAskTakeover(askID, false) {
		t.Fatal("NotifyAskTakeover(release) = true on an attended high-risk ask")
	}
	select {
	case <-done:
		t.Fatal("attended high-risk ask returned from the toggle alone; the takeover must never answer")
	case <-time.After(150 * time.Millisecond):
	}
	c.AnswerQuestion(askID, []event.AskAnswer{{QuestionID: "q1", Selected: []string{"keep"}}})
	select {
	case <-done:
		if err != nil {
			t.Fatalf("ask err = %v, want a clean user answer", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ask did not return after the user answered")
	}
}

// Edge: a held takeover must survive a session reopen — the panel coming back
// on screen re-presents the ask but must not secretly restart the window the
// user believes is parked. Release afterwards still re-arms a fresh window.
func TestAutopilotHighRiskAskTakeoverHoldSurvivesReattach(t *testing.T) {
	const askWait = 40 * time.Millisecond
	c, sink := askTimeoutController(t, true, askWait)
	done := make(chan struct{})
	var answers []event.AskAnswer
	var err error
	go func() {
		answers, err = c.Ask(context.Background(), askTimeoutHighRiskQuestion)
		close(done)
	}()
	askID := waitPanel(t, sink)

	if !c.NotifyAskTakeover(askID, true) {
		t.Fatal("NotifyAskTakeover(hold) = false on a high-risk timed ask")
	}
	// Settle the hold first: sitting out several windows without a refusal
	// proves the loop has consumed the hold and the window is parked, so the
	// reopen below lands strictly after the hold (a racing re-present that
	// arrives before the hold is processed re-arms legitimately).
	time.Sleep(3 * askWait)
	select {
	case <-done:
		t.Fatal("high-risk ask expired although the takeover was held")
	default:
	}
	c.NotifyAskReattached(askID)
	time.Sleep(3 * askWait)
	select {
	case <-done:
		t.Fatalf("high-risk ask returned after a reopen under a held takeover; the re-present must not restart the parked window (answers=%v err=%v)", answers, err)
	default:
	}
	if !c.NotifyAskTakeover(askID, false) {
		t.Fatal("NotifyAskTakeover(release) = false on a held high-risk ask")
	}
	select {
	case <-done:
		if err != nil {
			t.Fatalf("ask err = %v, want the refusal answers with no error", err)
		}
		if len(answers) != 1 || !strings.Contains(strings.Join(answers[0].Selected, " "), "refused, not approved") {
			t.Fatalf("answers = %+v, want the refusal after the release", answers)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ask never refused after the takeover was released post-reopen")
	}
}
