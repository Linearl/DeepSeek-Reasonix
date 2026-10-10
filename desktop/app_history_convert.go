package main

import (
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

func historyMessagesWithPlannerDisplays(msgs []provider.Message, resolveUserContent func(string) string, plannerTurns []plannerDisplayTurn, checkpointTurns map[int]int) []HistoryMessage {
	replayedTodoArgs := historyTodoArgsWithCompleteSteps(msgs)
	toolResults := historyToolResultsByID(msgs)
	return historyMessagesWithPlannerDisplaysAndLookups(msgs, resolveUserContent, plannerTurns, checkpointTurns, replayedTodoArgs, toolResults)
}

// historyMessageConvertState carries the cross-message state of a provider→
// HistoryMessage conversion pass: the planner-display queue (consumed in order
// per user-text hash) and the canonical-turn suppression a planner interrupt
// notice arms. Keeping it explicit lets the windowed history slice API convert
// one message at a time with exactly the same semantics as a full pass.
type historyMessageConvertState struct {
	plannerByUserHash     map[string][]plannerDisplayTurn
	suppressCanonicalTurn bool
}

func newHistoryMessageConvertState(plannerTurns []plannerDisplayTurn) *historyMessageConvertState {
	return &historyMessageConvertState{plannerByUserHash: plannerTurnsByUserHash(plannerTurns)}
}

func historyMessagesWithPlannerDisplaysAndLookups(
	msgs []provider.Message,
	resolveUserContent func(string) string,
	plannerTurns []plannerDisplayTurn,
	checkpointTurns map[int]int,
	replayedTodoArgs map[string]string,
	toolResults map[string]provider.Message,
) []HistoryMessage {
	out := make([]HistoryMessage, 0, len(msgs))
	state := newHistoryMessageConvertState(plannerTurns)
	for index, m := range msgs {
		out = append(out, state.convertHistoryMessage(index, m, resolveUserContent, checkpointTurns, replayedTodoArgs, toolResults)...)
	}
	return out
}

// convertHistoryMessage converts one provider message into its 0..n history
// rows. index is the message's position in the coordinate system of
// checkpointTurns (window-relative for the legacy full-pass callers, absolute
// for the windowed slice API).
func (state *historyMessageConvertState) convertHistoryMessage(
	index int,
	m provider.Message,
	resolveUserContent func(string) string,
	checkpointTurns map[int]int,
	replayedTodoArgs map[string]string,
	toolResults map[string]provider.Message,
) []HistoryMessage {
	var out []HistoryMessage
	if m.DecisionReceipt != nil {
		return append(out, HistoryMessage{
			Role:            "notice",
			Code:            event.NoticeCodeDecisionReceipt,
			Level:           "info",
			DecisionReceipt: cloneDecisionReceipt(m.DecisionReceipt),
		})
	}
	if rows, handled := historyLocalOnlyRows(m); handled {
		return append(out, rows...)
	}
	if state.suppressCanonicalTurn {
		if !agent.IsUserAuthoredTurnMessage(m) {
			// Host guidance keeps its notice row even inside a suppressed canonical
			// turn; every other non-authored message stays hidden here.
			if rows, handled := hostGuidanceRows(m); handled {
				return append(out, rows...)
			}
			return out
		}
		state.suppressCanonicalTurn = false
	}
	content := m.Content
	var checkpointTurn *int
	if m.Role == provider.RoleUser {
		// Mid-turn steer messages surface as a notice (matching the live
		// Steer event look) rather than a user bubble or filtered synthetic
		// (#4044); check the raw m.Content, not the trimmed display form.
		if rows, handled := historySteerRows(m.Content, false); handled {
			return append(out, rows...)
		}
		// Host guidance reaches the transcript as a notice row (task 172); internal
		// protocol messages keep falling through to the filter below.
		if rows, handled := hostGuidanceRows(m); handled {
			return append(out, rows...)
		}
		content = historyUserDisplayContent(m, resolveUserContent)
		if agent.IsHostGeneratedUserMessage(m) {
			return out
		}
		if turn, ok := checkpointTurns[index]; ok {
			turnCopy := turn
			checkpointTurn = &turnCopy
		}
	}
	hm, visible := historyMessageRow(m, content, checkpointTurn, replayedTodoArgs, toolResults)
	if visible {
		out = append(out, hm)
	}
	for _, receipt := range m.DecisionReceipts {
		if receipt == nil {
			continue
		}
		out = append(out, HistoryMessage{
			Role:            "notice",
			Code:            event.NoticeCodeDecisionReceipt,
			Level:           "info",
			DecisionReceipt: cloneDecisionReceipt(receipt),
		})
	}
	if m.LocalOnly && m.InterruptedTurn != nil {
		out = append(out, interruptedTurnHistoryNotice(m.InterruptedTurn))
	}
	if m.Role == provider.RoleUser {
		key := messageDisplayKey(agent.UserMessageText(m))
		if turns := state.plannerByUserHash[key]; len(turns) > 0 {
			out = append(out, cloneHistoryMessages(turns[0].Messages)...)
			state.suppressCanonicalTurn = plannerDisplaySuppressesCanonical(turns[0])
			state.plannerByUserHash[key] = turns[1:]
		}
	}
	return out
}

// consumeHistoryPlannerState advances only the cross-message planner state.
// Windowed pages call it for the prefix they do not render, so repeated user
// text and a planner interrupt at a page boundary behave exactly as one full
// conversion pass. Keep the early returns in lock-step with
// convertHistoryMessage: those rows never reach the planner attachment at its
// tail.
func (state *historyMessageConvertState) consumeHistoryPlannerState(m provider.Message, resolveUserContent func(string) string) {
	if m.DecisionReceipt != nil {
		return
	}
	if m.LocalOnly {
		if _, isSteer := agent.SteerText(m.Content); isSteer {
			return
		}
	}
	if state.suppressCanonicalTurn {
		if !agent.IsUserAuthoredTurnMessage(m) {
			return
		}
		state.suppressCanonicalTurn = false
	}
	if m.Role != provider.RoleUser {
		return
	}
	if agent.IsHostGeneratedUserMessage(m) {
		return
	}
	key := messageDisplayKey(agent.UserMessageText(m))
	if turns := state.plannerByUserHash[key]; len(turns) > 0 {
		state.suppressCanonicalTurn = plannerDisplaySuppressesCanonical(turns[0])
		state.plannerByUserHash[key] = turns[1:]
	}
}

func cloneDecisionReceipt(in *provider.DecisionReceipt) *provider.DecisionReceipt {
	if in == nil {
		return nil
	}
	copy := *in
	return &copy
}

func plannerDisplaySuppressesCanonical(turn plannerDisplayTurn) bool {
	for _, message := range turn.Messages {
		if message.Role == "notice" && message.Code == event.NoticeCodeCancelledTurn {
			return true
		}
	}
	return false
}

func historyPageFromProviderMessages(
	msgs []provider.Message,
	resolveUserContent func(string) string,
	plannerTurns []plannerDisplayTurn,
	checkpointTurns map[int]int,
	beforeTurn, limit int,
) HistoryPage {
	limit = normalizeHistoryPageLimit(limit)
	totalTurns := visibleHistoryUserTurns(msgs, resolveUserContent)
	if beforeTurn <= 0 || beforeTurn > totalTurns {
		beforeTurn = totalTurns
	}
	startTurn := max(beforeTurn-limit, 0)
	page := HistoryPage{
		StartTurn:  startTurn,
		EndTurn:    beforeTurn,
		TotalTurns: totalTurns,
		HasOlder:   startTurn > 0,
	}
	if len(msgs) == 0 || startTurn >= beforeTurn {
		page.Messages = []HistoryMessage{}
		return page
	}
	pageMessages, originalIndexes := providerMessagesForVisibleTurnRange(msgs, resolveUserContent, startTurn, beforeTurn)
	page.Messages = historyMessagesWithPlannerDisplaysAndLookups(
		pageMessages,
		resolveUserContent,
		plannerTurns,
		checkpointTurnsForProviderWindow(checkpointTurns, originalIndexes),
		historyTodoArgsWithCompleteSteps(msgs),
		historyToolResultsByID(msgs),
	)
	return page
}

func visibleHistoryUserTurns(msgs []provider.Message, resolveUserContent func(string) string) int {
	total := 0
	for _, msg := range msgs {
		if isVisibleHistoryUser(msg, resolveUserContent) {
			total++
		}
	}
	return total
}

func isVisibleHistoryUser(msg provider.Message, resolveUserContent func(string) string) bool {
	return agent.IsUserAuthoredTurnMessage(msg)
}

func providerMessagesForVisibleTurnRange(msgs []provider.Message, resolveUserContent func(string) string, startTurn, endTurn int) ([]provider.Message, []int) {
	out := make([]provider.Message, 0, len(msgs))
	indexes := make([]int, 0, len(msgs))
	turn := -1
	for index, msg := range msgs {
		if isVisibleHistoryUser(msg, resolveUserContent) {
			turn++
		}
		if turn < 0 {
			if startTurn == 0 {
				out = append(out, msg)
				indexes = append(indexes, index)
			}
			continue
		}
		if turn >= startTurn && turn < endTurn {
			out = append(out, msg)
			indexes = append(indexes, index)
		}
	}
	return out, indexes
}

func checkpointTurnsForProviderWindow(checkpointTurns map[int]int, originalIndexes []int) map[int]int {
	if len(checkpointTurns) == 0 || len(originalIndexes) == 0 {
		return nil
	}
	out := map[int]int{}
	for pageIndex, originalIndex := range originalIndexes {
		if turn, ok := checkpointTurns[originalIndex]; ok {
			out[pageIndex] = turn
		}
	}
	return out
}

func plannerTurnsByUserHash(turns []plannerDisplayTurn) map[string][]plannerDisplayTurn {
	out := map[string][]plannerDisplayTurn{}
	for _, turn := range turns {
		if strings.TrimSpace(turn.UserHash) == "" || len(turn.Messages) == 0 {
			continue
		}
		out[turn.UserHash] = append(out[turn.UserHash], turn)
	}
	return out
}

func cloneHistoryMessages(in []HistoryMessage) []HistoryMessage {
	if len(in) == 0 {
		return nil
	}
	out := make([]HistoryMessage, len(in))
	copy(out, in)
	for i := range out {
		if len(in[i].MemoryCitations) > 0 {
			out[i].MemoryCitations = append([]provider.MemoryCitation(nil), in[i].MemoryCitations...)
		}
		if len(in[i].ToolCalls) > 0 {
			out[i].ToolCalls = append([]HistoryToolCall(nil), in[i].ToolCalls...)
		}
	}
	return out
}

// historyMessageRow builds the canonical transcript row for one provider
// message and reports whether the row is visible in the desktop transcript.
func historyMessageRow(m provider.Message, content string, checkpointTurn *int, replayedTodoArgs map[string]string, toolResults map[string]provider.Message) (HistoryMessage, bool) {
	reasoning := ""
	if m.Role == provider.RoleAssistant || m.LocalOnly {
		reasoning = m.ReasoningContent
	}
	displayRole := string(m.Role)
	if m.LocalOnly {
		displayRole = "assistant"
	}
	hm := HistoryMessage{Role: displayRole, Content: content, CheckpointTurn: checkpointTurn, CreatedAt: m.CreatedAt, Reasoning: reasoning, WorkDurationMs: m.WorkDurationMs}
	if m.Role == provider.RoleAssistant && len(m.MemoryCitations) > 0 {
		hm.MemoryCitations = append([]provider.MemoryCitation(nil), m.MemoryCitations...)
	}
	if m.Role == provider.RoleUser && content != m.Content {
		replay := historyReplayUserContent(m.Content)
		if agent.ContainsMemoryCompilerExecution(m.Content) {
			// Never expose the compiler contract itself. A safely unwrapped
			// slash invocation is useful display metadata, though: it lets the
			// frontend restore the selected skill/subagent in history and trash.
			if strings.HasPrefix(strings.TrimSpace(replay), "/") && replay != content {
				hm.SubmitText = replay
			}
		} else if replay != content {
			hm.SubmitText = replay
		}
	}
	hm.ServerSearch = historyServerSearch(m.ServerSearch)
	if (m.Role == provider.RoleAssistant || m.LocalOnly) && len(m.ToolCalls) > 0 {
		hm.ToolCalls = make([]HistoryToolCall, len(m.ToolCalls))
		for i, tc := range m.ToolCalls {
			args := tc.Arguments
			if tc.Name == "todo_write" {
				if replayed, ok := replayedTodoArgs[tc.ID]; ok {
					args = replayed
				}
			}
			hm.ToolCalls[i] = historyToolCall(tc, args, toolResults[tc.ID])
		}
	}
	if m.Role == provider.RoleTool && !m.LocalOnly {
		hm.ToolCallID = m.ToolCallID
		hm.ToolName = m.Name
		hm.Content, hm.ToolResultArchived, hm.ToolResultError = historyToolResultContent(m.Content, m.ToolCallID != "")
		hm.Execution = m.ToolExecution
	}
	hasVisibleLocalContent := strings.TrimSpace(hm.Content) != "" || strings.TrimSpace(hm.Reasoning) != "" || len(hm.ToolCalls) > 0 || (!m.LocalOnly && m.Role == provider.RoleTool)
	return hm, !m.LocalOnly || hasVisibleLocalContent
}
