package main

// 任务 922（886 机制②桥盲区）：sessionCollabTargets 构造可见 tab target 不带
// ctrl 字段，而 sweepIdleInboxTurns 对 t.ctrl == nil 静默 continue——空闲桥对
// 全部可见 tab 全盲（2026-10-14 新6 现场：超限长会话 reattach 后停在可见 tab，
// queued_followup 4 次 drain 空转，桥全程看不见；佐证：全量生产日志
// decision=skip:active_tab 0 条——该分支当时不可达）。本文件钉死修复：
//   - ctrl==nil 的可见 tab target 经 tabByID.Ctrl 兜底被桥纳入并真开轮；
//   - active tab 仍带 activeTab 标记（边界 2 在 sweepTargets 内拦截，不变）；
//   - runtime 解析不到的 target 丢弃但不 panic（快照与 sweep 之间的关闭竞态）。

import (
	"context"
	"sync"
	"testing"
	"time"

	"reasonix/internal/control"
	"reasonix/internal/sessioninbox"
)

// bridgeSweepController 只覆写 sweep 视图会摸到的 SessionAPI 面；其余调用走
// stubSessionAPI 的 panic 默认（生产读者直呼端口，不吞错误）。
type bridgeSweepController struct {
	stubSessionAPI
	mu   sync.Mutex
	path string
	runs []string
}

func (c *bridgeSweepController) RuntimeStatus() control.RuntimeStatus { return control.RuntimeStatus{} }
func (c *bridgeSweepController) SessionPath() string                  { return c.path }
func (c *bridgeSweepController) InboxSnapshot() sessioninbox.InboxSnapshot {
	return sessioninbox.InboxSnapshot{Items: []sessioninbox.InboxItemMeta{
		{ID: "item-queued-1", State: sessioninbox.StateQueued, Source: "collab:contact-peer"},
	}}
}
func (c *bridgeSweepController) ResumeInboxAutoPause() (bool, error) { return true, nil }
func (c *bridgeSweepController) RunInboxTurn(_ context.Context, id string) error {
	c.mu.Lock()
	c.runs = append(c.runs, id)
	c.mu.Unlock()
	return nil
}
func (c *bridgeSweepController) runCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.runs)
}

// TestIdleTurnSweepResolvesVisibleTabCtrlByID 是 922 验收②的正面用例：可见
// tab 的 target 快照 ctrl==nil（盲区原形状），修复后经 tabByID.Ctrl 兜底进入
// 桥视图，并在空闲窗口到期后真的开出一轮消费队首——「被桥纳入拉起」的完整链。
func TestIdleTurnSweepResolvesVisibleTabCtrlByID(t *testing.T) {
	dir := t.TempDir()
	path := sessionFileWithContact(t, dir, "visible", "sc_bridge")
	ctrl := &bridgeSweepController{path: path}
	tab := &WorkspaceTab{ID: "tab-bridge", TopicTitle: "可见页", Ctrl: ctrl}
	app := &App{
		tabs:     map[string]*WorkspaceTab{"tab-bridge": tab},
		tabOrder: []string{"tab-bridge"},
	}
	p := newSessionCollabPump(app)

	live := app.sessionCollabLiveTargets(nil)
	if len(live) != 1 || live[0].tabID != "tab-bridge" || live[0].contactID != "sc_bridge" {
		t.Fatalf("live targets = %+v, want the visible tab bound to sc_bridge", live)
	}
	if live[0].ctrl != nil {
		t.Fatal("precondition: sessionCollabTargets must not carry ctrl (the 886 blind-spot shape)")
	}

	covered := map[string]bool{}
	now := time.Now()
	views := p.idleTurnTargetViews(live, covered, now)
	if len(views) != 1 {
		t.Fatalf("bridge views = %d, want 1 (visible tab must be flattened, not skipped)", len(views))
	}
	view := views[0]
	if view.contactID != "sc_bridge" || view.tabID != "tab-bridge" {
		t.Fatalf("view identity = %s/%s, want sc_bridge/tab-bridge", view.contactID, view.tabID)
	}
	if view.activeTab {
		t.Fatal("a background tab must not carry the activeTab marker")
	}
	if view.queuedID != "item-queued-1" {
		t.Fatalf("queued head = %q, want item-queued-1", view.queuedID)
	}
	if view.run == nil {
		t.Fatal("visible-tab view lost the RunInboxTurn seam — the bridge could never open a turn")
	}
	if !covered["sc_bridge"] {
		t.Fatal("covered must mark the visible tab's contact so the 887 dormant lookup never stands it up again")
	}

	// 完整开轮链：首拍起空闲钟，N 秒后的第二拍真调 RunInboxTurn 消费队首。
	b := newIdleTurnBridge()
	b.sweepTargets(views, now)
	fire := now.Add(sessionCollabIdleTurnDelay + time.Second)
	b.sweepTargets(p.idleTurnTargetViews(live, covered, fire), fire)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && ctrl.runCount() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := ctrl.runCount(); got != 1 {
		t.Fatalf("bridge opened %d turns for the visible tab, want exactly 1 (item-queued-1 consumed)", got)
	}
}

// TestIdleTurnSweepKeepsActiveTabMarker 是反向用例：兜底解析不得吞掉 activeTab
// 标记——active tab 是用户面前的会话，边界 2 仍由 sweepTargets 拦截（既有
// TestIdleTurnBridgeSkipsActiveTab 覆盖拦截本身，这里钉标记在新解析路径不丢）。
func TestIdleTurnSweepKeepsActiveTabMarker(t *testing.T) {
	dir := t.TempDir()
	path := sessionFileWithContact(t, dir, "active", "sc_active")
	ctrl := &bridgeSweepController{path: path}
	tab := &WorkspaceTab{ID: "tab-active", Ctrl: ctrl}
	app := &App{
		tabs:        map[string]*WorkspaceTab{"tab-active": tab},
		tabOrder:    []string{"tab-active"},
		activeTabID: "tab-active",
	}
	p := newSessionCollabPump(app)

	views := p.idleTurnTargetViews(app.sessionCollabLiveTargets(nil), map[string]bool{}, time.Now())
	if len(views) != 1 || !views[0].activeTab {
		t.Fatalf("active visible tab must resolve WITH activeTab=true, got %+v", views)
	}
}

// TestIdleTurnSweepDropsUnresolvableCtrlTarget 是反向用例：target 快照与 sweep
// 之间 runtime 消失（关 tab 竞态）时丢弃该 target——不 panic、不开轮；其余
// target 不受影响。
func TestIdleTurnSweepDropsUnresolvableCtrlTarget(t *testing.T) {
	app := &App{tabs: map[string]*WorkspaceTab{}}
	p := newSessionCollabPump(app)

	live := []sessionCollabTarget{
		{tabID: "tab-gone", contactID: "sc_gone"},
		{tabID: "", contactID: "sc_runtime_only"}, // 无 tab 的 runtime-only target：ctrl nil 同样丢弃
	}
	views := p.idleTurnTargetViews(live, map[string]bool{}, time.Now())
	if len(views) != 0 {
		t.Fatalf("unresolvable targets produced %d views, want 0", len(views))
	}
}
