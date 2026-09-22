package agent

import (
	"strings"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

func TestDedupeProviderVisibleResultOmitsExactRepeats(t *testing.T) {
	a := &Agent{}
	first := a.dedupeProviderVisibleResult("read_file", "c1", "hello world", "hello world")
	if first != "hello world" {
		t.Fatalf("first = %q", first)
	}
	second := a.dedupeProviderVisibleResult("read_file", "c2", "hello world", "hello world")
	if !strings.Contains(second, "duplicate tool result") || !strings.Contains(second, "c1") {
		t.Fatalf("second = %q", second)
	}
}

func TestDedupeUsesRawResultBeforeLossySummary(t *testing.T) {
	a := &Agent{}
	visible := "same bounded failure summary"
	first := a.dedupeProviderVisibleResult("read_file", "c1", "prefix hidden-one suffix", visible)
	second := a.dedupeProviderVisibleResult("read_file", "c2", "prefix hidden-two suffix", visible)
	if first != visible || second != visible {
		t.Fatalf("distinct raw results were deduped: first=%q second=%q", first, second)
	}
}

func TestResolvedSkipOutcomeDedupesRepeatedLocalDiscovery(t *testing.T) {
	a := &Agent{}
	result := `{"id":"mcp-tool:server/read","input_schema":{"type":"object"}}`
	plan := func(id string) *toolCallPlan {
		return &toolCallPlan{call: provider.ToolCall{ID: id, Name: "use_capability", Arguments: `{}`}}
	}
	resolved := tool.ResolvedCall{ProxyAction: "inspect", SkipExecute: true, ReadOnly: true, Result: result}
	first := a.resolvedSkipOutcome(plan("c1"), resolved)
	if first.output != result {
		t.Fatalf("first output = %q", first.output)
	}
	second := a.resolvedSkipOutcome(plan("c2"), resolved)
	if !strings.Contains(second.output, "duplicate tool result omitted") || !strings.Contains(second.output, "c1") {
		t.Fatalf("second output = %q", second.output)
	}
	if second.rawOutput != result {
		t.Fatalf("raw output = %q, want complete local result", second.rawOutput)
	}
}

func TestSummarizeCIOutputKeepsFailures(t *testing.T) {
	body := "##teamcity[testFailed name='A']\n" + strings.Repeat("ok\n", 400) + "exit status 1\n"
	got := summarizeCIOutput(body)
	if !strings.Contains(got, "exit_code: 1") || !strings.Contains(got, "testFailed") {
		t.Fatalf("summary = %q", got)
	}
	if len(got) >= len(body) {
		t.Fatal("summary should be smaller than the raw CI log")
	}
}

// TestDedupeExemptsTimeSensitiveStatusTools pins the task-214 second finding:
// read_collab_status polls mutable host state, so two identical readings are
// an incremental "nothing changed" signal, not a wasted repeat. Ordinary
// tools keep the existing dedup (leak-prevention semantics unchanged).
func TestDedupeExemptsTimeSensitiveStatusTools(t *testing.T) {
	a := &Agent{}
	status := `{"batch":"b1","sessions":[{"id":"s1","state":"running"}]}`
	first := a.dedupeProviderVisibleResult("read_collab_status", "c1", status, status)
	second := a.dedupeProviderVisibleResult("read_collab_status", "c2", status, status)
	if first != status || second != status {
		t.Fatalf("time-sensitive status result was deduped: first=%q second=%q", first, second)
	}
	ordinary := "same payload body"
	if got := a.dedupeProviderVisibleResult("read_file", "c3", ordinary, ordinary); got != ordinary {
		t.Fatalf("ordinary first call was altered: %q", got)
	}
	if got := a.dedupeProviderVisibleResult("read_file", "c4", ordinary, ordinary); !strings.Contains(got, "duplicate tool result") {
		t.Fatalf("ordinary repeat lost dedup: %q", got)
	}
}
