package agent

// Task 172 feedback touchpoints: T1 (completion) + T2 (mid-turn steer) with
// the anti-loop gates. tl1350 adds the classification contract pins: the
// inbox submission only for a defect Reasonix itself can fix (criterion ①),
// reusable know-how routed to memory:remember with no inbox mention
// (criterion ②), and gate 4 skipping the touchpoint once remember already
// succeeded this turn. tl1574 turns the touchpoint from an invitation into a
// direct submission with a one-line receipt. Every test below also pins the
// default-off contract: an agent constructed without Options.FeedbackNudge
// must be byte-for-byte free of nudge messages and extra provider rounds.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// countNudgeMessages counts host-injected task-172 nudge messages. Only
// host-origin messages count: a user message that merely quotes the marker
// (the gate-3 replay shape) must not read as a nudge.
func countNudgeMessages(a *Agent) int {
	n := 0
	for _, m := range a.Session().Messages {
		if m.Origin == provider.MessageOriginHost && strings.Contains(m.Content, FeedbackNudgeMarker) {
			n++
		}
	}
	return n
}

// TestFeedbackNudgeOffByDefaultKeepsTurnUntouched pins 铁律 2: with the dial
// off (the zero value) a completed turn costs exactly one provider round and
// leaves no host nudge in the conversation.
func TestFeedbackNudgeOffByDefaultKeepsTurnUntouched(t *testing.T) {
	mp := testutil.NewMock("m", testutil.Turn{Text: "work done"})
	a := New(mp, tool.NewRegistry(), NewSession(""), Options{}, event.Discard)

	if err := a.Run(context.Background(), "do the thing"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := mp.CallCount(); got != 1 {
		t.Fatalf("provider calls = %d, want exactly 1 with the nudge off", got)
	}
	if got := countNudgeMessages(a); got != 0 {
		t.Fatalf("nudge messages = %d, want 0 with the nudge off", got)
	}
}

// TestFeedbackNudgeCompletionAsksOnce pins T1: with the dial on, a completed
// turn is followed by exactly one invitation round, and the invitation's own
// answer ends the turn without a second question (gate 1: per-turn cap).
func TestFeedbackNudgeCompletionAsksOnce(t *testing.T) {
	mp := testutil.NewMock("m",
		testutil.Turn{Text: "work done"},
		testutil.Turn{Text: "anything to report? the inbox is open"},
	)
	a := New(mp, tool.NewRegistry(), NewSession(""), Options{FeedbackNudge: true}, event.Discard)

	if err := a.Run(context.Background(), "do the thing"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := mp.CallCount(); got != 2 {
		t.Fatalf("provider calls = %d, want work round + one invitation round", got)
	}
	if got := countNudgeMessages(a); got != 1 {
		t.Fatalf("nudge messages = %d, want exactly 1 (no second question)", got)
	}
	for _, m := range a.Session().Messages {
		if strings.Contains(m.Content, FeedbackNudgeMarker) && m.Origin != provider.MessageOriginHost {
			t.Fatal("the nudge must be a host-generated message, never user intent")
		}
	}
}

// TestFeedbackNudgeTenTurnsNoSecondQuestion pins the acceptance contract: ten
// consecutive turns after the invitation produce no second question (gates 1+2:
// per-turn cap plus the shared ≥10-round cooldown).
func TestFeedbackNudgeTenTurnsNoSecondQuestion(t *testing.T) {
	turns := []testutil.Turn{
		{Text: "work done"},
		{Text: "invitation answer"},
	}
	for i := 0; i < 10; i++ {
		turns = append(turns, testutil.Turn{Text: "later turn answer"})
	}
	mp := testutil.NewMock("m", turns...)
	a := New(mp, tool.NewRegistry(), NewSession(""), Options{FeedbackNudge: true}, event.Discard)

	if err := a.Run(context.Background(), "turn 1"); err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	for i := 2; i <= 10; i++ {
		if err := a.Run(context.Background(), "turn"); err != nil {
			t.Fatalf("Run %d: %v", i, err)
		}
	}
	if got := countNudgeMessages(a); got != 1 {
		t.Fatalf("nudge messages after 10 consecutive turns = %d, want exactly 1 (no death loop)", got)
	}
}

// TestFeedbackNudgeMarkerInputSkipped pins gate 3: an input that itself
// carries the nudge marker (replayed history, quoted guidance) never seeds
// another nudge.
func TestFeedbackNudgeMarkerInputSkipped(t *testing.T) {
	mp := testutil.NewMock("m", testutil.Turn{Text: "ack"})
	a := New(mp, tool.NewRegistry(), NewSession(""), Options{FeedbackNudge: true}, event.Discard)

	if err := a.Run(context.Background(), FeedbackNudgeMarker+" replayed guidance"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := mp.CallCount(); got != 1 {
		t.Fatalf("provider calls = %d, want 1 (no invitation round)", got)
	}
	if got := countNudgeMessages(a); got != 0 {
		t.Fatalf("nudge messages = %d, want 0 for a marker-carrying input", got)
	}
}

// nudgeSteerTool queues a steer while the turn is running (T2 trigger shape).
type nudgeSteerTool struct {
	agent *Agent
	text  string
}

func (s *nudgeSteerTool) Name() string        { return "nudge_steer" }
func (s *nudgeSteerTool) Description() string { return "queues a mid-turn steer" }
func (s *nudgeSteerTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (s *nudgeSteerTool) ReadOnly() bool { return true }
func (s *nudgeSteerTool) Execute(context.Context, json.RawMessage) (string, error) {
	if !s.agent.Steer(s.text) {
		return "", context.Canceled
	}
	return "steered", nil
}

// TestFeedbackNudgeSteerInjectsT2Once pins T2: after a consumed mid-turn
// steer, one guidance note lands right in the correction round — and the
// completion of that same turn adds no T1 invitation (shared cooldown).
func TestFeedbackNudgeSteerInjectsT2Once(t *testing.T) {
	mp := testutil.NewMock("m",
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "nudge_steer", Arguments: `{}`}}},
		testutil.Turn{Text: "corrected answer"},
	)
	hijack := &nudgeSteerTool{text: "use plan B instead"}
	reg := tool.NewRegistry()
	reg.Add(hijack)
	a := New(mp, reg, NewSession(""), Options{FeedbackNudge: true}, event.Discard)
	hijack.agent = a

	if err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := mp.CallCount(); got != 2 {
		t.Fatalf("provider calls = %d, want tool round + corrected final (no T1 round)", got)
	}
	nudges := 0
	steerSeen := false
	for _, m := range a.Session().Messages {
		if strings.Contains(m.Content, FeedbackNudgeMarker) {
			nudges++
			if !strings.Contains(m.Content, "T2") {
				t.Fatal("the injected touchpoint should be the T2 steer note")
			}
		}
		if _, ok := SteerText(m.Content); ok {
			steerSeen = true
		}
	}
	if !steerSeen {
		t.Fatal("steer message missing; T2 hook ran without its trigger")
	}
	if nudges != 1 {
		t.Fatalf("nudge messages = %d, want exactly 1 (T2 once, no T1 stacking)", nudges)
	}
}

// TestFeedbackNudgeCopyClassifiesBeforeInviting pins the tl1350 copy contract
// on both touchpoints. Criterion ①: the inbox touchpoint exists only inside a
// bucket gated on "Reasonix itself can fix", with non-fixable roots (upstream
// API, prompts, user workflow) named as out-of-scope. Criterion ②: reusable
// know-how is routed to memory:remember with an explicit "do NOT mention the
// feedback inbox" instruction, while the submit_feedback channel itself
// survives for real fork-fixable defects.
func TestFeedbackNudgeCopyClassifiesBeforeInviting(t *testing.T) {
	for _, tc := range []struct {
		name string
		copy string
	}{
		{"T1 completion", feedbackNudgeCompletionGuidance()},
		{"T2 steer", feedbackNudgeSteerGuidance()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Case-normalized: phrases appear mid-sentence in T2.
			copy := strings.ToLower(tc.copy)
			for _, want := range []string{
				"reasonix itself can fix",           // criterion ①: the only invitation bucket
				"outside reasonix",                  // criterion ①: non-fixable roots named
				"memory:remember",                   // criterion ②: self-record guidance
				"do not mention the feedback inbox", // criterion ②: no invite for know-how
				"submit_feedback",                   // invitation channel kept for fork-fixable defects
			} {
				if !strings.Contains(copy, want) {
					t.Fatalf("%s copy missing %q:\n%s", tc.name, want, tc.copy)
				}
			}
		})
	}
}

