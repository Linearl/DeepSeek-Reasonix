package control

// tl1460 默认停止审查层（判定源分层，前两层零 LLM）。
//
// 2026-11-06 早停事故的机制层收口：在此之前，一个会话想停就停——非 Goal
// 会话完全没有轮末审查（模型 end_turn 即停），Goal 会话的 FSM 只服务已立
// 目标。本层在每轮轮末（turn end）对停止意图做一次审查：
//
//	①确定性规则（必选，不新增状态源=宪法 III——只读既有真相源）：
//	    - 未完成 canonical todos           → 继续（todos_pending）
//	    - Delivery final readiness 缺证据  → 继续（readiness_gap）
//	    - plan 模式有未执行步骤            → 继续（plan_steps_pending）
//	②进度证据（零 LLM）：规则全部静默 → 放行停。标志性形态即「本轮零产出
//	    且无待办」的空转保护；有产出但无待办同样放行（活已干完）。
//	③LLM 复核：仅 Goal 模式、仅规则放行时运行——即既有 goal evaluator
//	    （goaleval / goal_complete.go），切片3 把它的准入门对齐「规则说继续
//	    就不许评估」。非 Goal 会话没有 LLM 层。
//
// 单向铁律（验收第一条）：本层唯一的输出是「继续理由集」或「放行」——
// stopReviewContinuationRules 只能产生继续，永远不能产生停止。停止只能来自
// 模型的显式动作（清空 todos）、既有机制的既有终态（budget/deadline/manual）
// 或下面的到顶兜底。到顶兜底是预算语义（同 Goal 预算牌），不是规则输出，
// 且必须带 cause 与未清清单报告（可见性，切片2）。
//
// 非目标（任务书 non-goals）：不做可编程钩子；不做每轮无条件 LLM 评估；
// 不做第二状态源（所有事实读既有 executor/goal 状态，本层自身只持有
// 「连续续轮计数」这一个会话内计数器，随真实用户轮复位）。
//
// 阈值口径：MaxContinuations 的占位默认=5，候校准——tl1459 的打点聚合器
// （tl1469，短轮占比/出口分布）出数后重新定值。

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/evidence"
)

// DefaultStopReviewMaxContinuations is the placeholder ceiling for consecutive
// review-driven continuations in one work segment (每轮显示「第 N/5」的那 5).
// Placeholder pending calibration against the tl1459 exit/duration aggregates
// (tl1469); the option exists so the value can move without a code change.
const DefaultStopReviewMaxContinuations = 5

// stopReviewPlanStepPrefix is the StepID identity plan_todos.go mints for steps
// seeded from an approved plan. A pending todo carrying it is an unexecuted
// plan step (判定规则③的事实形态——plan 步骤就是 canonical todos 的一个子集).
const stopReviewPlanStepPrefix = "plan_step_"

// StopReviewOptions carries the review layer's boot-time posture. The zero
// value keeps the layer ON: this is a defect remedy for the diagnosed silent
// early-stop family (按缺陷补齐论证豁免实验开关默认关的纪律——理由见任务书
// 交付报告), with a master switch for users who want the old free-stop
// behavior back.
type StopReviewOptions struct {
	// Disabled turns the default stop review off for the session (总开关).
	// Zero value (false) keeps the review on.
	Disabled bool
	// MaxContinuations caps consecutive review-driven continuations before the
	// capped stop fires. Zero or negative uses DefaultStopReviewMaxContinuations.
	MaxContinuations int
}

// normalizeStopReviewOptions applies the defaults a zero-valued option implies.
func normalizeStopReviewOptions(in StopReviewOptions) StopReviewOptions {
	out := in
	if out.MaxContinuations <= 0 {
		out.MaxContinuations = DefaultStopReviewMaxContinuations
	}
	return out
}

// stopReviewRule is one matched deterministic rule: why the turn-end review
// holds the stop and continues. Name is the stable machine token carried by
// notices (cause=) and log lines; Summary is the English human half.
type stopReviewRule struct {
	Name    string
	Summary string
}

