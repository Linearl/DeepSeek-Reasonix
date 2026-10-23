package control

import (
	"context"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
)

// Task 1022 integration tests (four-dimensional input set, end to end through
// Ask): heartbeat source × autopilot × high-risk, undeclared source × any
// posture, healthy source × high-risk, and the cancel-notice contract.
// These run with the cascade switch armed in an isolated home so the local
// cascadeLive path is exercised exactly as production wires it.

func tl1022Fixture(t *testing.T, source *CascadeSource, autopilot bool) (*Controller, *cascadeStubAsker, *askProbeSink) {
	t.Helper()
	enableCascadeApproval(t)
	stub := &cascadeStubAsker{answers: []event.AskAnswer{{QuestionID: "q1", Selected: []string{"parent answered"}}}}
	sink := &askProbeSink{}
	opts := Options{Sink: sink, SessionDir: t.TempDir(), ApprovalTimeout: 200 * time.Millisecond, Autopilot: autopilot}
	c := New(opts)
	// New may wrap the sink (inbox coalescer); read the one Ask actually
	// emits through so the assertions see the real events.
	if probe, ok := c.sink.(*askProbeSink); ok {
		sink = probe
	}
	sink.ctrl = c
	c.onCascadeDelegate = func(string) (agent.Asker, string, bool) { return stub, "src-contact", true }
	c.onCascadeSourceCapability = func(string) *CascadeSource { return source }
	return c, stub, sink
}

func highRiskQuestion() []event.AskQuestion {
	return []event.AskQuestion{{
		ID: "q1", Prompt: "Push the release branch to the public remote now, or hold?",
		Options: []event.AskOption{{Label: "Push"}, {Label: "Hold"}},
	}}
}

// The tl 994 defect, end to end: a heartbeat/守护线 task source × autopilot ×
// high-risk ask must NOT burn the 10-minute cascade wait on a forwarded ask.
// The prompt stays local (the run's own high-risk timer governs), the delegate
// is never called, and no cascade-forwarded notice is emitted.
func TestTL1022HeartbeatSourceAutopilotHighRiskStaysLocal(t *testing.T) {
	source := &CascadeSource{Contact: "src-contact", Heartbeat: true, AskResponder: true, Liveness: true}
	c, stub, sink := tl1022Fixture(t, source, true)

	// A separate goroutine answers the local panel once it appears — the
	// shape of a user who catches the prompt inside the high-risk wait
	// window instead of letting it expire.
	go func() {
		for {
			if asks, _ := sink.counts(); asks > 0 {
				c.AnswerQuestion(sink.asks[0].ID, []event.AskAnswer{{QuestionID: "q1", Selected: []string{"Hold"}}})
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	answers, err := c.Ask(context.Background(), highRiskQuestion())
	if err != nil {
		t.Fatalf("local-panel ask after the heartbeat screen: %v", err)
	}
	if len(answers) != 1 || answers[0].Selected[0] != "Hold" {
		t.Fatalf("answers = %+v, want the local user's selection", answers)
	}
	if stub.calls.Load() != 0 {
		t.Fatalf("delegate calls = %d, want 0 — a heartbeat source is never a legal cascade target (tl 994)", stub.calls.Load())
	}
	if asks, _ := sink.counts(); asks == 0 {
		t.Fatal("the local ask panel must be emitted when the heartbeat source is screened out")
	}
}

// An undeclared source (no ask-responder capability) also fails closed local
// regardless of risk/grant posture — the tl 1022 capability screen.
func TestTL1022UndeclaredSourceFailsClosedLocal(t *testing.T) {
	source := &CascadeSource{Contact: "src-contact", AskResponder: false, Liveness: true}
	c, stub, _ := tl1022Fixture(t, source, true)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := c.Ask(ctx, highRiskQuestion()); err == nil {
		t.Fatal("high-risk ask under autopilot with no human must end with an error")
	}
	if stub.calls.Load() != 0 {
		t.Fatalf("delegate calls = %d, want 0 — undeclared source fails closed (tl 1022)", stub.calls.Load())
	}
}

// The healthy path (old cascade behavior, now explicitly gated): a declared,
// alive, human source takes the hop and returns the parent's answer.
func TestTL1022HealthySourceCascadesAndAnswers(t *testing.T) {
	source := &CascadeSource{Contact: "src-contact", AskResponder: true, Liveness: true}
	c, stub, _ := tl1022Fixture(t, source, true)
	answers, err := c.Ask(context.Background(), highRiskQuestion())
	if err != nil {
		t.Fatalf("delegated ask: %v", err)
	}
	if len(answers) != 1 || answers[0].Selected[0] != "parent answered" {
		t.Fatalf("answers = %+v, want the parent's answer", answers)
	}
	if stub.calls.Load() != 1 {
		t.Fatalf("delegate calls = %d, want 1", stub.calls.Load())
	}
}

// The 10-minute fallback cancel entry (验收③): the forwarded-ask notice must
// carry the cancel affordance and the deadline, so the wait is user-visible
// and cancellable rather than a silent hang.
func TestTL1022ForwardedAskCarriesCancelNotice(t *testing.T) {
	source := &CascadeSource{Contact: "src-contact", AskResponder: true, Liveness: true}
	c, _, sink := tl1022Fixture(t, source, true)
	if _, err := c.Ask(context.Background(), highRiskQuestion()); err != nil {
		t.Fatalf("delegated ask: %v", err)
	}
	notices := sink.snapshotNotices()
	found := false
	for _, n := range notices {
		if n.Kind == event.Notice && n.Code == "cascade_ask_forwarded" {
			if !strings.Contains(n.Text, "cancel") {
				t.Fatalf("cascade forwarded notice %q missing the cancel affordance", n.Text)
			}
			if !strings.Contains(n.Text, "task source src-contact") {
				t.Fatalf("cascade forwarded notice %q missing the source contact", n.Text)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no cascade_ask_forwarded notice emitted — the forwarded wait would be an invisible hang (验收③)")
	}
}
