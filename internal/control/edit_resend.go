package control

// tl1544 编辑历史消息回溯重发（tl1375 调研 B 案落地，对标 zcode 交互三步合一：
// 选历史用户消息 → 抛弃其后输出 → 以编辑后文本从该点重发）。
//
// 方案定案（调研报告 §一/§四，20261110 21b89c1）：
//   形态 = 复用 rewind 的 fork-at-turn 语义 —— PrepareRewind(RewindConversation)
//   → CommitRewindInPlace（新 session 文件承载截断后历史，主账本永不被编辑重发
//   改写；sidecar rebind 全套由 forkNamedReady 免费拿）。本文件不引入任何新的
//   存储格式、账本物理形态或截断语义；真实增量只有「回合运行中」的原子编排：
//
//     1. 运行中止：CancelStop（L1）→ 有界等待 running/finishing 关窗，
//        超预算升级 L2（force grace）→ 仍不退则 L3（force），终极预算耗尽
//        显式报错退出（无状态损坏，编辑文本留在前端）。每级语义、阈值与
//        用户手动连按 stop 完全同构（任务461-P7 三级终止的复用，无新状态机）。
//     2. parked 队列治理（fork 之前）：parkedTurns FIFO 是「被编辑消息之后
//        的输出」的排队延伸，语义②涵盖它们；turn 形态 replay 行核销
//        （discard，不留 resendable），dock 形态行（steer unapplied）保留
//        停靠（引导永不自动升格，仅丢内存 body）。顺序硬约束：inbox 行绑定
//        session manifest 而 fork 不复制 inbox 项（rebindInbox 既有语义），
//        fork 后旧 manifest 不可达，核销必须发生在 fork 前。
//     3. 截断：PrepareRewind(turn, RewindConversation) → CommitRewindInPlace
//        （fork 承载截断后历史；sessionRevision bump、checkpoint rebind、
//        context 投影继承全部走既有 rewind 通道）。
//        b. 引导残留 settle —— 截断掉已注入 steer 回执的那段历史后，
//           steer_receipts 三层对账会失配；随编排触发一次
//           SettleConsumedGuidanceResidue（close 同款 in-process sweep）。
//        c. checkpoint 对账 —— fork 后 rebindCheckpoints(newPath) 使 bound
//           清空（新文件无 checkpoint 数据），rewind picker / turnsByMessageIndex
//           / UnfiledInputs 三个消费面自动归零，无 unfiled 横幅口径面。
//        d. 续跑标记 —— 新 turn 经 SubmitEditedDisplay → runOrchestratedTurn
//           的 per-turn 清理自动生效（clearTurnAskAnswered / beginRecoveryEpisode /
//           resetStopReviewStreak / markEditedForNewUser）。
//     4. 重发：SubmitEditedDisplay(display, input, original) 开新 turn，
//        Edited/Original local-only 元数据由 markEditedForNewUser 落账。
//
// 明确不做（调研报告 §一 / 任务书边界）：运行中原地改写内存态（与
// owned-baseline 快照恢复 + mid-turn autosave tl1491 契约打架）；steer/编辑/
// park 三入口统一提交语义层（宪法减法优先）。turn 运行中本入口绝不降级成
// steer（steer 保留已产出输出，编辑重发抛弃 —— 语义相反）。
//
// 已知竞态注记（诚实边界）：等待关窗与 rewind 之间，若 durable inbox
// dispatcher 或 finish-close 的 parked drain 抢先开轮，CommitRewindInPlace
// 的 beginRotation 会拒绝本编排（报错退出）；步骤 4 前的 running 复查把
// 「submit 落成 parked 新消息」的形态显式报错而非静默 park。两条都比既有
// rewind→send 两步链的静默 park 更诚实（既有两步链的同类窗口本件不扩大）。

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// 等待预算与轮询间隔。默认值对齐三级终止的既有阈值（stopAutoGraceAfter /
// stopForceGrace）各加少量缓冲；vars 供测试收缩。
var (
	editResendL1WaitBudget = stopAutoGraceAfter + 2*time.Second
	editResendL2WaitBudget = stopForceGrace + 2*time.Second
	editResendL3WaitBudget = 5 * time.Second
	editResendPollStep     = 20 * time.Millisecond
)

