package main

import (
	"log/slog"
	"strings"
	"time"
)

// tl1281 切换驱逐：非当前 tab 的解码态释放。
//
// 现状（tl1279 实测）：built tab 的控制器持有 executor 全量会话（LoadSession
// 解码后的全部消息），4 个大会话 tab 常驻即 literalStore 313.86MB 的稳态来源。
// 启动侧任务 619② 已保证只有原活跃 tab 构建；本件补齐稳态侧——切走的 tab 在
// 空闲且已落盘时释放控制器与租约，回到 619② 骨架态（冷读可读、点击重建），
// 稳态常驻收敛到「当前 tab 一份」。
//
// 只驱逐 oversize（≥32MiB，与冷读门同一判据）会话的 tab：小会话解码态几十
// KB 级，不值得付重建成本；大会话一份就是几十~两百 MB。
//
// 守卫（任一不满足即放弃本轮，下轮切换重试）：
//   - 仅本地可写会话 tab（ReadOnly / autopilot / detached / 远程不碰）；
//   - 无在跑轮、无挂起提示、无后台作业（hasActiveRuntimeWork）；
//   - 无未保存变更（SessionHasUnsavedChanges；切换路径本身已有 snapshot）；
//   - 非构建中（buildCancel）；无 StartupErr（保留显式重试面）；
//   - 防抖窗内用户切回（activeTabID 回到该 tab）即弃。
//
// 释放序列镜像 closeTabRuntimeAdmissionHeld（SetSessionPath("")→等存落→
// Cancel→Close→SharedHost→sink.clearContext→租约→运行时注销），并持
// lockRuntimeMutation 屏障防「守卫刚过、轮刚起」的竞窗；区别只在 tab 不移除、
// SessionPath 保留，懒构建（lazyTabNeedsBuildLocked）在下次点击自然接管。

// historyTabEvictionDebounce 是切走后的等待窗：覆盖切走快照落盘、也给「误切
// 再切回」留悔手期，避免构建抖动。
const historyTabEvictionDebounce = 10 * time.Second

// kickTabEviction schedules an async eviction check for the tab that just lost
// the foreground. Safe to call for tabs that cannot be evicted — the gate
// re-verifies everything after the debounce. Armed only on the Wails runtime
// (a.ctx non-nil), the same convention as startHistoryIdlePrefetch /
// startHistoryIndexMigration: a test-constructed App never spawns background
// goroutines that would reach back into a stale App after the test returns.
func (a *App) kickTabEviction(tabID string) {
	if tabID == "" || a.ctx == nil {
		return
	}
	a.mu.RLock()
	_, live := a.tabs[tabID]
	a.mu.RUnlock()
	if !live {
		return
	}
	a.goSafe("tabEviction", func() {
		time.Sleep(historyTabEvictionDebounce)
		a.evictIdleTabRuntime(tabID)
	})
}

// evictIdleTabRuntime releases one backgrounded tab's session runtime when
// every guard holds. One-shot: a failed gate just drops this round.
func (a *App) evictIdleTabRuntime(tabID string) {
	a.evictIdleTabRuntimeUnderBarrier(tabID)
	// 竞态收口：用户在驱逐执行窗内点回该 tab 时，Ctrl 仍非 nil——SetActiveTab
	// 的 kickLazyBuild 判 false，不会起构建；驱逐落地后它就是个没人构建的
	// 骨架。屏障已释放，此处按最新状态补探测重建（ lazyTabNeedsBuildLocked
	// 复查幂等：已被别的路径构建过即不重复）。
	a.mu.RLock()
	tab := a.tabs[tabID]
	needsBuild := tab != nil && a.activeTabID == tabID && lazyTabNeedsBuildLocked(tab)
	a.mu.RUnlock()
	if needsBuild {
		slog.Info("desktop: rebuilding tab evicted mid-switch", "tab", tabID)
		a.startTabControllerBuild(tab)
	}
}