// TestFeedbackNudgeCopySubmitsDirectlyWithReceipt pins the tl1574 receipt
// contract on both touchpoints: the invitation era is gone. Each guidance
// must (a) tell the model to submit without asking permission first,
// (b) keep the dedup check inside the flow, (c) prescribe the one-line
// receipt with its escalation clause (要立项说一声 — say the word and the
// task starts on the spot), and (d) name the submit path explicitly.
func TestFeedbackNudgeCopySubmitsDirectlyWithReceipt(t *testing.T) {
	for _, tc := range []struct {
		name string
		copy string
	}{
		{"T1 completion", feedbackNudgeCompletionGuidance()},
		{"T2 steer", feedbackNudgeSteerGuidance()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Case-normalized: phrases appear mid-sentence in T2.
			copy := strings.ToLower(tc.copy)
			for _, want := range []string{
				"do not ask permission", // (a) direct submit, no invite question first
				"submit it yourself",    // (d) the model calls the tool, not the user
				"for duplicates",        // (b) dedup stays part of the flow
				"receipt line",          // (c) the one-line receipt shape
				"已投意见箱",                 // (c) receipt prefix
				"要立项说一声",                // (c) escalation clause: on-the-spot task start
			} {
				if !strings.Contains(copy, want) {
					t.Fatalf("%s copy missing %q:\n%s", tc.name, want, tc.copy)
				}
			}
		})
	}
}

