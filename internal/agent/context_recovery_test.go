package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

type budgetTestTool struct{}

func (budgetTestTool) Name() string        { return "budget_fixture" }
func (budgetTestTool) Description() string { return "Budget recovery request fixture." }
func (budgetTestTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)
}
func (budgetTestTool) ReadOnly() bool { return true }
func (budgetTestTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "ok", nil
}

func sameProviderRequestExceptMaxTokens(a, b provider.Request) bool {
	a.MaxTokens = 0
	b.MaxTokens = 0
	return reflect.DeepEqual(a, b)
}

type scriptedBudgetProvider struct {
	mu     sync.Mutex
	policy provider.ContextBudgetPolicy
	errs   []error
	// errorsByAttempt, when non-empty, indexes errors per attempt instead of
	// errs (dense-incident shape: overflow on attempt 1, success on attempt 2).
	errorsByAttempt []error
	reqs            []provider.Request
	texts           []string
}

func (p *scriptedBudgetProvider) Name() string { return "scripted-budget" }
func (p *scriptedBudgetProvider) ContextBudgetPolicy() provider.ContextBudgetPolicy {
	return p.policy
}
func (p *scriptedBudgetProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.mu.Lock()
	p.reqs = append(p.reqs, req)
	idx := len(p.reqs) - 1
	var err error
	// errorsByAttempt drives the dense-incident case: a real DeepSeek body whose
	// reported prompt (752k) is PAST the configured hard input ceiling (window −
	// protocol reserve), so the first physical recovery must be the overflow
	// compaction (the incident's path), not an output trim that keeps an
	// already-unsendable prompt.
	if idx < len(p.errorsByAttempt) {
		err = p.errorsByAttempt[idx]
	} else if idx < len(p.errs) {
		err = p.errs[idx]
	}
	text := "ok"
	if idx < len(p.texts) && p.texts[idx] != "" {
		text = p.texts[idx]
	}
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	ch := make(chan provider.Chunk, 2)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: text}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

func issue8909Limit() *provider.ContextLimitError {
	return &provider.ContextLimitError{
		APIError:         &provider.APIError{Provider: "p", Status: 400, Body: "context"},
		WindowTokens:     1_048_576,
		RequestedTokens:  1_165_351,
		PromptTokens:     810_882,
		CompletionTokens: 354_469,
	}
}

func newBudgetAgent(t *testing.T, p provider.Provider) *Agent {
	t.Helper()
	sess := NewSession("")
	sess.Replace([]provider.Message{{Role: provider.RoleUser, Content: "continue"}})
	registry := tool.NewRegistry()
	registry.Add(budgetTestTool{})
	return New(p, registry, sess, Options{ContextWindow: 1_048_576, CompactRatio: 2, MaxOutputTokens: 0, Temperature: 0.25}, event.Discard)
}

