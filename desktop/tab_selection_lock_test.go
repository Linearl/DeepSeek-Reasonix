package main

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/control"
)

// P14 (SetActiveTab 移出锁等待) 的 desktop 层验收：
// ① save-path 锁被占时 SetActiveTab 短时限内返回且切换成功；
// ② 降级路径把落盘交给 tabSnapshotLoop，锁释放后数据最终落盘；
// ③ 锁空闲时内联快照、不产生后台重复刷新；
// ④ 无 try-lock 能力的 controller 保持原有同步快照行为。

// switchSavePathCtrl wraps a real controller: the try-lock form reports
// contention per flag, while the plain Snapshot blocks until released so any
// synchronous lock wait would hang the test instead of passing it.
type switchSavePathCtrl struct {
	control.SessionAPI

	busy          atomic.Bool
	tryLockCalls  atomic.Int32
	snapshotCalls atomic.Int32

	snapshotOnce  sync.Once
	snapshotBegan chan struct{}
	release       chan struct{}
}

func (c *switchSavePathCtrl) SnapshotIfSavePathFree() (bool, error) {
	c.tryLockCalls.Add(1)
	if c.busy.Load() {
		return false, nil
	}
	return true, nil
}

func (c *switchSavePathCtrl) Snapshot() error {
	c.snapshotCalls.Add(1)
	c.snapshotOnce.Do(func() { close(c.snapshotBegan) })
	<-c.release
	return c.SessionAPI.Snapshot()
}

func newSwitchSavePathFixture(t *testing.T) (*App, *WorkspaceTab, *switchSavePathCtrl, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "p14-switch.jsonl")
	a, tab := appWithTab(t, path)
	a.tabs["target_tab"] = &WorkspaceTab{
		ID:          "target_tab",
		Scope:       "global",
		Ready:       true,
		disabledMCP: map[string]ServerView{},
	}
	a.tabOrder = []string{"test_tab", "target_tab"}
	fake := &switchSavePathCtrl{
		SessionAPI:    tab.Ctrl,
		snapshotBegan: make(chan struct{}),
		release:       make(chan struct{}),
	}
	tab.Ctrl = fake
	return a, tab, fake, path
}

func TestSetActiveTabReturnsWhileSavePathBusy(t *testing.T) {
	a, tab, fake, path := newSwitchSavePathFixture(t)
	fake.busy.Store(true)

	switched := make(chan error, 1)
	go func() { switched <- a.SetActiveTab("target_tab") }()
	select {
	case err := <-switched:
		if err != nil {
			t.Fatalf("SetActiveTab on busy save path: %v", err)
		}
		if a.activeTabID != "target_tab" {
			t.Fatalf("active tab = %q, want target_tab", a.activeTabID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SetActiveTab blocked while the save path was busy; want an immediate switch")
	}
	if got := fake.tryLockCalls.Load(); got != 1 {
		t.Fatalf("SnapshotIfSavePathFree calls = %d, want 1", got)
	}

	// 降级路径：后台刷新被排上，且其 Snapshot 会阻塞到我们放行——证明刷新
	// 确实在后台进行而不是在切换调用里。
	select {
	case <-fake.snapshotBegan:
	case <-time.After(5 * time.Second):
		t.Fatal("degraded flush was not scheduled on the busy save path")
	}
	close(fake.release)
	waitForAutosaveIdleWithin(t, tab, autosaveTestTimeout)
	// 数据最终刷新：放行后真实 controller 的落盘写出了会话文件。
	waitForFile(t, path, "remember this turn")
}

func TestSetActiveTabSnapshotsInlineWhenPathFree(t *testing.T) {
	a, _, fake, _ := newSwitchSavePathFixture(t)

	if err := a.SetActiveTab("target_tab"); err != nil {
		t.Fatalf("SetActiveTab on free save path: %v", err)
	}
	if a.activeTabID != "target_tab" {
		t.Fatalf("active tab = %q, want target_tab", a.activeTabID)
	}
	if got := fake.tryLockCalls.Load(); got != 1 {
		t.Fatalf("SnapshotIfSavePathFree calls = %d, want 1", got)
	}
	// 空闲路径内联完成、无后台刷新：留给后台循环一个观察窗，Snapshot 必须
	// 保持零调用。
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := fake.snapshotCalls.Load(); got != 0 {
			t.Fatalf("free-path switch scheduled a background snapshot (%d calls), want none", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// legacySnapshotCtrl has no SnapshotIfSavePathFree capability (a bare override
// wrapper), so snapshotTabForSwitch must keep the previous blocking form.
type legacySnapshotCtrl struct {
	control.SessionAPI

	snapshotCalls atomic.Int32
}

func (c *legacySnapshotCtrl) Snapshot() error {
	c.snapshotCalls.Add(1)
	return c.SessionAPI.Snapshot()
}

func TestSetActiveTabLegacyControllerStillSnapshotsInline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p14-legacy.jsonl")
	a, tab := appWithTab(t, path)
	a.tabs["target_tab"] = &WorkspaceTab{
		ID:          "target_tab",
		Scope:       "global",
		Ready:       true,
		disabledMCP: map[string]ServerView{},
	}
	a.tabOrder = []string{"test_tab", "target_tab"}
	fake := &legacySnapshotCtrl{SessionAPI: tab.Ctrl}
	tab.Ctrl = fake

	if err := a.SetActiveTab("target_tab"); err != nil {
		t.Fatalf("SetActiveTab with legacy controller: %v", err)
	}
	if a.activeTabID != "target_tab" {
		t.Fatalf("active tab = %q, want target_tab", a.activeTabID)
	}
	if got := fake.snapshotCalls.Load(); got != 1 {
		t.Fatalf("inline Snapshot calls = %d, want 1", got)
	}
}
