package control

import (
	"context"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/tool"

	"reasonix/internal/agent"
)

// Task 844 tests: the reversible ask's auto-decide window must react to panel
// hover (mousemove) — reset, not stop — and the resets must be bounded by an
// absolute budget so a signal the user can produce forever cannot defer the
// auto-decide indefinitely. The 806 acceptance trio (panel surfaces / touch
// stops the timer / silent expiry decides) keeps its own file
// (autopilot_ask_timeout_test.go) unchanged.

// askActivityQuestion is a minimal reversible ask batch (the risk classifier
// reads only the question text, and a formatting question classifies as
// reversible).
func askActivityQuestion() []event.AskQuestion {
	return []event.AskQuestion{{
		ID:      "q1",
		Prompt:  "Structure the findings as a table or as bullets?",
		Options: []event.AskOption{{Label: "table"}, {Label: "bullets"}},
	}}
}

// waitPanel emits until the reversible ask's panel is out and returns its
// prompt ID — the same readiness wait the 806 tests use.
func waitPanel(t *testing.T, sink *askTimeoutSink) string {
	t.Helper()
	id := ""
	for i := 0; i < 200 && id == ""; i++ {
		id = sink.lastAskID()
		if id == "" {
			time.Sleep(2 * time.Millisecond)
		}
	}
	if id == "" {
		t.Fatal("no AskRequest emitted; the reversible ask must surface the panel")
	}
	return id
}

