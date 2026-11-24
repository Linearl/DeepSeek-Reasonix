package main

// tl1795 回归（autopilot 裸旗与显示层脱节）：
//  ① 档位离开必须落到活控制器——真实复现里控制器在构建期就带着 unattended
//     姿态（新 tab 默认 yolo+tl1741 默认开，或重启恢复），用户点 Yolo 档后
//     tab 裸旗已清、c.autopilot 还是 true，ask 仍按 autopilot 15s 自决
//     （dec-c9f7a50a 实锤）。断言切档后 AutopilotActive()==false。
//  ② SetApprovalTierForTab fail-fast：tab 失效必须报错（前端据此回滚乐观
//     落旗），不再静默 nil；legacy setter 保持静默兼容。
//  ③ 恢复 reconcile：tab entry（用户最后档位意图）=false 而 goal-state
//     sidecar（旧运行标记）=true 时，attended 胜出，不再复活 autopilot；
//     entry=true 的正路恢复不受影响。
//  ④ SetModeForTab legacy 入口补 325 反向联动：normal 翻 approval 到 ask
//     必须同调用关 autopilot 并打 notice。

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/store"
)

// tl1795PostureTab mirrors the real initial-build inputs (tabs.go passes
// tab.autopilot* into boot.Options): a tab whose live controller was built
// unattended — the state every "switched to YOLO, ask still self-answered"
// report started from.
func tl1795PostureTab(t *testing.T, app *App, id string) (*WorkspaceTab, *control.Controller) {
	t.Helper()
	ag := control.New(control.Options{
		Label:                      id,
		Autopilot:                  true,
		AutopilotMaxRuntime:        8 * time.Hour,
		AutopilotAskTimeoutEnabled: true,
		AutopilotAskWait:           15 * time.Second,
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
		autopilot:                  true,
		autopilotMaxRuntime:        8 * time.Hour,
		autopilotAskTimeoutEnabled: true,
		autopilotAskWait:           15 * time.Second,
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app, ctx: context.Background()}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() { ag.Close() })
	return tab, ag
}

func tl1795AutopilotConfig(t *testing.T) {
	t.Helper()
	cfg := config.Default()
	cfg.Desktop.AutopilotMaxRuntime = "8h"
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

// ① 595 档位离开（yolo）必须同步清掉活控制器的 unattended 姿态。
func TestTl1795TierLeaveDropsLiveAutopilotPosture(t *testing.T) {
	isolateDesktopUserDirs(t)
	tl1795AutopilotConfig(t)
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	tab, ag := tl1795PostureTab(t, app, "tl1795-leave")
	defer tab.Ctrl.Close()

	// 前置：构建期姿态在位（真实复现起点）。
	if !ag.AutopilotActive() {
		t.Fatal("precondition failed: built controller must carry the unattended posture")
	}
	if !tab.autopilot {
		t.Fatal("precondition failed: tab flag must start engaged")
	}

	app.SetApprovalTierForTab(tab.ID, control.ToolApprovalYolo)
	if tab.autopilot {
		t.Fatal("yolo tier pick must clear tab.autopilot (595)")
	}
	if ag.AutopilotActive() {
		t.Fatal("yolo tier pick must drop the live controller posture; a stale true re-arms the 15s ask self-answer (tl1795)")
	}
	if tab.toolApprovalMode != control.ToolApprovalYolo {
		t.Fatalf("approval after tier leave = %q, want yolo", tab.toolApprovalMode)
	}
}

// ② tier 切换 fail-fast：tab 失效必须报错；legacy setter 保持静默兼容。
func TestTl1795TierSwitchFailFastOnMissingTab(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}

	if _, err := app.SetApprovalTierForTab("no-such-tab", control.ToolApprovalYolo); err == nil {
		t.Fatal("tier switch on a missing tab must return an error (fail-fast, tl1795)")
	}
	if _, err := app.SetApprovalTierForTab("", control.ToolApprovalAsk); err == nil {
		t.Fatal("tier switch on an empty tab id must return an error")
	}
	// legacy 编程写入者保持 pre-tl1795 静默形状（heartbeat 调用方不读错误）。
	if drained := app.SetToolApprovalModeForTab("no-such-tab", control.ToolApprovalYolo); drained != nil {
		t.Fatalf("legacy setter drained = %v, want nil (silent compatibility)", drained)
	}
}