// via the remember tool mid-turn.
type rememberStubTool struct{}

func (r *rememberStubTool) Name() string        { return tool.HostRemember }
func (r *rememberStubTool) Description() string { return "records a lesson" }
func (r *rememberStubTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (r *rememberStubTool) ReadOnly() bool { return true }
func (r *rememberStubTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "remembered", nil
}

// TestFeedbackNudgeSkippedWhenTurnAlreadyRemembered pins gate 4 (the tl1350
// optional filter): a turn whose ledger already holds a successful remember
// receipt ends without any T1 invitation round — the knowledge is recorded,
// so the inbox nudge would only duplicate it.
func TestFeedbackNudgeSkippedWhenTurnAlreadyRemembered(t *testing.T) {
	mp := testutil.NewMock("m",
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "r1", Name: "remember", Arguments: `{}`}}},
		testutil.Turn{Text: "lesson recorded"},
	)
	reg := tool.NewRegistry()
	reg.Add(&rememberStubTool{})
	a := New(mp, reg, NewSession(""), Options{FeedbackNudge: true}, event.Discard)

	if err := a.Run(context.Background(), "record the lesson"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !a.task.ledger.HasSuccessfulToolReceipt(tool.HostRemember) {
		t.Fatal("test setup: remember receipt did not land; gate 4 was not exercised")
	}
	if got := mp.CallCount(); got != 2 {
		t.Fatalf("provider calls = %d, want tool round + final (no invitation round)", got)
	}
	if got := countNudgeMessages(a); got != 0 {
		t.Fatalf("nudge messages = %d, want 0 after a successful remember this turn", got)
	}
}