// askActivityController builds an autopilot controller with the reversible
// tier armed at the given (short) wait and the task-477 sub-option off, so a
// budget exhaustion takes the task-806 decide-for-yourself path.
func askActivityController(t *testing.T, wait time.Duration) (*Controller, *askTimeoutSink) {
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

// 验收①: a mousemove on the panel resets the auto-decide window. With hover
// landing inside every window, the ask must still be waiting well past the
// first (and second) plain expiry point — the user is looking at the panel,
// so the run must not decide under their eyes.
func TestAutopilotReversibleAskActivityResetsWindow(t *testing.T) {
	const askWait = 80 * time.Millisecond
	c, sink := askActivityController(t, askWait)
	done := make(chan struct{})
	var answers []event.AskAnswer
	var err error
	go func() {
		answers, err = c.Ask(context.Background(), askActivityQuestion())
		close(done)
	}()
	askID := waitPanel(t, sink)

	// Hover once per window, starting inside the first one. The plain expiry
	// points are at ~80ms and ~160ms; the resets must push the actual decide
	// past 160ms (the absolute budget is askAutoDecideHoverFactor×80ms).
	hovers := []time.Duration{40 * time.Millisecond, 120 * time.Millisecond, 200 * time.Millisecond}
	start := time.Now()
	for _, at := range hovers {
		delay := at - time.Since(start)
		if delay > 0 {
			time.Sleep(delay)
		}
		select {
		case <-done:
			t.Fatalf("ask self-answered at %v, before the hover budget could force it; the window was not reset by the hover at ~%v", time.Since(start).Round(time.Millisecond), at)
		default:
		}
		c.NotifyAskActivity(askID)
	}

	// Just past the first two windows and still open: the reset is real.
	select {
	case <-done:
		t.Fatalf("ask returned at %v although hovers kept resetting the window (budget=%v)", time.Since(start).Round(time.Millisecond), askAutoDecideHoverFactor*askWait)
	case <-time.After(60 * time.Millisecond):
	}

	// The absolute budget (240ms) then forces the original expiry semantics
	// even though no further hover arrives — covered in detail by the budget
	// test; here just wait the ask out and check the decide-for-yourself
	// answer shape.
	<-done
	if err != nil {
		t.Fatalf("ask err = %v, want the auto-decide answers with no error", err)
	}
	if len(answers) != 1 || answers[0].QuestionID != "q1" || answers[0].Selected[0] != autopilotNoHumanAnswer {
		t.Fatalf("answers = %+v, want the decide-for-yourself answer for q1", answers)
	}
	if got := time.Since(start); got < 2*askWait {
		t.Fatalf("ask decided at %v, before the second window could expire; resets did not extend the wait", got.Round(time.Millisecond))
	}
}

// 验收③: the absolute hover budget ends the deferral. Continuous mouse
// movement resets every window, yet the ask must decide within the budget
// (askAutoDecideHoverFactor×askWait) plus slack — an unattended run may not be
// held hostage forever. Task 991: the budget derives from the constant, so
// raising the factor stretches both the hover loop and the bound together.
func TestAutopilotReversibleAskActivityBudgetExpires(t *testing.T) {
	const askWait = 50 * time.Millisecond
	budget := askAutoDecideHoverFactor * askWait
	c, sink := askActivityController(t, askWait)
	done := make(chan struct{})
	var answers []event.AskAnswer
	var err error
	go func() {
		answers, err = c.Ask(context.Background(), askActivityQuestion())
		close(done)
	}()
	askID := waitPanel(t, sink)

	// Hover far more often than the window, for longer than the budget: the
	// resets must stop mattering once the budget is gone.
	start := time.Now()
	stop := time.After(budget + 300*time.Millisecond)
hoverLoop:
	for {
		select {
		case <-stop:
			break hoverLoop
		case <-time.After(10 * time.Millisecond):
			c.NotifyAskActivity(askID)
		case <-done:
			break hoverLoop
		}
	}
	select {
	case <-done:
		elapsed := time.Since(start)
		// The decide may fire at the first window (50ms) only if no reset
		// landed; under continuous hover it must be deferred to ~budget. Keep
		// the lower bound loose (CI scheduling) but meaningful: at least two
		// windows past the first expiry.
		if elapsed < budget {
			t.Fatalf("ask decided at %v although hover kept resetting; want deferral to ~%v", elapsed.Round(time.Millisecond), budget)
		}
		if elapsed > budget+300*time.Millisecond {
			t.Fatalf("ask decided at %v, long past the %v budget; the deferral is not bounded", elapsed.Round(time.Millisecond), budget)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ask never returned although the hover budget must force the auto-decide")
	}
	if err != nil {
		t.Fatalf("ask err = %v, want the auto-decide answers with no error", err)
	}
	if len(answers) != 1 || answers[0].Selected[0] != autopilotNoHumanAnswer {
		t.Fatalf("answers = %+v, want the decide-for-yourself answer", answers)
	}
	// The decision trail survives the hover path: the task-544 marker plus the
	// same grep-able notice the plain expiry emits.
	if left := c.consumeTurnAskAnswered(); left == "" {
		t.Fatal("turn-ask-answered marker empty; the hover-budget decide must keep the decision trail")
	}
	notices := sink.notices()
	if len(notices) != 1 || notices[0].Code != "autopilot_ask_auto_decide" {
		t.Fatalf("notices = %+v, want exactly the autopilot_ask_auto_decide anchor", notices)
	}
}

// 验收② (with the 844 signal in the mix): hover resets the window, then a
// real selection stops the timer entirely — the ask keeps waiting for the
// user's own answer, and the auto-decide never fires.
func TestAutopilotReversibleAskActivityThenInteractionStopsTimer(t *testing.T) {
	const askWait = 60 * time.Millisecond
	c, sink := askActivityController(t, askWait)
	done := make(chan struct{})
	var answers []event.AskAnswer
	var err error
	go func() {
		answers, err = c.Ask(context.Background(), askActivityQuestion())
		close(done)
	}()
	askID := waitPanel(t, sink)

	// Hover first (reset), then touch (stop): the touch wins — an attended
	// wait for an attended user, unbounded like any 806 interaction.
	time.Sleep(20 * time.Millisecond)
	c.NotifyAskActivity(askID)
	time.Sleep(20 * time.Millisecond)
	c.NotifyAskInteraction(askID)

	// Long past budget (180ms) and several windows: nothing may decide.
	select {
	case <-done:
		t.Fatalf("ask returned answers=%+v (err=%v) after the interaction stopped the timer; hover or expiry took over", answers, err)
	case <-time.After(400 * time.Millisecond):
	}

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
			t.Fatal("auto-decide fired although the interaction stopped the timer")
		}
	}
}

// The hover signal must be inert outside the reversible-timed class: an
// attended ask with a plain registration consumes it as a no-op, and the ask
// still answers only from the user.
func TestAttendedAskIgnoresActivity(t *testing.T) {
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
	for i := 0; i < 20; i++ {
		c.NotifyAskActivity(askID)
	}
	select {
	case <-done:
		t.Fatal("attended ask returned from hover alone; hover must never answer")
	case <-time.After(100 * time.Millisecond):
	}
	// Settle through the user's own answer so the ask goroutine exits before
	// the goleak check reads the goroutine set.
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
