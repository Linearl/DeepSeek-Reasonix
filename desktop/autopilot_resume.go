package main

// 任务 731 — 异常态会话自动续轮接入自动驾驶（579 家族第三形态）。
//
// 场景（用户 20261010 指令 + 20261007 合并线案例）：无人值守会话异常提前终止
// （回合异常终止 / 收信后异常态无法开轮），此前只能用户手动续轮。本件把
// 「异常态检测 + 自动续轮」做成自动驾驶的一项能力，续轮有界防无限循环。
//
// 三层判定的分层边界（任务书要点 3，互补不重叠）：
//   - 579 空闲开轮桥管「正常 idle+queued」的自动开轮（RunInboxTurn 路径）；
//     本件不参与信箱开轮，只在 579 的 3 次开轮预算用尽后升级为一次会话级
//     唤醒（S2 信号）——两条准入路径不同，天然不重复触发；
//   - 477 ask 超时管「无人应答的高风险提问」；ErrAutopilotAskUnanswered 的
//     终态停止是刻意设计（不替用户拍板），本件不复活它；
//   - 544 ask 自动续跑管「答复落账后的当轮续跑」（controller 内合成回合）；
//     本件是外层网——续跑回合自身再死，由本件按同一预算续；
//   - 326/547 autopilot 守护任务管「周期心跳型拉起」（分钟级、默认关）；
//     本件是事件驱动型（秒级响应），二者预算互不相干。
//
// 检测信号矩阵（任务书要点 1「各形态逐一落实」；全部为进程内观测面，
// 不依赖桌面日志——部分形态桌面日志本就无痕迹）：
//   S1 回合异常终止：tabEventSink 事件面上的 TurnDone——Err≠nil 且非取消
//      （用户/墙钟）且无契约型结局（Outcome==""：incomplete_read /
//      final_readiness / recovery_paused / completion_uncertain 各有自己的
//      恢复契约，本件不越）且非 477 终停、非用户暂停（709 绝对静默）、
//      非准入竞态（ErrTurnRunning=别人接手了）。
//   S2 收信后无法开轮：579 桥每队首 3 次开轮预算用尽（既有精确检测点
//      recordOpeningFailure 的 exhausted 钩子）→ 本件升级一次会话级唤醒。
//   S3 queued 超阈值不开轮：桌面层 submit 只有 started/refused 两态，
//      无 PendingPrompt 的「卡 queued」仅 collab 收信形态可达（即 S2）；
//      waiting_user 归 477 领地；故 S3 无独立检测面，由 S2 覆盖（报告载明）。
//
// 续轮动作（任务书要点 2）：经 SubmitToTab 走正常准入路径注入一条续轮提示
// （同任务 49 autopilotResumePrompt 的「事实+期望」形态，不带原始错误文案
// ——对齐 544：错误已由回合自身轨迹留痕）。忙时准入自动拒绝
// （control.ErrTurnRunning），天然防重复触发。
//
// 有界预算：每会话连续续轮计数，上限 3 次；注入成功即计 1 次，干净完成的
// 回合（Err==nil）或用户介入（手动停轮/手动提交）复位计数并解除封存；连续
// 3 次续轮后仍异常终止 → 停止续轮、封存、发用户可见告警（对齐「自动重试
// 必须有界」「无界等待=可用性缺陷」判据）。
//
// 开关（任务书要点 4）：tab.autopilot（自动驾驶总开关）——调度时与触发时
// 双查；开关关闭时本件不调度不注入，行为与现状完全一致。不设独立子开关
// （579 同款口径：能力随主开关，不再造一层开关面）。

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/secrets"
	"reasonix/internal/sessioninbox"
)

const (
	// autopilotResumeMaxStrikes is the 任务731 bounded budget per episode:
	// at most this many consecutive auto-resume turns per abnormal chain. A
	// turn that completes cleanly proves the session works again and resets
	// the count; the (N+1)-th abnormal stop seals the session until then.
	autopilotResumeMaxStrikes = 3

	// autopilotResumeGraceDelay is the window between an abnormal stop and
	// the resume fire. It absorbs the faster in-turn contracts: a 544
	// continuation (same orchestrated turn, no new TurnStarted) or the user
	// typing a follow-up — both keep a turn running, and the fire-time
	// admission then refuses instead of stacking a second turn.
	autopilotResumeGraceDelay = 15 * time.Second
)

