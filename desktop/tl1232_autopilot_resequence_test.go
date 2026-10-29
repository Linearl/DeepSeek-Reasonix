package main

// tl1232 复现测试：用户实测 yolo→autopilot→yolo 之后 autopilot 档位再也选不上。
// 这里按前端真实入口语义重放四段序列：
//   段1 yolo 档点 autopilot → SetCollaborationModeForTab("autopilot")
//   段2 autopilot 持有点 yolo → SetApprovalTierForTab("yolo")（595 单选原子离开）
//   段3 再点 autopilot   → SetCollaborationModeForTab("autopilot")
// 每段后断言引擎态（tab.autopilot / toolApprovalMode）与两条 wire 呈现
// （composer 快照 collaborationMode + tab-strip currentTabCollaborationMode）。
// 另附段2 走 legacy SetToolApprovalModeForTab("yolo") 的变体（325 反向联动语义）。

import (
	"testing"

	"reasonix/internal/control"
)

func tl1232AssertEngaged(t *testing.T, app *App, tab *WorkspaceTab, stage string) {
	t.Helper()
	if !tab.autopilot {
		t.Fatalf("%s: tab.autopilot = false, want true", stage)
	}
	if tab.toolApprovalMode != control.ToolApprovalYolo {
		t.Fatalf("%s: approval = %q, want yolo", stage, tab.toolApprovalMode)
	}
	if got := app.tabRuntimeSnapshot(tab).collaborationMode(); got != "autopilot" {
		t.Fatalf("%s: composer wire = %q, want autopilot", stage, got)
	}
	if got := currentTabCollaborationMode(tab); got != "autopilot" {
		t.Fatalf("%s: tab-strip wire = %q, want autopilot", stage, got)
	}
}

func tl1232AssertYolo(t *testing.T, app *App, tab *WorkspaceTab, stage string) {
	t.Helper()
	if tab.autopilot {
		t.Fatalf("%s: tab.autopilot = true, want false", stage)
	}
	if tab.toolApprovalMode != control.ToolApprovalYolo {
		t.Fatalf("%s: approval = %q, want yolo", stage, tab.toolApprovalMode)
	}
	if got := app.tabRuntimeSnapshot(tab).collaborationMode(); got != "normal" {
		t.Fatalf("%s: composer wire = %q, want normal", stage, got)
	}
	if got := currentTabCollaborationMode(tab); got != "normal" {
		t.Fatalf("%s: tab-strip wire = %q, want normal", stage, got)
	}
}

func TestTl1232YoloAutopilotYoloAutopilotTierPath(t *testing.T) {
	app, tab, _ := autopilotGateTestApp(t, control.ToolApprovalYolo)

	// 段1 yolo → autopilot
	app.SetCollaborationModeForTab(tab.ID, "autopilot")
	tl1232AssertEngaged(t, app, tab, "段1 yolo→autopilot")

	// 段2 autopilot → yolo（595 tier 单选原子离开，applyToolApprovalMode 的 autopilotDrops 路径）
	app.SetApprovalTierForTab(tab.ID, control.ToolApprovalYolo)
	tl1232AssertYolo(t, app, tab, "段2 autopilot→yolo (tier)")

	// 段3 yolo → autopilot（复选，报障点）
	app.SetCollaborationModeForTab(tab.ID, "autopilot")
	tl1232AssertEngaged(t, app, tab, "段3 yolo→autopilot 复选")
}

func TestTl1232YoloAutopilotYoloAutopilotLegacyPath(t *testing.T) {
	app, tab, _ := autopilotGateTestApp(t, control.ToolApprovalYolo)

	app.SetCollaborationModeForTab(tab.ID, "autopilot")
	tl1232AssertEngaged(t, app, tab, "段1 yolo→autopilot")

	// 段2 legacy 写入者：SetToolApprovalModeForTab("yolo") —— 325 反向联动对
	// (autopilot, yolo) 不动作，autopilot 保持开（pre-595 语义）。
	app.SetToolApprovalModeForTab(tab.ID, control.ToolApprovalYolo)
	if !tab.autopilot {
		t.Fatal("段2 legacy yolo: autopilot 应保持开（yolo 兼容，pre-595 语义）")
	}

	// 段2' 由 autopilot 直切 ask（325 反向联动必须关）再回 yolo，再复选 autopilot。
	app.SetToolApprovalModeForTab(tab.ID, control.ToolApprovalAsk)
	if tab.autopilot {
		t.Fatal("段2' legacy ask: 审批离开 yolo 必须自动关 autopilot（325 反向联动）")
	}
	app.SetToolApprovalModeForTab(tab.ID, control.ToolApprovalYolo)
	tl1232AssertYolo(t, app, tab, "段2' 回到 yolo")

	app.SetCollaborationModeForTab(tab.ID, "autopilot")
	tl1232AssertEngaged(t, app, tab, "段3 yolo→autopilot 复选 (legacy 路径后)")
}