func TestContextLimitRecoveryChangesOnlyOutputField(t *testing.T) {
	prov := &scriptedBudgetProvider{
		policy: provider.ContextBudgetPolicy{
			WindowMode: provider.ContextWindowShared, AutoOutputTokens: 384_000,
			MaxOutputTokens: 384_000, LimitMode: provider.OutputLimitOmitWhenSafe,
		},
		errs: []error{issue8909Limit(), nil},
	}
	a := newBudgetAgent(t, prov)
	a.sess.conversation.Replace([]provider.Message{
		{
			Role: provider.RoleAssistant, Content: "tool preface", ReasoningContent: "provider reasoning",
			ReasoningSignature: "reasoning-signature", ReasoningID: "reasoning-id", ReasoningStatus: "completed",
			ToolCalls:      []provider.ToolCall{{ID: "call-1", Name: "budget_fixture", Arguments: `{"q":"status"}`, ThoughtSignature: "thought-signature"}},
			ResponsesItems: []json.RawMessage{json.RawMessage(`{"type":"reasoning","id":"item-1"}`)},
			ServerSearch: []provider.ServerSearchCall{{
				ID: "search-1", Query: "context budgets",
				Results: []provider.ServerSearchHit{{Title: "Result", URL: "https://example.test"}},
				Raw:     json.RawMessage(`{"query":"context budgets"}`),
			}},
		},
		{Role: provider.RoleTool, Name: "budget_fixture", ToolCallID: "call-1", Content: "done"},
		{Role: provider.RoleUser, Content: "continue", Images: []string{"data:image/png;base64,AA=="}},
	})
	beforeMessages := a.sess.conversation.Snapshot()
	got := a.streamWithSamplingRecovery(WithResponseFormat(context.Background(), "json_object"), 1)
	if got.err != nil {
		t.Fatalf("recovery failed: %v", got.err)
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if len(prov.reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(prov.reqs))
	}
	if !sameProviderRequestExceptMaxTokens(prov.reqs[0], prov.reqs[1]) {
		t.Fatalf("provider request changed outside MaxTokens:\nfirst=%+v\nretry=%+v", prov.reqs[0], prov.reqs[1])
	}
	if prov.reqs[1].MaxTokens != 229_502 {
		t.Fatalf("retry MaxTokens = %d, want 229502", prov.reqs[1].MaxTokens)
	}
	if a.lastAdmission().LastRecovery != contextRecoveryLearnedRetry {
		t.Fatalf("last recovery = %s", a.lastAdmission().LastRecovery)
	}
	budget := a.ContextMaintenanceSnapshot().ContextBudget
	if budget == nil {
		t.Fatal("missing context budget snapshot after learned retry")
	}
	if budget.Source != provider.ContextBudgetSourceLearned || budget.WindowMode != provider.ContextWindowShared.String() {
		t.Fatalf("retry source/window = %s/%s, want learned/shared", budget.Source, budget.WindowMode)
	}
	if budget.RequestedOutputTokens != 384_000 || budget.EffectiveOutputTokens != 229_502 || budget.PhysicalRemaining != 229_502 || !budget.Clipped {
		t.Fatalf("retry budget = %+v, want requested=384000 effective=physical=229502 clipped", budget)
	}
	if budget.ObservedWindow != 1_048_576 || budget.ObservedPrompt != 810_882 || budget.ObservedCompletion != 354_469 {
		t.Fatalf("retry observations = %+v", budget)
	}
	if after := a.sess.conversation.Snapshot(); !reflect.DeepEqual(after, beforeMessages) {
		t.Fatalf("recovery mutated the transcript:\nbefore=%+v\nafter=%+v", beforeMessages, after)
	}
}

