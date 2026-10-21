package main

import (
	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

func historySteerRows(m provider.Message, unapplied bool) ([]HistoryMessage, bool) {
	text, handled := agent.ReplaySteerText(m.Content)
	if !handled {
		return nil, false
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