// The built-in hard rules (内置硬规则 3-5 条，全部可见). Order is display
// order: the first matched rule names the continuation in the visible
// 「因 X 继续」 line; every matched rule appears in the detail.
var (
	stopReviewRuleTodosPending = stopReviewRule{
		Name:    "todos_pending",
		Summary: "the canonical todo list still has unfinished items",
	}
	stopReviewRuleReadinessGap = stopReviewRule{
		Name:    "readiness_gap",
		Summary: "delivery final readiness still misses required evidence",
	}
	stopReviewRulePlanSteps = stopReviewRule{
		Name:    "plan_steps_pending",
		Summary: "the approved plan still has unexecuted steps",
	}
	stopReviewRuleGoalPursuit = stopReviewRule{
		// Goal 衔接（瘦身下沉的继续判定）: an active+armed goal is the
		// session's standing order — the review layer, not the FSM default
		// branch, now owns that decision (1472 契约: armed+running ⇒ 必续).
		Name:    "goal_in_pursuit",
		Summary: "an armed goal is still in pursuit",
	}
)

// stopReviewFacts gathers everything the rules read, off any lock (the caller
// snapshots the executor/goal state first). Every field comes from an existing
// source of truth; the review layer adds no state of its own.
type stopReviewFacts struct {
	// TodosPending is the incomplete subset of the canonical todos, in
	// evidence.IncompleteTodos's TodoStepMatch shape (Content/Status/StepID).
	TodosPending []evidence.TodoStepMatch
	// ReadinessMissing carries the delivery final-readiness gaps.
	ReadinessMissing []string
	ReadinessReason  string
	// PlanStepsPending is theTodosPending subset seeded from the approved plan.
	PlanStepsPending []evidence.TodoStepMatch
	// GoalInPursuit reports phase=active && activation=armed (tl1472 判据).
	GoalInPursuit bool
	// TurnOutputSignatures counts the host progress identities visible after
	// this turn (写文件/工具成功/commit 的既有证据面). Layer ②'s trace fact —
	// no rule reads it; the release log names it so 空转保护 is greppable.
	TurnOutputSignatures int
}

// stopReviewContinuationRules evaluates the deterministic layers and returns
// every matched continue rule; empty means the stop is released (layer ②:
// nothing pending — the idle-protection stop included). This function is the
// whole decision surface of layers ①+②: pure, zero-LLM, and structurally
// incapable of producing a stop (单向铁律的结构形态——没有「叫停」输出这个
// 选项).
func stopReviewContinuationRules(f stopReviewFacts) []stopReviewRule {
	var rules []stopReviewRule
	if len(f.TodosPending) > 0 {
		rules = append(rules, stopReviewRuleTodosPending)
	}
	if len(f.ReadinessMissing) > 0 {
		rules = append(rules, stopReviewRuleReadinessGap)
	}
	if len(f.PlanStepsPending) > 0 {
		rules = append(rules, stopReviewRulePlanSteps)
	}
	if f.GoalInPursuit {
		rules = append(rules, stopReviewRuleGoalPursuit)
	}
	return rules
}

// stopReviewPlanStepsOf filters the pending todos down to the steps seeded
// from an approved plan (rule ③'s fact shape).
func stopReviewPlanStepsOf(todos []evidence.TodoStepMatch) []evidence.TodoStepMatch {
	var steps []evidence.TodoStepMatch
	for _, t := range todos {
		if strings.HasPrefix(strings.TrimSpace(t.StepID), stopReviewPlanStepPrefix) {
			steps = append(steps, t)
		}
	}
	return steps
}

// stopReviewEnabled reads the master switch. Fixed at construction (同
// standardContinuation 的 boot 快照口径——restart to apply), so no lock.
func (c *Controller) stopReviewEnabled() bool {
	return c != nil && !c.stopReview.Disabled
}

