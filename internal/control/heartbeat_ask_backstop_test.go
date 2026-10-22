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

// heartbeatAskController builds a plain attended controller (no autopilot
// gear — the heartbeat-session posture). The task-1062 backstop is a zero-wait
// immediate refusal (拍板变更 20261022), so no duration injection is needed.
func heartbeatAskController(t *testing.T) (*Controller, *askTimeoutSink) {
	t.Helper()
	ag := agent.New(nil, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	sink := &askTimeoutSink{}
	c := New(Options{
		Runner:   ag,
		Executor: ag,
		Sink:     sink,
	})
	return c, sink
}

var heartbeatHighRiskQuestion = []event.AskQuestion{{
	ID:     "q1",
	Prompt: "Push the release branch to the public remote now, or hold?",
}}

var heartbeatReversibleQuestion = []event.AskQuestion{{
	ID:      "q1",
	Prompt:  "Which retry library should this module use?",
	Options: []event.AskOption{{Label: ".retry"}, {Label: "hand-rolled"}},
}}

// Task 1062 acceptance (heartbeat ask backstop, 拍板变更: 立即拒绝): a
// scheduled run's ask is refused IMMEDIATELY — no wait, no panel emitted —
// with no error (the turn continues) and a searchable notice. The refusal
// names the sanctioned way out (talk_to_session / 板面汇报).
func TestHeartbeatAskBackstopRefusesImmediately(t *testing.T) {
	c, sink := heartbeatAskController(t)
	started := time.Now()
	answers, err := c.Ask(withHeartbeatTurn(context.Background()), heartbeatHighRiskQuestion)
	if err != nil {
		t.Fatalf("ask err = %v, want the refusal answers with no error so the scheduled turn continues", err)
	}
	if errors.Is(err, ErrAutopilotAskUnanswered) {
		t.Fatal("the heartbeat backstop must never surface the task-109 B4 terminal stop")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("refusal took %s, want an immediate zero-wait answer", elapsed)
	}
	if len(answers) != 1 || answers[0].QuestionID != "q1" {
		t.Fatalf("answers = %+v, want one refusal for q1", answers)
	}
	if len(answers[0].Selected) != 1 || !strings.Contains(answers[0].Selected[0], "talk_to_session") {
		t.Fatalf("refusal = %+v, want the sanctioned way out (talk_to_session) named", answers[0].Selected)
	}
	if !strings.Contains(answers[0].Selected[0], "refused immediately") {
		t.Fatalf("refusal = %q, want the zero-wait wording", answers[0].Selected[0])
	}
	// 零等待的另一半：面板零出现——AskRequest 不发，等待循环不进。
	for _, e := range sink.all() {
		if e.Kind == event.AskRequest {
			t.Fatal("a heartbeat ask must not surface an ask panel (立即拒绝，面板未弹出)")
		}
	}
	notices := sink.notices()
	found := false
	for _, n := range notices {
		if n.Code == "heartbeat_ask_timeout" {
			found = true
		}
	}
	if !found {
		t.Fatalf("notices = %+v, want one heartbeat_ask_timeout notice (可检索判据锚)", notices)
	}
}

// Both risk tiers take the backstop: a scheduled run must never hold a panel
// open, and the reversible auto-decide machinery (806/844/849) is
// autopilot-gated so a heartbeat reversible ask must not fall through it into
// the attended wait.
func TestHeartbeatAskBackstopCoversReversibleTier(t *testing.T) {
	c, _ := heartbeatAskController(t)
	answers, err := c.Ask(withHeartbeatTurn(context.Background()), heartbeatReversibleQuestion)
	if err != nil {
		t.Fatalf("ask err = %v, want refusal answers", err)
	}
	if len(answers) != 1 || len(answers[0].Selected) != 1 || !strings.Contains(answers[0].Selected[0], "heartbeat") {
		t.Fatalf("answers = %+v, want one heartbeat refusal", answers)
	}
}

// Precedence pinned by the ruling (拍板：仅挂死路径): an autopilot session
// woken by a guard run keeps its own 477/109 waits — the heartbeat marker must
// not refuse an ask the autopilot branches own. With 477 off this is the
// task-109 B4 terminal stop, byte-for-byte.
func TestHeartbeatMarkerDoesNotOverrideAutopilotWait(t *testing.T) {
	c, sink := askTimeoutController(t, false, 20*time.Millisecond)
	_, err := c.Ask(withHeartbeatTurn(context.Background()), heartbeatHighRiskQuestion)
	if !errors.Is(err, ErrAutopilotAskUnanswered) {
		t.Fatalf("err = %v, want the task-109 B4 terminal stop untouched by the heartbeat marker", err)
	}
	for _, n := range sink.notices() {
		if n.Code == "heartbeat_ask_timeout" {
			t.Fatal("the heartbeat notice must not fire on the autopilot branch")
		}
	}
}

// Attended zero-regression: without the marker, an ask on a plain controller
// waits attended (no timer) — the panel is answered by the user, no backstop
// fires, no refusal is fabricated.
func TestAttendedAskUnchangedWithoutHeartbeatMarker(t *testing.T) {
	c, sink := heartbeatAskController(t)
	done := make(chan struct{})
	var answers []event.AskAnswer
	var err error
	go func() {
		defer close(done)
		answers, err = c.Ask(context.Background(), heartbeatHighRiskQuestion)
	}()
	// Let the attended panel register, then answer as the user would.
	deadline := time.Now().Add(5 * time.Second)
	var askID string
	for askID == "" && time.Now().Before(deadline) {
		askID = sink.lastAskID()
		if askID == "" {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if askID == "" {
		t.Fatal("no pending ask panel; the attended ask never surfaced")
	}
	c.AnswerQuestion(askID, []event.AskAnswer{{QuestionID: "q1", Selected: []string{"hold"}}})
	<-done
	if err != nil {
		t.Fatalf("attended ask err = %v, want the user's answer", err)
	}
	if len(answers) != 1 || answers[0].Selected[0] != "hold" {
		t.Fatalf("answers = %+v, want the user's selection", answers)
	}
	for _, n := range sink.notices() {
		if n.Code == "heartbeat_ask_timeout" {
			t.Fatal("attended ask must not trigger the heartbeat backstop")
		}
	}
}

// Delegation strip: a heartbeat child's ask forwarded to its task source (task
// 225) must arrive WITHOUT the marker, so the parent decides by its own
// posture — the delegation hop stays the sanctioned escape hatch.
func TestHeartbeatAskDelegationStripsMarker(t *testing.T) {
	enableCascadeApproval(t)
	ag := agent.New(nil, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	sink := &askTimeoutSink{}
	child := New(Options{
		Runner:   ag,
		Executor: ag,
		Sink:     sink,
	})
	delegateCtxCh := make(chan context.Context, 1)
	var delegateAsker agent.Asker = delegateFunc(func(ctx context.Context, questions []event.AskQuestion) ([]event.AskAnswer, error) {
		delegateCtxCh <- ctx
		return []event.AskAnswer{{QuestionID: "q1", Selected: []string{"parent answered"}}}, nil
	})
	child.onCascadeDelegate = func(selfPath string) (delegate agent.Asker, source string, ok bool) {
		return delegateAsker, "parent-session", true
	}
	answers, err := child.Ask(withHeartbeatTurn(context.Background()), heartbeatHighRiskQuestion)
	if err != nil {
		t.Fatalf("ask err = %v, want the delegated answer", err)
	}
	if answers[0].Selected[0] != "parent answered" {
		t.Fatalf("answers = %+v, want the parent's answer", answers)
	}
	fwd := <-delegateCtxCh
	if heartbeatTurnRequested(fwd) {
		t.Fatal("the heartbeat marker leaked through the task-225 delegation hop")
	}
}

type delegateFunc func(ctx context.Context, questions []event.AskQuestion) ([]event.AskAnswer, error)

func (f delegateFunc) Ask(ctx context.Context, questions []event.AskQuestion) ([]event.AskAnswer, error) {
	return f(ctx, questions)
}

// Marker round-trip helpers: add-only stamping, explicit strip, nil ctx safe.
func TestHeartbeatTurnMarkerRoundTrip(t *testing.T) {
	ctx := context.Background()
	if heartbeatTurnRequested(ctx) {
		t.Fatal("plain ctx must not carry the marker")
	}
	marked := withHeartbeatTurn(ctx)
	if !heartbeatTurnRequested(marked) {
		t.Fatal("stamped ctx lost the marker")
	}
	if !heartbeatTurnRequested(withHeartbeatTurn(marked)) {
		t.Fatal("re-stamping must keep the marker (add-only)")
	}
	if heartbeatTurnRequested(withoutHeartbeatTurn(marked)) {
		t.Fatal("strip must remove the marker")
	}
	if heartbeatTurnRequested(nil) {
		t.Fatal("nil ctx must read as not-heartbeat")
	}
}