// autopilotAbnormalResumePrompt is what an unattended auto-resume injects
// (distinct from the task-49 restart autopilotResumePrompt). Facts and
// expectation only; the raw provider error stays out of the prompt (544
// rationale: it is already redacted/surfaced by the turn's own trail).
const autopilotAbnormalResumePrompt = "Your previous turn stopped abnormally before the work could finish. Continue the interrupted work from where it stopped and carry it to completion; do not restate the plan and do not wait for input."

// Notice codes for the two user-visible outcomes (wire-stable, registered in
// internal/event/notice_codes.go; frontends localize by code).
const (
	noticeCodeAutopilotResumeFired    = event.NoticeCodeAutopilotResumeFired
	noticeCodeAutopilotResumeExhausted = event.NoticeCodeAutopilotResumeExhausted
)

// Go-side bilingual fallbacks; frontends localize by the codes above.
const (
	autopilotResumeFiredTextFormat = "Autopilot auto-resume: the previous turn stopped abnormally; a continuation turn was submitted automatically (continuation %d of %d) (无人值守自动续轮：异常终止自动接续)."
	autopilotResumeExhaustedText   = "Autopilot auto-resume stopped: consecutive resumes kept ending abnormally, so no more will be submitted to avoid an unbounded loop. The session will not be auto-resumed again until a turn completes cleanly; please check it manually (无人值守自动续轮已停止，请人工检查)."
)

// autopilotResumeState is one tab's episode bookkeeping.
type autopilotResumeState struct {
	timer   *time.Timer
	pending bool // a scheduled resume awaits its grace window
	strikes int  // consecutive admitted auto-resumes without a clean turn
	sealed  bool // budget spent; no more resumes until a reset signal
}

// autopilotResumeWatchdog owns the per-tab state. All entry points are
// nil-receiver safe: tests and early startup may leave the field unset.
type autopilotResumeWatchdog struct {
	app *App

	mu     sync.Mutex
	states map[string]*autopilotResumeState

	// Test seams (579 idleTurnTargetView pattern): production leaves them
	// nil and the defaults below are used.
	now    func() time.Time
	delay  time.Duration
	submit func(tabID, prompt string) error
	notify func(tabID string, level event.Level, code, text string)
}

func newAutopilotResumeWatchdog(app *App) *autopilotResumeWatchdog {
	return &autopilotResumeWatchdog{app: app, states: map[string]*autopilotResumeState{}}
}

func (w *autopilotResumeWatchdog) clock() time.Time {
	if w.now != nil {
		return w.now()
	}
	return time.Now()
}

func (w *autopilotResumeWatchdog) graceDelay() time.Duration {
	if w.delay > 0 {
		return w.delay
	}
	return autopilotResumeGraceDelay
}

func (w *autopilotResumeWatchdog) submitFn() func(tabID, prompt string) error {
	if w.submit != nil {
		return w.submit
	}
	return w.app.SubmitToTab
}

func (w *autopilotResumeWatchdog) notifyFn() func(tabID string, level event.Level, code, text string) {
	if w.notify != nil {
		return w.notify
	}
	return w.app.noticeCodeForTab
}

// stateFor returns (creating) the state for one tab. Caller holds mu.
func (w *autopilotResumeWatchdog) stateFor(tabID string) *autopilotResumeState {
	st, ok := w.states[tabID]
	if !ok {
		st = &autopilotResumeState{}
		w.states[tabID] = st
	}
	return st
}

// dropState cancels any pending timer and forgets the tab's episode.
func (w *autopilotResumeWatchdog) dropState(tabID string) {
	if w == nil || tabID == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if st, ok := w.states[tabID]; ok {
		if st.timer != nil {
			st.timer.Stop()
		}
		delete(w.states, tabID)
	}
}

// observeTurnStarted cancels a pending resume: a turn just started (the
// user's, or this watchdog's own resume) — the scheduled fire is stale. It
// does not touch strikes or the seal; the turn's own TurnDone decides those.
func (w *autopilotResumeWatchdog) observeTurnStarted(tabID string) {
	if w == nil || tabID == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if st, ok := w.states[tabID]; ok && st.pending {
		if st.timer != nil {
			st.timer.Stop()
		}
		st.timer = nil
		st.pending = false
	}
}

