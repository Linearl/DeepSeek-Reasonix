package main

// tl1500 刀①（装机全局挂死修复件）——活动绑定快照，把 UI 读者面与 a.mu 解耦。
//
// 诊断报告（worktrees/tl1493/docs/reports-diag/装机挂死诊断-20261108.md §2.1）
// 定案的主链：设置/用量/指标/切 tab 的 UI 绑定都要在解析 config 之前先拿
// a.mu.RLock（loadDesktopUserConfigForView → activeWorkspaceRoot → activeTab /
// reconcileTabWithPinnedSessionMetaObserved；右栏指标 ContextUsageForTab、
// Settings 后段的 activeCtrl 同一把锁）。Go RWMutex 写者优先：保存家族长时
// 持锁/排队时（settings_app.go:1958 注释自证的场景），所有新读者排队，前端
// 400/1200ms 重试放弃后全局挂死。tl1307 的实测前提「锁窗口单段 ≤336ms」
// 已被突破（锁窗口分钟级甚至无界）。
//
// 机制：读者链需要的状态（活动 tab 指针、它的控制器、工作区根）镜像为
// atomic.Pointer 快照。锁空闲时 TryRLock 成功 → 在锁内刷新快照再应答
// （与旧 RLock 读逐字节同语义，同时把快照追平到当前状态）；写锁被持有或
// 有写者排队时 TryRLock 失败 → 立即以最近一次快照应答（旧值 + stale 打标
// 落日志），不再排队。快照只由读路径刷新，写者零改动：写锁一释放，第一
// 次无竞争读即追平，因此旧值窗口以写者持锁时长为上界，不存在无界陈旧
// （tl1307 预案前置③「staleness 设 TTL 上限」在此结构下天然满足）。
//
// tl1307 预案前置对齐（settings_app.go:1899 注释）：①失效面从「40 个 Set*
// 的配置值缓存」收窄为「活动绑定三元组」，无 Set* 失效审计问题；②stale
// 应答逐次落 slog（可观测），不吞任何既有打点；③见上。
//
// 并发合同：快照内字段都是发布时在 a.mu 保护下拷出的不可变值（tab 指针、
// ctrl 接口值、root 字符串）；stale 应答只读这些拷贝，不触碰活 tab 的可变
// 字段——活字段仍然只准在 a.mu 下读写（tab.SessionPath/WorkspaceRoot 的锁外
// 读沿用 settings_app.go activeWorkspaceRoot 既有模式，本文件不新增锁外读）。

import (
	"log/slog"
	"strings"
	"time"

	"reasonix/internal/control"
)

// appBindingSnapshot is the lock-free mirror of the active binding state the
// UI reader chain needs. Immutable once published.
type appBindingSnapshot struct {
	at          time.Time
	activeTabID string
	tab         *WorkspaceTab
	ctrl        control.SessionAPI
	root        string // trimmed WorkspaceRoot of the active tab ("" → caller falls back to ".")
	present     bool   // an active tab existed at publish time
}

// publishBindingSnapshotLocked copies the current binding into a fresh
// snapshot and publishes it. Caller must hold a.mu (read or write).
func (a *App) publishBindingSnapshotLocked() *appBindingSnapshot {
	snap := &appBindingSnapshot{at: time.Now()}
	if tab := a.activeTabLocked(); tab != nil {
		snap.activeTabID = tab.ID
		snap.tab = tab
		snap.ctrl = tab.Ctrl
		snap.root = strings.TrimSpace(tab.WorkspaceRoot)
		snap.present = true
	}
	a.bindingSnap.Store(snap)
	return snap
}

// bindingSnapshotNow returns the newest binding state without waiting on a.mu.
// fresh=true: the TryRLock succeeded and the snapshot was refreshed from live
// state — byte-identical to the old RLock read. fresh=false: a writer holds or
// is queued on a.mu (Go RWMutex writer priority); the last published snapshot
// answers instead and every stale answer is logged (tl1307 precondition ②).
func (a *App) bindingSnapshotNow() (*appBindingSnapshot, bool) {
	if a.mu.TryRLock() {
		snap := a.publishBindingSnapshotLocked()
		a.mu.RUnlock()
		return snap, true
	}
	snap := a.bindingSnap.Load()
	if snap == nil {
		// Bootstrap: a writer holds the lock and no reader ever published.
		// Publish the empty binding (no active tab) so callers get a stable
		// stale answer; the next uncontended read refreshes it.
		snap = &appBindingSnapshot{at: time.Now()}
		a.bindingSnap.CompareAndSwap(nil, snap)
	}
	slog.Warn("desktop: binding snapshot stale fallback (writer holds a.mu; tl1500 刀①)",
		"age_ms", time.Since(snap.at).Milliseconds(),
		"active_tab", snap.activeTabID,
		"has_tab", snap.present)
	return snap, false
}
