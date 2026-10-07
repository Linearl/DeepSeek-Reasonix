package main

import (
	"context"
	"log"
	"sync"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/safego"
	"reasonix/internal/sessioninbox"
)

// 任务 569：空闲开轮桥（desktop 侧接上 Bot/ACP 已有的 inbox 开轮链路，「接桥」非「造桥」）。
//
// 问题（568 诊断 + 2026-10-07 调研核实）：desktop pump 每 4s 只做投递——MailStore →
// 目标会话 inbox 落箱 → Ack，不含任何开轮；消费锚定在「目标会话的 turn 内」。steer
// 在目标空闲时无法注入当轮，降级为排队 follow-up 后，controller 内建调度器对
// detached runtime 全数失效：desktop/turn_admission.go 的 beforeInboxDispatch 只在
// 可见 tab 里找 owner，detached runtime 找不到 → 返回 ErrInboxRuntimeUnpublished →
// dispatchInboxOnce 把这次 kick 当 idle 丢弃（「host owns the next dispatch kick」，
// 而 host 再也不会补踢）→ 消息永远躺在持久信箱（实测 21 会话 600+ 条、单会话最高
// 233 条，夜间无人值守时段损失约 5 小时产能）。
//
// Bot/ACP 有现成开轮链路：Controller.RunInboxTurn（internal/control/inbox_run.go，
// 注释原文 "Bot and ACP use this path"），调用方在 internal/acp/inbox_drain.go:38 与
// internal/bot/gateway.go:2271；desktop 此前无调用。本文件把该链路接到 pump 上。
//
// 四条硬边界（任务书，逐条落点）：
//  1. 触发 = 目标会话空闲持续 sessionCollabIdleTurnDelay（N 秒）+ inbox 存在排队
//     follow-up。N 常量取 45s（任务书给 30-60s 区间，取中位）：短于它容易抢在用户
//     两次输入之间开轮，长于它削弱消除静默死锁的时效。pump 周期 4s 是测量分辨率，
//     实际消费延迟 = max(N - 已空闲时长, 0) + 至多一个 pump 周期。
//  2. 不打断用户 = 仅非 active tab 开轮；detached runtime 无可见 tab，天然满足。
//  3. guard 对齐 = 走 Bot/ACP 同一条 RunInboxTurn：轮内动作服从该会话既有的审批/
//     guard 机制（ToolApprovalMode、guard 轮约束），本桥不新增任何能力面、不传任何
//     额外指令——消费的就是已落箱的那条 follow-up 原文。
//  4. 铁律 2 = experimental_collab_idle_turn 默认关；关 = 本桥整体不运行，行为与
//     现状等价（回退路径就是把开关关掉，无迁移、无残留状态）。
//
// 另两处保守约束：experimental_collab_background_delivery（task 224）开启时 pump
// 本就退出投递（drain_inbox 是唯一消费者），桥随之停用，两条消费模式不叠加；inbox
// 处于 paused（用户显式持有队列，如恢复横幅）时绝不开轮——暂停是用户意志，桥不越。

// sessionCollabIdleTurnDelay is the idle window N of the task-569 bridge. The
// task book prescribes 30-60s; 45s sits in the middle: below it the bridge can
// fire between two of the user's own inputs, above it the silent-deadlock
// relief gets slow. The 4s pump period is the measurement resolution.
const sessionCollabIdleTurnDelay = 45 * time.Second

// sessionCollabIdleTurnEnabled reads the live user config (铁律 2: default off).
// Live per pass, so a settings flip applies from the next pump tick.
func sessionCollabIdleTurnEnabled() bool {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return false
	}
	return cfg.Agent.ExperimentalCollabIdleTurn
}

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
	// run opens one inbox turn (the Bot/ACP RunInboxTurn path). Tests inject a
	// stub; production asserts nothing — SessionAPI embeds Inbox.
	run func(ctx context.Context, id string) error
}

// idleTurnBridge is the pump-owned per-target state: when a target was first
// observed idle, and which targets have a bridge turn in flight.
type idleTurnBridge struct {
	mu        sync.Mutex
	idleSince map[string]time.Time
	inFlight  map[string]bool
}

func newIdleTurnBridge() *idleTurnBridge {
	return &idleTurnBridge{idleSince: map[string]time.Time{}, inFlight: map[string]bool{}}
}

// observeIdle records the idle clock for one target: running resets it, first
// idle observation starts it.
func (b *idleTurnBridge) observeIdle(contactID string, running bool, now time.Time) {
	if b == nil || contactID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if running {
		delete(b.idleSince, contactID)
		return
	}
	if _, ok := b.idleSince[contactID]; !ok {
		b.idleSince[contactID] = now
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

// sweepTargets is the bridge pass over one pump tick's flattened targets. It
// tracks the idle clock for every target, then opens at most one turn per
// eligible target. Purity stops at run(): each start goes out on its own
// goroutine because RunInboxTurn is synchronous for the whole turn.
func (b *idleTurnBridge) sweepTargets(targets []idleTurnTargetView, now time.Time, enabled bool) {
	for _, target := range targets {
		b.observeIdle(target.contactID, target.running, now)
	}
	if !enabled {
		return
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
			if err != nil {
				// ErrTurnRunning / ErrInvalidState / ErrNotFound mean another
				// admission path won the race for this item or the turn — the
				// item stays owned by whoever claimed it; not a bridge failure.
				log.Printf("[session-collab] idle-turn bridge: turn for contact %s item %s ended with error: %v", contact, id, err)
				return
			}
			log.Printf("[session-collab] idle-turn bridge: turn completed for contact %s (inbox item %s consumed)", contact, id)
		})
	}
}

// sweepIdleInboxTurns is the App-facing adapter: flatten the live collab
// targets (visible non-active tabs + detached runtimes) and run one bridge
// pass. Called from the pump's drainOnce so the bridge rides the existing 4s
// tick and the existing sessionCollabEnabled gate. The idle clock runs even
// while the switch is off, so flipping it on relieves an existing backlog
// immediately instead of after another N seconds; the inbox snapshot (the only
// store-touching part) is read only while enabled.
func (p *sessionCollabPump) sweepIdleInboxTurns(now time.Time) {
	enabled := sessionCollabIdleTurnEnabled()
	if enabled && collabBackgroundDelivery() {
		// drain_inbox 模式（task 224）：pump 不投递、降级不产生，桥不叠加。
		enabled = false
	}
	var targets []idleTurnTargetView
	for _, t := range p.app.sessionCollabLiveTargets(nil) {
		view := idleTurnTargetView{contactID: t.contactID, activeTab: t.activeTab}
		if t.ctrl == nil {
			continue
		}
		view.running = t.ctrl.RuntimeStatus().Running
		if enabled {
			snap := t.ctrl.InboxSnapshot()
			view.paused = snap.Paused
			for _, item := range snap.Items {
				if item.State == sessioninbox.StateQueued {
					view.queuedID = item.ID
					break
				}
			}
			view.run = t.ctrl.RunInboxTurn
		}
		targets = append(targets, view)
	}
	p.idleTurns.sweepTargets(targets, now, enabled)
}
