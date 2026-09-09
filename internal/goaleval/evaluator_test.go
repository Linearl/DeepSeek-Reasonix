package goaleval

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

type scriptedProvider struct {
	turns          []string // one response per call, recycled
	reasoningTurns []string // if non-empty, emit ChunkReasoning instead of ChunkText
	err            error    // stream-open error
	timeout        bool     // hang until ctx deadline
	usage          *provider.Usage
	calls          int
}

func (s *scriptedProvider) Name() string { return "scripted" }

func (s *scriptedProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	ch := make(chan provider.Chunk, 2)
	if s.timeout {
		<-ctx.Done()
		close(ch)
		return ch, nil
	}
	i := s.calls - 1
	if i >= len(s.turns) {
		i = len(s.turns) - 1
	}
	if s.reasoningTurns != nil {
		r := s.reasoningTurns[i%len(s.reasoningTurns)]
		ch <- provider.Chunk{Type: provider.ChunkReasoning, Text: r}
	} else {
		ch <- provider.Chunk{Type: provider.ChunkText, Text: s.turns[i]}
	}
	if s.usage != nil {
		ch <- provider.Chunk{Type: provider.ChunkUsage, Usage: s.usage}
	}
	close(ch)
	return ch, nil
}

func evaluate(t *testing.T, prov provider.Provider, evidence GoalEvidence) (Verdict, error) {
	t.Helper()
	s := NewSession(prov, nil)
	return s.Evaluate(context.Background(), evidence)
}

func TestEvaluateParsesVerdicts(t *testing.T) {
	for _, tc := range []struct {
		body    string
		outcome Outcome
	}{
		{`{"outcome":"complete","reason":"the goal is done"}`, OutcomeComplete},
		{`{"outcome":"continue","reason":"more work remains"}`, OutcomeContinue},
		{`{"outcome":"blocked","reason":"needs user input"}`, OutcomeBlocked},
		{`{"outcome":"uncertain","reason":"cannot judge"}`, OutcomeUncertain},
		// Fenced / prose-wrapped JSON is tolerated.
		{"```json\n{\"outcome\":\"complete\",\"reason\":\"done\"}\n```", OutcomeComplete},
		{"Here is my judgment: {\"outcome\":\"continue\",\"reason\":\"keep going\"}", OutcomeContinue},
	} {
		t.Run(tc.body, func(t *testing.T) {
			prov := &scriptedProvider{turns: []string{tc.body}}
			verdict, err := evaluate(t, prov, GoalEvidence{GoalContract: "fix the parser"})
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if verdict.Outcome != tc.outcome {
				t.Fatalf("outcome = %q, want %q", verdict.Outcome, tc.outcome)
			}
		})
	}
}

func TestEvaluateReasoningOnly(t *testing.T) {
	// DeepSeek thinking SKUs put the final JSON verdict in reasoning_content with
	// an empty content block. boundedllm must surface it (not treat as "empty"),
	// so parseVerdict can extract the verdict.
	prov := &scriptedProvider{
		reasoningTurns: []string{`Let me judge. {"outcome":"continue","reason":"more work remains"}`},
	}
	verdict, err := evaluate(t, prov, GoalEvidence{GoalContract: "fix the parser"})
	if err != nil {
		t.Fatalf("Evaluate() error = %v (want no error for reasoning-only response)", err)
	}
	if verdict.Outcome != OutcomeContinue {
		t.Fatalf("outcome = %q, want %q", verdict.Outcome, OutcomeContinue)
	}
}

