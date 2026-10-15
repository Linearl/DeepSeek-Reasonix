package main

// 任务 902（批 33 验证先行件）：上游 #10335 / PR #10825 同款缺陷的复现层测试。
// 上游症状：关闭软件 / 切页后，会话权限 preset 被静默改回默认（workspace-write）。
// fork 无 SetPermissionPresetForTab / desktop-session-presets.json，权限轴是
// toolApprovalMode（ask/auto/yolo），持久化两处：desktop-tabs.json 列 + session
// BranchMeta sidecar。本文件按验证计划做状态序列化往返：
//
//	T1 设权限 → tabs 文件往返 → restorePersistedTabs（重启等价）→ 查 preset
//	T2 切页等价：saveTabSessionMeta（rebind 切走前的写入）→ loadTabSessionProfile 回读
//	T3 打开会话等价：applyTabSessionProfile(loadTabSessionProfile) → 查 preset
//	T4 前端启动回推等价：SetComposerProfileForTab 带水合后的正确值 → preset 保持
//	T5 前端回推未水合默认值（同族泄漏面实证）：SetComposerProfileForTab(ask) 是否静默打回并持久化
//	T6 app_modes.go:157-161 重算语义：SetModeForTab("normal") 对 yolo 的降档行为
//
// 判定（可证伪）：重建后 preset ≠ 用户所设值 = 复现；= 用户所设值 = 不复现。

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/control"
)

// presetReproApp builds an isolated App with one global-scope tab whose live
// controller and tab field both carry the given approval mode (the same
// invariant SetToolApprovalModeForTab maintains: it applies to controller and
// saves tabs). Global scope keeps restorePersistedTabs off the project
// admission path so the restart-equivalent runs headless.
func presetReproApp(t *testing.T, id, approval string) (*App, *WorkspaceTab) {
	t.Helper()
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	tab := &WorkspaceTab{
		ID:               id,
		Scope:            "global",
		WorkspaceRoot:    globalTabWorkspaceRoot(),
		Ready:            true,
		Ctrl:             control.New(control.Options{Label: id}),
		mode:             "normal",
		disabledMCP:      map[string]ServerView{},
		toolApprovalMode: normalizeToolApprovalMode(approval),
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app, ctx: context.Background()}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
	})
	return app, tab
}

func mustSetPresetApproval(t *testing.T, app *App, tabID, approval string) {
	t.Helper()
	app.SetToolApprovalModeForTab(tabID, approval)
	got := currentTabToolApprovalMode(tabByIDForPresetTest(t, app, tabID))
	if got != approval {
		t.Fatalf("precondition: SetToolApprovalModeForTab(%q) landed as %q", approval, got)
	}
}

func tabByIDForPresetTest(t *testing.T, app *App, id string) *WorkspaceTab {
	t.Helper()
	app.mu.RLock()
	tab := app.tabs[id]
	app.mu.RUnlock()
	if tab == nil {
		t.Fatalf("tab %q missing", id)
	}
	return tab
}

// T1 重启等价：设权限 → saveTabsLocked → loadTabsFile → restorePersistedTabs
// （新 App 状态重建）→ 查 preset。对应上游「关闭软件后重启」症状。
func TestPreset902RestartRoundTripKeepsApprovalMode(t *testing.T) {
	for _, approval := range []string{control.ToolApprovalYolo, control.ToolApprovalAuto} {
		t.Run(approval, func(t *testing.T) {
			app, tab := presetReproApp(t, "rt-tab", control.ToolApprovalAsk)
			mustSetPresetApproval(t, app, tab.ID, approval)

			app.mu.Lock()
			app.saveTabsLocked()
			app.mu.Unlock()
			f := loadTabsFile()
			if len(f.Tabs) != 1 {
				t.Fatalf("persisted %d tabs, want 1", len(f.Tabs))
			}
			if got := f.Tabs[0].ToolApprovalMode; got != approval {
				t.Fatalf("desktop-tabs.json toolApprovalMode = %q, want %q (重启等价的输入在落盘时就丢了)", got, approval)
			}

			// 重启等价：全新 App 从持久化文件重建 tab 骨架。
			restored := NewApp()
			restored.ctx = context.Background()
			restored.readyHook = func() {}
			restored.restorePersistedTabs(context.Background(), f)
			restored.mu.RLock()
			gotTab := restored.tabs[tab.ID]
			restored.mu.RUnlock()
			if gotTab == nil {
				t.Fatalf("restored tab %q missing", tab.ID)
			}
			if got := normalizeToolApprovalMode(gotTab.toolApprovalMode); got != approval {
				t.Fatalf("restart-equivalent restore: toolApprovalMode = %q, want %q", got, approval)
			}
			// 控制器构建输入同源（tabs.go buildToolApprovalMode := tab.toolApprovalMode）。
			if got := normalizeToolApprovalMode(gotTab.toolApprovalMode); got != approval {
				t.Fatalf("controller build input = %q, want %q", got, approval)
			}
		})
	}
}