// observeTurnDone consumes one terminal turn record from the tab's event
// plane. Clean completion resets the episode; a user cancellation suppresses
// the pending resume and resets too (a person took over); every other
// terminal outcome schedules nothing.
func (w *autopilotResumeWatchdog) observeTurnDone(tabID string, e event.Event) {
	if w == nil || tabID == "" {
		return
	}
	if e.Cancelled {
		// 用户停轮=接管：撤销待发续轮，复位本次链条（709 同款：用户意志优先）。
		w.dropState(tabID)
		return
	}
	if e.Err == nil {
		// 干净完成=会话能正常跑完回合：复位预算并解除封存。
		w.mu.Lock()
		if st, ok := w.states[tabID]; ok {
			if st.timer != nil {
				st.timer.Stop()
			}
			w.states[tabID] = &autopilotResumeState{}
		}
		w.mu.Unlock()
		return
	}
	if !autopilotAbnormalStop(e) {
		return
	}
	w.schedule(tabID, "turn_abnormal_stop")
}

// autopilotAbnormalStop classifies one failed terminal turn as an abnormal
// early stop worth resuming. Every exclusion keeps a stop that is someone
// else's contract exactly as it was (the 544 exclusion philosophy):
//   - Outcome != "": incomplete_read / final_readiness / recovery_paused /
//     completion_uncertain each own their recovery surface;
//   - context.Canceled / DeadlineExceeded: the user or the autopilot wall
//     clock ended the run on purpose;
//   - ErrAutopilotAskUnanswered: the 477-off terminal stop must not be
//     resurrected behind 477's back;
//   - sessioninbox.ErrPaused: a queue a person is holding (709 绝对静默);
//   - control.ErrTurnRunning: another admission path won the race.
func autopilotAbnormalStop(e event.Event) bool {
	if e.Err == nil || e.Cancelled {
		return false
	}
	if e.Outcome != "" {
		return false
	}
	if errors.Is(e.Err, context.Canceled) || errors.Is(e.Err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(e.Err, control.ErrAutopilotAskUnanswered) {
		return false
	}
	if errors.Is(e.Err, sessioninbox.ErrPaused) {
		return false
	}
	if errors.Is(e.Err, control.ErrTurnRunning) {
		return false
	}
	return true
}

// escalateInboxStuck is the S2 entry: the 579 idle-turn bridge spent its
// opening budget on this target's queue head (收信后异常态无法开轮，579 已
// 精确检测). The escalation bypasses the grace window — the bridge budget
// already spanned minutes — and shares the per-tab bounded budget. fire()
// re-checks the seal, so the pre-check here is just a cheap short circuit.
func (w *autopilotResumeWatchdog) escalateInboxStuck(tabID string, attempts int, lastErr error) {
	if w == nil || tabID == "" {
		return
	}
	w.mu.Lock()
	sealed := w.stateFor(tabID).sealed
	w.mu.Unlock()
	if sealed {
		return
	}
	w.fire(tabID, fmt.Sprintf("inbox_open_exhausted after %d attempts (%v)", attempts, secrets.RedactError(lastErr)))
}

// schedule arms one resume after the grace window. A pending schedule is
// never duplicated; a sealed tab stays silent. The timer is created and
// stored under the lock so a concurrent observeTurnStarted cannot race the
// assignment.
func (w *autopilotResumeWatchdog) schedule(tabID, reason string) {
	w.mu.Lock()
	st := w.stateFor(tabID)
	if st.sealed || st.pending {
		w.mu.Unlock()
		return
	}
	st.pending = true
	delay := w.graceDelay()
	st.timer = time.AfterFunc(delay, func() {
		w.mu.Lock()
		live, ok := w.states[tabID]
		if !ok || !live.pending {
			w.mu.Unlock()
			return
		}
		live.pending = false
		live.timer = nil
		w.mu.Unlock()
		w.fire(tabID, reason)
	})
	w.mu.Unlock()
	log.Printf("[autopilot-resume] abnormal stop scheduled for tab %s (reason %s, fire in %s)", tabID, reason, delay)
}

// fire runs one resume attempt: gate on the autopilot master switch (re-read
// at fire time), admit through the normal SubmitToTab path, count the strike
// only for an admitted submission. Seal + warn once the budget is spent.
// The pending flag doubles as the one-fire-at-a-time claim, so a scheduled
// fire and a racing S2 escalation cannot inject two turns at once.
func (w *autopilotResumeWatchdog) fire(tabID, reason string) {
	if w == nil || tabID == "" {
		return
	}
	tab := w.app.tabByID(tabID)
	if tab == nil || tab.removed {
		return
	}
	w.app.mu.RLock()
	autopilotOn, ready := tab.autopilot, tab.Ready
	w.app.mu.RUnlock()
	if !autopilotOn {
		// 开关关闭：不注入不留痕，行为与现状一致（验收④）。
		w.dropState(tabID)
		return
	}
	if !ready {
		return
	}

	// 认领本次 fire 槽位（sealed / 已有 pending / 预算用尽三查）。
	w.mu.Lock()
	st := w.stateFor(tabID)
	if st.sealed || st.pending {
		w.mu.Unlock()
		return
	}
	if st.strikes >= autopilotResumeMaxStrikes {
		st.sealed = true
		w.mu.Unlock()
		log.Printf("[autopilot-resume] budget exhausted for tab %s after %d consecutive resumes; sealed until a turn completes cleanly (reason %s)", tabID, autopilotResumeMaxStrikes, reason)
		w.notifyFn()(tabID, event.LevelWarn, noticeCodeAutopilotResumeExhausted, autopilotResumeExhaustedText)
		return
	}
	st.pending = true
	w.mu.Unlock()

	// 正常准入路径：忙时（用户或 544 续跑已在跑）自动拒绝，不烧预算。
	submitErr := w.submitFn()(tabID, autopilotAbnormalResumePrompt)

	w.mu.Lock()
	if cur, ok := w.states[tabID]; ok && cur == st {
		// 本次链条仍在：解除认领；注入成功才计次。状态若已被复位信号
		// （干净完成 / 用户停轮 / 开关关闭）替换或丢弃，则本次计数随旧
		// 链条一并作废——复位信号是更高优先级的事实。
		st.pending = false
		if submitErr == nil {
			st.strikes++
			attempt := st.strikes
			w.mu.Unlock()
			log.Printf("[autopilot-resume] continuation turn submitted for tab %s (continuation %d of %d, reason %s)", tabID, attempt, autopilotResumeMaxStrikes, reason)
			w.notifyFn()(tabID, event.LevelInfo, noticeCodeAutopilotResumeFired, fmt.Sprintf(autopilotResumeFiredTextFormat, attempt, autopilotResumeMaxStrikes))
			return
		}
		w.mu.Unlock()
		if errors.Is(submitErr, control.ErrTurnRunning) {
			log.Printf("[autopilot-resume] resume for tab %s not needed — a turn is already running (reason %s)", tabID, reason)
			return
		}
		// 准入失败（workspace 未就绪等）：不注入不计次不重试——没有注入
		// 就没有链条；下一次异常终止事件会重新调度。
		log.Printf("[autopilot-resume] resume submit for tab %s refused: %v (reason %s)", tabID, secrets.RedactError(submitErr), reason)
		return
	}
		w.mu.Unlock()
	}

// escalateStuckInboxForTab is the App-facing S2 adapter called from the 579
// bridge's exhaustion hook. It gates on the master switch before touching the
// watchdog so an off-autopilot tab never schedules anything.
func (a *App) escalateStuckInboxForTab(tabID string, attempts int, lastErr error) {
	if a == nil || a.autopilotResume == nil || strings.TrimSpace(tabID) == "" {
		return
	}
	tab := a.tabByID(tabID)
	if tab == nil {
		return
	}
	a.mu.RLock()
	on := tab.autopilot
	a.mu.RUnlock()
	if !on {
		return
	}
	a.autopilotResume.escalateInboxStuck(tabID, attempts, lastErr)
}

// observeAutopilotResumeTurnDone is the App-facing S1 tap wired into
// tabEventSink.Emit. Nil-safe for every test-constructed App.
func (a *App) observeAutopilotResumeTurnDone(tabID string, e event.Event) {
	if a == nil || a.autopilotResume == nil {
		return
	}
	a.autopilotResume.observeTurnDone(tabID, e)
}

// observeAutopilotResumeTurnStarted cancels a pending resume when any turn
// starts on the tab (user follow-up during the grace window included).
func (a *App) observeAutopilotResumeTurnStarted(tabID string) {
	if a == nil || a.autopilotResume == nil {
		return
	}
	a.autopilotResume.observeTurnStarted(tabID)
}
