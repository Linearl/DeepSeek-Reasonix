package control

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/tool"
)

// askTimeoutSink captures the events an ask timeout emits. Kind-filtered
// helpers keep the pre-477 assertions intact; the raw event log is what the
// task-806 reversible tests read the emitted AskRequest (the surfaced panel)
// and its prompt ID from.
type askTimeoutSink struct {
	mu     sync.Mutex
	events []event.Event
}

func (s *askTimeoutSink) Emit(e event.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

func (s *askTimeoutSink) all() []event.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]event.Event(nil), s.events...)
}

func (s *askTimeoutSink) notices() []event.Event {
	var out []event.Event
	for _, e := range s.all() {
		if e.Kind == event.Notice {
			out = append(out, e)
		}
	}
	return out
}

// lastAskID returns the prompt ID of the most recent AskRequest, or "" while
// the panel has not been emitted yet.
func (s *askTimeoutSink) lastAskID() string {
	id := ""
	for _, e := range s.all() {
		if e.Kind == event.AskRequest {
			id = e.ItemID
		}
	}
	return id
}

// askTimeoutController builds an autopilot controller with the task-477
// sub-option in the requested state and a short high-risk ask wait.
func askTimeoutController(t *testing.T, enabled bool, wait time.Duration) (*Controller, *askTimeoutSink) {
	t.Helper()
	ag := agent.New(nil, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	sink := &askTimeoutSink{}
	c := New(Options{
		Runner:                     ag,
		Executor:                   ag,
		Sink:                       sink,
		Autopilot:                  true,
		AutopilotMaxRuntime:        time.Minute,
		AutopilotAskWait:           wait,
		AutopilotAskTimeoutEnabled: enabled,
	})
	return c, sink
}

var askTimeoutHighRiskQuestion = []event.AskQuestion{{
	ID:     "q1",
	Prompt: "Delete the release branch and force push the result, or keep it?",
}}

// Task 477: with the experimental ask-timeout sub-option on, an expired
// high-risk ask is ANSWERED with an explicit refusal — the run continues (the
// goal loop keeps driving it) instead of the task-109 B4 terminal stop. The
// refusal must never read as permission: the action is declined, not approved.
func TestAutopilotAskTimeoutRefusesAndContinues(t *testing.T) {
	c, sink := askTimeoutController(t, true, 20*time.Millisecond)
	started := time.Now()
	answers, err := c.Ask(context.Background(), askTimeoutHighRiskQuestion)
	if err != nil {
		t.Fatalf("ask err = %v, want the refusal answers with no error so the run continues", err)
	}
	if errors.Is(err, ErrAutopilotAskUnanswered) {
		t.Fatal("the sub-option on state must not surface the terminal stop")
	}
	if len(answers) != 1 || answers[0].QuestionID != "q1" {
		t.Fatalf("answers = %+v, want one refusal for q1", answers)
	}
	selection := strings.Join(answers[0].Selected, " ")
	if !strings.Contains(selection, "refused, not approved") {
		t.Fatalf("refusal answer = %q, want an explicit declined-not-approved marker", selection)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("ask took %s; the sub-option exists to stop the stall promptly", elapsed)
	}

	notices := sink.notices()
	if len(notices) != 1 {
		t.Fatalf("notices = %d, want exactly the timeout notice", len(notices))
	}
	if notices[0].Code != "autopilot_ask_timeout" {
		t.Fatalf("notice code = %q, want the grep-able autopilot_ask_timeout anchor", notices[0].Code)
	}
	if !strings.Contains(notices[0].Text, "20ms") {
		t.Fatalf("notice text = %q, want the actual configured wait", notices[0].Text)
	}
}

// The timeout fires one grep-able desktop/daemon log line — the other half of
// the task-477 判据锚 pair (notice + log line).
func TestAutopilotAskTimeoutLogsOneAnchorLine(t *testing.T) {
	c, _ := askTimeoutController(t, true, 20*time.Millisecond)
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(log.Writer())
	_, err := c.Ask(context.Background(), askTimeoutHighRiskQuestion)
	if err != nil {
		t.Fatalf("ask err = %v, want nil", err)
	}
	logged := buf.String()
	if !strings.Contains(logged, "[autopilot-ask]") || !strings.Contains(logged, "task 477") {
		t.Fatalf("log = %q, want the [autopilot-ask] task 477 anchor line", logged)
	}
	if strings.Count(logged, "[autopilot-ask]") != 1 {
		t.Fatalf("log = %q, want exactly one anchor line per timeout", logged)
	}
}

// Task 806 验收①②: a reversible ask under autopilot SURFACES THE PANEL (the
// task-49 A3 instant self-answer is gone) and only decides for the user after
// the interaction-aware wait expires untouched. The silent-expiry answer keeps
// the task-49 reply text and the task-544 decision trail (要素②), and carries
// one grep-able notice so the auto-decision is visible (the 806 P0 half).
func TestAutopilotReversibleAskSurfacesPanelThenTimesOut(t *testing.T) {
	c, sink := askTimeoutController(t, true, 30*time.Millisecond)
	reversible := []event.AskQuestion{{
		ID:      "q1",
		Prompt:  "Structure the findings as a table or as bullets?",
		Options: []event.AskOption{{Label: "table"}, {Label: "bullets"}},
	}}
	done := make(chan struct{})
	var answers []event.AskAnswer
	var err error
	go func() {
		answers, err = c.Ask(context.Background(), reversible)
		close(done)
	}()

	// The panel must come out long before the timer fires — that is the whole
	// point of the rework (验收①: no more 0s self-answer before the panel).
	var askID string
	for i := 0; i < 100 && askID == ""; i++ {
		askID = sink.lastAskID()
		if askID == "" {
			time.Sleep(2 * time.Millisecond)
		}
	}
	if askID == "" {
		t.Fatal("no AskRequest emitted; the reversible ask must surface the panel instead of self-answering")
	}
	select {
	case <-done:
		t.Fatalf("ask returned before any interaction could land; the 0s self-answer is back (answers=%v err=%v)", answers, err)
	case <-time.After(5 * time.Millisecond):
	}

	<-done
	if err != nil {
		t.Fatalf("reversible ask err = %v, want the auto-decide answers with no error", err)
	}
	if len(answers) != 1 || answers[0].QuestionID != "q1" || answers[0].Selected[0] != autopilotNoHumanAnswer {
		t.Fatalf("answers = %+v, want the decide-for-yourself answer for q1 (decision trail unchanged)", answers)
	}
	if left := c.consumeTurnAskAnswered(); left == "" {
		t.Fatal("turn-ask-answered marker empty; the task-544 decision_id trail must survive the rework")
	}
	notices := sink.notices()
	if len(notices) != 1 {
		t.Fatalf("notices = %d, want exactly the auto-decide notice", len(notices))
	}
	if notices[0].Code != "autopilot_ask_auto_decide" {
		t.Fatalf("notice code = %q, want the grep-able autopilot_ask_auto_decide anchor", notices[0].Code)
	}
}

// Task 806 验收②: the first interaction on the panel stops the auto-decide
// timer. After the touch the ask waits like any attended one — the run must
// not take the decision away while the user is mid-choice — and the user's
// own answer (not the auto-decide reply) is what comes back.
func TestAutopilotReversibleAskInteractionStopsTimer(t *testing.T) {
	c, sink := askTimeoutController(t, true, 60*time.Millisecond)
	reversible := []event.AskQuestion{{
		ID:      "q1",
		Prompt:  "Structure the findings as a table or as bullets?",
		Options: []event.AskOption{{Label: "table"}, {Label: "bullets"}},
	}}
	type askResult struct {
		answers []event.AskAnswer
		err     error
	}
	done := make(chan askResult, 1)
	go func() {
		answers, err := c.Ask(context.Background(), reversible)
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
		t.Fatal("no AskRequest emitted for the reversible ask")
	}
	// 要素①: one selection on the panel — delivered through the same
	// NotifyAskInteraction entry the desktop bridge calls — stops the timer.
	c.NotifyAskInteraction(askID)
	select {
	case r := <-done:
		t.Fatalf("ask returned %+v (err=%v) after the interaction; the timer was not stopped", r.answers, r.err)
	case <-time.After(200 * time.Millisecond):
		// Well past the 60ms timer: the touch really did stop it.
	}

	c.AnswerQuestion(askID, []event.AskAnswer{{QuestionID: "q1", Selected: []string{"table"}}})
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("ask err = %v, want the user's own answer", r.err)
		}
		if len(r.answers) != 1 || len(r.answers[0].Selected) != 1 || r.answers[0].Selected[0] != "table" {
			t.Fatalf("answers = %+v, want the user's selection to win over the auto-decide", r.answers)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ask did not return after the user answered")
	}
	for _, n := range sink.notices() {
		if n.Code == "autopilot_ask_auto_decide" {
			t.Fatal("auto-decide notice emitted although the user answered; the timer should have been stopped")
		}
	}
}

