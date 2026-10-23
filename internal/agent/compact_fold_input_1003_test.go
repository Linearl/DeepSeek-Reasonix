package agent

import (
	"context"
	"strconv"
	"testing"

	"reasonix/internal/provider"
)

// preFoldProvider records every summarizer request and enforces a provider
// window exactly like a real one: over-window requests answer the trusted 400
// (the 44572-requested shape), under-window requests answer a digest.
type preFoldProvider struct {
	window int
	reqs   []provider.Request
}

func (p *preFoldProvider) Name() string { return "pre-fold-1003" }

func (p *preFoldProvider) ContextBudgetPolicy() provider.ContextBudgetPolicy {
	return provider.ContextBudgetPolicy{
		WindowMode: provider.ContextWindowShared, AutoOutputTokens: 8192, MaxOutputTokens: 8192,
		LimitMode: provider.OutputLimitOmitWhenSafe,
	}
}

func (p *preFoldProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.reqs = append(p.reqs, req)
	tokens := requestTokensOf(req)
	if tokens+req.MaxTokens > p.window {
		body := `{"error":{"message":"This model's maximum context length is ` +
			strconv.Itoa(p.window) + ` tokens. However, you requested ` + strconv.Itoa(tokens+req.MaxTokens) +
			` tokens (` + strconv.Itoa(tokens) + ` in the messages, ` + strconv.Itoa(req.MaxTokens) +
			` in the completion).","type":"invalid_request_error","param":null,"code":"invalid_request_error"}}`
		limit := provider.ParseContextLimitError(&provider.APIError{Provider: p.Name(), Status: 400, Body: body})
		if limit == nil {
			return nil, errFixture
		}
		return nil, limit
	}
	ch := make(chan provider.Chunk, 3)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "digest"}
	ch <- provider.Chunk{Type: provider.ChunkUsage, Usage: &provider.Usage{PromptTokens: tokens, CompletionTokens: 8, TotalTokens: tokens + 8, RequestCount: 1}}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

// requestTokensOf prices a request with the wire's own ruler (the request the
// provider is measured against) so the fixture's 400 matches the estimator's
// view of an over-window request.
func requestTokensOf(req provider.Request) int {
	total := 0
	for _, m := range req.Messages {
		total += 4
		total += estimateTextTokens(m.Content)
		total += estimateTextTokens(m.ReasoningContent)
		total += estimateTextTokens(m.Name)
		total += estimateTextTokens(m.ToolCallID)
		for _, tc := range m.ToolCalls {
			total += 8
			total += estimateTextTokens(tc.ID)
			total += estimateTextTokens(tc.Name)
			total += estimateTextTokens(tc.Arguments)
		}
		for _, schema := range req.Tools {
			total += 8
			total += estimateTextTokens(schema.Name)
			total += estimateTextTokens(schema.Description)
			total += estimateTextTokens(string(schema.Parameters))
		}
	}
	return total
}

var errFixture = errFixtureMark("pre-fold fixture")

type errFixtureMark string

func (e errFixtureMark) Error() string { return string(e) }

// buildPreFoldAgent builds an agent on a session already above the fold force
// ratio, with tool schemas attached so the summary request carries them (the
// schema-blind cold-ruler underestimate the incident is about).
func buildPreFoldAgent(t *testing.T, prov provider.Provider, turns int, window int) *Agent {
	t.Helper()
	sess := foldableSessionOverForce(turns)
	return agentOverForceWindow(t, prov, sess, window)
}

// TestSummaryInputPreFoldedUnderCeiling pins 任务 1003's fix end to end: a fold
// whose request is over the destination window is pre-folded BEFORE the send,
// so the summarizer request that reaches the provider is under the ceiling and
// the compaction succeeds instead of 400-ing back to the full view.
func TestSummaryInputPreFoldedUnderCeiling(t *testing.T) {
	const window = 40_000
	// Dense history: the fold region's tool-result bodies are large enough that
	// the request crosses the window on the cold ruler once schemas ride along.
	turns := 120
	prov := &preFoldProvider{window: window}
	a := buildPreFoldAgent(t, prov, turns, window)
	msgs := a.sess.conversation.Snapshot()
	head, start, ok := a.planFoldRegion(msgs, true, true)
	if !ok {
		t.Fatal("fixture has no foldable prefix")
	}
	fold := msgs[head:start]
	// The fixture must actually be over budget on the wire before the fix
	// (otherwise this test would pass without exercising the fold).
	budget := a.summaryInputBudget("")
	if budget <= 0 {
		t.Fatalf("summaryInputBudget = %d, want > 0", budget)
	}
	// The planner's cold estimate must fit (that's why it chose this prefix) —
	// reproduce the incident's under-estimate: estimateMessagesTokens fits but
	// the calibrated wire ruler does not.
	if a.estimatedRequestTokens(a.summaryRequest(fold, "")) <= budget {
		t.Fatalf("fixture request already fits the budget (%d <= %d); enlarge the fold to reproduce the over-window shape",
			a.estimatedRequestTokens(a.summaryRequest(fold, "")), budget)
	}
	// Compaction must succeed (no 400) and the request must land under the
	// window (the pre-fold happened before the send).
	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("pressure maintenance: %v", err)
	}
	if len(prov.reqs) == 0 {
		t.Fatal("no summary request reached the provider")
	}
	for i, req := range prov.reqs {
		if tokens := requestTokensOf(req); tokens > window {
			t.Fatalf("summary request %d reached the provider over the window: %d > %d (pre-fold did not run before send)", i, tokens, window)
		}
	}
	// The visible view must have landed a projection (compaction made progress
	// instead of falling back to the full history).
	if receipt := a.sess.compactionState.LastReceipt; receipt == nil || receipt.Status != "applied" {
		t.Fatalf("compaction receipt = %+v, want applied projection (no full-history fallback)", receipt)
	}
}

// TestSummaryUnderBudgetUnchanged pins zero regression: a fold that already
// fits the budget is sent byte-for-byte (same messages, same MaxTokens) — the
// pre-fold must be a no-op, not a re-shaping.
func TestSummaryUnderBudgetUnchanged(t *testing.T) {
	const window = 200_000
	prov := &preFoldProvider{window: window}
	a := buildPreFoldAgent(t, prov, 8, window)
	msgs := a.sess.conversation.Snapshot()
	head, start, ok := a.planFoldRegion(msgs, true, true)
	if !ok {
		t.Fatal("fixture has no foldable prefix")
	}
	fold := msgs[head:start]
	budget := a.summaryInputBudget("")
	if budget <= 0 {
		t.Fatalf("summaryInputBudget = %d, want > 0", budget)
	}
	if got := a.estimatedRequestTokens(a.summaryRequest(fold, "")); got > budget {
		t.Fatalf("fixture already over budget (%d > %d); enlarge the window or shrink the fold", got, budget)
	}
	before := a.summaryRequest(fold, "")
	folded, affected := a.elideOversizedContentsForSummary(fold, budget)
	if affected != 0 {
		t.Fatalf("under-budget fold was touched: affected=%d (want 0 — no-op path)", affected)
	}
	if len(folded) != len(fold) {
		t.Fatalf("fold length changed: %d -> %d (want byte-identical)", len(fold), len(folded))
	}
	for i := range folded {
		if folded[i].Content != fold[i].Content {
			t.Fatalf("message %d content changed on the no-op path", i)
		}
	}
	after := a.summaryRequest(folded, "")
	if requestTokensOf(before) != requestTokensOf(after) {
		t.Fatalf("request tokens changed on the no-op path: %d -> %d", requestTokensOf(before), requestTokensOf(after))
	}
}