// stopReviewMaxContinuations reads the configured ceiling (0/negative already
// normalized at New).
func (c *Controller) stopReviewMaxContinuations() int {
	if c == nil || c.stopReview.MaxContinuations <= 0 {
		return DefaultStopReviewMaxContinuations
	}
	return c.stopReview.MaxContinuations
}

// resetStopReviewStreak clears the consecutive-continuation counter. A real
// user turn (non-synthetic) always resets: the user's own message opens a new
// work segment, and a review budget must never leak across it. Release and
// capped-stop reset too (runStopReviewContinuations), for the same reason.
func (c *Controller) resetStopReviewStreak() {
	c.mu.Lock()
	c.stopReviewStreak = 0
	c.mu.Unlock()
}

// --- 轮末审查的运行时半区（切片2：默认存在性 + 结论可见性）---

// stopReviewLogMessage is the greppable line the review layer writes when it
// acts (continues, caps) or releases. Same discipline as the tl1459 goal loop
// trace: lines only at decision points, never per model round; an ordinary
// turn costs at most one line.
const stopReviewLogMessage = "controller: stop review"

// gatherStopReviewFacts snapshots the review's fact sources from the existing
// truth surfaces (executor todos/readiness/progress, goal object). No new
// state source: everything here is already maintained elsewhere.
func (c *Controller) gatherStopReviewFacts() stopReviewFacts {
	f := stopReviewFacts{}
	if c.executor != nil {
		pending := evidence.IncompleteTodos(c.goalTodos())
		f.TodosPending = pending
		f.PlanStepsPending = stopReviewPlanStepsOf(pending)
		if r := c.executor.ReadinessResult(); len(r.Missing) > 0 {
			f.ReadinessMissing = append([]string(nil), r.Missing...)
			f.ReadinessReason = r.Reason
		}
		f.TurnOutputSignatures = len(c.executor.HostProgressSignatures())
	}
	f.GoalInPursuit = c.goals.armedForAutoContinue()
	return f
}

// stopReviewRuleNames renders the matched rules' stable tokens (cause= detail).
func stopReviewRuleNames(rules []stopReviewRule) []string {
	names := make([]string, 0, len(rules))
	for _, r := range rules {
		names = append(names, r.Name)
	}
	return names
}

// stopReviewNextContinuation spends the continuation budget: returns the
// 1-based continuation number within the cap, and capped=true once the streak
// has reached the ceiling (到顶转停的判定半区; the stop itself is the budget's
// semantics, not a rule verdict — the iron law stays intact).
func (c *Controller) stopReviewNextContinuation() (n, max int, capped bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	max = c.stopReviewMaxContinuations()
	if c.stopReviewStreak >= max {
		return c.stopReviewStreak, max, true
	}
	c.stopReviewStreak++
	return c.stopReviewStreak, max, false
}

// stopReviewRelease closes a released stop (layer ②): nothing pending — the
// idle-protection stop included. The streak resets so the next user turn's
// segment starts fresh. Debug-level: a release is the session's ordinary
// default state, not an event worth an Info line per chat turn; the continue
// and capped actions log at Info/Warn below.
func (c *Controller) stopReviewRelease(facts stopReviewFacts) {
	c.resetStopReviewStreak()
	slog.Debug(stopReviewLogMessage,
		"action", "release",
		"session", agent.BranchID(c.SessionPath()),
		"output_signatures", facts.TurnOutputSignatures,
		"todos_pending", len(facts.TodosPending))
}

// stopReviewNoticeDetail renders the machine half of a stop_review notice in
// the goalNoticeDetail key=value style: parsers read key=value pairs up to the
// trailing free-text reason.
func stopReviewNoticeDetail(ruleNames []string, n, max int, reason string) string {
	parts := []string{"cause=stop_review", "rules=" + strings.Join(ruleNames, "+")}
	if n > 0 {
		parts = append(parts, fmt.Sprintf("continuation=%d/%d", n, max))
	}
	if reason = strings.Join(strings.Fields(reason), " "); reason != "" {
		parts = append(parts, "reason="+clipGoalReason(reason))
	}
	return strings.Join(parts, " ")
}

