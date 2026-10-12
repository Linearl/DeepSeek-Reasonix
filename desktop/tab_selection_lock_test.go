package main

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/control"
)

// P14 (SetActiveTab 移出锁等待) + 任务 831① (切出快照完全异步化) + 任务 837
// (切换路径同步快照残留封死) 的 desktop 层验收：
// ① save-path 锁被占时 SetActiveTab 短时限内返回且切换成功（P14 原验收）；
// ② 降级路径把落盘交给 tabSnapshotLoop，锁释放后数据最终落盘（P14 原验收）；
// ③ 831① 语义变更：save-path 空闲时也不再内联快照——切换立即返回，快照由
//    后台单飞补做（728MB 会话的 snapshot_ms=1000 不再落在切换路径上）；
// ④ 831① 语义变更：无 try-lock 能力的 legacy controller 同样转后台，
//    切换路径上不存在任何同步快照回落；
// ⑤ 837 语义变更：local→remote 切换前的本地快照（snapshotActiveLocalBefore
//    Remote）与 local↔local 同屏障——同样移交后台单飞，不再走 durable 内联。

// switchSavePathCtrl wraps a real controller: Snapshot blocks until released
// so any synchronous lock wait would hang the test instead of passing it.
// Since 831① the switch path never calls SnapshotIfSavePathFree; the fake
// keeps the method to prove it stays uncalled from SetActiveTab.
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
		// 测试环境 a.ctx==nil 时 lazy controller build 是同步的（整段 boot
		// 3-4s），会把 SetActiveTab 拖过断言窗——本组测试只关心切换与快照
		// 的时序，不关心 build，占位一个 buildCancel 把 lazy build 关掉。
		buildCancel: func() {},
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
	// 831①: the try-lock is gone from the switch path entirely — busy or not,
	// the flush is handed to the background loop.
	if got := fake.tryLockCalls.Load(); got != 0 {
		t.Fatalf("SnapshotIfSavePathFree calls = %d, want 0 (switch must not snapshot inline)", got)
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

// TestSetActiveTabHandsSnapshotToBackgroundLoop is the 831① form of the old
// inline-on-free-path assertion: a free save path must behave exactly like a
// busy one — the switch returns while the loop's (blocked) Snapshot proves the
// flush runs on the background goroutine, not inside the click.
func TestSetActiveTabHandsSnapshotToBackgroundLoop(t *testing.T) {
	a, tab, fake, _ := newSwitchSavePathFixture(t)

	switched := make(chan error, 1)
	go func() { switched <- a.SetActiveTab("target_tab") }()
	select {
	case err := <-switched:
		if err != nil {
			t.Fatalf("SetActiveTab with a pending background snapshot: %v", err)
		}
		if a.activeTabID != "target_tab" {
			t.Fatalf("active tab = %q, want target_tab", a.activeTabID)
		}
	case <-time.After(3 * time.Second):
		// The loop's Snapshot is held on fake.release, so an inline snapshot
		// would hang exactly here.
		t.Fatal("SetActiveTab waited for the snapshot; want an immediate switch with the flush in the background")
	}
	if got := fake.tryLockCalls.Load(); got != 0 {
		t.Fatalf("SnapshotIfSavePathFree calls = %d, want 0 (no try-lock on the switch path)", got)
	}
	select {
	case <-fake.snapshotBegan:
	case <-time.After(5 * time.Second):
		t.Fatal("background snapshot loop did not pick up the switch flush")
	}
	close(fake.release)
	waitForAutosaveIdleWithin(t, tab, autosaveTestTimeout)
}

// TestSnapshotActiveLocalBeforeRemoteDefersToBackground is the 837 form of
// snapshotActiveLocalBeforeRemote: switching from a local tab to a remote one
// used to run snapshotTabForAction's durable inline snapshot on the selection
// path. Since the local-to-local switch hands its flush to the background
// loop, the local-to-remote boundary is the same kind of event (nothing
// mutates the session) and must behave identically — the blocked fake proves
// no inline materialization happens and the flush lands on the loop.
func TestSnapshotActiveLocalBeforeRemoteDefersToBackground(t *testing.T) {
	a, tab, fake, path := newSwitchSavePathFixture(t)

	done := make(chan error, 1)
	go func() { done <- a.snapshotActiveLocalBeforeRemote() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("snapshotActiveLocalBeforeRemote: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("local-to-remote barrier snapshotted inline; want a background hand-off")
	}
	select {
	case <-fake.snapshotBegan:
	case <-time.After(5 * time.Second):
		t.Fatal("local-to-remote barrier flush was not scheduled in the background")
	}
	close(fake.release)
	waitForAutosaveIdleWithin(t, tab, autosaveTestTimeout)
	// 最终落盘不变：后台单飞写出的仍是完整会话内容。
	waitForFile(t, path, "remember this turn")
}

// legacySnapshotCtrl has no SnapshotIfSavePathFree capability (a bare override
// wrapper). Since 831① the switch path has no blocking fallback: legacy
// controllers degrade to the same background loop.
type legacySnapshotCtrl struct {
	control.SessionAPI

	snapshotCalls atomic.Int32
	snapshotOnce  sync.Once
	snapshotBegan chan struct{}
	release       chan struct{}
}

func (c *legacySnapshotCtrl) Snapshot() error {
	c.snapshotCalls.Add(1)
	c.snapshotOnce.Do(func() { close(c.snapshotBegan) })
	<-c.release
	return c.SessionAPI.Snapshot()
}

func TestSetActiveTabLegacyControllerAlsoDefersToBackground(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p14-legacy.jsonl")
	a, tab := appWithTab(t, path)
	a.tabs["target_tab"] = &WorkspaceTab{
		ID:          "target_tab",
		Scope:       "global",
		Ready:       true,
		disabledMCP: map[string]ServerView{},
		buildCancel: func() {}, // 见 newSwitchSavePathFixture：免同步 lazy build
	}
	a.tabOrder = []string{"test_tab", "target_tab"}
	fake := &legacySnapshotCtrl{
		SessionAPI:    tab.Ctrl,
		snapshotBegan: make(chan struct{}),
		release:       make(chan struct{}),
	}
	tab.Ctrl = fake

	switched := make(chan error, 1)
	go func() { switched <- a.SetActiveTab("target_tab") }()
	select {
	case err := <-switched:
		if err != nil {
			t.Fatalf("SetActiveTab with legacy controller: %v", err)
		}
		if a.activeTabID != "target_tab" {
			t.Fatalf("active tab = %q, want target_tab", a.activeTabID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SetActiveTab ran the legacy blocking snapshot inline; want a background hand-off")
	}
	select {
	case <-fake.snapshotBegan:
	case <-time.After(5 * time.Second):
		t.Fatal("legacy controller flush was not scheduled in the background")
	}
	close(fake.release)
	waitForAutosaveIdleWithin(t, tab, autosaveTestTimeout)
}
