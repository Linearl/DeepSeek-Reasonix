package control

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/event"
)

// Task 544 — ask 答复后自动续跑（实验子选项，铁律 2 默认关）。
//
// 现场实锤（fork开发 2026-08-31，docs/issues/reports 问题诊断归档）：ask 已答复
// （decision_id 落账），但续跑的模型回合被终态错误打死 —— turn 被记
// `interrupted_turn:{pending:true}`，goal FSM 走 "stays running for the next
// user turn"、普通会话则直接闲置；用户需连发两条「继续」（第一条被中断恢复
// 块的消费形态吃掉）才真正推进。
//
// 本子选项的语义边界（与 477 分工）：
//   - 477 管「超时多久视作拒绝」——477 关态的超时 = task 109 B4 终态停止，
//     本件**不得**复活它（无 ask 被答复，标记天然为空，另加显式排除）；
//   - 本件管「答复后是否自动续跑」——只针对 **已有答复落账**（人工答复或
//     477 超时拒绝/低风险即时自答）之后回合仍异常终止的形态，补一发
//     **一次性** 宿主续跑回合，把已记录的决策带给模型。
//
// 自限性：标记每回合开始即清零、续跑触发即消费 —— 续跑回合自身再死不会
// 连环触发（它在启动时已清掉标记，且死的若是同一 provider 故障，续跑回合
// 撑不到下一次 ask 答复）。

// askAutoContinueNoticeCode is the grep-able notice code; the paired
// [ask-autocontinue] log line is the other half of the 判据锚 (task 477 shape).
const askAutoContinueNoticeCode = "ask_auto_continue"

// askAutoContinueTurnFormat is the host-owned continuation prompt. It carries
// the recorded decision and the fact of the stop; the raw provider error stays
// out of the prompt (it is already redacted/surfaced by the turn's own trail).
const askAutoContinueTurnFormat = "Your mid-turn questions were answered and the decision was recorded (%s), but the turn stopped immediately after before the work could continue. Resume the interrupted work from that recorded decision now: pick up where the turn stopped and carry it to completion. Do not re-ask the answered questions."

// markTurnAskAnswered records that an ask of the running turn was answered
// (human reply or host auto-answer) with a one-line decision summary. The
// summary rides the task-544 continuation prompt when the turn stops anyway.
func (c *Controller) markTurnAskAnswered(summary string) {
	if c == nil {
		return
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return
	}
	c.mu.Lock()
	c.turnAskAnsweredSummary = clipUTF8(summary, 240)
	c.mu.Unlock()
}

// clearTurnAskAnswered resets the per-turn marker; called at orchestrated-turn
// start so the marker covers exactly the turn that just ended.
func (c *Controller) clearTurnAskAnswered() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.turnAskAnsweredSummary = ""
	c.mu.Unlock()
}

// consumeTurnAskAnswered takes the marker one-shot: non-empty only when an ask
// was answered during the turn that just ended.
func (c *Controller) consumeTurnAskAnswered() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	summary := c.turnAskAnsweredSummary
	c.turnAskAnsweredSummary = ""
	return summary
}

// askDecisionSummary renders one compact line of "prompt: selection" pairs —
// the same shape the decision receipt carries, so transcript, receipt, and the
// task-544 continuation prompt all read identically.
func askDecisionSummary(questions []event.AskQuestion, answers []event.AskAnswer) string {
	selected := make(map[string][]string, len(answers))
	for _, answer := range answers {
		selected[answer.QuestionID] = append([]string(nil), answer.Selected...)
	}
	parts := make([]string, 0, len(questions))
	for _, question := range questions {
		answer := strings.TrimSpace(strings.Join(selected[question.ID], ", "))
		if answer == "" {
			answer = "—"
		}
		prompt := strings.TrimSpace(question.Prompt)
		if prompt == "" {
			prompt = strings.TrimSpace(question.Header)
		}
		if prompt == "" {
			prompt = question.ID
		}
		parts = append(parts, prompt+": "+answer)
	}
	return strings.Join(parts, " · ")
}

// maybeAskAutoContinueTurn fires the task-544 one-shot continuation after a
// terminal turn stop that followed an answered ask. It reports whether the
// continuation turn ran; err is the continuation's own result when it did.
// Every exclusion below keeps a stop that is someone else's contract exactly
// as it was: user cancellation, the 477-off terminal stop, and the gate-owned
// pauses (recovery/readiness) that carry their own recovery flows.
func (o *turnOrchestrator) maybeAskAutoContinueTurn(ctx context.Context, turnErr error) (bool, error) {
	c := o.c
	if c == nil || !c.autopilotAskAutoContinue {
		return false, nil
	}
	if turnErr == nil || ctx.Err() != nil {
		return false, nil
	}
	switch {
	case errors.Is(turnErr, context.Canceled), errors.Is(turnErr, context.DeadlineExceeded):
		// The user (or the run's wall clock) ended this turn on purpose.
		return false, nil
	case errors.Is(turnErr, ErrAutopilotAskUnanswered):
		// 477-off terminal stop: nothing was answered; the run must not be
		// resurrected behind 477's back.
		return false, nil
	}
	var recoveryPause *agent.RecoveryPauseError
	if errors.As(turnErr, &recoveryPause) {
		// Layer-② pause owns its own "send continue" contract.
		return false, nil
	}
	var readinessErr *agent.FinalReadinessError
	if errors.As(turnErr, &readinessErr) {
		// Layer-③ readiness boundary owns its own recovery card.
		return false, nil
	}
	summary := c.consumeTurnAskAnswered()
	if summary == "" {
		return false, nil
	}
	turnID, _, _, _ := c.turnEventRuntimeStatus()
	log.Printf("[ask-autocontinue] answered ask followed by a terminal stop; one continuation turn submitted (task 544) turn=%s", turnID)
	c.sink.Emit(event.Event{
		Kind:   event.Notice,
		Level:  event.LevelInfo,
		Code:   askAutoContinueNoticeCode,
		Text:   "ask auto-continue — the turn stopped right after the answer was recorded; resuming from the recorded decision (task 544)",
		Detail: "one host continuation turn carries the answered decision so the user does not have to send a follow-up message",
	})
	prompt := fmt.Sprintf(askAutoContinueTurnFormat, summary)
	err := o.runOrchestratedTurn(ctx, orchestratedTurn{input: prompt, raw: prompt, display: "", synthetic: true})
	return true, err
}
