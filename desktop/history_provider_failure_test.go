package main

import (
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

func TestHistoryRestoresProviderFailureInsteadOfInterruptedNotice(t *testing.T) {
	message := provider.Message{
		Role: provider.RoleTool, LocalOnly: true,
		InterruptedTurn: &provider.InterruptedTurnRecovery{
			Pending: true, TerminalStatus: "failed",
			FailureDiagnostic: &provider.FailureDiagnostic{Kind: "request", Status: 404, ProviderID: "deepseek-anthropic", ProviderDisplayName: "Deepseek2", Protocol: "openai", RequestPath: "/anthropic/v1/chat/completions"},
		},
	}
	history := historyMessages([]provider.Message{message}, func(value string) string { return value })
	if len(history) != 1 {
		t.Fatalf("history = %+v", history)
	}
	got := history[0]
	if got.Code != event.NoticeCodeProviderRequestFailed || got.Level != "warn" || !strings.Contains(got.Content, "Deepseek2 · Chat Completions") || !strings.Contains(got.Content, "HTTP 404") {
		t.Fatalf("failure history = %+v", got)
	}
	if got.Detail != "Connection ID: deepseek-anthropic\nRequest path: /anthropic/v1/chat/completions" || got.Diagnostic == nil || got.Diagnostic.ProviderID != "deepseek-anthropic" {
		t.Fatalf("failure detail = %+v", got)
	}
}

func TestHistoryKeepsLegacyAndExplicitInterruptionsCompatible(t *testing.T) {
	for _, recovery := range []*provider.InterruptedTurnRecovery{{Pending: true}, {Pending: true, TerminalStatus: "interrupted"}} {
		history := historyMessages([]provider.Message{{Role: provider.RoleTool, LocalOnly: true, InterruptedTurn: recovery}}, func(value string) string { return value })
		if len(history) != 1 || history[0].Code != event.NoticeCodeCancelledTurn {
			t.Fatalf("interrupted history = %+v", history)
		}
	}
}

// 任务1047 回归钉（恢复重放路径不丢工具块）：LocalOnly 恢复重放消息上挂的
// ToolCalls 必须转成规范的 assistant+toolCalls 行，随本地提示行一起进入
// 历史。修复前 historyLocalOnlyRows 的每条分支都提前 return，这些调用被
// 整条吞掉——长会话表现为「连续 N 段思考之间没有任何工具块」。
func TestHistoryKeepsToolCallsOnLocalOnlyRecoveryMessages(t *testing.T) {
	calls := []provider.ToolCall{
		{ID: "call-keep-1", Name: "bash", Arguments: `{"command":"ls"}`},
		{ID: "call-keep-2", Name: "read_file", Arguments: `{"path":"notes.txt"}`},
	}
	message := provider.Message{
		Role: provider.RoleTool, LocalOnly: true, ToolCalls: calls,
		InterruptedTurn: &provider.InterruptedTurnRecovery{Pending: true, TerminalStatus: "interrupted"},
	}
	history := historyMessages([]provider.Message{
		{Role: provider.RoleUser, Content: "继续完成任务"},
		message,
		{Role: provider.RoleUser, Content: "继续"},
	}, func(value string) string { return value })
	if len(history) != 4 {
		t.Fatalf("history = %+v, want user + tool-calls row + cancelled notice + user", history)
	}
	row := history[1]
	if row.Role != "assistant" || len(row.ToolCalls) != 2 {
		t.Fatalf("local-only tool calls = %+v, want an assistant row carrying both calls", row)
	}
	if row.ToolCalls[0].ID != "call-keep-1" || row.ToolCalls[0].Name != "bash" || row.ToolCalls[1].ID != "call-keep-2" {
		t.Fatalf("tool call order/content changed: %+v", row.ToolCalls)
	}
	if history[2].Role != "notice" || history[2].Code != event.NoticeCodeCancelledTurn {
		t.Fatalf("cancelled-turn notice = %+v", history[2])
	}

	// ReadPause keeps its single-row contract (upsertReadPause consumes exactly
	// one history row); a call-carrying LocalOnly carrier on the assistant/tool
	// roles already renders through the normal conversion without the bridge.
	withReadPause := historyMessages([]provider.Message{{
		Role: provider.RoleTool, LocalOnly: true, ToolCalls: []provider.ToolCall{{ID: "call-keep-3", Name: "bash"}},
		ReadPause: &provider.ReadPause{ID: "pause-1"},
	}}, func(value string) string { return value })
	if len(withReadPause) != 1 || withReadPause[0].Code != event.TurnOutcomeIncompleteRead || withReadPause[0].ReadPause == nil {
		t.Fatalf("read-pause history = %+v, want the single read-pause notice", withReadPause)
	}
}

// 任务1047: an interrupted LocalOnly carrier on an assistant/tool role already
// renders its calls through the normal conversion — the bridge must not emit a
// second copy, and the cancelled-turn notice must still follow (unchanged
// behaviour the pre-existing tests pin).
func TestHistoryLocalOnlyToolCallBridgeSkipsAssistantCarriers(t *testing.T) {
	for _, role := range []provider.Role{provider.RoleAssistant, provider.RoleTool} {
		history := historyMessages([]provider.Message{{
			Role: role, LocalOnly: true, ToolCalls: []provider.ToolCall{{ID: "call-dup", Name: "bash"}},
			InterruptedTurn: &provider.InterruptedTurnRecovery{Pending: true},
		}}, func(value string) string { return value })
		if len(history) != 2 {
			t.Fatalf("role %s history = %+v, want the normal assistant row + cancelled notice", role, history)
		}
		if history[0].Role != "assistant" || len(history[0].ToolCalls) != 1 || history[0].ToolCalls[0].ID != "call-dup" {
			t.Fatalf("role %s assistant row = %+v, want exactly one call", role, history[0])
		}
		if history[1].Role != "notice" || history[1].Code != event.NoticeCodeCancelledTurn {
			t.Fatalf("role %s cancelled notice = %+v", role, history[1])
		}
	}
}