// T1b：ask（默认值）的往返也必须稳定——persistedToolApprovalMode 把 ask 存成
// 空串（omitempty），恢复端 normalize("")=ask，这条不变式断言不被误改。
func TestPreset902RestartRoundTripAskStaysDefault(t *testing.T) {
	app, tab := presetReproApp(t, "rt-ask", control.ToolApprovalAsk)
	app.mu.Lock()
	app.saveTabsLocked()
	app.mu.Unlock()
	f := loadTabsFile()
	if len(f.Tabs) != 1 || f.Tabs[0].ToolApprovalMode != "" {
		t.Fatalf("ask must persist as empty column, got %+v", f.Tabs)
	}
	restored := NewApp()
	restored.ctx = context.Background()
	restored.readyHook = func() {}
	restored.restorePersistedTabs(context.Background(), f)
	restored.mu.RLock()
	gotTab := restored.tabs[tab.ID]
	restored.mu.RUnlock()
	if gotTab == nil {
		t.Fatalf("restored tab missing")
	}
	if got := normalizeToolApprovalMode(gotTab.toolApprovalMode); got != control.ToolApprovalAsk {
		t.Fatalf("restored approval = %q, want ask", got)
	}
}

// T2 切页等价：用户在 tab 上设 yolo → 切到别的会话前 rebind 会
// saveTabSessionMeta(tab, oldPath)（app_session_rebind.go:118/187）→ 切回时
// loadTabSessionProfile(oldPath) 必须回读到 yolo，而不是默认 ask。
func TestPreset902SessionSidecarRoundTripKeepsApprovalMode(t *testing.T) {
	for _, approval := range []string{control.ToolApprovalYolo, control.ToolApprovalAuto} {
		t.Run(approval, func(t *testing.T) {
			app, tab := presetReproApp(t, "sw-tab", control.ToolApprovalAsk)
			mustSetPresetApproval(t, app, tab.ID, approval)

			path := filepath.Join(t.TempDir(), "session-a.jsonl")
			if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
				t.Fatalf("seed session file: %v", err)
			}
			// 切走前的 sidecar 写入（rebind 的真实调用）。
			if err := app.saveTabSessionMeta(tab, path); err != nil {
				t.Fatalf("saveTabSessionMeta: %v", err)
			}
			profile := loadTabSessionProfile(path)
			if profile.toolApprovalMode != approval {
				t.Fatalf("切页 sidecar 回读 toolApprovalMode = %q, want %q", profile.toolApprovalMode, approval)
			}
		})
	}
}

// T3 打开会话等价：openTopicTab 新建 tab 时 applyTabSessionProfile(loadTabSessionProfile)
// （tabs.go:2538/2550）。session 自己 sidecar 里的 preset 必须原样落到新 tab。
func TestPreset902SessionOpenAppliesOwnSidecarPreset(t *testing.T) {
	for _, approval := range []string{control.ToolApprovalYolo, control.ToolApprovalAuto} {
		t.Run(approval, func(t *testing.T) {
			app, tab := presetReproApp(t, "op-tab", control.ToolApprovalAsk)
			mustSetPresetApproval(t, app, tab.ID, approval)

			path := filepath.Join(t.TempDir(), "session-b.jsonl")
			if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
				t.Fatalf("seed session file: %v", err)
			}
			if err := app.saveTabSessionMeta(tab, path); err != nil {
				t.Fatalf("saveTabSessionMeta: %v", err)
			}

			fresh := &WorkspaceTab{ID: "op-target", disabledMCP: map[string]ServerView{}}
			applyTabSessionProfile(fresh, loadTabSessionProfile(path))
			if got := normalizeToolApprovalMode(fresh.toolApprovalMode); got != approval {
				t.Fatalf("session open applies preset = %q, want %q", got, approval)
			}
		})
	}
}

