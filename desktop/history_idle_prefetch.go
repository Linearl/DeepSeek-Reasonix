package main

import (
	"context"
	"strings"
	"time"
)

// 任务 451 方案 B：idle 预取循环。
//
// 恢复的冷 tab（任务 619 骨架：启动只构建原活跃 tab，其余 Ctrl==nil）与
// 后台打开的会话（协作泵 stand-up，471 报告）都是高频切换目标，切换时
// HistorySliceForTab 走冷路径，实测常态 0.8-2.2s。本循环在空闲时把它们
// 的最新页预先切好，存进 historyWarmPageCache（desktop/history_warm_page.go
// ，命中一次 stat 即回），用户点 tab 时首响从秒级降到 stat 级。
//
// 纪律（都是给「不添堵」上的约束）：
//   - 只在 Wails startup 钩子武装（a.ctx 非 nil），测试构造的 App 永不
//     后台起循环——与 startHistoryIndexMigration 同一约定；
//   - 事件驱动为主：tabsRestored（启动恢复完）/ SetActiveTab（切 tab）/
//     openTopicTabWithActivation（开话题）各 kick 一次，防抖后跑一轮；
//   - 慢速兜底：historyIdlePrefetchIdle 周期重扫一轮（捞协作泵后台新开
//     的冷会话、以及被保存动过 mtime 的失效页），无候选时一轮只有几次
//     stat，可忽略；
//   - 前台让路：任一 tab 的 runtime 在跑轮/有挂起提示/有后台作业
//     （hasActiveRuntimeWork，含运行中 boot 的让路面）就不跑、中途出现
//     立刻收手——预取永远不与前台轮和 boot 抢 IO/CPU；
//   - 单并发顺序预取，会话间让 50ms；每轮最多 historyIdlePrefetchMaxSessions
//     个会话；跳过构建中的 tab（boot 本身就是重 IO，别叠加）。

const (
	// historyIdlePrefetchDebounce 是 kick 后的防抖窗：等切换动作的连击
	// 停下来再跑，也把预取从「点击的那一刻」挪开。
	historyIdlePrefetchDebounce = 1500 * time.Millisecond
	// historyIdlePrefetchIdle 是无 kick 时的慢速兜底重扫周期。
	historyIdlePrefetchIdle = 60 * time.Second
	// historyIdlePrefetchYield 是一轮内会话之间的让步间隔。
	historyIdlePrefetchYield = 50 * time.Millisecond
	// historyIdlePrefetchMaxSessions 是单轮预取的会话数上限。
	historyIdlePrefetchMaxSessions = 4
	// historyIdlePrefetchTurns 必须与前端最新页的 HISTORY_PAGE_TURNS
	// （useController.ts / historyPaging.ts，均 60）一致：预取页只有与
	// 切换时请求的页同形才会被 historyWarmPageCache 命中。
	historyIdlePrefetchTurns = 60
)

// startHistoryIdlePrefetch arms the idle prefetch loop. Only called from the
// Wails startup hook, so test-constructed Apps never spawn the worker.
func (a *App) startHistoryIdlePrefetch() {
	if a.ctx == nil {
		return
	}
	a.historySliceMu.Lock()
	if a.historyIdlePrefetchKick != nil {
		a.historySliceMu.Unlock()
		return
	}
	a.historyIdlePrefetchKick = make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(a.ctx)
	a.historyIdlePrefetchCancel = cancel
	a.historySliceMu.Unlock()
	a.goSafe("historyIdlePrefetch", func() { a.historyIdlePrefetchLoop(ctx) })
}

