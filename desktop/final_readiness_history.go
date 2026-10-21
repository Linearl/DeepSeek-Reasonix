package main

import (
	"fmt"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"strings"
)

func historyLocalOnlyRows(m provider.Message) ([]HistoryMessage, bool) {
	if !m.LocalOnly {
		return nil, false
	}
	// 任务1047: a LocalOnly recovery-replay message (recordInterruptedDisplay /
	// reasoning replay) carries the interrupted turn's ToolCalls so the
	// transcript keeps showing the tool blocks that ran before the
	// interruption. Every branch below historically returned early, so those
	// calls were silently swallowed — long sessions rendered "N consecutive
	// thinking rounds with no tool execution". The canonical assistant row
	// holding the calls now rides ahead of whatever local-only notice this
	// message also represents.
	rows := historyLocalOnlyToolCallRows(m)
	// ReadPause renders through the frontend's upsertReadPause and only
	// accepts exactly one history row — a leading tool-calls row would make
	// the pause read as the wrong row and drop it. A paused read cannot have
	// executed display calls, so the calls stay swallowed there (status quo).
	if m.ReadPause != nil {
		if len(rows) > 0 {
			return append(rows, HistoryMessage{Role: "notice", Code: event.TurnOutcomeIncompleteRead, Level: "info", ReadPause: m.ReadPause}), true
		}
		return []HistoryMessage{{Role: "notice", Code: event.TurnOutcomeIncompleteRead, Level: "info", ReadPause: m.ReadPause}}, true
	}
	if m.ReadCompletion != nil {
		return append(rows, HistoryMessage{Role: "notice", Code: "read_completion", Level: "info", Content: "Partial read coverage was accepted for this turn.", Detail: formatReadCompletionDetail(m.ReadCompletion), ReadCompletion: m.ReadCompletion}), true
	}
	if len(m.ProtocolRecovery) > 0 {
		if r, ok := provider.DecodeProtocolRecovery(m.ProtocolRecovery); ok && r.State == "pending" {
			return append(rows, HistoryMessage{Role: "notice", Code: "protocol_recovery", Level: "info", Pending: true, ProtocolRecovery: &provider.ProtocolRecoveryAction{ID: r.ID}}), true
		}
		return rows, len(rows) > 0
	}
	if recovery := m.FinalReadinessRecovery; recovery != nil && recovery.Pending {
		return append(rows, HistoryMessage{
			Role: "notice", Code: event.NoticeCodeFinalReadiness, Level: "info", Pending: true,
			Content: "Task is not complete; continue the remaining work or checks.",
			Readiness: &event.FinalReadiness{
				Attempts: 1,
				Missing:  append([]string(nil), recovery.Missing...),
			},
		}), true
	}
	if steerRows, handled := historySteerRows(m.Content, true); handled {
		return append(rows, steerRows...), true
	}
	return rows, len(rows) > 0
}

// historyLocalOnlyToolCallRows converts the display tool calls a LocalOnly
// recovery message carries into the canonical assistant-plus-calls row the
// frontend's item conversion already understands (任务1047). A message with
// no calls contributes nothing; the caller still decides whether its
// surrounding local-only notice is handled on its own.
func historyLocalOnlyToolCallRows(m provider.Message) []HistoryMessage {
	if m.Role == provider.RoleAssistant || m.Role == provider.RoleTool {
		// The assistant/tool branches of the normal conversion already render
		// these calls; only foreign-role LocalOnly carriers need the bridge.
		return nil
	}
	converted, visible := historyMessageRow(provider.Message{
		Role:             provider.RoleAssistant,
		ToolCalls:        m.ToolCalls,
		ReasoningContent: m.ReasoningContent,
	}, "", nil, nil, nil)
	if !visible || len(converted.ToolCalls) == 0 {
		return nil
	}
	return []HistoryMessage{converted}
}

func formatReadCompletionDetail(r *provider.ReadCompletion) string {
	if r == nil {
		return ""
	}
	parts := make([]string, 0, len(r.Reads))
	for _, read := range r.Reads {
		parts = append(parts, fmt.Sprintf("%s · %s · covered=%v", read.Path, read.Verdict, read.Covered))
	}
	return strings.Join(parts, "\n")
}