// T4 前端启动回推等价（水合完成后的真实值）：controllerReady 后 App.tsx:2166
// 用 meta 水合出的 profile 回推 SetComposerProfileForTab。水合值=后端值时，
// 回推不得改变 preset（幂等），也不得把它持久化成别的值。
func TestPreset902ComposerProfilePushWithHydratedValueKeepsPreset(t *testing.T) {
	app, tab := presetReproApp(t, "pf-tab", control.ToolApprovalAsk)
	mustSetPresetApproval(t, app, tab.ID, control.ToolApprovalYolo)

	if _, err := app.SetComposerProfileForTab(tab.ID, "normal", control.ToolApprovalYolo, ""); err != nil {
		t.Fatalf("SetComposerProfileForTab: %v", err)
	}
	if got := currentTabToolApprovalMode(tab); got != control.ToolApprovalYolo {
		t.Fatalf("hydrated profile push changed preset to %q, want yolo", got)
	}
	app.mu.Lock()
	app.saveTabsLocked()
	app.mu.Unlock()
	f := loadTabsFile()
	if got := f.Tabs[0].ToolApprovalMode; got != control.ToolApprovalYolo {
		t.Fatalf("persisted after hydrated push = %q, want yolo", got)
	}
}

// T5 同族泄漏面实证：后端 SetComposerProfileForTab 没有会话围栏（上游修法
// SetPermissionPresetForTab(expectedSessionID) 在 fork 不存在）。若前端在水合
// 完成前用 defaultComposerProfile(toolApprovalMode=ask) 回推，后端会静默接受、
// 覆盖 yolo 并写进 desktop-tabs.json —— 这是 data-loss 级的写放大面。
// 本测试断言该面存在（写入被接受且持久化），作为「同族风险」的代码级证据，
// 而非断言前端时序一定触发（前端时序无法在 Go 无头环境复现，见报告）。
func TestPreset902UnfencedProfilePushCanLosePreset(t *testing.T) {
	app, tab := presetReproApp(t, "stale-tab", control.ToolApprovalAsk)
	mustSetPresetApproval(t, app, tab.ID, control.ToolApprovalYolo)

	// 模拟未水合的前端回推（defaultComposerProfile.toolApprovalMode = "ask"）。
	if _, err := app.SetComposerProfileForTab(tab.ID, "normal", control.ToolApprovalAsk, ""); err != nil {
		t.Fatalf("SetComposerProfileForTab: %v", err)
	}
	if got := currentTabToolApprovalMode(tab); got != control.ToolApprovalAsk {
		t.Fatalf("unfenced push landed %q, want ask (后端无围栏会接受)", got)
	}
	app.mu.Lock()
	app.saveTabsLocked()
	app.mu.Unlock()
	f := loadTabsFile()
	if len(f.Tabs) != 1 {
		t.Fatalf("persisted %d tabs, want 1", len(f.Tabs))
	}
	// 持久化列变空 = 用户的 yolo 选择在磁盘上消失，下次重启回到默认。
	if got := f.Tabs[0].ToolApprovalMode; got != "" {
		t.Fatalf("persisted toolApprovalMode = %q, want empty (yolo 已被覆盖)", got)
	}
}

// T6 app_modes.go:157-161 重算语义钉住：SetModeForTab 切到不带 autoapprove 的
// mode（normal/plan）时，当前 yolo 会被降为 ask 并持久化。这是显式模式切换的
// 联动降档（用户点模式胶囊才触发），不是关闭/切页路径——但它证明「preset 变
// 默认」在 fork 有一条后端确定性通道，只要前端把 SetModeForTab 打到重建后的
// tab 上。
func TestPreset902SetModeForTabNormalDemotesYoloToAsk(t *testing.T) {
	app, tab := presetReproApp(t, "mode-tab", control.ToolApprovalAsk)
	mustSetPresetApproval(t, app, tab.ID, control.ToolApprovalYolo)

	app.SetModeForTab(tab.ID, "normal")
	got := currentTabToolApprovalMode(tab)
	if got != control.ToolApprovalAsk {
		t.Fatalf("SetModeForTab(normal) approval = %q, want ask (157-161 重算降档)", got)
	}
	app.mu.Lock()
	app.saveTabsLocked()
	app.mu.Unlock()
	if f := loadTabsFile(); f.Tabs[0].ToolApprovalMode != "" {
		t.Fatalf("demoted preset still persisted as %q", f.Tabs[0].ToolApprovalMode)
	}
}
