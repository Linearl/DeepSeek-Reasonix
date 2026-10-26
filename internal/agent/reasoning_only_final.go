package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"reasonix/internal/event"
	"reasonix/internal/i18n"
	"reasonix/internal/provider"
)

// tl-1166: a reasoning-only final. The model spends minutes thinking, then
// ends the turn with an EMPTY visible answer — the full answer lives in the
// reasoning channel only, and the user must expand the thinking panel to find
// it (the 2026-10-26 incident: 5min50s of thinking, empty body). Root causes:
// isEmptyStreamResult counts reasoning as content (no empty-retry trigger),
// the stream retry whitelist has no "empty" lane, and reasoning is an
// attempt-level buffer so an automatic full-round retry would discard it.
//
// 三方案取舍（用户 2026-10-26 拍板）：
//   - A（改判空据 + 白名单自动重试整轮）已否决，禁止复活：reasoning 在
//     deferredStreamSink 是 attempt 级缓冲未落库，重试 = 丢弃全部已收思考 +
//     重新整轮采样 —— 5 分钟思考白烧（token+时间），新轮仍可能只思考。
//   - B（主）：识别 reasoning-only 终轮后，把已收 reasoning 作为合法历史
//     （提交边界本就带 ReasoningContent 落库），再经既有 HostGeneratedUser
//     注入通道提醒模型直接作答。限 1 次。
//   - C（保底）：B 之后的轮次仍 reasoning-only 时，把两段思考合成为正文
//     展示（原始轮思考 + 续写轮思考，按时间序，标注「思考 1 / 思考 2」），
//     附重试按钮 notice。B 失败时第一次思考绝不能丢。
const maxReasoningOnlyContinuations = 1

// reasoningOnlyFinalShape reports the exact B/C trigger shape: the turn ended
// cleanly with an empty (or whitespace-only) visible answer, non-empty
// reasoning, and NO structured work — no tool calls, no Responses-API items,
// no server-side search. A round with any of those did real work and must
// never be treated as a malfunction (normal tool rounds stay untouched).
// calls==0 is normally guaranteed by the no-tool-calls branch at the commit
// boundary; the parameter stays explicit so the shape check cannot be bypassed
// by a future caller.
func reasoningOnlyFinalShape(text, reasoning string, calls []provider.ToolCall, responsesItems []json.RawMessage, serverSearch []provider.ServerSearchCall) bool {
	return strings.TrimSpace(text) == "" &&
		strings.TrimSpace(reasoning) != "" &&
		len(calls) == 0 &&
		len(responsesItems) == 0 &&
		len(serverSearch) == 0
}

// reasoningOnlyContinuationMessage is the model-facing B reminder (injected
// through the established HostGeneratedUserMessage channel, same seam as the
// empty-final / executor-handoff nudges).
func reasoningOnlyContinuationMessage() string {
	return "Your previous reply contained only a thinking process; the visible answer was empty. Reply now with the user-facing answer directly. Do not output any further thinking."
}