// stopHistoryIdlePrefetch stops the idle prefetch loop; called from shutdown.
// The worker also stops with the Wails context.
func (a *App) stopHistoryIdlePrefetch() {
	a.historySliceMu.Lock()
	cancel := a.historyIdlePrefetchCancel
	a.historySliceMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// kickHistoryIdlePrefetch coalesces into a single pending kick. Safe (no-op)
// before the loop is armed.
func (a *App) kickHistoryIdlePrefetch() {
	a.historySliceMu.Lock()
	kick := a.historyIdlePrefetchKick
	a.historySliceMu.Unlock()
	if kick == nil {
		return
	}
	select {
	case kick <- struct{}{}:
	default:
	}
}

// historyIdlePrefetchLoop waits for tab restore, then alternates between the
// debounce/idle timer and kick signals. Rounds themselves re-check liveness.
func (a *App) historyIdlePrefetchLoop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-a.tabsRestoredSignal():
	}
	timer := time.NewTimer(historyIdlePrefetchDebounce)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			a.historyIdlePrefetchRound(ctx)
			timer.Reset(historyIdlePrefetchIdle)
		case <-a.historyIdlePrefetchKick:
			if !timer.Stop() {
				// 兼容旧 timer 语义排空；Go 1.23+ 为同步 timer，此分支为
				// 无操作。标准的 Stop/Reset 竞态处理形态。
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(historyIdlePrefetchDebounce)
		}
	}
}

// historyIdlePrefetchRound warms the newest page of cold, inactive tabs —
// the sessions a tab click would otherwise cold-read. Best-effort and
// interruptible: any foreground runtime work before or mid-round aborts it.
func (a *App) historyIdlePrefetchRound(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	type prefetchCandidate struct{ tabID, sessionDir, sessionPath string }
	a.mu.RLock()
	activeID := a.activeTabID
	candidates := make([]prefetchCandidate, 0, len(a.tabs))
	busy := false
	for _, tab := range a.tabs {
		if tab == nil || tab.removed || tab.closing {
			continue
		}
		if tab.Ctrl != nil {
			// 活 tab 走内存活路径，无需预取；但它在跑就是前台工作。
			busy = busy || tab.hasActiveRuntimeWork()
			continue
		}
		if tab.buildDone != nil {
			// 构建中：boot 是 5-15s 的重 IO 段（471 报告），预取不跑、
			// 也不把该 tab 当候选（boot 完成后它走活路径，无需预取）。
			busy = true
			continue
		}
		path := strings.TrimSpace(tab.SessionPath)
		if path == "" || tab.ID == activeID {
			continue
		}
		candidates = append(candidates, prefetchCandidate{tab.ID, tabSessionDir(tab), path})
	}
	// 471 报告实锤：detached 会话（协作泵 stand-up）在后台跑轮，同属让路面。
	for _, tab := range a.detachedSessions {
		if tab != nil && tab.Ctrl != nil && tab.hasActiveRuntimeWork() {
			busy = true
			break
		}
	}
	a.mu.RUnlock()
	if busy || len(candidates) == 0 || a.anyTabRuntimeWork() {
		return
	}
	seen := make(map[string]bool, len(candidates))
	deduped := candidates[:0]
	for _, c := range candidates {
		if seen[c.sessionPath] {
			continue
		}
		seen[c.sessionPath] = true
		deduped = append(deduped, c)
	}
	if len(deduped) > historyIdlePrefetchMaxSessions {
		deduped = deduped[:historyIdlePrefetchMaxSessions]
	}
	for _, c := range deduped {
		if ctx.Err() != nil {
			return
		}
		if a.anyTabRuntimeWork() {
			return
		}
		// 经公开入口走：tab 在两次快照之间可能已拿到控制器（转活路径）
		// 或被关闭（安全报错），入口内部都会按最新状态重新解析。
		// 请求形状见 historyIdlePrefetchTurns 的注释。
		_ = a.HistorySliceForTab(c.tabID, HistorySliceRequest{Turns: historyIdlePrefetchTurns})
		select {
		case <-ctx.Done():
			return
		case <-time.After(historyIdlePrefetchYield):
		}
	}
}

// anyTabRuntimeWork reports whether any tab's runtime (visible or detached —
// the collab pump's background stand-ups, 471 report) is mid-turn, has a
// pending prompt, or runs background jobs — the "foreground wins" gate.
func (a *App) anyTabRuntimeWork() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, tab := range a.tabs {
		if tab != nil && tab.Ctrl != nil && tab.hasActiveRuntimeWork() {
			return true
		}
	}
	for _, tab := range a.detachedSessions {
		if tab != nil && tab.Ctrl != nil && tab.hasActiveRuntimeWork() {
			return true
		}
	}
	return false
}
