package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/safego"
	"reasonix/internal/sessioncollab"
	"reasonix/internal/sessioninbox"
)

// 任务 569：空闲开轮桥（desktop 侧接上 Bot/ACP 已有的 inbox 开轮链路，「接桥」非「造桥」）。
// 任务 579：三合一修订——①桥去开关，始终打开（用户口径「不需要做成可以开关的
// 特性」）；②③开轮失败不再无限重试：每个卡住的队首 item 至多 3 次尝试，
// 用尽后保留队列、写 570 回执（open_retry_exhausted）并留 Error 日志——旧行为
// 是每 4s 一个 pump tick 无限重开且只写 log，永不终止。
//
// 问题（568 诊断 + 2026-10-07 调研核实）：desktop pump 每 4s 只做投递——MailStore →
// 目标会话 inbox 落箱 → Ack，不含任何开轮；消费锚定在「目标会话的 turn 内」。steer
// 在目标空闲时无法注入当轮，降级为排队 follow-up 后，controller 内建调度器对
// detached runtime 全数失效：desktop/turn_admission.go 的 beforeInboxDispatch 只在
// 可见 tab 里找 owner，detached runtime 找不到 → 返回 ErrInboxRuntimeUnpublished →
// dispatchInboxOnce 把这次 kick 当 idle 丢弃（579 已改为留痕 + 有界重试）→ 消息
// 永远躺在持久信箱（实测 21 会话 600+ 条、单会话最高 233 条，夜间无人值守时段
// 损失约 5 小时产能）。
//
// Bot/ACP 有现成开轮链路：Controller.RunInboxTurn（internal/control/inbox_run.go，
// 注释原文 "Bot and ACP use this path"），调用方在 internal/acp/inbox_drain.go:38 与
// internal/bot/gateway.go:2271；desktop 此前无调用。本文件把该链路接到 pump 上。
//
// 四条硬边界（任务书 + 579 补充件，逐条落点）：
//  1. 触发 = 目标会话空闲持续 sessionCollabIdleTurnDelay（N 秒）+ inbox 存在排队
//     follow-up。N 常量取 45s（任务书给 30-60s 区间，取中位）：短于它容易抢在用户
//     两次输入之间开轮，长于它削弱消除静默死锁的时效。pump 周期 4s 是测量分辨率，
//     实际消费延迟 = max(N - 已空闲时长, 0) + 至多一个 pump 周期。
//  2. 不打断用户 = 仅非 active tab 开轮；detached runtime 无可见 tab，天然满足。
//  3. guard 对齐 = 走 Bot/ACP 同一条 RunInboxTurn：轮内动作服从该会话既有的审批/
//     guard 机制（ToolApprovalMode、guard 轮约束），本桥不新增任何能力面、不传任何
//     额外指令——消费的就是已落箱的那条 follow-up 原文。
//  4. 与 experimental_collab_background_delivery（task 224）互斥：该开关开启时 pump
//     本就退出投递（drain_inbox 是唯一消费者），桥随 sweep 门整体停用，两条消费
//     模式不叠加。579 移除的是本桥自己的开关，这条互斥守卫原样保留。
//
// 另一处保守约束（任务709 细化）：inbox 处于 paused 时——
//   - 用户显式暂停（UserPaused）：绝不开轮——暂停是用户意志，桥不越；
//   - 自动暂停（重启恢复 / 重开带遗留队列 / 错误防护，Paused&&!UserPaused）：
//     桥在开轮前先解除该暂停再开轮。579 之前的实现把一切 paused 都当用户
//     持有，长期闲置（stale）会话在重开时被自动暂停后，新投递的协作消息
//     永远静默（708 派单实测 10+ 分钟不开轮，人工唤醒才消费）——盲区即此。
//     自动暂停不是用户意志，且只有存在可认领的排队工作（queuedID != ""）
//     时桥才会走到唤醒，纯待人工检视的架子（Uncertain）不会被打扰。

// sessionCollabIdleTurnDelay is the idle window N of the task-569 bridge. The
// task book prescribes 30-60s; 45s sits in the middle: below it the bridge can
// fire between two of the user's own inputs, above it the silent-deadlock
// relief gets slow. The 4s pump period is the measurement resolution.
// 任务579 补充件：保持写死常量（不做可配），默认值即 45s。
const sessionCollabIdleTurnDelay = 45 * time.Second

// idleTurnMaxAttempts is the 任务579 opening budget per stuck (contact, item):
// the initial attempt plus this many retries would be unbounded (the old
// behaviour re-opened every 4s tick forever); 3 strikes then the item is
// reported and suppressed until something changes (new head, contact ran).
const idleTurnMaxAttempts = 3

// idleTurnFinishingStuckTimeout is the 任务796 finishing-only leak threshold
// the bridge feeds to Controller.HealStuckFinishing. A TurnDone fan-out is a
// milliseconds-long window; when RuntimeStatus reports not-running but the
// finishing flag has held longer than this, the finisher goroutine is wedged
// inside Emit and the admission gate is shut forever (579 family shape 3:
// 20261007 146 min / 20261011 75+ min silent queue stranding). 90s mirrors
// control.FinishingStuckTimeout — twice Task 303's 45s running-stale bound,
// conservative against slow-but-alive fan-outs.
const idleTurnFinishingStuckTimeout = 90 * time.Second

