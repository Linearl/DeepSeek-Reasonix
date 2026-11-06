package agent

import (
	"context"
	"encoding/json"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1454: the workflow engine budgets runs by what a dispatched child spent.
// RunProfileSpec must record the tracker's usage into a ctx-installed sink on
// every exit (success and failure alike), and stay a no-op without one.
func TestSubagentUsageSinkRecordsChildUsage(t *testing.T) {
	sub := &scriptedProvider{name: "sub", turns: [][]provider.Chunk{
		{toolCallChunk("r1", "read_file", `{"path":"a.txt"}`),
			{Type: provider.ChunkUsage, Usage: &provider.Usage{PromptTokens: 100, CompletionTokens: 20}}, {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "done"},
			{Type: provider.ChunkUsage, Usage: &provider.Usage{PromptTokens: 50, CompletionTokens: 5}}, {Type: provider.ChunkDone}},
	}}
	parent := tool.NewRegistry()
	parent.Add(fakeReadFileTool{})
	task := NewTaskToolWithOptions(TaskToolOptions{Provider: sub, ParentRegistry: parent, SysPrompt: DefaultTaskSystemPrompt}).
		WithTranscripts(NewSubagentStore(t.TempDir()), t.TempDir(), "base-model", "base-effort")
	ctx := WithSubagentUsageSink(testTaskContext())
	if _, err := task.Execute(ctx, json.RawMessage(`{"prompt":"read it","tools":["read_file"]}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	usage, ok := SubagentUsage(ctx)
	if !ok {
		t.Fatal("usage sink installed but nothing was recorded")
	}
	if usage.Tokens != 175 || usage.Requests != 2 {
		t.Fatalf("recorded usage = %+v, want 175 tokens over 2 requests", usage)
	}
}

func TestSubagentUsageSinkRecordsOnFailure(t *testing.T) {
	task := NewTaskToolWithOptions(TaskToolOptions{Provider: &scriptedProvider{name: "sub"}, ParentRegistry: tool.NewRegistry()})
	ctx := WithSubagentUsageSink(testTaskContext())
	if _, err := task.RunProfileSpec(ctx, ProfileExecSpec{}); err == nil {
		t.Fatal("empty objective must fail")
	}
	if _, ok := SubagentUsage(ctx); !ok {
		t.Fatal("failure exit must still record (zero) usage so the budget sees every attempt")
	}
}

func TestSubagentUsageWithoutSinkIsNoop(t *testing.T) {
	ctx := context.Background()
	RecordSubagentUsage(ctx, SubagentUsageSummary{Tokens: 9})
	if _, ok := SubagentUsage(ctx); ok {
		t.Fatal("no sink installed, yet usage reported as recorded")
	}
}