// ErrEditResendTurnUnwound is returned when the running turn refused to end
// even after the full L1→L2→L3 escalation. The controller is left untouched:
// no rewind, no truncation, no resend — the edit text stays with the frontend.
var ErrEditResendTurnUnwound = errors.New("previous turn did not unwind after stop escalation; edit-resend aborted without changes")

// ErrEditResendCompactionInFlight is returned when a compaction pass is
// mid-flight: a conversation fork during the snapshot→install window would
// corrupt the pass (same gate the turn admission holds).
var ErrEditResendCompactionInFlight = errors.New("compaction in flight; retry edit-resend after it settles")

// ErrEditResendClosed seals a torn-down controller against edit-resend.
var ErrEditResendClosed = errors.New("session is closed; edit-resend unavailable")

// ErrEditResendRaceAdmitted is returned when another surface admitted a turn
// inside the编排窗口 between the unwind wait and the resend. Fail loud instead
// of parking the edited text as a new queued message (which would invert the
// rewind semantics).
var ErrEditResendRaceAdmitted = errors.New("another surface started a turn during edit-resend; the edit was not submitted")

// EditAndResend runs the atomic 「中止（若在跑）→ fork-at-turn 截断 → 清
// parked → settle 引导残留 → 编辑重发」 chain for one historical user message.
// turn is the checkpoint turn whose user message is being replaced (the same
// numbering the rewind picker shows); everything at or after that turn's
// boundary is discarded into a fork, and the edited text becomes the new user
// message. The parent session file is never rewritten.
func (c *Controller) EditAndResend(turn int, display, input, original string) error {
	if c == nil || c.executor == nil {
		return fmt.Errorf("edit-resend unavailable: no active session")
	}
	if strings.TrimSpace(original) == "" {
		// An edit-resend always replaces an existing prompt; without the
		// original text markEditedForNewUser cannot stamp the Edited metadata
		// and this would degrade into a plain new-message submit.
		return fmt.Errorf("edit-resend requires the original prompt text")
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrEditResendClosed
	}
	if c.executor.CompactionInFlight() {
		return ErrEditResendCompactionInFlight
	}

	// 步骤 1：回合运行中 —— 三级终止（同构用户连按 stop）+ 有界关窗等待。
	if st := c.RuntimeStatus(); st.Running {
		c.CancelStop() // L1 graceful
		if !c.waitForEditResendTurnClose(editResendL1WaitBudget) {
			slog.Info("control: edit-resend escalating stop to L2", "turn", turn)
			c.CancelStop() // L2 force grace
			if !c.waitForEditResendTurnClose(editResendL2WaitBudget) {
				slog.Warn("control: edit-resend escalating stop to L3 (force)", "turn", turn)
				c.CancelStop() // L3 force abandon
				if !c.waitForEditResendTurnClose(editResendL3WaitBudget) {
					return ErrEditResendTurnUnwound
				}
			}
		}
	}
	// The cancelled turn may have handed a compaction pass to the settle
	// chain; re-check before touching the session.
	if c.executor.CompactionInFlight() {
		return ErrEditResendCompactionInFlight
	}

	// 步骤 2：治理配套（fork 之前）—— 两件都受 inbox manifest 绑定约束：
	// inbox 行绑定 session manifest 而 fork 不复制 inbox 项（rebindInbox 既有
	// 语义），fork 之后再 discard/settle 就永远触达不了旧 manifest 里的行；
	// 且 fork 前 transcript 凭证最全（截断掉的历史里已注入的 steer 回执仍是
	// settle 证据）。报告 §三「截断后 settle」是原地形态推演，fork 形态下
	// 顺序必然反转。
	//   a. parked 队列清空 —— turn 形态 replay 行核销（discard，不留
	//      resendable），dock 形态行保留停靠（引导永不自动升格，仅丢内存
	//      body）；同时缩小 finish-close drain 抢跑开轮的窗口。
	//   b. 引导残留 settle —— consumed steer 行按 transcript 注入凭证核销；
	//      凭证不足的留 uncertain（可检索、永不自动派发）。
	c.discardParkedTurnsForEditResend()
	if dropped := c.SettleConsumedGuidanceResidue("edit_resend"); dropped > 0 {
		slog.Info("control: edit-resend settled consumed guidance residue", "count", dropped, "turn", turn)
	}

	// 步骤 3：fork-at-turn 截断（rewind 通道原样：rotation gate + mutation
	// barrier + 事务 + sessionRevision bump 都在通道内）。
	plan, err := c.PrepareRewind(turn, RewindConversation)
	if err != nil {
		return err
	}
	if !plan.CanConversation {
		return fmt.Errorf("%s", nonEmpty(plan.DisabledReason, "conversation rewind unavailable for this turn"))
	}
	result, err := c.CommitRewindInPlace(plan.PlanID)
	if err != nil {
		return err
	}
	if !result.OK || !result.ConversationForked {
		return fmt.Errorf("edit-resend rewind did not fork: %+v", result)
	}

	// 步骤 4：编辑重发。先复查 running（竞态防护：dispatcher/drain 抢跑时
	// 显式失败，绝不把编辑文本静默 park 成新消息）。
	if c.RuntimeStatus().Running {
		return ErrEditResendRaceAdmitted
	}
	c.SubmitEditedDisplay(display, input, original)
	return nil
}

