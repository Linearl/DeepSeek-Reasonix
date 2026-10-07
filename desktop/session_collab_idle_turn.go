package main

import (
	"context"
	"errors"
	"fmt"
	"log"
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
// 另一处保守约束：inbox 处于 paused（用户显式持有队列，如恢复横幅）时绝不开轮
// ——暂停是用户意志，桥不越。

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

// idleTurnTargetView is one pump pass's view of a live collab target, flattened
// so the sweep core is unit-testable without a desktop App (same seam pattern
// as collabDelivery).
type idleTurnTargetView struct {
	contactID string
	// activeTab marks the currently focused visible tab: the user may be about
	// to type there, so the bridge never opens a turn (边界 2).
	activeTab bool
	running   bool
	// paused mirrors the inbox snapshot: a paused queue is user-held state.
	paused   bool
	queuedID string // FIFO head queued item, "" when none
	// 任务309 receipt coordinates + collab source of the queued head, so a
	// spent budget can answer the sender through get_message_status (570).
	// Empty for local queue entries.
	queuedCollabMsgID  string
	queuedCollabMailTo string
	queuedSource       string
	// run opens one inbox turn (the Bot/ACP RunInboxTurn path). Tests inject a
	// stub; production asserts nothing — SessionAPI embeds Inbox.
	run func(ctx context.Context, id string) error
	// exhausted fires once per stuck item when the 3-strike budget is spent.
	// Production records a 570 receipt; tests collect. nil keeps log-only.
	exhausted func(contactID, itemID, collabMsgID, collabMailTo, source string, attempts int, lastErr error)
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
}

func newIdleTurnBridge() *idleTurnBridge {
	return &idleTurnBridge{
		idleSince:  map[string]time.Time{},
		inFlight:   map[string]bool{},
		headItem:   map[string]string{},
		failCounts: map[string]int{},
		exhausted:  map[string]bool{},
	}
}

// idleTurnItemKey namespaces budget state to one (contact, head item) pair.
func idleTurnItemKey(contactID, itemID string) string {
	return contactID + "\x00" + itemID
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
// gone, owned by another admission path, or blocked by materialization — none
// of them is a bridge failure, and none may burn the opening budget.
func idleTurnRaceError(err error) bool {
	return errors.Is(err, control.ErrTurnRunning) ||
		errors.Is(err, sessioninbox.ErrNotFound) ||
		errors.Is(err, sessioninbox.ErrInvalidState)
}

// recordOpeningFailure books one persistent failure against the head item's
// budget. At the 3rd strike the item is suppressed (kept queued — never
// dropped) and the exhaustion sink fires so the sender can see the state.
func (b *idleTurnBridge) recordOpeningFailure(target idleTurnTargetView, itemID string, runErr error) {
	contactID := target.contactID
	b.mu.Lock()
	key := idleTurnItemKey(contactID, itemID)
	b.failCounts[key]++
	attempts := b.failCounts[key]
	if attempts < idleTurnMaxAttempts {
		b.mu.Unlock()
		log.Printf("[session-collab] idle-turn bridge: opening attempt %d/%d failed for contact %s item %s: %v",
			attempts, idleTurnMaxAttempts, contactID, itemID, runErr)
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
func (b *idleTurnBridge) sweepTargets(targets []idleTurnTargetView, now time.Time) {
	for _, target := range targets {
		b.observeIdle(target.contactID, target.running, now)
	}
	for _, target := range targets {
		// 边界 2：active tab 是用户面前的会话，永不开轮。
		if target.activeTab || target.contactID == "" {
			continue
		}
		if target.running || target.paused || target.queuedID == "" {
			continue
		}
		if target.run == nil {
			continue
		}
		// 任务579：预算用尽的队首不再自动重开（保留队列，等新队首/会话活动
		// 复位预算），否则就是旧的「每 4s 无限重试」。
		if b.advanceHead(target.contactID, target.queuedID) {
			continue
		}
		idle, tracked := b.idleFor(target.contactID, now)
		if !tracked || idle < sessionCollabIdleTurnDelay {
			continue
		}
		if !b.markInFlight(target.contactID) {
			continue // previous bridge turn still running for this target
		}
		id := target.queuedID
		contact := target.contactID
		run := target.run
		safego.Go("sessioncollab.idle-turn", func() {
			defer b.clearInFlight(contact)
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
	for _, t := range p.app.sessionCollabLiveTargets(nil) {
		if t.ctrl == nil {
			continue
		}
		view := idleTurnTargetView{contactID: t.contactID, activeTab: t.activeTab}
		view.running = t.ctrl.RuntimeStatus().Running
		snap := t.ctrl.InboxSnapshot()
		view.paused = snap.Paused
		for _, item := range snap.Items {
			if item.State == sessioninbox.StateQueued {
				view.queuedID = item.ID
				view.queuedCollabMsgID = item.CollabMsgID
				view.queuedCollabMailTo = item.CollabMailTo
				view.queuedSource = item.Source
				break
			}
		}
		view.run = t.ctrl.RunInboxTurn
		view.exhausted = p.recordIdleTurnExhausted
		targets = append(targets, view)
	}
	p.idleTurns.sweepTargets(targets, now)
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
