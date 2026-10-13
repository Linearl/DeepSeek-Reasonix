package control

import (
	"context"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
)

// Task 806b pins the explicit ask grant's priority semantics: a granted
// reversible ask waits attended (no auto-decide ever), clearing the grant
// restores the task-806 timed path byte-for-byte, the grant never reaches the
// high-risk tier, and the 806/844 panel signals stay harmless no-ops.

var explicitGrantReversibleQuestion = []event.AskQuestion{{
	ID:      "q1",
	Prompt:  "Structure the findings as a table or as bullets?",
	Options: []event.AskOption{{Label: "table"}, {Label: "bullets"}},
}}

// 验收①: with the grant set, a reversible ask under autopilot surfaces the
// panel and then waits like an attended one — well past the auto-decide wait
// nothing answers for the user, and the user's own answer is what comes back.
func TestExplicitAskGrantKeepsReversibleAskAttended(t *testing.T) {
	c, sink := askTimeoutController(t, true, 20*time.Millisecond)
	c.SetExplicitAskGranted(true)
	if !c.ExplicitAskGranted() {
		t.Fatal("grant not visible after SetExplicitAskGranted(true)")
	}
	type askResult struct {
		answers []event.AskAnswer
		err     error
	}
	done := make(chan askResult, 1)
	go func() {
		answers, err := c.Ask(context.Background(), explicitGrantReversibleQuestion)
		done <- askResult{answers, err}
	}()

	var askID string
	for i := 0; i < 100 && askID == ""; i++ {
		askID = sink.lastAskID()
		if askID == "" {
			time.Sleep(2 * time.Millisecond)
		}
	}
	if askID == "" {
		t.Fatal("no AskRequest emitted; the grant must not keep the panel from surfacing")
	}
	// Ten times the auto-decide wait: the grant must keep the ask waiting.
	select {
	case r := <-done:
		t.Fatalf("ask returned %+v (err=%v) although the explicit grant forbids auto-deciding", r.answers, r.err)
	case <-time.After(200 * time.Millisecond):
	}

	c.AnswerQuestion(askID, []event.AskAnswer{{QuestionID: "q1", Selected: []string{"table"}}})
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("ask err = %v, want the user's own answer", r.err)
		}
		if len(r.answers) != 1 || len(r.answers[0].Selected) != 1 || r.answers[0].Selected[0] != "table" {
			t.Fatalf("answers = %+v, want the user's selection", r.answers)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ask did not return after the user answered")
	}
	for _, n := range sink.notices() {
		if n.Code == "autopilot_ask_auto_decide" {
			t.Fatal("auto-decide notice emitted although the explicit ask grant was set")
		}
	}
}

// 验收②: clearing the grant restores the task-806 path verbatim — the
// reversible ask auto-decides after the wait with the unchanged decision
// trail. The zero value keeps the pre-806b behavior byte-for-byte.
func TestExplicitAskGrantClearedRestoresAutoDecide(t *testing.T) {
	c, sink := askTimeoutController(t, true, 20*time.Millisecond)
	c.SetExplicitAskGranted(true)
	c.SetExplicitAskGranted(false)
	if c.ExplicitAskGranted() {
		t.Fatal("grant still visible after SetExplicitAskGranted(false)")
	}
	answers, err := c.Ask(context.Background(), explicitGrantReversibleQuestion)
	if err != nil {
		t.Fatalf("reversible ask err = %v, want the auto-decide answers with no error", err)
	}
	if len(answers) != 1 || answers[0].QuestionID != "q1" || answers[0].Selected[0] != autopilotNoHumanAnswer {
		t.Fatalf("answers = %+v, want the decide-for-yourself answer (decision trail unchanged)", answers)
	}
	notices := sink.notices()
	if len(notices) != 1 || notices[0].Code != "autopilot_ask_auto_decide" {
		t.Fatalf("notices = %+v, want exactly the autopilot_ask_auto_decide anchor", notices)
	}
}