// waitForEditResendTurnClose polls until the turn gate (running||finishing)
// closes or the budget expires. RuntimeStatus().Running merges both flags, so
// a closed window here means CommitRewindInPlace's beginRotation can proceed.
func (c *Controller) waitForEditResendTurnClose(budget time.Duration) bool {
	deadline := time.Now().Add(budget)
	for {
		if !c.RuntimeStatus().Running {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(editResendPollStep)
	}
}

// discardParkedTurnsForEditResend drops every queued parked body — they are
// the queued extension of the output being discarded (报告 §三 tl1357b) — and
// writes off their durable replay rows so the shelf never offers them as
// resendable work. Dock-kind rows (unapplied steer guidance) keep their
// uncertain shelf row: guidance must never auto-escalate, and Retry remains
// the user's own path back.
func (c *Controller) discardParkedTurnsForEditResend() {
	c.mu.Lock()
	entries := c.parkedTurns
	c.parkedTurns = nil
	c.mu.Unlock()
	if len(entries) == 0 {
		return
	}
	var turnRowIDs []string
	for _, e := range entries {
		r := e.replay
		if r == nil {
			continue
		}
		// Wait for the park-side row write so a concurrently committing row
		// is either visible to the discard below or never existed (the same
		// settleParkReplay ordering the FIFO drain relies on).
		r.mu.Lock()
		written := r.written
		r.mu.Unlock()
		if written != nil {
			<-written
		}
		if r.kind != parkReplayTurn {
			continue // dock rows keep their uncertain shelf row
		}
		if id := r.dockedRowID(); id != "" {
			turnRowIDs = append(turnRowIDs, id)
		}
	}
	if len(turnRowIDs) == 0 {
		slog.Info("control: edit-resend dropped parked bodies", "count", len(entries))
		return
	}
	st, err := c.ensureInbox()
	if err != nil {
		// No durable inbox: the in-memory drop above is the whole治理面.
		slog.Warn("control: edit-resend parked replay rows not discarded (no durable inbox)",
			"ids", turnRowIDs, "err", err)
		return
	}
	discarded, err := st.DiscardPendingItemsOwnedResult(turnRowIDs, "")
	if err != nil {
		slog.Warn("control: edit-resend parked replay row discard failed", "err", err)
		return
	}
	slog.Info("control: edit-resend dropped parked bodies and wrote off replay rows",
		"bodies", len(entries), "discarded", len(discarded))
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}