// idleTurnSweepLogInterval rate-limits the 任务796 sweep observability line
// per (contact, decision): the pump ticks every 4s and a healthy idle target
// waiting out its 45s window must not produce one log line per tick.
const idleTurnSweepLogInterval = 60 * time.Second

// idleTurnHeadStuckWarnTimeout is the 任务1017 批C queue-head stranding alarm
// (2026-10-20 根因定位报告 §四-#5): a queue head that stays the head for this
// long is no longer "waiting out its idle window" (45s) nor a normal turn —
// it is the 579-family stranding shape (failed opening, wedged fan-out, or a
// vanished target), and every later message queues behind it silently. The
// WARN line is rate-limited to once per minute per contact.
const idleTurnHeadStuckWarnTimeout = 10 * time.Minute

// idleTurnTargetView is one pump pass's view of a live collab target, flattened
// so the sweep core is unit-testable without a desktop App (same seam pattern
// as collabDelivery).
type idleTurnTargetView struct {
	contactID string
	// tabID is the desktop tab backing this target (empty for runtime-only
	// contacts). 任务731 S2: when this item's opening budget is spent, the
	// autopilot abnormal-stop watchdog escalates a session-level wake
	// through it (see autopilot_resume.go).
	tabID string
	// activeTab marks the currently focused visible tab: the user may be about
	// to type there, so the bridge never opens a turn (边界 2).
	activeTab bool
	running   bool
	// 任务709: paused now means USER-held only (snap.Paused && snap.UserPaused)
	// — a queue a person is holding, never opened. autoPaused marks the
	// automatic pauses a restart/reopen leaves behind (snap.Paused &&
	// !snap.UserPaused): the bridge clears one before opening so a stale
	// session's fresh delivery is not held hostage (708 实测盲区).
	paused     bool
	autoPaused bool
	// resume clears the automatic pause (Controller.ResumeInboxAutoPause).
	// A user pause that lands between the snapshot and the wake wins: the
	// store-level check is atomic. Tests inject a stub; nil + autoPaused would
	// be a wiring bug, so the sweep treats it as a persistent failure.
	resume   func() error
	queuedID string // FIFO head queued item, "" when none
	// 任务796 observability: "state:count" summary of every durable inbox
	// item, so a target with NO queued head can still be told apart — an
	// empty inbox reads "none", a stranded running/uncertain item reads
	// "running:1" and explains why the bridge sees no work.
	itemsSummary string
	// 任务309 receipt coordinates + collab source of the queued head, so a
	// spent budget can answer the sender through get_message_status (570).
	// Empty for local queue entries.
	queuedCollabMsgID  string
	queuedCollabMailTo string
	queuedSource       string
	// run opens one inbox turn (the Bot/ACP RunInboxTurn path). Tests inject a
	// stub; production asserts nothing — SessionAPI embeds Inbox.
	run func(ctx context.Context, id string) error
	// 任务796: finishing mirrors the TurnDone fan-out window (RuntimeStatus.
	// Finishing). finishing-only (not running, finishing held past the leak
	// threshold) is the third 579-family anomaly — a wedged fan-out keeps the
	// admission gate shut while the bridge would otherwise read the target as
	// merely busy. finishingFor is the observed window age.
	finishing    bool
	finishingFor time.Duration
	// heal clears a wedged finishing window (Controller.HealStuckFinishing).
	// Tests inject a stub; nil disables the heal and keeps the skip+log
	// behaviour (observability without action).
	heal func() (bool, time.Duration)
	// exhausted fires once per stuck item when the 3-strike budget is spent.
	// Production records a 570 receipt; tests collect. nil keeps log-only.
	exhausted func(contactID, itemID, collabMsgID, collabMailTo, source string, attempts int, lastErr error)
	// retrying fires on every persistent failure BELOW the strike budget
	// (任务1017 批C-D): the per-attempt intermediate state, recorded as a
	// failed_retrying receipt so get_message_status can answer "retrying
	// 1/3" while the bridge is still working the head. nil keeps log-only.
	retrying func(contactID, itemID, collabMsgID, collabMailTo, source string, attempts int, lastErr error)
}

// idleTurnBridge is the pump-owned per-target state: when a target was first
// observed idle, which targets have a bridge turn in flight, and the 任务579
// per-item opening budget.
type idleTurnBridge struct {
	mu        sync.Mutex
	idleSince map[string]time.Time
	inFlight  map[string]bool
	// headItem tracks the queue head we last saw per contact, so budget state
	// is keyed to (contact, head item) and reset when a different message
	// arrives. failCounts counts persistent opening failures for the head;
	// exhausted marks heads whose budget is spent (kept queued, not dropped).
	headItem   map[string]string
	failCounts map[string]int
	exhausted  map[string]bool
	// headSeen tracks when the current queue head was first observed per
	// contact (任务1017 批C): the age of an unchanged head is the stranding
	// clock the >10min WARN reports. Keyed by the item itself so the clock
	// survives across ticks regardless of budget bookkeeping; reset when the
	// head changes or clears.
	headSeen map[string]headObservation
	// lastFailed records the most recent persistent opening failure per
	// contact (任务1017 批B): {head item, time, error}. The sweep consults it
	// to detect the "target vanished from the live list right after its
	// opening attempt failed" shape (2026-10-19 17:31 现场) and say so out
	// loud instead of letting the contact go silent.
	lastFailed map[string]failedOpening
	// idleTurnSweepLogKeys rate-limits the 任务796 sweep observability line:
	// key = contactID + "\x00" + decision, value = last emit time. A target
	// stuck in the same skip shape must not log one line per 4s pump tick.
	sweepLog map[string]time.Time
}

