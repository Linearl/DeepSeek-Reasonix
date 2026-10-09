package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

type userInputCaptureProvider struct {
	request provider.Request
}

func (p *userInputCaptureProvider) Name() string { return "capture" }

func (p *userInputCaptureProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.request = req
	ch := make(chan provider.Chunk, 1)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "done"}
	close(ch)
	return ch, nil
}

func TestRunPersistsRawUserInputSeparatelyFromProviderContext(t *testing.T) {
	prov := &userInputCaptureProvider{}
	sess := NewSession("system")
	a := New(prov, tool.NewRegistry(), sess, Options{}, event.Discard)

	const raw = "fix the bug"
	const composed = "<capability-route version=\"1\">\nuse review\n</capability-route>\n\nfix the bug"
	ctx := withNoClosedLoop(WithRawUserInput(context.Background(), raw))
	if err := a.Run(ctx, composed); err != nil {
		t.Fatalf("Run: %v", err)
	}

	stored := sess.Snapshot()
	if len(stored) < 2 {
		t.Fatalf("stored messages = %d, want system and user", len(stored))
	}
	// Task 664: the turn leads with the transient current-time anchor; strip
	// that one block and the rest must equal the composed text exactly.
	bare, ok := trimLeadingTransientBlock(stored[1].Content, "current-time")
	if !ok {
		t.Fatalf("stored provider content = %q, want a leading current-time anchor", stored[1].Content)
	}
	if bare != composed {
		t.Fatalf("stored provider content = %q, want anchor + composed %q", bare, composed)
	}
	if got := stored[1].RawContent; got != raw {
		t.Fatalf("stored raw content = %q, want raw %q", got, raw)
	}
	if stored[1].Origin != provider.MessageOriginUser {
		t.Fatalf("stored origin = %q, want user", stored[1].Origin)
	}
	if stored[1].ProviderContent != "" {
		t.Fatalf("stored transitional provider content was not cleared: %+v", stored[1])
	}
	reqBare, ok := trimLeadingTransientBlock(prov.request.Messages[1].Content, "current-time")
	if len(prov.request.Messages) < 2 || !ok || reqBare != composed {
		t.Fatalf("provider request did not receive anchor + composed context: %+v", prov.request.Messages)
	}
	if prov.request.Messages[1].RawContent != "" || prov.request.Messages[1].ProviderContent != "" || prov.request.Messages[1].Origin != "" {
		t.Fatalf("provider request leaked display metadata: %+v", prov.request.Messages[1])
	}

	encoded, err := json.Marshal(stored[1])
	if err != nil {
		t.Fatalf("marshal stored user turn: %v", err)
	}
	var legacy struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(encoded, &legacy); err != nil {
		t.Fatalf("decode with previous-release shape: %v", err)
	}
	if legacy.Content != stored[1].Content {
		t.Fatalf("previous-release decode = %q, want the stored content verbatim %q", legacy.Content, stored[1].Content)
	}

	if receipt := a.CompletionReceipt(); receipt != nil && len(receipt.Gaps) > 0 {
		if strings.Contains(receipt.Gaps[0].Detail, "capability-route") {
			t.Fatalf("completion receipt leaked transient provider context: %+v", receipt.Gaps)
		}
	}
}

func TestTransientCapabilityRouteCannotTurnConversationIntoDeliveryReceipt(t *testing.T) {
	prov := &userInputCaptureProvider{}
	a := New(prov, tool.NewRegistry(), NewSession("system"), Options{}, event.Discard)

	const raw = "请解释这个项目目前的进度"
	const composed = `<capability-route version="1">
Relevant capabilities for this turn:
- skill:minimax-docx prefer: the skill trigger matches the user request
Policy: prefer means use the skill for the required change
</capability-route>

` + raw
	if err := a.Run(WithRawUserInput(context.Background(), raw), composed); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if a.CompletionReceipt() != nil {
		t.Fatalf("an advisory turn received a delivery receipt from transient routing: %+v", a.CompletionReceipt())
	}
	if len(prov.request.Messages) < 2 ||
		!strings.Contains(prov.request.Messages[1].Content, `<capability-route version="1">`) ||
		!strings.Contains(prov.request.Messages[1].Content, raw) {
		t.Fatalf("provider lost the capability route: %+v", prov.request.Messages)
	}
	if got := a.turn.turnInput; got != raw {
		t.Fatalf("contract input = %q, want authenticated raw input %q", got, raw)
	}
	c := a.LiveContract()
	if c == nil || len(c.Requirements) != 0 || len(c.Checks) != 0 {
		t.Fatalf("transient route created delivery requirements: %+v", c)
	}
}

func TestCompletionContractUsesGoalScopeTaskText(t *testing.T) {
	prov := &userInputCaptureProvider{}
	a := New(prov, tool.NewRegistry(), NewSession("system"), Options{}, event.Discard)
	ctx := withNoClosedLoop(WithRawUserInput(context.Background(), "Continue working."))
	ctx = WithDeliveryExecutionScope(ctx, DeliveryExecutionScope{ID: "goal-1", TaskText: "fix the parser"})

	if err := a.Run(ctx, "<goal-context>continue</goal-context>"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := a.turn.turnInput; got != "fix the parser" {
		t.Fatalf("goal scope task text = %q", got)
	}
}

func TestCompletionContractUsesPristineSubagentTaskText(t *testing.T) {
	prov := &userInputCaptureProvider{}
	a := New(prov, tool.NewRegistry(), NewSession("system"), Options{
		ClassifierTaskText: "fix the parser",
	}, event.Discard)
	const wrapped = "<workspace-context>private host framing</workspace-context>\n\nfix the parser"

	if err := a.Run(withNoClosedLoop(context.Background()), wrapped); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := a.turn.turnInput; got != "fix the parser" {
		t.Fatalf("classifier task text = %q", got)
	}
}

func TestSubagentImageCandidatesAreCopiedAndIsolated(t *testing.T) {
	images := []string{"data:image/png;base64,AAAA"}
	ctx := WithSubagentImageCandidates(context.Background(), images)
	images[0] = "mutated"

	got := SubagentImageCandidates(ctx)
	if len(got) != 1 || got[0] != "data:image/png;base64,AAAA" {
		t.Fatalf("candidates = %v, want an isolated copy of the original image", got)
	}
	got[0] = "mutated again"
	if again := SubagentImageCandidates(ctx); again[0] != "data:image/png;base64,AAAA" {
		t.Fatalf("candidate accessor exposed mutable context state: %v", again)
	}
}
