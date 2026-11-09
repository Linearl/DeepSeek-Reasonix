package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// tl1514 面二: the in-running loop breaker. The 2026-11-06 field incident
// (deepseek-flash 会话反复「确认图片路径→读取」同一动作 10 分 32 秒 51 次工具调
// 用) exposed the hole the retired no-progress guards left: upstream #10223
// removed the only in-round stoppers because they counted ROUNDS and could cut
// legitimate long read chains — but a loop is not a long chain: it repeats the
// same call shape while producing zero new host evidence. This breaker judges
// exactly that pair (同形工具调用 + 无新 host 证据), so a long chain of
// DIFFERENT reads never trips and a repeating loop does.
//
// 判据口径（沿用 tl1460 朴素判据风格——只读既有真相源，零新状态源）：
//   - 同形: this round's (tool name, arguments) sequence equals the previous
//     round's, byte for byte. A stuck weak model re-emits the same call.
//   - 无新 host 证据: no successful write/command receipt landed
//     (ledger.HasWriteOrCommandSince — the loopGuardAllowsFinal primitive),
//     no NEW unique read identity appeared (the reads.visible set the review
//     layer's TurnOutputSignatures face also reads), and no canonical todo
//     advanced to completed (canonicalTodoProgress).
//   - 打断: after threshold consecutive zero-gain same-shape rounds the turn
//     ends as recovery_paused with StopReason=loop_breaker (the text_repeat
//     exit shape — completed work is kept, the cause is visible, "continue"
//     resumes). tl1460's turn-end review never sees the turn (it reviews
//     clean turns only), so the two layers cannot fight: the review layer
//     owns 轮末, this owns running 内, and both put the user back in control.
//
// 验收④: a consumed mid-turn steer resets the count and tightens the window
// to loopBreakerSteerThreshold (≤2 tool boundaries) — the user's direction
// change gets two rounds to take effect; past that the breaker interrupts
// instead of letting the model ignore the guidance for the rest of the turn.
//
// 阈值是 package var（stopAutoGraceAfter / deterministicBreakStreak 的既定模
// 式——生产阈值集中可审，测试缩到毫秒/个位）。≤0 = 关闭（回退开关）；生产值候
// 校准（tl1460 占位 5 同款，tl1459 聚合器出数后定值）。
var (
	// loopBreakerRoundThreshold: consecutive same-shape zero-gain rounds that
	// trip the breaker on a turn with no steer. Placeholder 5, 候校准.
	loopBreakerRoundThreshold = 5
	// loopBreakerSteerThreshold: the tightened window after a consumed steer
	// (验收④「≤2 个工具边界」).
	loopBreakerSteerThreshold = 2
)

// loopBreakerPollTools: legitimate same-shape waiting faces. A turn polling
// bash_output/wait/event_wait repeats the same call ON PURPOSE — the background
// job is the progress. The breaker never judges a round made only of these
// (conservative direction: 漏判可再点停止，误判会截断真实等待).
var loopBreakerPollTools = map[string]bool{
	"wait":        true,
	"bash_output": true,
	"event_wait":  true,
}

// loopBreakerRoundSignature is the round's call-shape identity: every call's
// (resolved name, raw arguments) in provider order. Any difference resets the
// streak — the model changed its behavior, which is exactly what the breaker
// exists to wait for.
func loopBreakerRoundSignature(calls []provider.ToolCall) string {
	var sb strings.Builder
	for i := range calls {
		name := calls[i].Name
		if calls[i].ResolvedName != "" {
			name = calls[i].ResolvedName
		}
		sb.WriteString(name)
		sb.WriteByte(0)
		sb.WriteString(calls[i].Arguments)
		sb.WriteByte(0)
	}
	return sb.String()
}

