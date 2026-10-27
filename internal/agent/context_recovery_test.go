package agent

import (
	"context"
	"encoding/json"
	"errors"
	"math"
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
	// tl 1190 L2: the correction cache is package-level and the recovery lanes
	// write it on every numbered overflow — reset it around each test so the
	// provider-keyed factor never leaks between fixtures sharing a Name().
	reset := func() {
		promptCorrectionCache.Lock()
		promptCorrectionCache.entries = map[string]promptCorrectionEntry{}
		promptCorrectionCache.Unlock()
	}
	reset()
	t.Cleanup(reset)
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
	// tl 1190 L1: the retry cap is provider arithmetic against the reported
	// numbers — window − messagesReal − safetyMargin(max(prompt×10%, 8192)):
	// 1048576 − 810882 − 81088 = 156606.
	if prov.reqs[1].MaxTokens != 156_606 {
		t.Fatalf("retry MaxTokens = %d, want 156606", prov.reqs[1].MaxTokens)
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
	if budget.RequestedOutputTokens != 384_000 || budget.EffectiveOutputTokens != 156_606 || budget.PhysicalRemaining != 156_606 || !budget.Clipped {
		t.Fatalf("retry budget = %+v, want requested=384000 effective=physical=156606 clipped", budget)
	}
	if budget.ObservedWindow != 1_048_576 || budget.ObservedPrompt != 810_882 || budget.ObservedCompletion != 354_469 {
		t.Fatalf("retry observations = %+v", budget)
	}
	if after := a.sess.conversation.Snapshot(); !reflect.DeepEqual(after, beforeMessages) {
		t.Fatalf("recovery mutated the transcript:\nbefore=%+v\nafter=%+v", beforeMessages, after)
	}
}

// tl 1190 兜底: an overflow without token numbers (Zhipu GLM 1261) first walks
// the bounded budget-shrink ladder — the SAME request resent with
// max_tokens×0.9 — because a smaller-than-configured gateway completion window
// is the one overflow shape a cap shrink actually fixes. Only the ladder's
// exhaustion hands the recovery to overflow compaction (covered by
// TestUnnumberedOverflowFallbackLadderBoundedAtFive; the sampling loop's
// 4-attempt cap can never reach it end-to-end).
func TestUnnumberedContextLimitShrinksBudgetBeforeCompaction(t *testing.T) {
	prov := &scriptedBudgetProvider{
		policy: provider.ContextBudgetPolicy{
			// OutputLimitAlways pins a positive wire max_tokens even though the
			// fixture's estimate leaves plenty of room — the shrink lane needs
			// a cap to shrink.
			WindowMode: provider.ContextWindowShared, AutoOutputTokens: 384_000,
			MaxOutputTokens: 384_000, LimitMode: provider.OutputLimitAlways,
		},
		errs: []error{&provider.ContextLimitError{APIError: &provider.APIError{
			Provider: "glm", Status: 400, Body: `{"error":{"code":"1261","message":"Prompt exceeds max length"}}`,
		}}},
	}
	a := newBudgetAgent(t, prov)

	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err != nil {
		t.Fatalf("recovery failed: %v", got.err)
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if len(prov.reqs) != 2 {
		t.Fatalf("requests = %d, want rejected sampling then the shrunk resend", len(prov.reqs))
	}
	if !sameProviderRequestExceptMaxTokens(prov.reqs[0], prov.reqs[1]) {
		t.Fatalf("shrink retry must not change anything but MaxTokens:\nfirst=%+v\nretry=%+v", prov.reqs[0], prov.reqs[1])
	}
	want := int(float64(prov.reqs[0].MaxTokens) * 0.9)
	if prov.reqs[1].MaxTokens != want || want < minPreciseRetryOutputTokens {
		t.Fatalf("retry MaxTokens = %d, want the ×0.9 shrink %d", prov.reqs[1].MaxTokens, want)
	}
	if a.lastAdmission().LastRecovery != contextRecoveryLearnedRetry {
		t.Fatalf("last recovery = %s, want learned_retry", a.lastAdmission().LastRecovery)
	}
}

// tl 1190 兜底边界: the ×0.9 ladder runs at most maxUnnumberedFallbackRetries
// times per turn, then the recovery escalates to overflow compaction — the
// prompt-side overflow shape the shrink can never fix (the old
// TestUnnumberedContextLimitSkipsIdenticalRetry contract, now behind the
// ladder instead of in front of it).
func TestUnnumberedOverflowFallbackLadderBoundedAtFive(t *testing.T) {
	prov := &scriptedBudgetProvider{
		policy: provider.ContextBudgetPolicy{
			WindowMode: provider.ContextWindowShared, AutoOutputTokens: 384_000,
			MaxOutputTokens: 384_000, LimitMode: provider.OutputLimitOmitWhenSafe,
		},
	}
	a := newBudgetAgent(t, prov)
	a.sess.conversation.Replace(foldableSessionOverForce(6).Messages)
	limit := &provider.ContextLimitError{APIError: &provider.APIError{
		Provider: "glm", Status: 400, Body: `{"error":{"code":"1261","message":"Prompt exceeds max length"}}`,
	}}
	frozen := samplingRequest{req: provider.Request{Messages: a.sess.conversation.Snapshot(), MaxTokens: 10_000}, prov: prov}
	budget := &contextRecoveryBudget{}
	wantCaps := []int{9_000, 8_100, 7_290, 6_561, 5_904}
	for i, want := range wantCaps {
		next, ok, kind := a.recoverContextLimit(context.Background(), frozen, limit, budget)
		if !ok || kind != contextRecoveryLearnedRetry {
			t.Fatalf("step %d: ok=%v kind=%s, want a shrink retry", i, ok, kind)
		}
		if next.req.MaxTokens != want {
			t.Fatalf("step %d: MaxTokens = %d, want %d", i, next.req.MaxTokens, want)
		}
		frozen = next
	}
	next, ok, kind := a.recoverContextLimit(context.Background(), frozen, limit, budget)
	if !ok || kind != contextRecoveryCompacted {
		t.Fatalf("after the ladder: ok=%v kind=%s, want compaction", ok, kind)
	}
	if next.req.MaxTokens == 5_904 {
		t.Fatal("compaction must rebuild the request, not keep the shrunk cap")
	}
	if _, ok, _ := a.recoverContextLimit(context.Background(), next, limit, budget); ok {
		t.Fatal("compaction is a once-per-turn lane; the next overflow must fail")
	}
}

func TestContextLimitRecoveryPublishesUnknownGatewayBudget(t *testing.T) {
	// tl 1190: numbers sized so the corrected cap clears the 4096 minimum —
	// 32768 − 12000 − max(1200, reserve(32768)=256) = 19568. A correction that
	// would land below the minimum must NOT resend (compaction owns it
	// instead).
	limit := &provider.ContextLimitError{
		APIError:         &provider.APIError{Provider: "compatible", Status: 400, Body: "context"},
		WindowTokens:     32_768,
		RequestedTokens:  32_000,
		PromptTokens:     12_000,
		CompletionTokens: 20_000,
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
	if len(prov.reqs) != 2 || prov.reqs[0].MaxTokens != 0 || prov.reqs[1].MaxTokens != 19_568 {
		t.Fatalf("unknown gateway requests = %+v, want omitted then 19568", prov.reqs)
	}
	prov.mu.Unlock()
	budget := a.ContextMaintenanceSnapshot().ContextBudget
	if budget == nil {
		t.Fatal("missing learned unknown-gateway budget")
	}
	if budget.Source != provider.ContextBudgetSourceLearned || budget.WindowMode != provider.ContextWindowShared.String() ||
		budget.AutoOutputTokens != 20_000 || budget.RequestedOutputTokens != 20_000 ||
		budget.EffectiveOutputTokens != 19_568 || budget.PhysicalRemaining != 19_568 || !budget.Clipped ||
		budget.LastRecovery != contextRecoveryLearnedRetry {
		t.Fatalf("unknown gateway retry budget = %+v", budget)
	}
}

// tl 1190 L1 boundary: a corrected cap below the 4096 minimum is not a useful
// agent turn — the request must NOT be resent; the recovery escalates to
// overflow compaction (or fails the turn when nothing is compactable).
func TestContextLimitBelowMinimumOutputSkipsResend(t *testing.T) {
	// learned window 20000: 20000 − 17000 − max(1700, 256) = 1300 < 4096.
	limit := &provider.ContextLimitError{
		APIError:         &provider.APIError{Provider: "compatible", Status: 400, Body: "context"},
		WindowTokens:     20_000,
		RequestedTokens:  20_500,
		PromptTokens:     17_000,
		CompletionTokens: 3_500,
	}
	prov := &scriptedBudgetProvider{
		policy: provider.ContextBudgetPolicy{WindowMode: provider.ContextWindowUnknown, LimitMode: provider.OutputLimitOmitWhenSafe},
		errs:   []error{limit},
	}
	a := newBudgetAgent(t, prov)
	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err == nil {
		t.Fatal("expected the turn to fail without a doomed resend")
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	// The only follow-up request is the overflow compaction's own summary —
	// never a resend of the rejected sampling request.
	if len(prov.reqs) != 2 {
		t.Fatalf("requests = %d, want the rejected request plus the compaction summary", len(prov.reqs))
	}
	if !requestContains(prov.reqs[1], "Compact the preceding conversation prefix") {
		t.Fatalf("second request must be the compaction summary, got %+v", prov.reqs[1])
	}
	if a.lastAdmission().LastRecovery != contextRecoveryFailed {
		t.Fatalf("last recovery = %s, want failed", a.lastAdmission().LastRecovery)
	}
}

// tl 1190 L1: the precise correction is bounded at maxPreciseContextRetries
// (2) per turn — two same-request resends with provider-arithmetic caps, then
// the turn fails instead of looping. (红线：单请求参数修正重发，绝不整轮重跑。)
func TestContextLimitPreciseRetryBoundedAtTwo(t *testing.T) {
	limit := issue8909Limit()
	limit.PromptTokens = 820_000
	limit.CompletionTokens = 20_000
	limit.RequestedTokens = 840_000
	// corrected cap: 1048576 − 820000 − max(82000, 8192) = 146576
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
	if got := len(prov.reqs); got != 3 {
		t.Fatalf("provider requests = %d, want initial request plus two precise retries", got)
	}
	for _, req := range prov.reqs[1:] {
		if req.MaxTokens != 146_576 {
			t.Fatalf("retry MaxTokens = %d, want the provider-arithmetic 146576", req.MaxTokens)
		}
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
	// Same tl 1190 L2 cache isolation as newBudgetAgent.
	reset := func() {
		promptCorrectionCache.Lock()
		promptCorrectionCache.entries = map[string]promptCorrectionEntry{}
		promptCorrectionCache.Unlock()
	}
	reset()
	t.Cleanup(reset)
	sess := NewSession("")
	sess.Replace([]provider.Message{{Role: provider.RoleUser, Content: "continue"}})
	registry := tool.NewRegistry()
	registry.Add(budgetTestTool{})
	return New(p, registry, sess, Options{ContextWindow: 1_048_576, CompactRatio: 2, MaxOutputTokens: 0, Temperature: 0.25}, sink)
}

// tl 1190 验收②: the 2026-10-27 deepseek overflow body — the provider's four
// exact numbers must drive ONE same-request resend whose cap is
// limit − messagesReal − safetyMargin, and the resent pair must fit the
// provider's own arithmetic (红线：单请求参数修正，非整轮重跑).
func TestDeepSeekOverflowRetriesWithProviderArithmetic(t *testing.T) {
	const deepSeekOverflowBody = `{"error":{"message":"This model's maximum context length is 1048576 tokens. However, you requested 1097863 tokens (737203 in the messages, 360660 in the completion). Please reduce the length of the messages or completion. (request_id: 0327aa60-03b1-4358-b248-a4d5ecfc13b8)","type":"invalid_request_error","param":null,"code":"invalid_request_error"}}`
	limit := provider.ParseContextLimitError(&provider.APIError{Provider: "deepseek", Status: 400, Body: deepSeekOverflowBody})
	if limit == nil || limit.WindowTokens != 1_048_576 || limit.RequestedTokens != 1_097_863 ||
		limit.PromptTokens != 737_203 || limit.CompletionTokens != 360_660 {
		t.Fatalf("deepseek overflow body must parse with exact numbers, got %+v", limit)
	}
	prov := &scriptedBudgetProvider{
		policy: provider.ContextBudgetPolicy{
			WindowMode: provider.ContextWindowShared, AutoOutputTokens: provider.DeepSeekMaxOutputTokens,
			MaxOutputTokens: provider.DeepSeekMaxOutputTokens, LimitMode: provider.OutputLimitOmitWhenSafe,
		},
		errs: []error{limit, nil},
	}
	a := newBudgetAgent(t, prov)
	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err != nil {
		t.Fatalf("deepseek overflow recovery failed: %v", got.err)
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if len(prov.reqs) != 2 {
		t.Fatalf("requests = %d, want the rejected request plus exactly one precise resend", len(prov.reqs))
	}
	// corrected cap = 1048576 − 737203 − max(73720, 8192) = 237653
	if prov.reqs[1].MaxTokens != 237_653 {
		t.Fatalf("resend MaxTokens = %d, want the provider-arithmetic 237653", prov.reqs[1].MaxTokens)
	}
	if !sameProviderRequestExceptMaxTokens(prov.reqs[0], prov.reqs[1]) {
		t.Fatal("the resend must be the SAME request with only MaxTokens corrected")
	}
	if prov.reqs[1].MaxTokens+limit.PromptTokens > limit.WindowTokens {
		t.Fatalf("resend still overflows: %d + %d > %d", prov.reqs[1].MaxTokens, limit.PromptTokens, limit.WindowTokens)
	}
}

// tl 1190 验收③ L2 unit: the EWMA factor keys per provider, only grows on
// provider-confirmed undershoots, and clamps into [1, 4].
func TestProviderPromptCorrectionEWMAAndIsolation(t *testing.T) {
	resetPromptCorrectionCache(t)
	if got := promptCorrectionFactor("corr-a"); got != 1 {
		t.Fatalf("unknown provider factor = %v, want 1", got)
	}
	noteProviderPromptCorrection("corr-a", 1_080, 1_000)
	if got := promptCorrectionFactor("corr-a"); math.Abs(got-1.08) > 1e-9 {
		t.Fatalf("first observation factor = %v, want 1.08", got)
	}
	// ratio ≤ 1 carries no undershoot signal and must not move the factor.
	noteProviderPromptCorrection("corr-a", 900, 1_000)
	if got := promptCorrectionFactor("corr-a"); math.Abs(got-1.08) > 1e-9 {
		t.Fatalf("overshoot observation must be ignored, factor = %v", got)
	}
	// EWMA α=0.5: 1.08 and 1.20 average to 1.14.
	noteProviderPromptCorrection("corr-a", 1_200, 1_000)
	if got := promptCorrectionFactor("corr-a"); math.Abs(got-1.14) > 1e-9 {
		t.Fatalf("EWMA factor = %v, want 1.14", got)
	}
	// provider isolation: another key stays at 1 — the "switch resets" face.
	if got := promptCorrectionFactor("corr-b"); got != 1 {
		t.Fatalf("unrelated provider factor = %v, want 1", got)
	}
	// degenerate inputs are ignored.
	noteProviderPromptCorrection("", 100, 10)
	noteProviderPromptCorrection("corr-c", 0, 10)
	noteProviderPromptCorrection("corr-c", 100, 0)
	if got := promptCorrectionFactor("corr-c"); got != 1 {
		t.Fatalf("degenerate observations must be ignored, factor = %v", got)
	}
	// clamp: one wildly undershooting reply cannot price a session out.
	noteProviderPromptCorrection("corr-d", 100_000, 100)
	if got := promptCorrectionFactor("corr-d"); got != promptCorrectionFactorMax {
		t.Fatalf("clamped factor = %v, want %v", got, promptCorrectionFactorMax)
	}
}

// tl 1190 验收③ L2 integration: once the provider's overflow taught the
// estimator, later estimates on THAT provider ride the factor; the session
// calibration path is untouched by other providers' factors.
func TestEstimatedShapeTokensAppliesProviderCorrection(t *testing.T) {
	resetPromptCorrectionCache(t)
	prov := &scriptedBudgetProvider{}
	a := newBudgetAgent(t, prov)
	msgs := []provider.Message{{Role: provider.RoleUser, Content: strings.Repeat("x", 40_000)}}
	shape := a.requestCalibrationShape(provider.Request{Messages: msgs})
	base := a.estimatedShapeTokens(shape)
	if base <= 0 {
		t.Fatalf("base estimate = %d", base)
	}
	noteProviderPromptCorrection("scripted-budget", base*2, base)
	if got := a.estimatedShapeTokens(shape); got != base*2 {
		t.Fatalf("corrected estimate = %d, want %d (×2 factor)", got, base*2)
	}
	// a different provider key must leave this agent's estimates alone
	noteProviderPromptCorrection("some-other-route", base*3, base)
	if got := a.estimatedShapeTokens(shape); got != base*2 {
		t.Fatalf("estimate after foreign-provider note = %d, want %d", got, base*2)
	}
}

func resetPromptCorrectionCache(t *testing.T) {
	t.Helper()
	promptCorrectionCache.Lock()
	promptCorrectionCache.entries = map[string]promptCorrectionEntry{}
	promptCorrectionCache.Unlock()
	t.Cleanup(func() {
		promptCorrectionCache.Lock()
		promptCorrectionCache.entries = map[string]promptCorrectionEntry{}
		promptCorrectionCache.Unlock()
	})
}
