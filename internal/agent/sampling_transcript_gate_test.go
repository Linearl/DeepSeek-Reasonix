package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/extension"
	"reasonix/internal/extension/dispatch"
	"reasonix/internal/extension/protocol"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

func TestSamplingTranscriptGateBlocksInvalidArgumentsBeforeProvider(t *testing.T) {
	for _, args := range []string{`{"path":`, `[]`, `null`, `"string"`} {
		t.Run(args, func(t *testing.T) {
			mp := &mockProvider{name: "p", chunks: []provider.Chunk{{Type: provider.ChunkDone}}}
			a := New(mp, tool.NewRegistry(), NewSession("sys"), Options{}, event.Discard)
			req := provider.Request{Messages: []provider.Message{
				{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "a", Name: "read_file", Arguments: args}}},
				{Role: provider.RoleTool, ToolCallID: "a", Name: "read_file", Content: "result"},
			}}
			_, err := a.streamProviderRequest(context.Background(), nil, req)
			if err == nil {
				t.Fatal("invalid tool arguments reached provider")
			}
			if len(mp.requests) != 0 {
				t.Fatalf("invalid request sent %d times", len(mp.requests))
			}
		})
	}
}

// TestSamplingPoisonedEmptyWaitArgumentsRequestGoesOut is the 任务 930
// end-to-end acceptance test (验收①): a session whose history holds a
// committed wait call with arguments:"" (the multi-session poison pill — the
// call executed, so the never-ran repair whitelist never covered it) must
// still produce a provider request. Before 930 the gate rejected every such
// request ("transcript gate: tool arguments must be a JSON object"), leaving
// the session permanently unusable; after S1 the request goes out, and the
// outbound copy carries the normalized "{}".
func TestSamplingPoisonedEmptyWaitArgumentsRequestGoesOut(t *testing.T) {
	mp := &mockProvider{name: "p", chunks: []provider.Chunk{{Type: provider.ChunkDone}}}
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "wait for the jobs"})
	// Poison shape straight from the incident: assistant wait call executed
	// with an empty arguments string, followed by its completed result.
	s.Add(provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{
		{ID: "call_wait", Name: "wait", Arguments: ""},
	}})
	s.Add(provider.Message{Role: provider.RoleTool, ToolCallID: "call_wait", Name: "wait",
		Content: "No background jobs to wait for.", ToolRunState: provider.ToolRunCompleted})
	a := New(mp, tool.NewRegistry(), s, Options{}, event.Discard)

	req, err := a.buildSamplingRequest(context.Background(), CompactionTriggerPressure)
	if err != nil {
		t.Fatalf("poisoned session can no longer build a request: %v", err)
	}
	if _, err := a.streamProviderRequest(context.Background(), nil, req.req); err != nil {
		t.Fatalf("poisoned session request rejected: %v", err)
	}
	if len(mp.requests) != 1 {
		t.Fatalf("provider requests=%d, want 1", len(mp.requests))
	}
	var found bool
	for _, m := range mp.requests[0].Messages {
		for _, c := range m.ToolCalls {
			if c.Name == "wait" {
				found = true
				if c.Arguments != "{}" {
					t.Fatalf("outbound wait arguments=%q, want normalized %q", c.Arguments, "{}")
				}
			}
		}
	}
	if !found {
		t.Fatal("wait tool call missing from outbound request")
	}
	// The stored history keeps the original bytes: normalization happens on
	// the outbound copy only. Index 2 = system(0), user(1), assistant(2).
	if got := s.Messages[2].ToolCalls[0].Arguments; got != "" {
		t.Fatalf("stored arguments mutated: %q", got)
	}
}

func TestSamplingTranscriptGateRunsAfterProviderInterceptor(t *testing.T) {
	client := &fakeDispatchClient{interceptFn: func(ev protocol.InterceptEvent, payload json.RawMessage) (protocol.InterceptResult, error) {
		if ev != protocol.EventProviderRequest {
			return protocol.InterceptResult{Decision: protocol.DecisionContinue}, nil
		}
		var in dispatch.ProviderRequestPayload
		if err := json.Unmarshal(payload, &in); err != nil {
			return protocol.InterceptResult{}, err
		}
		// A schema-valid replacement can still contain a protocol-invalid tool
		// transcript. The final gate must inspect this replacement.
		var extra []protocol.ProviderMessage
		if err := json.Unmarshal([]byte(`[{"role":"assistant","tool_calls":[{"id":"a","name":"read_file","arguments":"[]"}]},{"role":"tool","tool_call_id":"a","name":"read_file","content":"result"}]`), &extra); err != nil {
			return protocol.InterceptResult{}, err
		}
		in.Request.Messages = append(in.Request.Messages, extra...)
		return replaceWith(t, in), nil
	}}
	d := newExtDispatcher(client, true, nil, extension.PointProviderRequest)
	mp := &mockProvider{name: "p", chunks: []provider.Chunk{{Type: provider.ChunkDone}}}
	a := New(mp, tool.NewRegistry(), NewSession("sys"), Options{Extensions: d}, event.Discard)
	if _, err := a.buildSamplingRequest(context.Background(), CompactionTriggerPressure); err == nil {
		t.Fatal("final interceptor replacement bypassed transcript gate")
	}
	if len(mp.requests) != 0 {
		t.Fatal("request preparation contacted provider")
	}
}

func TestSamplingTranscriptGatePreservesHealthyRequestBytes(t *testing.T) {
	mp := &mockProvider{name: "p", chunks: []provider.Chunk{{Type: provider.ChunkDone}}}
	a := New(mp, tool.NewRegistry(), NewSession("sys"), Options{}, event.Discard)
	req := provider.Request{MaxTokens: 1234, Temperature: provider.OptionalTemperature(0.25), Messages: []provider.Message{
		{Role: provider.RoleSystem, Content: "stable cache prefix"},
		{Role: provider.RoleUser, Content: "read both"},
		{Role: provider.RoleAssistant, ReasoningContent: "read files", ToolCalls: []provider.ToolCall{{ID: "a", Name: "read_file", Arguments: ` { "path": "one" } `}, {ID: "b", Name: "read_file", Arguments: `{}`}}},
		{Role: provider.RoleTool, ToolCallID: "b", Name: "read_file", Content: "two"},
		{Role: provider.RoleTool, ToolCallID: "a", Name: "read_file", Content: "one"},
	}}
	before, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := a.streamProviderRequest(context.Background(), nil, req)
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if len(mp.requests) != 1 {
		t.Fatalf("provider requests=%d", len(mp.requests))
	}
	got, err := json.Marshal(mp.requests[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, got) {
		t.Fatalf("healthy request changed\nbefore=%s\nafter=%s", before, got)
	}
	after, _ := json.Marshal(req)
	if !bytes.Equal(before, after) {
		t.Fatal("transcript gate mutated caller request")
	}
}
