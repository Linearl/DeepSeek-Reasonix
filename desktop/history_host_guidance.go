package main

import (
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

// hostGuidanceRows renders a host-generated guidance message as a notice row — the same
// shape mid-turn steers use (↪ text).
//
// The user has to be able to see why the host interjected: readiness catch-up, the plan
// research gate, finalization nudges, goal redirects, incomplete-read continuations and
// loop corrections all change what the agent is asked to do next, and a transcript that
// silently skips them reads as the host interrupting for no reason (task 172).
//
// The row is a notice rather than a user bubble on purpose: guidance is not something the
// user said, and marking it as user-authored would corrupt turn attribution
// (IsUserAuthoredTurnMessage stays false for every host message).
//
// Internal protocol messages stay out of the transcript entirely — a compaction briefing
// is a machine contract, not conversation (see agent.IsHostProtocolMessage).
func hostGuidanceRows(m provider.Message) ([]HistoryMessage, bool) {
	if !agent.IsHostGeneratedUserMessage(m) || agent.IsHostProtocolMessage(m) {
		return nil, false
	}
	text := strings.TrimSpace(agent.StripTransientUserBlocks(m.Content))
	if text == "" {
		return nil, false
	}
	return []HistoryMessage{{Role: "notice", Content: "↪ " + text}}, true
}
