package control

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/skill"
	"reasonix/internal/tool"
)

// historyBackedController wires a real agent (scripted answer turn) as both
// runner and executor so the user turn — including its RawContent — lands in
// the session snapshot that c.History() exposes.
func historyBackedController(t *testing.T, skills []skill.Skill) *Controller {
	t.Helper()
	prov := &scriptedTurns{turns: [][]provider.Chunk{textTurn("answer")}}
	ag := agent.New(prov, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	events := make(chan event.Event, 24)
	c := New(Options{
		Runner:      ag,
		Executor:    ag,
		Sink:        event.FuncSink(func(e event.Event) { events <- e }),
		Skills:      skills,
		SessionPath: filepath.Join(t.TempDir(), "session.jsonl"),
	})
	t.Cleanup(c.Close)
	return c
}

// firstUserAuthoredTurn returns the persisted user turn message, whose
// RawContent is the user's exact text and whose Content is the composed
// provider input.
func firstUserAuthoredTurn(t *testing.T, c *Controller) provider.Message {
	t.Helper()
	for _, msg := range c.History() {
		if msg.Role == provider.RoleUser && agent.IsUserAuthoredTurnMessage(msg) {
			return msg
		}
	}
	t.Fatalf("no user-authored turn message in history: %+v", c.History())
	return provider.Message{}
}

// Task 402 (upstream #11145/#11149): composer chips are the mid-message skill
// invocation channel — a chip at any offset must send the same pinned product
// as the run_skill tool and the typed "/name args" path (task 395), while the
// surrounding text stays the user's raw message verbatim.
func TestSubmitInvocationDisplayMidTextSkillSendsPinnedAndKeepsRawText(t *testing.T) {
	c := historyBackedController(t, []skill.Skill{
		{Name: "format", Body: "FORMAT_RULE", RunAs: skill.RunInline, Scope: skill.ScopeGlobal},
	})

	// Chip sits mid-text: "先看这里" + chip + "再看这里".
	input := "先看这里 /format 再看这里"
	c.SubmitInvocationDisplay(input, input, []InvocationRequest{
		{Name: "format", Kind: "skill", Offset: len("先看这里 ")},
	})
	waitIdle(t, c)

	userTurn := firstUserAuthoredTurn(t, c)

	// The composed provider input carries the pinned skill product followed by
	// the user's surrounding text.
	composed := userTurn.Content
	if !strings.Contains(composed, `<skill-pin name="format">`) || !strings.Contains(composed, "</skill-pin>") {
		t.Fatalf("mid-text chip input missing pinned wrapper: %q", composed)
	}
	if !strings.Contains(composed, "FORMAT_RULE") {
		t.Fatalf("mid-text chip input missing skill body: %q", composed)
	}
	if !strings.Contains(composed, "先看这里") || !strings.Contains(composed, "再看这里") {
		t.Fatalf("mid-text chip input lost surrounding text: %q", composed)
	}

	// The transcript's raw user message stays the user's exact composer text —
	// the rendered skill body must not consume it.
	if userTurn.RawContent != input {
		t.Fatalf("raw content = %q, want user's original %q", userTurn.RawContent, input)
	}
	if strings.Contains(userTurn.RawContent, "FORMAT_RULE") || strings.Contains(userTurn.RawContent, "skill-pin") {
		t.Fatalf("raw content consumed the rendered skill body: %q", userTurn.RawContent)
	}
}

// Regression lock for the pre-existing subagent contract on the structured
// path: a taskless subagent chip never starts a turn.
func TestSubmitInvocationDisplaySubagentWithoutTaskStillRejected(t *testing.T) {
	events := make(chan event.Event, 2)
	prov := &scriptedTurns{turns: [][]provider.Chunk{textTurn("answer")}}
	ag := agent.New(prov, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	c := New(Options{
		Runner:   ag,
		Executor: ag,
		Sink:     event.FuncSink(func(e event.Event) { events <- e }),
		Skills: []skill.Skill{
			{Name: "helper", RunAs: skill.RunSubagent, Scope: skill.ScopeGlobal},
		},
	})
	c.SubmitInvocationDisplay("", "", []InvocationRequest{{Name: "helper", Kind: "subagent"}})
	select {
	case e := <-events:
		if e.Kind != event.Notice || !strings.Contains(e.Text, "subagent invocation requires a task") {
			t.Fatalf("event = %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("missing task-required notice")
	}
	if c.Running() || len(c.History()) > 1 {
		t.Fatalf("taskless subagent chip started a turn: running=%v history=%d", c.Running(), len(c.History()))
	}
}

// Regression lock for task 395's typed line-start path: "/name args" still
// sends the pinned product with the args attached and the raw typed line.
func TestSubmitSlashInlineSkillStillSendsPinnedWithArgs(t *testing.T) {
	c := historyBackedController(t, []skill.Skill{
		{Name: "format", Body: "FORMAT_RULE", RunAs: skill.RunInline, Invocation: "manual", Scope: skill.ScopeGlobal},
	})
	c.Submit("/format now strict")
	waitIdle(t, c)

	userTurn := firstUserAuthoredTurn(t, c)

	if !strings.Contains(userTurn.Content, `<skill-pin name="format">`) || !strings.Contains(userTurn.Content, "</skill-pin>") {
		t.Fatalf("slash input missing pinned wrapper: %q", userTurn.Content)
	}
	if !strings.Contains(userTurn.Content, "Arguments: now strict") {
		t.Fatalf("slash input missing args: %q", userTurn.Content)
	}
	if userTurn.RawContent != "/format now strict" {
		t.Fatalf("slash raw content = %q, want typed line", userTurn.RawContent)
	}
}