// failedOpening is one contact's last persistent opening failure, kept so the
// sweep can name the item and the error when the target later vanishes.
type failedOpening struct {
	itemID string
	at     time.Time
	err    error
}

// headObservation is one contact's unchanged-queue-head clock entry.
type headObservation struct {
	itemID string
	since  time.Time
}

func newIdleTurnBridge() *idleTurnBridge {
	return &idleTurnBridge{
		idleSince:  map[string]time.Time{},
		inFlight:   map[string]bool{},
		headItem:   map[string]string{},
		failCounts: map[string]int{},
		exhausted:  map[string]bool{},
		headSeen:   map[string]headObservation{},
		lastFailed: map[string]failedOpening{},
		sweepLog:   map[string]time.Time{},
	}
}

// sweepDecision is the 任务796 classification of what one bridge pass did
// with a queued target. It is the observable answer to "why is this follow-up
// not being consumed" — the 20261011 incident had no such trace and stayed
// unexplained for 75+ minutes.
type sweepDecision string

const (
	sweepOpened          sweepDecision = "opened"
	sweepSkipActiveTab   sweepDecision = "skip:active_tab"
	sweepSkipRunning     sweepDecision = "skip:running"
	sweepHealedFinishing sweepDecision = "healed:finishing_only"
	sweepSkipPausedUser  sweepDecision = "skip:paused_user"
	sweepSkipNoHead      sweepDecision = "skip:no_queued_head"
	sweepSkipBudget      sweepDecision = "skip:budget_exhausted"
	sweepSkipWarming     sweepDecision = "skip:idle_warming"
	sweepSkipInFlight    sweepDecision = "skip:in_flight"
	sweepSkipNoRun       sweepDecision = "skip:no_run_seam"
	// 任务1017 批C: the queue head has held its position past the stranding
	// threshold — the WARN-level "this line is being silently blocked" trace.
	sweepWarnHeadStuck sweepDecision = "warn:head_stuck"
	// 任务1017 批B: a target whose opening attempt just failed is no longer
	// in this pass's target list at all — the 2026-10-19 17:31 shape where
	// the contact went permanently silent. WARN so the next现场 reads the
	// disappearance directly instead of deriving it from absent logs.
	sweepWarnTargetVanished sweepDecision = "warn:target_vanished_after_failure"
)

// logSweepDecision emits the rate-limited observability line for one target's
// evaluation. Pure formatting here; the rate-limit state lives on the bridge
// so tests can assert on the produced lines directly. Returns whether a line
// was emitted this call (任务1017 批C gates the paired slog.Warn on it).
func (b *idleTurnBridge) logSweepDecision(target idleTurnTargetView, decision sweepDecision, idle time.Duration, tracked bool, now time.Time) bool {
	if b == nil || target.contactID == "" {
		return false
	}
	b.mu.Lock()
	key := target.contactID + "\x00" + string(decision)
	if last, ok := b.sweepLog[key]; ok && now.Sub(last) < idleTurnSweepLogInterval {
		b.mu.Unlock()
		return false
	}
	b.sweepLog[key] = now
	b.mu.Unlock()
	idleText := "untracked"
	if tracked {
		idleText = idle.Round(time.Second).String()
	}
	log.Printf("[idle-turn-sweep] contact=%s tab=%s decision=%s running=%v finishing=%v finishing_for=%s paused=user:%v/auto:%v active_tab=%v items=[%s] head=%s idle=%s in_flight=%v",
		target.contactID, target.tabID, decision,
		target.running, target.finishing, target.finishingFor.Round(time.Second),
		target.paused, target.autoPaused, target.activeTab,
		target.itemsSummary, target.queuedID, idleText, b.inFlightNow(target.contactID))
	return true
}

// idleTurnItemKey namespaces budget state to one (contact, head item) pair.
func idleTurnItemKey(contactID, itemID string) string {
	return contactID + "\x00" + itemID
}

// noteHeadSeen advances the stranding clock for one contact's queue head
// (任务1017 批C). A new or changed head restarts the clock at now; an
// unchanged head returns how long it has held the position. Nil-map safe:
// a zero-value bridge treats the observation as new.
func (b *idleTurnBridge) noteHeadSeen(contactID, itemID string, now time.Time) time.Duration {
	if b == nil || contactID == "" {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.headSeen == nil {
		b.headSeen = map[string]headObservation{}
	}
	if obs, ok := b.headSeen[contactID]; ok && obs.itemID == itemID {
		return now.Sub(obs.since)
	}
	b.headSeen[contactID] = headObservation{itemID: itemID, since: now}
	return 0
}

// clearHeadSeen drops one contact's stranding clock (head consumed or moved).
func (b *idleTurnBridge) clearHeadSeen(contactID string) {
	if b == nil || contactID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.headSeen, contactID)
}

