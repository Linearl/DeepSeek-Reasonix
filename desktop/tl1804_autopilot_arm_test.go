package main

// tl1804 回归（1795 镜像的「建」向）：会话内新参与 autopilot——composer 切上
// autopilot 档——必须把 unattended 姿态立到活控制器上。1795 之前的真实起点
// 是「构建期带 unattended 姿态的控制器」（tl1795PostureTab）；本件的起点是它
// 的镜像：「构建期 attended 的控制器 + 用户切上 autopilot 档」——tab 裸旗
// 已立、活控制器仍是 attended，ask 链走有人值守等待直到下次 rebuild。
//  ① join 落活控制器：SetCollaborationModeForTab("autopilot") 后
//     AutopilotActive()==true（armAutopilotLivePosture 接线生效）；
//  ② 建→清端到端：join 后再切走（595 tier leave），1795 的 drop 在 armed
//     控制器上照常回落——清向语义零回退。

import (
	"context"
	"testing"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/control"
)

// tl1804AttendedTab mirrors the pre-join start state: a tab whose live
// controller was built attended (the mirror of tl1795PostureTab's built-
// unattended start), YOLO approval per the tl1741 new-tab default.
func tl1804AttendedTab(t *testing.T, app *App, id string) (*WorkspaceTab, *control.Controller) {
	t.Helper()
	ag := control.New(control.Options{
		Label: id,
	})
	tab := &WorkspaceTab{
		ID:                         id,
		Scope:                      "global",
		WorkspaceRoot:              globalTabWorkspaceRoot(),
		Ready:                      true,
		Ctrl:                       ag,
		mode:                       tabModeFromAxes(false, true),
		disabledMCP:                map[string]ServerView{},
		toolApprovalMode:           control.ToolApprovalYolo,
		autopilot:                  false,
		autopilotMaxRuntime:        0,
		autopilotAskTimeoutEnabled: false,
		autopilotAskWait:           0,
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app, ctx: context.Background()}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() { ag.Close() })
	return tab, ag
}

// ① 会话内新参与：切上 autopilot 档后活控制器姿态 unattended。
func TestTl1804TierJoinArmsLiveAutopilotPosture(t *testing.T) {
	isolateDesktopUserDirs(t)
	tl1795AutopilotConfig(t)
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	tab, ag := tl1804AttendedTab(t, app, "tl1804-join")
	defer tab.Ctrl.Close()

	// 前置：构建期 attended（真实复现起点，1795PostureTab 的镜像）。
	if ag.AutopilotActive() {
		t.Fatal("precondition failed: the built controller must start attended")
	}
	if tab.autopilot {
		t.Fatal("precondition failed: tab flag must start disengaged")
	}

	app.SetCollaborationModeForTab(tab.ID, "autopilot")

	if !tab.autopilot {
		t.Fatal("autopilot tier pick must engage tab.autopilot")
	}
	if tab.autopilotMaxRuntime != 8*time.Hour {
		t.Fatalf("tab.maxRuntime after join = %v, want the configured 8h bound", tab.autopilotMaxRuntime)
	}
	if !ag.AutopilotActive() {
		t.Fatal("autopilot tier join must arm the live controller posture; a stale false leaves the ask chain waiting attended in an unattended session (tl1804)")
	}
}

// ② 建→清端到端：join 后再切走，1795 drop 语义在 armed 控制器上零回退。
func TestTl1804JoinThenLeaveRoundTrip(t *testing.T) {
	isolateDesktopUserDirs(t)
	tl1795AutopilotConfig(t)
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	tab, ag := tl1804AttendedTab(t, app, "tl1804-roundtrip")
	defer tab.Ctrl.Close()

	app.SetCollaborationModeForTab(tab.ID, "autopilot")
	if !ag.AutopilotActive() {
		t.Fatal("precondition failed: the join must arm the live posture first")
	}

	app.SetApprovalTierForTab(tab.ID, control.ToolApprovalYolo)
	if tab.autopilot {
		t.Fatal("yolo tier pick must clear tab.autopilot (595)")
	}
	if ag.AutopilotActive() {
		t.Fatal("yolo tier pick must drop the armed live posture (tl1795 drop semantics intact)")
	}
}

// config pref 灰度：preference off 时切 autopilot 档不立姿态（join 向不得
// 绕过 tl1741 无界拒绝——缺 bound 降级为关）。
func TestTl1804TierJoinWithoutBoundStaysAttended(t *testing.T) {
	isolateDesktopUserDirs(t)
	cfg := config.Default()
	// 不设 AutopilotMaxRuntime：desktopAutopilotDefaults 降级为关。
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	tab, ag := tl1804AttendedTab(t, app, "tl1804-nobound")
	defer tab.Ctrl.Close()

	app.SetCollaborationModeForTab(tab.ID, "autopilot")
	if tab.autopilot {
		t.Fatal("no runtime bound must keep the tier off (unbounded unattended is never implicit)")
	}
	if ag.AutopilotActive() {
		t.Fatal("no runtime bound must never arm the live posture")
	}
}