// Attended is untouched by the task-806 rework on the reversible tier too: a
// session that is not autopilot keeps waiting for the panel answer with no
// timer at all, exactly as before (验收⑤: the yolo/attended axis stays clean).
func TestAttendedReversibleAskKeepsWaiting(t *testing.T) {
	ag := agent.New(nil, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	c := New(Options{
		Runner:           ag,
		Executor:         ag,
		Sink:             event.Discard,
		AutopilotAskWait: 20 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := c.Ask(ctx, []event.AskQuestion{{
		ID:      "q1",
		Prompt:  "Structure the findings as a table or as bullets?",
		Options: []event.AskOption{{Label: "table"}, {Label: "bullets"}},
	}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("attended reversible ask err = %v, want it to keep waiting (no auto-decide timer)", err)
	}
}

// The wait resolution matrix, pinned without sleeping on timers: configured
// wins; the sub-option default is 15s; the off state keeps the 10-minute
// task-109 B4 default byte-for-byte.
func TestAutopilotAskWaitResolution(t *testing.T) {
	c, _ := askTimeoutController(t, true, 0)
	if got := c.autopilotAskWaitFor(); got != DefaultAutopilotAskTimeoutWait {
		t.Fatalf("on + unset wait = %s, want the 15s sub-option default", got)
	}
	c, _ = askTimeoutController(t, true, 42*time.Second)
	if got := c.autopilotAskWaitFor(); got != 42*time.Second {
		t.Fatalf("on + configured wait = %s, want the configured value", got)
	}
	c, _ = askTimeoutController(t, false, 0)
	if got := c.autopilotAskWaitFor(); got != DefaultAutopilotAskWait {
		t.Fatalf("off + unset wait = %s, want the 10m terminal-stop default", got)
	}
	c, _ = askTimeoutController(t, false, 42*time.Second)
	if got := c.autopilotAskWaitFor(); got != 42*time.Second {
		t.Fatalf("off + configured wait = %s, want the configured value", got)
	}
}

// Attended is untouched by the sub-option (task 469 user ruling: waiting for a
// human IS the attended design). Even with the flag flipped on, a session that
// is not autopilot keeps waiting until the caller's own deadline.
func TestAttendedAskIgnoresTimeoutSwitch(t *testing.T) {
	ag := agent.New(nil, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	c := New(Options{
		Runner:                     ag,
		Executor:                   ag,
		Sink:                       event.Discard,
		AutopilotAskTimeoutEnabled: true,
		AutopilotAskWait:           20 * time.Millisecond,
	})
	if c.autopilot {
		t.Fatal("controller reported itself unattended without the autopilot option")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := c.Ask(ctx, askTimeoutHighRiskQuestion)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("attended ask err = %v, want it to keep waiting (no timeout injected)", err)
	}
}

// The off state is the guaranteed baseline (铁律 2): same wait default, same
// terminal stop, no refusal answers — exercised here with a configured wait so
// both the switch-off and the switch-on shapes are pinned side by side.
func TestAutopilotAskTimeoutOffKeepsTerminalStop(t *testing.T) {
	c, sink := askTimeoutController(t, false, 20*time.Millisecond)
	answers, err := c.Ask(context.Background(), askTimeoutHighRiskQuestion)
	if !errors.Is(err, ErrAutopilotAskUnanswered) {
		t.Fatalf("off-state ask err = %v, want the task-109 B4 terminal stop", err)
	}
	if len(answers) != 0 {
		t.Fatalf("off-state answers = %+v, want none", answers)
	}
	for _, n := range sink.notices() {
		if n.Code == "autopilot_ask_timeout" {
			t.Fatal("off state must not carry the task-477 notice code")
		}
	}
}

// 无人值守 + 目标驱动一等组合态（task 477 新范式③）: the refusal answers come
// back as a normal answered ask, so nothing feeds goalPauseFromRunError — the
// goal loop keeps running where the off state would have parked it.
func TestAutopilotAskTimeoutDoesNotPauseTheGoal(t *testing.T) {
	c, _ := askTimeoutController(t, true, 20*time.Millisecond)
	answers, err := c.Ask(context.Background(), askTimeoutHighRiskQuestion)
	if err != nil {
		t.Fatalf("ask err = %v; a goal-driven run must not see an error here", err)
	}
	if len(answers) == 0 {
		t.Fatal("no refusal answers; the goal loop would see an unanswered ask")
	}
	if _, _, ok := goalPauseFromRunError(err); ok {
		t.Fatal("the on-state timeout must not map to a Goal pause")
	}
}