func (a *App) evictIdleTabRuntimeUnderBarrier(tabID string) {
	defer a.lockRuntimeMutation("evict-tab")()
	a.sessionRemovalMu.Lock()
	defer a.sessionRemovalMu.Unlock()

	a.mu.RLock()
	tab := a.tabs[tabID]
	if tab == nil || tab.removed || tab.closing || tab.autopilot || tab.ReadOnly {
		a.mu.RUnlock()
		return
	}
	if a.activeTabID == tabID {
		// 用户在防抖窗内切回来了：不驱逐。
		a.mu.RUnlock()
		return
	}
	ctrl := tab.Ctrl
	if ctrl == nil || tab.buildCancel != nil || strings.TrimSpace(tab.StartupErr) != "" {
		a.mu.RUnlock()
		return
	}
	if tab.hasActiveRuntimeWork() {
		a.mu.RUnlock()
		return
	}
	sessionPath := tab.currentSessionPath()
	if strings.TrimSpace(sessionPath) == "" {
		a.mu.RUnlock()
		return
	}
	if !historyColdDecodeOversize(sessionPath) {
		// 小会话不值得驱逐（解码态本身可忽略）。
		a.mu.RUnlock()
		return
	}
	busy := false
	for _, detached := range a.detachedSessions {
		if detached == tab {
			busy = true
		}
	}
	if busy {
		a.mu.RUnlock()
		return
	}
	a.mu.RUnlock()

	// 屏障已挡住新轮/新构建。此刻再核活路径状态：轮可能刚结束、自动保存可能
	// 未落——有未保存变更就放弃（下轮重试），已落盘才继续。
	if ctrl.SessionHasUnsavedChanges() {
		slog.Debug("desktop: idle tab eviction skipped (unsaved changes)", "tab", tabID)
		return
	}
	if status := ctrl.RuntimeStatus(); status.Running || status.PendingPrompt || status.BackgroundJobs > 0 {
		slog.Debug("desktop: idle tab eviction skipped (runtime work)", "tab", tabID)
		return
	}

	a.mu.RLock()
	// 驱逐执行期间 tab 可能被关掉/换绑：指针与绑定以锁内最新为准。
	tab = a.tabs[tabID]
	if tab == nil || tab.removed || tab.Ctrl != ctrl {
		a.mu.RUnlock()
		return
	}
	closeSink := tab.sink
	a.mu.RUnlock()

	slog.Info("desktop: evicting idle background tab runtime (tl1281)",
		"tab", tabID, "path", sessionPath)
	ctrl.SetSessionPath("") // future snapshots become no-ops
	if !a.awaitIdleTabAutosave(tab) {
		// 快照迟迟不落：放弃本轮，别让 Close 跟一次在飞的保存竞写。
		slog.Debug("desktop: idle tab eviction skipped (snapshot still saving)", "tab", tabID)
		return
	}
	ctrl.Cancel()
	ctrl.Close()
	a.releaseTabSharedHost(tab)
	if closeSink != nil {
		closeSink.clearContext()
	}
	tab.releaseSessionLease()
	a.mu.Lock()
	if cur := a.tabs[tabID]; cur == tab {
		// 回到任务 619② 骨架态：无控制器、冷读可读、点击懒构建。
		tab.Ctrl = nil
		tab.Ready = false
	}
	a.releaseSessionRuntimeLocked(tab)
	a.mu.Unlock()
}

// awaitIdleTabAutosave waits for an in-flight tab snapshot to finish WITHOUT
// marking the tab closing — eviction keeps the tab alive, and closing is a
// one-way flag (nothing resets it), so setting it here would silently disable
// every future snapshot of the rebuilt session. Bounded poll instead of a
// cond wait: false = still saving past the deadline, and the caller aborts
// this round (conservative; the next switch-away retries).
func (a *App) awaitIdleTabAutosave(tab *WorkspaceTab) bool {
	if tab == nil {
		return true
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		tab.saveMu.Lock()
		saving := tab.saving
		tab.saveMu.Unlock()
		if !saving {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}
