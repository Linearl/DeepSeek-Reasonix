package control

import (
	"context"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// 任务925（autopilot 零面板代答需可见提示）: the reversible ask's host
// auto-answer must leave (a) a live notice in the task's own format carrying
// the dec-* decision id, and (b) a durable receipt in the session's decision
// trail so reopening the session replays the auto-answer instead of showing no
// evidence a question was ever asked. The receipt is durable-only — the
// auto-decide notice stays the single live card — and the attended/human path
// (recordAskDecisionReceipt, "answered") is untouched.
func TestReversibleAutoDecideLeavesNoticeAndDurableReceipt(t *testing.T) {
	const askWait = 150 * time.Millisecond
	session := agent.NewSession("sys")
	session.Add(provider.Message{Role: provider.RoleAssistant, Content: "working on it"})
	ag := agent.New(nil, tool.NewRegistry(), session, agent.Options{}, event.Discard)
	sink := &askTimeoutSink{}
	c := New(Options{
		Runner:              ag,
		Executor:            ag,
		Sink:                sink,
		Autopilot:           true,
		AutopilotMaxRuntime: time.Minute,
		AutopilotAskWait:    askWait,
	})
	questions := askActivityQuestion()
	wantDecisionID := agent.DecisionIDForQuestions(questions)
	if !strings.HasPrefix(wantDecisionID, "dec-") {
		t.Fatalf("decision id = %q, want the dec- prefix the ask tool hands the model", wantDecisionID)
	}

	done := make(chan struct{})
	var answers []event.AskAnswer
	var err error
	go func() {
		answers, err = c.Ask(context.Background(), questions)
		close(done)
	}()
	askID := waitPanel(t, sink)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("ask never auto-decided")
	}
	if err != nil {
		t.Fatalf("ask err = %v, want the auto-decide answers with no error", err)
	}
	if len(answers) != 1 || answers[0].Selected[0] != autopilotNoHumanAnswer {
		t.Fatalf("answers = %+v, want the decide-for-yourself answer", answers)
	}

	// ① live notice: task-925 format with the answer and the dec-* id, under
	// the unchanged grep-able code anchor; and exactly one live card — the
	// receipt must not double-emit an event beside it.
	autoNotice := (*event.Event)(nil)
	for i, n := range sink.notices() {
		if n.Code == event.NoticeCodeDecisionReceipt {
			t.Fatalf("auto path emitted a second (receipt) notice; the auto-decide notice must stay the single live card: %+v", sink.notices())
		}
		if n.Code == "autopilot_ask_auto_decide" {
			if autoNotice != nil {
				t.Fatalf("duplicate auto-decide notices (second at index %d): %+v", i, sink.notices())
			}
			copy := n
			autoNotice = &copy
		}
	}
	if autoNotice == nil {
		t.Fatalf("notices = %+v, want the autopilot_ask_auto_decide anchor", sink.notices())
	}
	if !strings.Contains(autoNotice.Text, "本问题已自动回答") {
		t.Fatalf("auto-decide notice text = %q, want the 任务925 「本问题已自动回答」 format", autoNotice.Text)
	}
	if !strings.Contains(autoNotice.Text, wantDecisionID) {
		t.Fatalf("auto-decide notice text = %q, want the decision id %s for回查", autoNotice.Text, wantDecisionID)
	}
	if !strings.Contains(autoNotice.Text, "bullets") && !strings.Contains(autoNotice.Text, "table") {
		t.Fatalf("auto-decide notice text = %q, want the answer summary in the line", autoNotice.Text)
	}

	// ② durable receipt: attached to the session so reopening replays it as a
	// decision row (historyMessages → NoticeCodeDecisionReceipt), outcome
	// marking the host path, subject carrying the same dec-* id.
	messages := session.Snapshot()
	var receipt *provider.DecisionReceipt
	for i := range messages {
		for _, r := range messages[i].DecisionReceipts {
			if r.Kind == "ask" {
				receipt = r
			}
		}
	}
	if receipt == nil {
		t.Fatalf("no durable ask receipt in %d session messages; the auto-answer left no trace", len(messages))
	}
	if receipt.Outcome != "auto_answered" {
		t.Fatalf("receipt outcome = %q, want auto_answered (the user path keeps answered)", receipt.Outcome)
	}
	if receipt.ID != askID {
		t.Fatalf("receipt id = %q, want the prompt id %q", receipt.ID, askID)
	}
	if !strings.Contains(receipt.Subject, wantDecisionID) {
		t.Fatalf("receipt subject = %q, want the decision id %s", receipt.Subject, wantDecisionID)
	}
	if !strings.Contains(receipt.Subject, "table") {
		t.Fatalf("receipt subject = %q, want the answer summary", receipt.Subject)
	}

	// ③ the task-544 resume marker still stamps (decision trail unchanged).
	if c.consumeTurnAskAnswered() == "" {
		t.Fatal("turn-ask-answered marker empty; the auto-decide must keep the resume trail")
	}
}
