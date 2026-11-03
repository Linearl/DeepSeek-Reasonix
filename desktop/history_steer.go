package main

import (
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

func historySteerRows(m provider.Message, unapplied bool) ([]HistoryMessage, bool) {
	text, handled := agent.ReplaySteerText(m.Content)
	if !handled {
		return nil, false
	}
	// tl1347: mid-turn steer bodies embed the controller's frozen reference
	// preamble ("Referenced context:" + <image>/<file>/<dir>/<resource> XML
	// blocks — the model-side representation of the user's @-references).
	// The user-turn replay path strips it (historyReplayUserContent); the
	// steer path never did, so the raw XML leaked into replayed steer
	// bubbles. Same strip here; a reference-only steer then has no visible
	// body and is dropped by the empty guard below.
	if stripped := control.StripReferencedContextPrefix(text); stripped != text {
		text = strings.TrimSpace(stripped)
	}
	if text == "" {
		return nil, true
	}
	if unapplied {
		return []HistoryMessage{{
			Role:      "notice",
			Content:   agent.UnappliedSteerNotice(text),
			Code:      event.NoticeCodeUnappliedSteer,
			Level:     "warn",
			CreatedAt: m.CreatedAt,
		}}, true
	}
	// 任务998：steer 行带 CreatedAt（后端不带则前端退化为重建时刻）。
	return []HistoryMessage{{Role: "notice", Content: "↪ " + text, CreatedAt: m.CreatedAt}}, true
}