// noticeStopReviewContinued surfaces a review-driven continuation: the visible
// 「因 X 继续（第 N/5）」 line (English technical half; frontends localize by
// Code). The streak is already spent when this fires.
func (c *Controller) noticeStopReviewContinued(rules []stopReviewRule, n, max int) {
	names := stopReviewRuleNames(rules)
	text := fmt.Sprintf("stop review: continuing (%d/%d) — %s.", n, max, rules[0].Summary)
	c.emitGoalNotice(event.NoticeCodeStopReviewContinued, event.LevelInfo, text,
		stopReviewNoticeDetail(names, n, max, ""))
	slog.Info(stopReviewLogMessage,
		"action", "continue",
		"session", agent.BranchID(c.SessionPath()),
		"rules", strings.Join(names, "+"),
		"continuation", fmt.Sprintf("%d/%d", n, max))
}

// noticeStopReviewCapped reports the budget stop: the review held the stop for
// max consecutive rounds and the work is still unfinished, so the loop stops
// WITH a readable report of what remains (到顶转停+报告, cause 可见). Warn
// level: an early-stop-shaped exit the user should see.
func (c *Controller) noticeStopReviewCapped(rules []stopReviewRule, facts stopReviewFacts, max int) {
	names := stopReviewRuleNames(rules)
	var b strings.Builder
	fmt.Fprintf(&b, "stop review: continuation budget reached (%d/%d); stopping with unfinished work — %s.",
		max, max, rules[0].Summary)
	if len(facts.TodosPending) > 0 {
		b.WriteString(" Pending: ")
		b.WriteString(stopReviewPendingList(facts.TodosPending))
	}
	if len(facts.ReadinessMissing) > 0 {
		fmt.Fprintf(&b, " Readiness gaps: %s.", strings.Join(facts.ReadinessMissing, ", "))
	}
	c.emitGoalNotice(event.NoticeCodeStopReviewCapped, event.LevelWarn, b.String(),
		stopReviewNoticeDetail(names, max, max, facts.ReadinessReason))
	slog.Warn(stopReviewLogMessage,
		"action", "capped_stop",
		"session", agent.BranchID(c.SessionPath()),
		"rules", strings.Join(names, "+"),
		"todos_pending", len(facts.TodosPending))
}

// stopReviewPendingList renders the unfinished todos for the capped-stop
// report, bounded so a long list cannot flood the notice.
func stopReviewPendingList(todos []evidence.TodoStepMatch) string {
	var b strings.Builder
	const show = 8
	for i, t := range todos {
		if i >= show {
			fmt.Fprintf(&b, " … (+%d more)", len(todos)-show)
			break
		}
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%q (%s)", t.Content, t.Status)
	}
	return b.String()
}

// stopReviewContinuationTurnPrompt renders the synthetic continuation turn's
// instruction: what is still pending and the two clean exits (finish and clear
// the list, or state the blocker). It carries no goal block — plain sessions
// have no goal contract to inject.
func stopReviewContinuationTurnPrompt(rules []stopReviewRule, facts stopReviewFacts) string {
	var b strings.Builder
	b.WriteString("Stop review: the previous turn ended with unfinished work (")
	b.WriteString(strings.Join(stopReviewRuleNames(rules), ", "))
	b.WriteString("). Continue and finish it.\n")
	if len(facts.TodosPending) > 0 {
		b.WriteString("Still pending on the todo list:\n")
		for _, t := range facts.TodosPending {
			fmt.Fprintf(&b, "- %s (%s)\n", t.Content, t.Status)
		}
	}
	if len(facts.ReadinessMissing) > 0 {
		fmt.Fprintf(&b, "Delivery readiness still misses: %s.\n", strings.Join(facts.ReadinessMissing, ", "))
	}
	b.WriteString("When the work is done, mark every item completed on the todo list (todo_write) so the session can stop. If an item cannot proceed without the user, say exactly what you need in your reply.")
	return b.String()
}