func TestEvaluateFailClosedOnBadResponses(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"empty", ""},
		{"invalid json", "{not json"},
		{"missing outcome", `{"reason":"no outcome"}`},
		{"invalid outcome", `{"outcome":"maybe","reason":"x"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prov := &scriptedProvider{turns: []string{tc.body}}
			if _, err := evaluate(t, prov, GoalEvidence{}); err == nil {
				t.Fatalf("Evaluate() error = nil, want fail-closed error for %q", tc.body)
			}
		})
	}
}

func TestEvaluateFailsOnProviderErrors(t *testing.T) {
	prov := &scriptedProvider{err: errors.New("provider exploded")}
	if _, err := evaluate(t, prov, GoalEvidence{}); err == nil {
		t.Fatal("Evaluate() error = nil, want provider error")
	}
}

func TestEvaluateTimesOut(t *testing.T) {
	prov := &scriptedProvider{timeout: true}
	s := NewSession(prov, nil)
	s.timeout = 50 * time.Millisecond
	start := time.Now()
	_, err := s.Evaluate(context.Background(), GoalEvidence{})
	if err == nil {
		t.Fatal("Evaluate() error = nil, want timeout")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Evaluate() took %v, want bounded timeout", elapsed)
	}
}

func TestEvaluateOverlongOutputFailsClosed(t *testing.T) {
	prov := &scriptedProvider{turns: []string{strings.Repeat("x", MaxOutputBytes+1024)}}
	if _, err := evaluate(t, prov, GoalEvidence{}); err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("Evaluate() error = %v, want output cap error", err)
	}
}

func TestEvaluateEmitsGoalEvaluatorUsage(t *testing.T) {
	usage := &provider.Usage{TotalTokens: 123, PromptTokens: 100, CompletionTokens: 23}
	prov := &scriptedProvider{turns: []string{`{"outcome":"continue","reason":"keep going"}`}, usage: usage}
	var seen *event.Event
	sink := event.FuncSink(func(e event.Event) {
		if e.Kind == event.Usage {
			seen = &e
		}
	})
	s := NewSessionWithSink(prov, nil, "test/model", sink)
	if _, err := s.Evaluate(context.Background(), GoalEvidence{GoalContract: "x"}); err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if seen == nil || seen.UsageSource != event.UsageSourceGoalEvaluator || seen.Usage == nil || seen.Usage.TotalTokens != 123 {
		t.Fatalf("usage event = %+v, want goal-evaluator attribution with tokens", seen)
	}
}

func TestEvidenceIsBoundedAndUntrusted(t *testing.T) {
	// Oversized evidence fields must be clipped, not rejected — and the JSON
	// stays valid.
	prov := &scriptedProvider{turns: []string{`{"outcome":"complete","reason":"done"}`}}
	ev := GoalEvidence{
		GoalContract:   strings.Repeat("g", 10_000),
		AssistantFinal: strings.Repeat("a", 10_000),
		TodoSummary:    strings.Repeat("t", 10_000),
	}
	_, err := evaluate(t, prov, ev)
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want clipped evidence accepted", err)
	}
	if prov.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", prov.calls)
	}
}

// #9678 / upstream #9679: thinking models quote example objects before the real
// verdict, so parsing must take the last complete top-level object.
func TestParseVerdictPrefersLastTopLevelObject(t *testing.T) {
	text := `I considered {"outcome":"continue"} but the goal is done. Final: {"outcome":"complete","reason":"all checks passed"}`
	v, err := parseVerdict(text)
	if err != nil {
		t.Fatalf("parseVerdict: %v", err)
	}
	if v.Outcome != OutcomeComplete || v.Reason != "all checks passed" {
		t.Fatalf("verdict = %+v, want the final object", v)
	}
}

func TestParseVerdictHandlesNestedStringsAndTrailingFragment(t *testing.T) {
	v, err := parseVerdict(`{"outcome":"continue","reason":"saw {\"a\":1} inside a string"}`)
	if err != nil || v.Outcome != OutcomeContinue {
		t.Fatalf("nested braces in a string broke parsing: %+v err = %v", v, err)
	}
	v, err = parseVerdict(`{"outcome":"complete","reason":"ok"} {"outcome":`)
	if err != nil || v.Outcome != OutcomeComplete {
		t.Fatalf("trailing fragment masked the last complete object: %+v err = %v", v, err)
	}
}