// An overflow without token numbers (Zhipu GLM 1261) must not be answered
// with the same prompt under a clipped output cap: the estimate that admitted
// the request is exactly what the provider rejected, so recovery goes straight
// to overflow compaction and retries the rebuilt request.
func TestUnnumberedContextLimitSkipsIdenticalRetry(t *testing.T) {
	prov := &scriptedBudgetProvider{
		policy: provider.ContextBudgetPolicy{
			WindowMode: provider.ContextWindowShared, AutoOutputTokens: 384_000,
			MaxOutputTokens: 384_000, LimitMode: provider.OutputLimitOmitWhenSafe,
		},
		errs: []error{&provider.ContextLimitError{APIError: &provider.APIError{
			Provider: "glm", Status: 400, Body: `{"error":{"code":"1261","message":"Prompt exceeds max length"}}`,
		}}},
	}
	a := newBudgetAgent(t, prov)
	a.sess.conversation.Replace(foldableSessionOverForce(6).Messages)

	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err != nil {
		t.Fatalf("recovery failed: %v", got.err)
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if len(prov.reqs) != 3 {
		t.Fatalf("requests = %d, want rejected sampling, one summary, and the rebuilt sampling", len(prov.reqs))
	}
	if !requestContains(prov.reqs[1], "Compact the preceding conversation prefix") {
		t.Fatal("second request must be the overflow compaction summary, not a retry of the rejected prompt")
	}
	if sameProviderRequestExceptMaxTokens(prov.reqs[0], prov.reqs[2]) {
		t.Fatal("the retried request must be rebuilt on the compacted view, not the rejected prompt")
	}
	if a.lastAdmission().LastRecovery != contextRecoveryCompacted {
		t.Fatalf("last recovery = %s, want compacted", a.lastAdmission().LastRecovery)
	}
}

func TestContextLimitRecoveryPublishesUnknownGatewayBudget(t *testing.T) {
	limit := &provider.ContextLimitError{
		APIError:         &provider.APIError{Provider: "compatible", Status: 400, Body: "context"},
		WindowTokens:     20_000,
		RequestedTokens:  25_000,
		PromptTokens:     10_000,
		CompletionTokens: 15_000,
	}
	prov := &scriptedBudgetProvider{
		policy: provider.ContextBudgetPolicy{WindowMode: provider.ContextWindowUnknown, LimitMode: provider.OutputLimitOmitWhenSafe},
		errs:   []error{limit, nil},
	}
	a := newBudgetAgent(t, prov)
	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err != nil {
		t.Fatalf("unknown gateway recovery failed: %v", got.err)
	}
	prov.mu.Lock()
	if len(prov.reqs) != 2 || prov.reqs[0].MaxTokens != 0 || prov.reqs[1].MaxTokens != 1_808 {
		t.Fatalf("unknown gateway requests = %+v, want omitted then 1808", prov.reqs)
	}
	prov.mu.Unlock()
	budget := a.ContextMaintenanceSnapshot().ContextBudget
	if budget == nil {
		t.Fatal("missing learned unknown-gateway budget")
	}
	if budget.Source != provider.ContextBudgetSourceLearned || budget.WindowMode != provider.ContextWindowShared.String() ||
		budget.AutoOutputTokens != 15_000 || budget.RequestedOutputTokens != 15_000 ||
		budget.EffectiveOutputTokens != 1_808 || budget.PhysicalRemaining != 1_808 || !budget.Clipped ||
		budget.LastRecovery != contextRecoveryLearnedRetry {
		t.Fatalf("unknown gateway retry budget = %+v", budget)
	}
}

func TestContextLimitRecoveryRetriesOriginalRequestOnlyOnce(t *testing.T) {
	limit := issue8909Limit()
	limit.PromptTokens = 1_040_000
	limit.CompletionTokens = 20_000
	limit.RequestedTokens = 1_060_000
	prov := &scriptedBudgetProvider{
		policy: provider.ContextBudgetPolicy{
			WindowMode: provider.ContextWindowShared, AutoOutputTokens: 384_000,
			LimitMode: provider.OutputLimitOmitWhenSafe,
		},
		errs: []error{limit, limit, limit},
	}
	a := newBudgetAgent(t, prov)
	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err == nil {
		t.Fatal("expected terminal context overflow")
	}
	if a.lastAdmission().LastRecovery != contextRecoveryFailed {
		t.Fatalf("last recovery = %s, want failed", a.lastAdmission().LastRecovery)
	}
	if provider.AsContextLimitError(got.err) == nil && !errors.Is(got.err, ErrCompactionRequired) {
		t.Fatalf("terminal err = %v", got.err)
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if got := len(prov.reqs); got != 2 {
		t.Fatalf("provider requests = %d, want initial request plus one retry", got)
	}
}

func TestContextBudgetLearnAndSnapshotRace(t *testing.T) {
	a := &Agent{agentConfig: agentConfig{contextWindow: 1_048_576}, sess: sessionRuntime{conversation: NewSession("")}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 200 {
			a.learnContextBudget(1_000_000-i, 1000+i, true)
			a.setLastRecovery(contextRecoveryLearnedRetry)
			_ = a.ContextMaintenanceSnapshot()
			_ = a.effectiveContextWindow()
		}
	}()
	for range 200 {
		a.learnContextBudget(900_000, 2000, true)
		_ = a.ContextMaintenanceSnapshot()
		_ = a.lastAdmission()
	}
	<-done
}

func TestThreeStateMaxOutputTokens(t *testing.T) {
	prov := &policyWindowProvider{policy: provider.ContextBudgetPolicy{
		WindowMode: provider.ContextWindowShared, AutoOutputTokens: 384_000,
		MaxOutputTokens: 384_000, LimitMode: provider.OutputLimitOmitWhenSafe,
	}}
	a := &Agent{agentConfig: agentConfig{contextWindow: 1_048_576}, svc: agentServices{prov: prov}}
	msgs := []provider.Message{{Role: provider.RoleUser, Content: "hi"}}
	pos := provider.Request{Messages: msgs, MaxTokens: 8192}
	if err := a.applyAdmissionToRequest(&pos); err != nil || pos.MaxTokens != 8192 {
		t.Fatalf("positive cap = %d err=%v", pos.MaxTokens, err)
	}
	zero := provider.Request{Messages: msgs, MaxTokens: 0}
	if err := a.applyAdmissionToRequest(&zero); err != nil || zero.MaxTokens != 0 {
		t.Fatalf("auto omit = %d err=%v", zero.MaxTokens, err)
	}
	neg := provider.Request{Messages: msgs, MaxTokens: -1}
	if err := a.applyAdmissionToRequest(&neg); err != nil || neg.MaxTokens != -1 {
		t.Fatalf("explicit omit = %d err=%v", neg.MaxTokens, err)
	}
}

// tl 1083 (2026-10-22 incident): a user-configured max_output_tokens reservation
// that pushes the request past the window (messages 752k + completion 327k >
// 1M) must land on the COMPACTED retry path — the summary rebuild, not the
// learned-output retry, because the oversized completion cannot be rescued by
// trimming it alone. This pins the B half of the incident (「回退全量投影」=
// handing the turn the full transcript is forbidden); the C half (the grep-able
// notice with the recovery kind and token numbers) is asserted on the same run.
func TestIncidentOutputReservationRecoversByCompaction(t *testing.T) {
	prov := &scriptedBudgetProvider{
		policy: provider.ContextBudgetPolicy{
			WindowMode: provider.ContextWindowShared, AutoOutputTokens: 384_000,
			MaxOutputTokens: 384_000, LimitMode: provider.OutputLimitOmitWhenSafe,
		},
		errorsByAttempt: []error{
			// The incident's shape: a max_output_tokens reservation that pushes
			// the request past the window, and — as in the dense real world —
			// the reported prompt sits PAST the hard input ceiling so the first
			// physical recovery must be the overflow compaction (the incident's
			// path), not an output trim that keeps an unsendable prompt.
			&provider.ContextLimitError{
				APIError:         &provider.APIError{Provider: "p", Status: 400, Body: "context"},
				WindowTokens:     1_048_576,
				RequestedTokens:  1_080_357,
				PromptTokens:     1_048_000,
				CompletionTokens: 327_766,
			},
			nil,
		},
	}
	recoveryNotices := []event.Event{}
	sink := event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && e.Detail != "" && strings.Contains(e.Detail, "recovery=") {
			recoveryNotices = append(recoveryNotices, e)
		}
	})
	a := newBudgetAgentSink(t, prov, sink)
	// 260 turns puts the canonical transcript well past the window, so the
	// provider-reported prompt (752k) is consistent with a genuinely oversized
	// session (the incident's shape) and the overflow compaction has real work
	// to do — a smaller fixture would let the overflow fire on a prompt the
	// estimator already sees as tiny, which is not the incident.
	a.sess.conversation.Replace(foldableSessionOverForce(260).Messages)

	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err != nil {
		t.Fatalf("incident recovery failed: %v", got.err)
	}
	prov.mu.Lock()
	if len(prov.reqs) != 3 {
		prov.mu.Unlock()
		visible := a.modelVisibleMessages()
		t.Fatalf("requests = %d, want rejected sampling, one summary, and the compacted retry (last admission=%+v, est=%d)",
			len(prov.reqs), a.lastAdmission(), a.estimatedVisibleRequestTokens(visible))
	}
	if !requestContains(prov.reqs[1], "Compact the preceding conversation prefix") {
		prov.mu.Unlock()
		t.Fatal("the second request must be the overflow compaction summary — not a learned-output retry of the oversized reservation")
	}
	if sameProviderRequestExceptMaxTokens(prov.reqs[0], prov.reqs[2]) {
		prov.mu.Unlock()
		t.Fatal("the retry must be rebuilt on the compacted projection, not the rejected full view")
	}
	// C: the recovery is grep-able and names the kind + observed tokens.
	// Accept the learned-retry or compacted frame (the compaction summary itself
	// can carry a second observed overflow), but the FIRST frame must be one of
	// the two legitimate recovery kinds with numbers attached.
	prov.mu.Unlock()
	if len(recoveryNotices) == 0 {
		t.Fatal("missing the context-limit recovery notice (tl 1083 C)")
	}
	first := recoveryNotices[0]
	if !strings.Contains(first.Detail, "window=1048576") || !strings.Contains(first.Detail, "prompt=1048000") ||
		!strings.Contains(first.Detail, "completion=327766") {
		t.Fatalf("recovery notice detail = %q, want the observed window/prompt/completion numbers", first.Detail)
	}
	if a.lastAdmission().LastRecovery != contextRecoveryCompacted {
		t.Fatalf("last recovery = %s, want compacted", a.lastAdmission().LastRecovery)
	}
	// B: the context occupancy must not blow up after recovery — the retried
	// view stays inside the window (the incident's 1.0→4.7x blowup shape).
	visible := a.modelVisibleMessages()
	if est := a.estimatedVisibleRequestTokens(visible); est > 1_048_576 {
		t.Fatalf("post-recovery visible estimate = %d tokens, exceeds the window — full-transcript fallback", est)
	}
}

