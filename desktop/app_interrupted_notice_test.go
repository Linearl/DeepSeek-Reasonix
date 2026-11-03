package main

import (
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// Task 340 (upstream #10778, fixes #10254): the history-rebuild failure notice
// must carry the stored failure summary - the real provider error text, status
// line and response-body snippet, scrubbed when the record was written - not
// only the status class. Without it a reload showed "Provider request failed
// (HTTP 402)." while the actual cause stayed invisible.
func TestInterruptedTurnHistoryNoticeKeepsFailureSummary(t *testing.T) {
	recovery := &provider.InterruptedTurnRecovery{
		TerminalStatus:    "failed",
		FailureDiagnostic: &provider.FailureDiagnostic{Kind: "quota", Status: 402, ProviderID: "relay", Protocol: "openai"},
		FailureSummary:    "Relay · Chat Completions: status 402: insufficient balance",
	}
	notice := interruptedTurnHistoryNotice(recovery)
	if notice.Code != event.NoticeCodeProviderRequestFailed || notice.Level != "warn" {
		t.Fatalf("notice = %+v", notice)
	}
	if !strings.Contains(notice.Detail, "Connection ID: relay") {
		t.Fatalf("diagnostic detail lost: %q", notice.Detail)
	}
	if !strings.Contains(notice.Detail, "status 402: insufficient balance") {
		t.Fatalf("failure summary lost on reload: %q", notice.Detail)
	}
	if strings.Contains(notice.Content, "insufficient balance") {
		t.Fatalf("content stays the status-class message, got %q", notice.Content)
	}
}

// A record without a summary (older writer, or a build before task 340) keeps
// the exact pre-340 notice shape.
func TestInterruptedTurnHistoryNoticeWithoutSummaryUnchanged(t *testing.T) {
	recovery := &provider.InterruptedTurnRecovery{
		TerminalStatus:    "failed",
		FailureDiagnostic: &provider.FailureDiagnostic{Kind: "request", Status: 404, ProviderID: "relay", Protocol: "openai"},
	}
	notice := interruptedTurnHistoryNotice(recovery)
	if notice.Detail != "Connection ID: relay" {
		t.Fatalf("detail = %q, want the diagnostic detail only", notice.Detail)
	}
	if interruptedTurnHistoryNotice(nil).Code != event.NoticeCodeCancelledTurn {
		t.Fatal("nil recovery keeps the interrupted guidance")
	}
	interrupted := &provider.InterruptedTurnRecovery{TerminalStatus: "interrupted"}
	if notice := interruptedTurnHistoryNotice(interrupted); notice.Level != "info" || notice.Code != event.NoticeCodeCancelledTurn {
		t.Fatalf("interrupted recovery keeps the guidance notice, got %+v", notice)
	}
}

// tl1347 (second round): the interrupted turn's already-generated partial
// text must stay visible in the history (reload) projection, beside the
// interrupted guidance notice. The user-facing shape was "sent a message,
// got no reply at all" — the live reducer keeps the streamed text, but the
// reload projection runs through historyLocalOnlyRows first, and a silent
// regression there (an early handled=true without the content row) would
// black-hole the partial text on every tab reopen. The record shape here is
// exactly what agent.recordInterruptedDisplay persists (Role tool +
// LocalOnly + InterruptedTurn, local-only tool id/name).
func TestInterruptedTurnPartialTextStaysVisibleInHistoryProjection(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "查一下这个问题"},
		{
			Role:             provider.RoleTool,
			Content:          "中断前已生成的部分回答文本",
			ReasoningContent: "中断前的推理片段",
			ToolCallID:       provider.LocalOnlyToolID,
			Name:             provider.LocalOnlyToolName,
			LocalOnly:        true,
			InterruptedTurn: &provider.InterruptedTurnRecovery{
				TerminalStatus:          "interrupted",
				Pending:                 true,
				DroppedPartialText:      true,
				DroppedPartialReasoning: true,
			},
		},
	}
	rows := historyMessagesWithPlannerDisplaysAndLookups(msgs, func(s string) string { return s }, nil, nil, nil, nil)
	var textRow, noticeRow *HistoryMessage
	for i := range rows {
		r := &rows[i]
		switch {
		case strings.Contains(r.Content, "中断前已生成的部分回答文本"):
			textRow = r
		case r.Code == event.NoticeCodeCancelledTurn:
			noticeRow = r
		}
	}
	if textRow == nil {
		t.Fatalf("partial text lost from history projection; rows = %+v", rows)
	}
	if textRow.Role != "assistant" {
		t.Fatalf("partial text row role = %q, want assistant", textRow.Role)
	}
	if textRow.Reasoning != "中断前的推理片段" {
		t.Fatalf("partial reasoning lost from history projection: %q", textRow.Reasoning)
	}
	if noticeRow == nil {
		t.Fatalf("interrupted guidance notice missing beside the text; rows = %+v", rows)
	}
}

// A zero-output interruption (the stream died before any text) must not
// materialize an empty assistant row — only the guidance notice.
func TestInterruptedTurnZeroOutputHistoryProjectionNoticeOnly(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "查一下这个问题"},
		{
			Role:       provider.RoleTool,
			Content:    "",
			ToolCallID: provider.LocalOnlyToolID,
			Name:       provider.LocalOnlyToolName,
			LocalOnly:  true,
			InterruptedTurn: &provider.InterruptedTurnRecovery{
				TerminalStatus: "interrupted",
				Pending:        true,
			},
		},
	}
	rows := historyMessagesWithPlannerDisplaysAndLookups(msgs, func(s string) string { return s }, nil, nil, nil, nil)
	for _, r := range rows {
		if r.Role == "assistant" {
			t.Fatalf("empty assistant row leaked for a zero-output interruption; rows = %+v", rows)
		}
	}
	if len(rows) == 0 || !containsCancelledTurnNotice(rows) {
		t.Fatalf("interrupted guidance notice missing for a zero-output interruption; rows = %+v", rows)
	}
}

func containsCancelledTurnNotice(rows []HistoryMessage) bool {
	for _, r := range rows {
		if r.Code == event.NoticeCodeCancelledTurn {
			return true
		}
	}
	return false
}
