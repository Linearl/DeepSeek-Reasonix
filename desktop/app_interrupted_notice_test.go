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