// recordFailedOpening remembers one contact's latest persistent opening
// failure (任务1017 批B) so the sweep can flag the target's later vanishing.
func (b *idleTurnBridge) recordFailedOpening(contactID, itemID string, at time.Time, runErr error) {
	if b == nil || contactID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lastFailed == nil {
		b.lastFailed = map[string]failedOpening{}
	}
	b.lastFailed[contactID] = failedOpening{itemID: itemID, at: at, err: runErr}
}

// clearFailedOpening forgets the failure marker once the contact is seen
// again in a sweep pass (presence proves the live list owns it again).
func (b *idleTurnBridge) clearFailedOpening(contactID string) {
	if b == nil || contactID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.lastFailed, contactID)
}

// snapshotFailedOpenings copies the failure markers so the sweep can walk
// them without holding the bridge lock.
func (b *idleTurnBridge) snapshotFailedOpenings() map[string]failedOpening {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.lastFailed) == 0 {
		return nil
	}
	out := make(map[string]failedOpening, len(b.lastFailed))
	for contactID, failure := range b.lastFailed {
		out[contactID] = failure
	}
	return out
}

// warnTargetVanished emits the rate-limited WARN for a contact that dropped
// out of the sweep's target list while its last opening attempt had failed
// and its item is still the durable queue head — the exact 2026-10-19 17:31
// shape (attempt 1 canceled → contact silent forever, zero skip logs because
// a target outside the list never even reaches the noisy check). The
// acceptance criterion "a failed attempt is followed by a next evaluation
// record within 60s" is what this line guarantees for the vanished case: the
// very next pump tick (4s) produces it when the normal sweep cannot.
func (b *idleTurnBridge) warnTargetVanished(contactID string, failure failedOpening, now time.Time) {
	if b == nil {
		return
	}
	b.mu.Lock()
	key := contactID + "\x00" + string(sweepWarnTargetVanished)
	if last, ok := b.sweepLog[key]; ok && now.Sub(last) < idleTurnSweepLogInterval {
		b.mu.Unlock()
		return
	}
	b.sweepLog[key] = now
	b.mu.Unlock()
	slog.Warn("[idle-turn-sweep] collab target vanished from sweep after opening failure",
		"contact", contactID,
		"item", failure.itemID,
		"failed_at", failure.at.Format("15:04:05.000"),
		"since_failure", now.Sub(failure.at).Round(time.Second),
		"last_error", failure.err,
		"hint", "该 contact 已不在本轮 sweep 目标列表（live+runtime-only+dormant 反查都没接住）；队列消息将无人评估。排查 tab detach/ctrl 丢失/contact 注销路径")
}

// observeIdle records the idle clock for one target: running resets it (and
// the opening budget — a turn proves the session can run again), first idle
// observation starts it.
func (b *idleTurnBridge) observeIdle(contactID string, running bool, now time.Time) {
	if b == nil || contactID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if running {
		delete(b.idleSince, contactID)
		b.clearContactBudgetLocked(contactID)
		return
	}
	if _, ok := b.idleSince[contactID]; !ok {
		b.idleSince[contactID] = now
	}
}

// clearContactBudgetLocked drops every budget entry belonging to one contact's
// current head (caller holds mu).
func (b *idleTurnBridge) clearContactBudgetLocked(contactID string) {
	prefix := contactID + "\x00"
	for key := range b.failCounts {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			delete(b.failCounts, key)
		}
	}
	for key := range b.exhausted {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			delete(b.exhausted, key)
		}
	}
}

