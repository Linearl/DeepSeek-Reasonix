package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/control"
)

// tl1500 刀①（装机全局挂死修复件）验收钉：写锁被 holder 挂住时，读者链必须
// 返回而不是排队。
//
// 诊断报告（worktrees/tl1493/docs/reports-diag/装机挂死诊断-20261108.md §2.1）
// 定案的主链：设置/用量/指标/切 tab 的 UI 绑定都要在解析 config 之前先拿
// a.mu.RLock（loadDesktopUserConfigForView → activeWorkspaceRoot → activeTab /
// reconcileTabWithPinnedSessionMetaObserved；右栏指标 ContextUsageForTab 同一把
// 锁；Settings 后段还有 activeCtrl）。Go RWMutex 写者优先：保存家族长时持锁
// 时所有新读者排队，前端 400/1200ms 重试放弃后全局挂死。
//
// 钉的构造（与任务书一致）：测试内直接持住 a.mu 写锁（不真挂 30s——构造出
// 「写锁被占住」这一状态即可），断言设置快照/用量链/指标/切 tab 绑定各读者
// 在 100ms 内返回。修前红：activeTab/reconcile 入口在写锁下排队，读者永不到
// 达完成（1.5s 兜底判红）；修后绿：<100ms 返回（哪怕数据是快照旧值）。
func TestBindingReadersReturnWhileWriteLockHeld(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	app.runtimeEvents.emit = func(context.Context, string, ...any) {}

	root := filepath.Join("C:\\", "ws", "held-root")
	ctrl := control.New(control.Options{Label: "held"})
	defer ctrl.Close()
	tab := &WorkspaceTab{
		ID:            "held_tab",
		Scope:         "project",
		WorkspaceRoot: root,
		SessionPath:   filepath.Join("C:\\", "ws", "held.jsonl"),
		Ready:         true,
		Ctrl:          ctrl,
	}
	app.mu.Lock()
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	app.mu.Unlock()

	// 热身：锁空闲期先各读一遍（修后这一步同时发布快照），并预热 Settings 链上
	// 的进程内缓存（sandbox shell 扫描等），让持锁段的耗时只反映锁语义而不是
	// 冷缓存磁盘工作。
	if got := app.activeWorkspaceRoot(); got != root {
		t.Fatalf("activeWorkspaceRoot warmup = %q, want %q", got, root)
	}
	if app.activeTab() != tab {
		t.Fatal("activeTab warmup must return the active tab")
	}
	if app.activeCtrl() == nil {
		t.Fatal("activeCtrl warmup must return the tab controller")
	}
	// 合成 tab 的 session 文件不存在，reconcile 解析不到绑定属正常——热身只调
	// 用不判结果；持锁段的钉同样只断言「返回够快」，不断言绑定内容。
	_, _ = app.reconcileTabWithPinnedSessionMeta(tab)
	_ = app.ContextUsageForTab("")
	_ = app.openCodeGoUsageKey()
	_ = app.Settings()

	// 写锁 holder（测试内构造）：整个断言窗口内 a.mu 写锁被本测试持有。
	app.mu.Lock()
	defer app.mu.Unlock()

	assertFast := func(name string, fn func()) {
		t.Helper()
		done := make(chan struct{})
		started := time.Now()
		go func() {
			defer close(done)
			fn()
		}()
		select {
		case <-done:
			if ms := time.Since(started); ms > 100*time.Millisecond {
				t.Fatalf("%s took %v (>100ms) while the write lock is held — reader did not serve from the snapshot", name, ms)
			}
		case <-time.After(1500 * time.Millisecond):
			t.Fatalf("%s still blocked after 1.5s while the write lock is held — a.mu reader convoy (tl1500 刀① red nail)", name)
		}
	}

	assertFast("activeWorkspaceRoot(设置快照的根解析)", func() { _ = app.activeWorkspaceRoot() })
	assertFast("activeTab(切 tab 绑定读者)", func() { _ = app.activeTab() })
	assertFast("activeCtrl(设置后段)", func() { _ = app.activeCtrl() })
	assertFast("reconcileTabWithPinnedSessionMeta(切 tab 绑定)", func() {
		_, _ = app.reconcileTabWithPinnedSessionMeta(tab)
	})
	assertFast("loadDesktopUserConfigForView(config 解析前的根链)", func() {
		_, _, _ = app.loadDesktopUserConfigForView()
	})
	assertFast("openCodeGoUsageKey(用量轮询链)", func() { _ = app.openCodeGoUsageKey() })
	assertFast("ContextUsageForTab(右栏指标)", func() { _ = app.ContextUsageForTab("") })
	assertFast("ContextUsageForTab(显式活动 tabID)", func() { _ = app.ContextUsageForTab(tab.ID) })
	assertFast("Settings(设置快照全链)", func() { _ = app.Settings() })
}