// runStopReviewContinuationTurn dispatches one review-driven continuation as a
// synthetic turn. It deliberately does NOT bind a goal recorder or a goal
// snapshot: a non-goal continuation has no goal contract, and its own end
// returns to this loop for the next review step.
func (o *turnOrchestrator) runStopReviewContinuationTurn(ctx context.Context, rules []stopReviewRule, facts stopReviewFacts) error {
	text := stopReviewContinuationTurnPrompt(rules, facts)
	return o.runSyntheticTurnWithRawDisplay(ctx, text, text, "")
}

// runStopReviewContinuations is the turn-end review loop for turns that bind
// no goal (非 Goal 会话也生效的默认存在性). Called from continueGoal's
// ordinary-turn exit; Goal turns never reach it (the FSM owns them). The loop
// reviews, spends the budget, dispatches one continuation per matched verdict,
// and stops only on release, cap, cancellation, or a turn error — every stop
// it produces carries a cause and is user-visible (单向铁律 + 可见性).
func (o *turnOrchestrator) runStopReviewContinuations(ctx context.Context, turnErr error) error {
	c := o.c
	// Clean turns only: error-path ends keep their existing contracts (the
	// readiness boundary and recovery pauses are separate mechanisms with
	// their own resume semantics).
	//
	// A live goal keeps the FSM loop as its only continuation engine even when
	// one of its turns reaches this entry (runTurnWithRawDisplay): 审查层不得
	// 成为 Goal 会话的第二引擎（不得两套并存）——goal_in_pursuit 规则活在
	// advance 的 default 分支里，那里消费的是同一套规则函数。
	//
	// tl1070 的 standard_continuation 实验开关在实验期内独占 Standard todos
	// 续跑：审查层让位，两引擎绝不交错（减法候选已登记：tl1460 跑顺后
	// 1070 的 todo-continuation 切片按开关三终局走转正观察，不静默删）。
	if c == nil || !c.stopReviewEnabled() || turnErr != nil ||
		c.goals.active() || c.standardContinuation {
		return nil
	}
	for {
		if ctx.Err() != nil {
			// The turn machinery owns cancel semantics (⑦): a canceled context
			// ends the review without fabricating an error — the graceful nil
			// the turn returned stays the returned nil.
			return nil
		}
		if c.CancelRequested() {
			// 让位于用户（前 1460 契约 ③ 的保留）：a requested cancel / pending
			// user takeover preempts the review — the user is driving, and the
			// layer's continuations must never fight them for the turn. Fresh
			// segment on the next real user message.
			c.resetStopReviewStreak()
			slog.Debug(stopReviewLogMessage,
				"action", "yield_to_user",
				"session", agent.BranchID(c.SessionPath()))
			return nil
		}
		facts := c.gatherStopReviewFacts()
		rules := stopReviewContinuationRules(facts)
		if len(rules) == 0 {
			c.stopReviewRelease(facts)
			return nil
		}
		n, max, capped := c.stopReviewNextContinuation()
		if capped {
			c.noticeStopReviewCapped(rules, facts, max)
			c.resetStopReviewStreak()
			return nil
		}
		c.noticeStopReviewContinued(rules, n, max)
		if err := o.runStopReviewContinuationTurn(ctx, rules, facts); err != nil {
			// The continuation turn failed. The review's budget is not a
			// license to spin on a failing provider: stop reviewing and let
			// the error surface the way the ordinary turn-error path does.
			// The next real user message opens a fresh segment.
			c.resetStopReviewStreak()
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return nil
			}
			slog.Warn(stopReviewLogMessage,
				"action", "halted_on_error",
				"session", agent.BranchID(c.SessionPath()),
				"err", clipGoalReason(err.Error()))
			return err
		}
	}
}