// tl 1083 非超窗回归（任务书验收第三条）: a 400 that is NOT a context-limit
// overflow (e.g. a malformed-parameter rejection) must keep the pre-change
// behavior — no compaction, no projection rebuild, no learned-budget state,
// terminal error surfaced as-is.
func TestNonContext400KeepsPlainFailurePath(t *testing.T) {
	prov := &scriptedBudgetProvider{
		policy: provider.ContextBudgetPolicy{WindowMode: provider.ContextWindowShared, LimitMode: provider.OutputLimitOmitWhenSafe},
		errs:   []error{&provider.APIError{Provider: "p", Status: 400, Body: `{"error":{"message":"invalid temperature","param":"temperature"}}`}},
	}
	a := newBudgetAgent(t, prov)
	a.sess.conversation.Replace(foldableSessionOverForce(45).Messages)

	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err == nil {
		t.Fatal("expected the original 400 to surface")
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if len(prov.reqs) != 1 {
		t.Fatalf("provider requests = %d, want 1 (no compaction, no retry)", len(prov.reqs))
	}
	if provider.AsContextLimitError(got.err) != nil {
		t.Fatalf("a parameter 400 must not be classified as a context limit: %v", got.err)
	}
}

// newBudgetAgentSink is newBudgetAgent with a custom sink, so the notice test
// can observe events without touching the discard-sink default fixture.
func newBudgetAgentSink(t *testing.T, p provider.Provider, sink event.Sink) *Agent {
	t.Helper()
	sess := NewSession("")
	sess.Replace([]provider.Message{{Role: provider.RoleUser, Content: "continue"}})
	registry := tool.NewRegistry()
	registry.Add(budgetTestTool{})
	return New(p, registry, sess, Options{ContextWindow: 1_048_576, CompactRatio: 2, MaxOutputTokens: 0, Temperature: 0.25}, sink)
}