// maybeInjectReasoningOnlyContinuation is the B branch: one host reminder per
// turn asking the model to answer in the visible channel. The reasoning of the
// rejected final is already durable (the commit boundary stores it as
// ReasoningContent on the assistant message) — nothing is discarded, unlike
// the rejected plan A. Returns true when the reminder was injected and the
// tool loop should continue with a continuation round.
func (a *Agent) maybeInjectReasoningOnlyContinuation(state *turnRuntime, reasoning string, usage *provider.Usage) bool {
	if !a.svc.reasoningOnlyContinuation {
		return false
	}
	if state.terminal.reasoningOnlyContinues >= maxReasoningOnlyContinuations {
		return false
	}
	state.terminal.reasoningOnlyContinues++
	state.terminal.reasoningOnlyFirstReasoning = reasoning
	// tl-1166 留痕：turn 用用户消息的 CreatedAt 毫秒戳标识（agent 层无独立
	// turn id），injection 记第几次（恒为 1/1），reasoning_chars 支撑后续
	// 触发频率统计。
	slog.Info("agent: reasoning-only final — injecting one continuation reminder (tl-1166)",
		"session", a.recoveryLogSessionName(),
		"turn_created_at", a.activeTurnCreatedAt.Load(),
		"injection", state.terminal.reasoningOnlyContinues,
		"max", maxReasoningOnlyContinuations,
		"reasoning_chars", len(reasoning))
	a.svc.sink.Emit(event.Event{
		Kind:  event.Notice,
		Level: event.LevelInfo,
		Code:  event.NoticeCodeReasoningOnlyContinuation,
		Text:  i18n.M.ReasoningOnlyContinuation,
		Detail: fmt.Sprintf("injection=%d/%d reasoning_chars=%d",
			state.terminal.reasoningOnlyContinues, maxReasoningOnlyContinuations, len(reasoning)),
	})
	a.sess.conversation.Add(HostGeneratedUserMessage(a.withTurnPreferences(reasoningOnlyContinuationMessage())))
	a.contextManager().ObserveUsage(usage)
	return true
}

// synthesizeReasoningOnlyDisplay builds the C fallback body. first is the
// ORIGINAL turn's reasoning (the one B's reminder followed); second is the
// continuation round's reasoning when the model thought again instead of
// answering. Both segments always appear in chronological order — losing the
// first segment would discard the only visible answer the user can get.
func synthesizeReasoningOnlyDisplay(first, second string) string {
	var b strings.Builder
	b.WriteString(i18n.M.ReasoningOnlyFinalBody)
	b.WriteString("\n\n")
	writeReasoningSegment(&b, i18n.M.ReasoningOnlyThinking1, first)
	if trimmed := strings.TrimSpace(second); trimmed != "" {
		b.WriteString("\n\n")
		writeReasoningSegment(&b, i18n.M.ReasoningOnlyThinking2, second)
	}
	return b.String()
}

func writeReasoningSegment(b *strings.Builder, label, body string) {
	b.WriteString("**")
	b.WriteString(label)
	b.WriteString("**\n\n")
	b.WriteString(strings.TrimSpace(body))
}

// maybeSynthesizeReasoningOnlyDisplay is the C gate at the commit boundary:
// once B's single reminder is spent, any further reasoning-only final is
// converted into a visible body (both thinking segments) instead of ending the
// turn with an empty answer. Returns the replacement body and true when the
// fallback fired. Usage accounting stays with the caller: the synthesized body
// flows into the ordinary final path (handleFinalResponse observes usage
// there), so observing here too would double-count the round.
func (a *Agent) maybeSynthesizeReasoningOnlyDisplay(state *turnRuntime, text, reasoning string) (string, bool) {
	if state.terminal.reasoningOnlyContinues < maxReasoningOnlyContinuations {
		return text, false
	}
	synthesized := synthesizeReasoningOnlyDisplay(state.terminal.reasoningOnlyFirstReasoning, reasoning)
	slog.Info("agent: reasoning-only final persisted after continuation — synthesizing fallback body (tl-1166)",
		"session", a.recoveryLogSessionName(),
		"turn_created_at", a.activeTurnCreatedAt.Load(),
		"first_chars", len(state.terminal.reasoningOnlyFirstReasoning),
		"second_chars", len(strings.TrimSpace(reasoning)))
	a.svc.sink.Emit(event.Event{
		Kind:  event.Notice,
		Level: event.LevelWarn,
		Code:  event.NoticeCodeReasoningOnlyFinal,
		Text:  i18n.M.ReasoningOnlyFinal,
		Detail: fmt.Sprintf("first_chars=%d second_chars=%d",
			len(state.terminal.reasoningOnlyFirstReasoning), len(strings.TrimSpace(reasoning))),
	})
	return synthesized, true
}