// 验收③: the grant never reaches the high-risk tier — a granted session's
// destructive/outward-facing ask still goes through the task-477 wait and is
// refused on expiry, because "ask me" cannot turn a high-risk question into
// one the run may answer itself.
func TestExplicitAskGrantDoesNotTouchHighRiskPath(t *testing.T) {
	c, sink := askTimeoutController(t, true, 20*time.Millisecond)
	c.SetExplicitAskGranted(true)
	answers, err := c.Ask(context.Background(), askTimeoutHighRiskQuestion)
	if err != nil {
		t.Fatalf("high-risk ask err = %v, want the refusal answers", err)
	}
	if len(answers) != 1 || answers[0].QuestionID != "q1" {
		t.Fatalf("answers = %+v, want one refusal for q1", answers)
	}
	if !strings.Contains(strings.Join(answers[0].Selected, " "), "refused, not approved") {
		t.Fatalf("refusal answer = %+v, want the declined-not-approved marker", answers[0])
	}
	notices := sink.notices()
	if len(notices) != 1 || notices[0].Code != "autopilot_ask_timeout" {
		t.Fatalf("notices = %+v, want exactly the autopilot_ask_timeout anchor", notices)
	}
}

// 验收④: the task-806 interaction and task-844 hover signals stay harmless
// no-ops on a granted ask — the plain registration carries no channels, so
// firing them must neither panic nor answer nor disturb the attended wait.
func TestExplicitAskGrantIgnoresInteractionAndHoverSignals(t *testing.T) {
	c, sink := askTimeoutController(t, true, 20*time.Millisecond)
	c.SetExplicitAskGranted(true)
	type askResult struct {
		answers []event.AskAnswer
		err     error
	}
	done := make(chan askResult, 1)
	go func() {
		answers, err := c.Ask(context.Background(), explicitGrantReversibleQuestion)
		done <- askResult{answers, err}
	}()

	var askID string
	for i := 0; i < 100 && askID == ""; i++ {
		askID = sink.lastAskID()
		if askID == "" {
			time.Sleep(2 * time.Millisecond)
		}
	}
	if askID == "" {
		t.Fatal("no AskRequest emitted for the granted reversible ask")
	}
	c.NotifyAskInteraction(askID)
	c.NotifyAskActivity(askID)
	select {
	case r := <-done:
		t.Fatalf("ask returned %+v (err=%v) from panel signals alone; a granted ask must wait for the user's answer", r.answers, r.err)
	case <-time.After(200 * time.Millisecond):
		// Well past the auto-decide wait: signals neither answered nor
		// disturbed the attended wait.
	}

	c.AnswerQuestion(askID, []event.AskAnswer{{QuestionID: "q1", Selected: []string{"bullets"}}})
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("ask err = %v, want the user's own answer", r.err)
		}
		if len(r.answers) != 1 || r.answers[0].Selected[0] != "bullets" {
			t.Fatalf("answers = %+v, want the user's selection", r.answers)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ask did not return after the user answered")
	}
	for _, n := range sink.notices() {
		if n.Code == "autopilot_ask_auto_decide" {
			t.Fatal("auto-decide notice emitted although the explicit ask grant was set")
		}
	}
}

// The accessor itself: default false (zero value = pre-806b behavior),
// settable both ways, and nil-safe on both entries.
func TestExplicitAskGrantAccessorRoundTrip(t *testing.T) {
	c, _ := askTimeoutController(t, true, time.Second)
	if c.ExplicitAskGranted() {
		t.Fatal("default grant = true, want the zero value to keep the pre-806b behavior")
	}
	c.SetExplicitAskGranted(true)
	if !c.ExplicitAskGranted() {
		t.Fatal("grant not visible after SetExplicitAskGranted(true)")
	}
	c.SetExplicitAskGranted(false)
	if c.ExplicitAskGranted() {
		t.Fatal("grant still visible after SetExplicitAskGranted(false)")
	}
	var nilCtl *Controller
	if nilCtl.ExplicitAskGranted() {
		t.Fatal("nil controller must read false")
	}
	nilCtl.SetExplicitAskGranted(true) // must not panic
}
