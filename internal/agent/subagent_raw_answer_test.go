package agent

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// G1 regression (audit 2026-11-01, tl1325): the host wraps a contract-obeying
// child's answer with the adjudicated completion envelope
// ("status: …", "summary: …", criterion/host-lowered/unresolved lines) plus
// host receipts. That envelope is parent-model prose; a consumer that parses
// the answer programmatically must be able to retrieve the child's own final
// text untouched, via the ctx raw-answer bypass.
func TestSubagentRawAnswerBypassesHostEnvelope(t *testing.T) {
	ctx := WithSubagentRawAnswerSink(withNoClosedLoop(context.Background()))
	reg := tool.NewRegistry()
	reg.Add(fakeWriteFileTool{})
	AttachCompleteSubtaskTool(reg)
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{toolCallChunk("1", "write_file", `{"path":"parser.go"}`), {Type: provider.ChunkDone}},
		{toolCallChunk("2", "complete_subtask", `{"status":"complete","summary":"fixed the parser","acceptance_criteria":[{"id":"AC1","status":"satisfied","evidence":[{"kind":"diff","summary":"the fix","paths":["parser.go"]}]},{"id":"AC2","status":"satisfied","evidence":[{"kind":"verification","summary":"suite","command":"go test ./..."}]}],"unresolved":["integration suite not executed"]}`), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: `{"findings": [], "count": 0}`}, {Type: provider.ChunkDone}},
	}}

	answer, err := RunSubAgentWithSession(ctx, prov, reg, NewSession("sys"),
		"fix the parser", Options{}, event.Discard)
	if err != nil {
		t.Fatalf("RunSubAgentWithSession: %v", err)
	}

	// The parent-facing answer keeps its host envelope (status first,
	// receipts last) — nothing about the parent view changes.
	if !strings.HasPrefix(answer, "status: partial") {
		t.Fatalf("parent answer must still lead with the adjudicated status:\n%s", answer)
	}
	if !strings.Contains(answer, hostReceiptsHeader) {
		t.Fatalf("parent answer must still carry host receipts:\n%s", answer)
	}

	// The bypass carries the child's own final text, byte-identical, with no
	// status/summary prefix, no criterion/lowered/unresolved lines, and no
	// receipts block.
	raw := SubagentRawAnswer(ctx)
	if raw != `{"findings": [], "count": 0}` {
		t.Fatalf("raw answer bypass polluted or missing, got:\n%s", raw)
	}
}

// No sink on ctx (ordinary parent delegation): the recording is a no-op and
// the run behaves exactly as before.
func TestSubagentRawAnswerAbsentSinkIsNoop(t *testing.T) {
	ctx := withNoClosedLoop(context.Background())
	reg := tool.NewRegistry()
	reg.Add(fakeWriteFileTool{})
	AttachCompleteSubtaskTool(reg)
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{toolCallChunk("1", "write_file", `{"path":"parser.go"}`), {Type: provider.ChunkDone}},
		{toolCallChunk("2", "complete_subtask", `{"status":"complete","summary":"fixed the parser","acceptance_criteria":[{"id":"AC1","status":"satisfied","evidence":[{"kind":"diff","summary":"the fix","paths":["parser.go"]}]}]}`), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "all good"}, {Type: provider.ChunkDone}},
	}}

	answer, err := RunSubAgentWithSession(ctx, prov, reg, NewSession("sys"),
		"fix the parser", Options{}, event.Discard)
	if err != nil {
		t.Fatalf("RunSubAgentWithSession: %v", err)
	}
	if !strings.HasPrefix(answer, "status: complete") {
		t.Fatalf("parent answer unexpected:\n%s", answer)
	}
	if got := SubagentRawAnswer(ctx); got != "" {
		t.Fatalf("no sink installed, yet raw answer = %q", got)
	}
}