// loopBreakerReadIdentities counts the distinct read identities this turn's
// visible read deliveries carry (same face recentReadPaths walks). Re-reading
// the same path adds no identity — that is what makes the field incident's
// repeated 确认路径→读取 rounds zero-gain while a genuine file sweep keeps
// growing the set.
func (a *Agent) loopBreakerReadIdentities() int {
	seen := make(map[string]struct{}, len(a.reads.visible))
	for _, delivery := range a.reads.visible {
		identity := strings.TrimSpace(delivery.source.Identity)
		if identity == "" {
			continue
		}
		seen[identity] = struct{}{}
	}
	return len(seen)
}

// loopBreakerCheck judges the tool round that just finished and returns the
// typed pause when the breaker trips. mark is the evidence-ledger length from
// just before the round's batch ran (captured in runToolLoop next to the
// handleToolRound call). Callers run it only when the round returned cont=true.
func (a *Agent) loopBreakerCheck(ctx context.Context, state *turnRuntime, step int, calls []provider.ToolCall, mark int) *RecoveryPauseError {
	if ctx.Err() != nil {
		// The stop ladder owns this exit; never stack a second terminal face
		// over a user stop.
		return nil
	}
	if loopBreakerRoundThreshold <= 0 {
		return nil // the documented kill switch: ≤0 disables the breaker
	}
	if len(calls) == 0 {
		return nil
	}
	pollOnly := true
	for i := range calls {
		name := calls[i].Name
		if calls[i].ResolvedName != "" {
			name = calls[i].ResolvedName
		}
		if !loopBreakerPollTools[name] {
			pollOnly = false
			break
		}
	}
	if pollOnly {
		return nil
	}

	// Gain half (无新 host 证据). First evaluation of the turn seeds the
	// baselines instead of judging them.
	reads := a.loopBreakerReadIdentities()
	todos, _ := a.canonicalTodoProgress()
	gain := false
	if a.task.ledger != nil && a.task.ledger.HasWriteOrCommandSince(mark) {
		gain = true
	}
	if reads > state.loopBreakerReads || todos > state.loopBreakerTodosComplete {
		gain = true
	}
	state.loopBreakerReads = reads
	state.loopBreakerTodosComplete = todos

	sig := loopBreakerRoundSignature(calls)
	if gain || sig != state.loopBreakerSig {
		state.loopBreakerSig = sig
		state.loopBreakerStreak = 1
		return nil
	}
	state.loopBreakerStreak++

	threshold := loopBreakerRoundThreshold
	boosted := false
	if state.loopBreakerSteerStep >= 0 && step-state.loopBreakerSteerStep <= loopBreakerSteerThreshold {
		threshold = loopBreakerSteerThreshold
		boosted = true
	}
	if state.loopBreakerStreak < threshold {
		return nil
	}

	names := make([]string, 0, len(calls))
	for i := range calls {
		name := calls[i].Name
		if calls[i].ResolvedName != "" {
			name = calls[i].ResolvedName
		}
		names = append(names, name)
	}
	detail := fmt.Sprintf("cause=loop_breaker rounds=%d/%d tools=%s", state.loopBreakerStreak, threshold, strings.Join(names, ","))
	if boosted {
		detail += " boost=steer_window"
	}
	slog.Warn("agent: loop breaker interrupted the turn (tl1514 面二)",
		"streak", state.loopBreakerStreak, "threshold", threshold,
		"steer_boost", boosted, "tools", strings.Join(names, ","),
		"session", a.recoveryLogSessionName())
	a.svc.sink.Emit(event.Event{
		Kind:  event.Notice,
		Level: event.LevelWarn,
		Code:  event.NoticeCodeLoopBreaker,
		Text: fmt.Sprintf("loop breaker: the same tool call repeated %d times with no new evidence; the turn was interrupted to stop the loop (%d/%d)",
			state.loopBreakerStreak, state.loopBreakerStreak, threshold),
		Detail: detail,
	})
	message := fmt.Sprintf("Stopped: the same tool call ran %d times with no new result, so the turn was interrupted to stop the loop. Completed work is kept. Send a follow-up with what should change — or \"continue\" to let the task try again.",
		state.loopBreakerStreak)
	return &RecoveryPauseError{
		Message:    message,
		StopReason: "loop_breaker",
		Detail:     detail,
	}
}