func (b *idleTurnBridge) idleFor(contactID string, now time.Time) (time.Duration, bool) {
	if b == nil || contactID == "" {
		return 0, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	since, ok := b.idleSince[contactID]
	if !ok {
		return 0, false
	}
	return now.Sub(since), true
}

func (b *idleTurnBridge) markInFlight(contactID string) bool {
	if b == nil || contactID == "" {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.inFlight[contactID] {
		return false
	}
	b.inFlight[contactID] = true
	return true
}

func (b *idleTurnBridge) clearInFlight(contactID string) {
	if b == nil || contactID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.inFlight, contactID)
}

// inFlightNow snapshots the in-flight flag for the 任务796 sweep log line.
func (b *idleTurnBridge) inFlightNow(contactID string) bool {
	if b == nil || contactID == "" {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.inFlight[contactID]
}

// advanceHead re-keys the budget when a different message becomes the queue
// head: a fresh message always gets a fresh budget, and the old head's spent
// state cannot leak onto it. Returns whether the head is suppressed.
func (b *idleTurnBridge) advanceHead(contactID, itemID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.headItem[contactID] != itemID {
		b.clearContactBudgetLocked(contactID)
		b.headItem[contactID] = itemID
	}
	return b.exhausted[idleTurnItemKey(contactID, itemID)]
}

// idleTurnRaceError classifies the "someone else won" outcomes: the item is
// gone, owned by another admission path, blocked by materialization, or the
// queue got paused again — ErrPaused is the 任务709 edge where a user pause
// lands between the bridge's snapshot and its wake (the atomic store-level
// check let the resume through as a no-op, the claim then answers ErrPaused).
// A queue a human just took over is not a bridge failure and must not burn
// the opening budget.
func idleTurnRaceError(err error) bool {
	return errors.Is(err, control.ErrTurnRunning) ||
		errors.Is(err, sessioninbox.ErrNotFound) ||
		errors.Is(err, sessioninbox.ErrInvalidState) ||
		errors.Is(err, sessioninbox.ErrPaused)
}

// recordOpeningFailure books one persistent failure against the head item's
// budget. At the 3rd strike the item is suppressed (kept queued — never
// dropped) and the exhaustion sink fires so the sender can see the state.
// 任务1017 批B: every persistent failure is also remembered per contact so
// the sweep can flag the target vanishing right after a failure.
func (b *idleTurnBridge) recordOpeningFailure(target idleTurnTargetView, itemID string, runErr error) {
	contactID := target.contactID
	b.recordFailedOpening(contactID, itemID, time.Now(), runErr)
	b.mu.Lock()
	key := idleTurnItemKey(contactID, itemID)
	b.failCounts[key]++
	attempts := b.failCounts[key]
	if attempts < idleTurnMaxAttempts {
		b.mu.Unlock()
		log.Printf("[session-collab] idle-turn bridge: opening attempt %d/%d failed for contact %s item %s: %v",
			attempts, idleTurnMaxAttempts, contactID, itemID, runErr)
		// 任务1017 批C-D: the intermediate state the sender can query —
		// failed_retrying with the attempt count, overwritten by a later
		// success or by the terminal exhausted receipt at strike 3.
		if hook := target.retrying; hook != nil {
			hook(contactID, itemID, target.queuedCollabMsgID, target.queuedCollabMailTo, target.queuedSource, attempts, runErr)
		}
		return
	}
	b.exhausted[key] = true
	delete(b.failCounts, key)
	hook := target.exhausted
	b.mu.Unlock()
	log.Printf("[session-collab] idle-turn bridge: opening budget exhausted after %d attempts for contact %s item %s (msg %s); item stays queued, not dropped; last error: %v",
		idleTurnMaxAttempts, contactID, itemID, target.queuedCollabMsgID, runErr)
	if hook != nil {
		hook(contactID, itemID, target.queuedCollabMsgID, target.queuedCollabMailTo, target.queuedSource, idleTurnMaxAttempts, runErr)
	}
}

// sweepTargets is the bridge pass over one pump tick's flattened targets. It
// tracks the idle clock for every target, then opens at most one turn per
// eligible target. Purity stops at run(): each start goes out on its own
// goroutine because RunInboxTurn is synchronous for the whole turn.
//
// 任务796: every queued target gets a classified, rate-limited observability
// line ([idle-turn-sweep]) stating exactly which gate held it back, and the
// finishing-only leak shape (wedged TurnDone fan-out, 579 family shape 3) is
// healed through the controller seam instead of being skipped silently.
func (b *idleTurnBridge) sweepTargets(targets []idleTurnTargetView, now time.Time) {
	// 任务1017 批B: before anything else, check for targets that vanished
	// from this pass right after their opening attempt failed (2026-10-19
	// 17:31 shape: attempt 1 canceled, then zero sweep records ever again).
	// Presence clears the marker; absence past the rate limit says so out
	// loud — the "next evaluation" the acceptance criteria demand even when
	// the live list has already dropped the contact.
	present := make(map[string]bool, len(targets))
	for _, target := range targets {
		if target.contactID != "" {
			present[target.contactID] = true
		}
	}
	for contactID, failure := range b.snapshotFailedOpenings() {
		if present[contactID] {
			b.clearFailedOpening(contactID)
			continue
		}
		b.warnTargetVanished(contactID, failure, now)
	}
	for _, target := range targets {
		b.observeIdle(target.contactID, target.running, now)
	}
	for _, target := range targets {
		idle, tracked := b.idleFor(target.contactID, now)
		// 任务1017 批C: the queue-head stranding clock. A user-held pause is
		// a person's will, not a strand — no alarm there. Every other held
		// head (active tab, running, warming, exhausted budget) past the
		// threshold gets one WARN per minute.
		if target.queuedID != "" && !target.paused {
			if stuck := b.noteHeadSeen(target.contactID, target.queuedID, now); stuck >= idleTurnHeadStuckWarnTimeout {
				if b.logSweepDecision(target, sweepWarnHeadStuck, idle, tracked, now) {
					slog.Warn("[idle-turn-sweep] inbox queue head stuck past stranding threshold",
						"contact", target.contactID,
						"tab", target.tabID,
						"item", target.queuedID,
						"stuck_for", stuck.Round(time.Second),
						"threshold", idleTurnHeadStuckWarnTimeout,
						"running", target.running,
						"finishing", target.finishing,
						"active_tab", target.activeTab,
						"items", target.itemsSummary,
						"hint", "后续消息全部排在这条队首之后；若 attempt 失败与 target 消失成对出现，见 warn:target_vanished_after_failure")
				}
			}
		} else {
			b.clearHeadSeen(target.contactID)
		}
		// 边界 2：active tab 是用户面前的会话，永不开轮。
		if target.activeTab || target.contactID == "" {
			if target.contactID != "" && target.queuedID != "" {
				b.logSweepDecision(target, sweepSkipActiveTab, idle, tracked, now)
			}
			continue
		}
		// 任务796: a wedged TurnDone fan-out keeps RuntimeStatus().Running true
		// (the merge includes finishing), so the target reads as merely busy to
		// every check below and observeIdle never even starts its clock. The
		// unambiguous leak shape is the finishing flag itself held past the
		// threshold (a live FIFO drain re-stamps finishingSince each turn, so
		// an old stamp means no drain is flowing). Heal — reopen the gate —
		// and let the NEXT tick take the normal opening path; the heal never
		// opens a turn itself.
		if target.finishing && target.finishingFor >= idleTurnFinishingStuckTimeout && target.queuedID != "" && target.heal != nil {
			healed, stuck := target.heal()
			if healed {
				b.logSweepDecision(target, sweepHealedFinishing, idle, tracked, now)
				log.Printf("[session-collab] idle-turn bridge: healed finishing-only gate for contact %s (wedged %s ≥ %s, queued item %s re-arms next sweep)",
					target.contactID, stuck.Round(time.Second), idleTurnFinishingStuckTimeout, target.queuedID)
				continue
			}
		}
		// 任务709：paused 只指用户显式持有（UserPaused）；自动暂停（重启恢复/
		// 重开遗留）不算——那种队列走下面的唤醒路径，stale 会话的新投递不再
		// 静默搁浅。
		if target.running || target.paused || target.queuedID == "" {
			decision := sweepSkipRunning
			if !target.running {
				if target.paused {
					decision = sweepSkipPausedUser
				} else {
					decision = sweepSkipNoHead
				}
			}
			// A genuinely empty inbox is the healthy no-head shape: logging it
			// once a minute per target would be pure noise. Everything else —
			// a queued head held back by running/pause, or stranded items in
			// non-queued states (任务796 H4 shape) — is worth the trace.
			noisy := target.queuedID != "" || target.itemsSummary != "" && target.itemsSummary != "none"
			if noisy {
				b.logSweepDecision(target, decision, idle, tracked, now)
			}
			continue
		}
		if target.run == nil {
			b.logSweepDecision(target, sweepSkipNoRun, idle, tracked, now)
			continue
		}
		// 任务579：预算用尽的队首不再自动重开（保留队列，等新队首/会话活动
		// 复位预算），否则就是旧的「每 4s 无限重试」。
		if b.advanceHead(target.contactID, target.queuedID) {
			b.logSweepDecision(target, sweepSkipBudget, idle, tracked, now)
			continue
		}
		if !tracked || idle < sessionCollabIdleTurnDelay {
			b.logSweepDecision(target, sweepSkipWarming, idle, tracked, now)
			continue
		}
		if !b.markInFlight(target.contactID) {
			b.logSweepDecision(target, sweepSkipInFlight, idle, tracked, now)
			continue // previous bridge turn still running for this target
		}
		b.logSweepDecision(target, sweepOpened, idle, tracked, now)
		id := target.queuedID
		contact := target.contactID
		run := target.run
		resume := target.resume
		autoPaused := target.autoPaused
		safego.Go("sessioncollab.idle-turn", func() {
			defer b.clearInFlight(contact)
			if autoPaused {
				// 任务709：开轮前解除自动暂停。失败按持续性失败计 strike
				//（预算护住，不形成热循环）；用户在快照与唤醒之间恰好暂停的
				// 竞态由 store 层原子判定接住（resume 返回 false，无错）。
				if resume == nil {
					b.recordOpeningFailure(target, id, errors.New("auto-paused target has no resume seam"))
					return
				}
				if err := resume(); err != nil {
					log.Printf("[session-collab] idle-turn bridge: auto-pause resume failed for contact %s item %s: %v", contact, id, err)
					b.recordOpeningFailure(target, id, err)
					return
				}
				log.Printf("[session-collab] idle-turn bridge: cleared automatic inbox pause for contact %s before opening (stale session wake, 709)", contact)
			}
			log.Printf("[session-collab] idle-turn bridge: opening turn for contact %s (inbox item %s, idle %s ≥ N=%s)",
				contact, id, idle.Round(time.Second), sessionCollabIdleTurnDelay)
			err := run(context.Background(), id)
			if err == nil {
				b.clearBudget(contact, id)
				log.Printf("[session-collab] idle-turn bridge: turn completed for contact %s (inbox item %s consumed)", contact, id)
				return
			}
			if idleTurnRaceError(err) {
				// ErrTurnRunning / ErrInvalidState / ErrNotFound mean another
				// admission path won the race for this item or the turn — the
				// item stays owned by whoever claimed it; not a bridge failure
				// and not a strike against the budget.
				b.clearBudget(contact, id)
				log.Printf("[session-collab] idle-turn bridge: turn for contact %s item %s ended with admission-race error: %v", contact, id, err)
				return
			}
			// 任务579：持续性失败计一次 strike，3 次用尽后封存并上报。
			b.recordOpeningFailure(target, id, err)
		})
	}
}

// clearBudget resets one head item's opening budget (success or lost race).
func (b *idleTurnBridge) clearBudget(contactID, itemID string) {
	if b == nil || contactID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	key := idleTurnItemKey(contactID, itemID)
	delete(b.failCounts, key)
	delete(b.exhausted, key)
}

// idleTurnSweepActive is the 守卫④ gate: the bridge and the drain_inbox
// consumption mode (experimental_collab_background_delivery, task 224) are
// mutually exclusive — when drain_inbox owns consumption the pump delivers
// nothing and the bridge must not open turns either. Pure so the exclusion
// stays regression-testable without boot state.
func idleTurnSweepActive(backgroundDelivery bool) bool {
	return !backgroundDelivery
}

// summarizeInboxItems renders the 任务796 durable-state summary for one
// sweep log line: "state:count" pairs in first-seen order (small inboxes only,
// capped to keep the line bounded), "none" for an empty inbox. It exists so a
// target with no queued head can still be told apart — "queued:1" is the
// healthy wait, "running:1" strands a claim (H4 shape), "uncertain:1" waits
// for human review by design.
func summarizeInboxItems(items []sessioninbox.InboxItemMeta) string {
	if len(items) == 0 {
		return "none"
	}
	order := make([]string, 0, 4)
	counts := map[string]int{}
	for i, item := range items {
		if i >= 16 {
			break
		}
		state := string(item.State)
		if counts[state] == 0 {
			order = append(order, state)
		}
		counts[state]++
	}
	parts := make([]string, 0, len(order))
	for _, state := range order {
		parts = append(parts, state+":"+strconv.Itoa(counts[state]))
	}
	return strings.Join(parts, ",")
}

// sweepIdleInboxTurns is the App-facing adapter: flatten the live collab
// targets (visible non-active tabs + detached runtimes) and run one bridge
// pass. Called from the pump's drainOnce so the bridge rides the existing 4s
// tick and the existing sessionCollabEnabled gate. 任务579①: the bridge has no
// switch of its own any more — the only gate left is the drain_inbox mutual
// exclusion (守卫④).
func (p *sessionCollabPump) sweepIdleInboxTurns(now time.Time) {
	if !idleTurnSweepActive(collabBackgroundDelivery()) {
		// drain_inbox 模式（task 224）：pump 不投递、降级不产生，桥不叠加。
		return
	}
	var targets []idleTurnTargetView
	// 任务887: contacts covered by a live runtime this pass — the dormant
	// reverse lookup must never stand up a session the live half already owns.
	covered := map[string]bool{}
	targets = append(targets, p.idleTurnTargetViews(p.app.sessionCollabLiveTargets(nil), covered, now)...)
	// 任务887: 第三态——durable inbox 有排队 follow-up、本进程却没有任何
	// runtime 的会话（从未打开/重启后不落盘）。反查出的候选注入同一趟
	// sweepTargets：空闲窗口、579 三振、in-flight 去重、570 回执、709 唤醒
	// 全部复用；唯一新增是 run 前按需拉起 runtime（wakeDormantRuntime）。
	targets = append(targets, p.dormantIdleTurnTargets(now, covered)...)
	p.idleTurns.sweepTargets(targets, now)
}

// idleTurnTargetViews flattens one sweep pass's live collab targets into sweep
// views. 任务922（886 机制②）: sessionCollabTargets builds visible-tab targets
// WITHOUT the ctrl field, so the previous unconditional `t.ctrl == nil`
// skip made the bridge blind to every visible tab — including an over-limit
// long session left sitting on one (2026-10-14 新6 现场：reattach 后会话在
// 可见 tab，桥全程看不见，queued_followup 4 次 drain 空转无人接续；佐证是
// 生产日志 decision=skip:active_tab 0 条——该分支当时不可达）。Resolve the
// controller through the tab registry — the same tabByID.Ctrl fallback
// collabSessionWorkDetail (任务667) already uses — so visible tabs rejoin the
// 569 设计内的桥可见性（「仅非 active tab 开轮」的边界 2 仍在 sweepTargets
// 内判定，active tab 照旧不开）。filled marks which contacts received a view
// (887 dormant reverse lookup must not stand up a session the live half owns).
func (p *sessionCollabPump) idleTurnTargetViews(live []sessionCollabTarget, filled map[string]bool, now time.Time) []idleTurnTargetView {
	var views []idleTurnTargetView
	for _, t := range live {
		if t.contactID != "" {
			filled[t.contactID] = true
		}
		ctrl := t.ctrl
		if ctrl == nil && t.tabID != "" {
			if tab := p.app.tabByID(t.tabID); tab != nil {
				ctrl = tab.Ctrl
			}
		}
		if ctrl == nil {
			// 留痕不再静默：target 快照与 sweep 之间 runtime 消失正是 886
			// 隐蔽盲区的形状，一行日志让下一次现场可直接读出。
			log.Printf("[idle-turn-sweep] contact=%s tab=%s skip:no_ctrl_resolvable (target snapshot lost its runtime before the sweep)", t.contactID, t.tabID)
			continue
		}
		view := idleTurnTargetView{contactID: t.contactID, tabID: t.tabID, activeTab: t.activeTab}
		st := ctrl.RuntimeStatus()
		view.running = st.Running
		// 任务796: the raw TurnDone fan-out window and its age. RuntimeStatus.
		// Running merges running||finishing, so a wedged fan-out is invisible
		// in view.running — the sweep classifies the leak from these two.
		view.finishing = st.Finishing
		if st.FinishingSinceUnix != 0 {
			view.finishingFor = now.Sub(time.Unix(st.FinishingSinceUnix, 0))
		}
		snap := ctrl.InboxSnapshot()
		// 任务709: pause provenance — only a user-held pause mutes the bridge;
		// an automatic pause (restart recovery / reopen backlog) becomes a
		// wake at open time. paused and autoPaused are mutually exclusive.
		view.paused = snap.Paused && snap.UserPaused
		view.autoPaused = snap.Paused && !snap.UserPaused
		view.resume = func() error {
			_, err := ctrl.ResumeInboxAutoPause()
			return err
		}
		// 任务796: durable state summary for the sweep observability line.
		view.itemsSummary = summarizeInboxItems(snap.Items)
		view.heal = func() (bool, time.Duration) {
			return ctrl.HealStuckFinishing(idleTurnFinishingStuckTimeout)
		}
		for _, item := range snap.Items {
			if item.State == sessioninbox.StateQueued {
				view.queuedID = item.ID
				view.queuedCollabMsgID = item.CollabMsgID
				view.queuedCollabMailTo = item.CollabMailTo
				view.queuedSource = item.Source
				break
			}
		}
		view.run = ctrl.RunInboxTurn
		exhausted := p.recordIdleTurnExhausted
		if t.tabID != "" {
			// 任务731 S2 (收信后异常态无法开轮的合并线形态): the bridge has
			// spent its opening budget on this queue head — escalate one
			// session-level wake through the autopilot watchdog. The
			// watchdog re-checks the autopilot master switch at fire time,
			// so non-autopilot targets stay exactly as today (receipt only).
			tabID := t.tabID
			exhausted = func(contactID, itemID, collabMsgID, collabMailTo, source string, attempts int, lastErr error) {
				p.recordIdleTurnExhausted(contactID, itemID, collabMsgID, collabMailTo, source, attempts, lastErr)
				p.app.escalateStuckInboxForTab(tabID, attempts, lastErr)
			}
		}
		view.exhausted = exhausted
		view.retrying = p.recordIdleTurnRetrying
		views = append(views, view)
	}
	return views
}

// recordIdleTurnRetrying persists the per-attempt intermediate state as a
// failed_retrying receipt (任务1017 批C-D), so get_message_status answers
// "retrying N/3" while the budget is not yet spent — the 2026-10-19 现场's
// silence (sender saw queued_followup forever, log-only attempts) is what
// this closes. Best-effort like the exhausted receipt: a failure only costs
// visibility. A later success or the terminal exhausted receipt overwrites it.
func (p *sessionCollabPump) recordIdleTurnRetrying(contactID, itemID, collabMsgID, collabMailTo, source string, attempts int, lastErr error) {
	if collabMsgID == "" {
		return // local queue entry: no mail message to answer
	}
	mailDir := config.SessionCollabMailDir()
	if mailDir == "" {
		return
	}
	store := sessioncollab.NewMailStoreWithHopLimit(mailDir, sessionCollabHopLimit())
	detail := fmt.Sprintf("目标空闲开轮第 %d/%d 次尝试未成功（%v）；消息保留在目标收件箱队列，未丢失，桥将自动重试", attempts, idleTurnMaxAttempts, lastErr)
	if err := store.RecordDeliveryReceipt(context.Background(), sessioncollab.DeliveryReceipt{
		MessageID: collabMsgID,
		From:      strings.TrimPrefix(source, "collab:"),
		To:        collabMailTo,
		Outcome:   sessioncollab.ReceiptFailedRetrying,
		Detail:    detail,
		Attempts:  attempts,
	}); err != nil {
		log.Printf("[session-collab] idle-turn retrying receipt for %s: %v", collabMsgID, err)
	}
}

// recordIdleTurnExhausted persists the spent opening budget as a 任务570
// delivery receipt (outcome open_retry_exhausted), so get_message_status can
// tell the sender the truth instead of leaving the queued_followup green light
// standing. Best-effort: the item itself is already durable in the target
// inbox; a receipt failure only costs visibility, never the message.
func (p *sessionCollabPump) recordIdleTurnExhausted(contactID, itemID, collabMsgID, collabMailTo, source string, attempts int, lastErr error) {
	if collabMsgID == "" {
		return // local queue entry: no mail message to answer
	}
	mailDir := config.SessionCollabMailDir()
	if mailDir == "" {
		return
	}
	store := sessioncollab.NewMailStoreWithHopLimit(mailDir, sessionCollabHopLimit())
	detail := fmt.Sprintf("目标空闲开轮尝试 %d 次未成功（%v）；消息仍保留在目标会话收件箱队列，未丢失，可人工重试", attempts, lastErr)
	if err := store.RecordDeliveryReceipt(context.Background(), sessioncollab.DeliveryReceipt{
		MessageID: collabMsgID,
		From:      strings.TrimPrefix(source, "collab:"),
		To:        collabMailTo,
		Outcome:   sessioncollab.ReceiptOpenRetryExhausted,
		Detail:    detail,
		Attempts:  attempts,
	}); err != nil {
		log.Printf("[session-collab] idle-turn exhaustion receipt for %s: %v", collabMsgID, err)
	}
}