// ③ 恢复 reconcile：entry(false) vs sidecar(true) 冲突时 attended 胜出。
func TestTl1795RestoreReconcileAttendedEntryBeatsStaleSidecar(t *testing.T) {
	isolateDesktopUserDirs(t)
	tl1795AutopilotConfig(t)
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	app, tab, _ := autopilotGateTestApp(t, control.ToolApprovalYolo)
	// 参与 autopilot（真实序列第一步），再让运行写下 sidecar 标记。
	app.SetCollaborationModeForTab(tab.ID, "autopilot")
	if !tab.autopilot {
		t.Fatal("precondition failed: autopilot tier must engage")
	}
	tab.SessionPath = filepath.Join(t.TempDir(), "session-20261124.jsonl")
	sidecar := tabSessionGoalStateFixture(t, tab.SessionPath, true)

	// 用户切回 Yolo 档（task-595 leave）→ tab 旗清 + 落盘（真实序列第二步）。
	app.SetApprovalTierForTab(tab.ID, control.ToolApprovalYolo)
	if tab.autopilot {
		t.Fatal("tier leave must clear the flag before persisting")
	}
	app.mu.Lock()
	app.saveTabsLocked()
	app.mu.Unlock()
	f := loadTabsFile()
	if len(f.Tabs) != 1 || f.Tabs[0].Autopilot {
		t.Fatalf("persisted entry must carry autopilot=false, got %+v", f.Tabs)
	}
	_ = sidecar

	// 重启等价：全新 App 恢复。旧逻辑会因 sidecar=true + approval=yolo 复活
	// autopilot（gate 认为 (autopilot, yolo) 合法不纠正）；tl1795 起 entry 胜出。
	restored := NewApp()
	restored.ctx = context.Background()
	restored.readyHook = func() {}
	restored.restorePersistedTabs(context.Background(), f)
	restored.mu.RLock()
	gotTab := restored.tabs[tab.ID]
	restored.mu.RUnlock()
	if gotTab == nil {
		t.Fatal("restored tab missing")
	}
	if gotTab.autopilot {
		t.Fatal("restore resurrected autopilot from the stale sidecar behind a YOLO composer (tl1795 reconcile failed)")
	}
	if gotTab.toolApprovalMode != control.ToolApprovalYolo {
		t.Fatalf("restored approval = %q, want yolo", gotTab.toolApprovalMode)
	}
	if !strings.Contains(logBuf.String(), "[autopilot-restore] reconcile") {
		t.Fatalf("reconcile anchor log missing; got:\n%s", logBuf.String())
	}
	_ = os.Remove(store.SessionGoalState(tab.SessionPath))
}

// ③' 正路恢复不受 reconcile 误伤：entry=true + sidecar=true 照常恢复 unattended。
func TestTl1795RestoreEngagedEntryStillArms(t *testing.T) {
	isolateDesktopUserDirs(t)
	tl1795AutopilotConfig(t)

	app, tab, _ := autopilotGateTestApp(t, control.ToolApprovalYolo)
	app.SetCollaborationModeForTab(tab.ID, "autopilot")
	tab.SessionPath = filepath.Join(t.TempDir(), "session-engaged.jsonl")
	_ = tabSessionGoalStateFixture(t, tab.SessionPath, true)
	app.mu.Lock()
	app.saveTabsLocked()
	app.mu.Unlock()
	f := loadTabsFile()
	if len(f.Tabs) != 1 || !f.Tabs[0].Autopilot {
		t.Fatalf("engaged entry must persist autopilot=true, got %+v", f.Tabs)
	}

	restored := NewApp()
	restored.ctx = context.Background()
	restored.readyHook = func() {}
	restored.restorePersistedTabs(context.Background(), f)
	restored.mu.RLock()
	gotTab := restored.tabs[tab.ID]
	restored.mu.RUnlock()
	if gotTab == nil {
		t.Fatal("restored tab missing")
	}
	if !gotTab.autopilot {
		t.Fatal("a genuinely engaged entry must still restore unattended (reconcile must not over-suppress)")
	}
	_ = os.Remove(store.SessionGoalState(tab.SessionPath))
}

// tabSessionGoalStateFixture 写一份 goal-state sidecar（task 49 A2 恢复标记）。
func tabSessionGoalStateFixture(t *testing.T, sessionPath string, autopilot bool) string {
	t.Helper()
	data := ""
	if autopilot {
		data = `{"goal":"unattended objective","status":"running","autopilot":true}`
	} else {
		data = `{"goal":"unattended objective","status":"running","autopilot":false}`
	}
	path := store.SessionGoalState(sessionPath)
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write goal-state sidecar: %v", err)
	}
	return path
}

// ④ legacy SetModeForTab 入口：normal 把 approval 翻到 ask 时必须同调用关
// autopilot（325 反向联动）并打 warn notice。
func TestTl1795LegacySetModeNormalClosesAutopilot(t *testing.T) {
	app, tab, codes := autopilotGateTestApp(t, control.ToolApprovalYolo)
	app.SetCollaborationModeForTab(tab.ID, "autopilot")
	if !tab.autopilot {
		t.Fatal("precondition failed: autopilot tier must engage")
	}

	app.SetModeForTab(tab.ID, "normal")
	if tab.autopilot {
		t.Fatal("legacy SetModeForTab(normal) must close autopilot — approval flipped to ask, the unattended flag may not stand (325/tl1795)")
	}
	if tab.toolApprovalMode != control.ToolApprovalAsk {
		t.Fatalf("approval after legacy normal = %q, want ask", tab.toolApprovalMode)
	}
	if !waitForNoticeCode(t, codes, NoticeCodeAutopilotClosedOffYolo) {
		t.Fatalf("off-yolo close notice %q not emitted, got %v", NoticeCodeAutopilotClosedOffYolo, *codes)
	}
}
