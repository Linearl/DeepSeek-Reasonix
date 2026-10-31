package main

// tl1298 — 心跳任务 approvalMode=yolo 生效迟到（会话 meta 缺 tool_approval_mode
// ⇒ tab 回落 ask ⇒ 首轮弹审批面板，第二轮才生效）。
//
// 修复的两条腿各有一个主断言：
//  1. 建会话即落档：openTopicTabWithActivation 新建分支把 config 新会话默认档
//     写进 meta（与 createTabEntryWithID/EnsureBlankTab 两条新建路径同语义），
//     tab 诞生即按该档运行——任何 actor 在首轮提交的 turn 都不再撞 ask 面板。
//  2. 设档即落档：心跳 fresh 模式在 SetToolApprovalModeForTab 的同一时刻把
//     任务档写进 meta——重开/分离重升/重启恢复的会话首轮即任务档。
//
// 回归钉（双断言）：
//  - 既有会话的 meta 永不被 open 路径覆写（reuse 分支零写）；
//  - ask 档保持缺席键形态（磁盘形状与 tl1298 之前逐字节一致）；
//  - 非 fresh 心跳（resume/legacy 绑定用户会话，task 437）不写任务档进 meta。
//
// Zero SKIP：每个前置条件失败即 t.Fatal。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
)

func seedDesktopDefaultApproval(t *testing.T, mode string) {
	t.Helper()
	cfg := config.LoadForEdit(config.UserConfigPath())
	if err := cfg.SetDesktopDefaultToolApprovalMode(mode); err != nil {
		t.Fatalf("SetDesktopDefaultToolApprovalMode(%q): %v", mode, err)
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save user config: %v", err)
	}
}

func globalSessionDirForTest(t *testing.T) string {
	t.Helper()
	dir := desktopSessionDir(globalWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir global sessions: %v", err)
	}
	return dir
}

func readSessionApprovalMode(t *testing.T, path string) string {
	t.Helper()
	meta, ok, err := agent.LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta(%q): ok=%v err=%v", path, ok, err)
	}
	return meta.ToolApprovalMode
}

func singleSessionInDir(t *testing.T, dir string) string {
	t.Helper()
	names := sessionJSONLNames(t, dir)
	if len(names) != 1 {
		t.Fatalf("sessions dir = %v, want exactly one minted session", names)
	}
	return filepath.Join(dir, names[0])
}

// 腿 1 主断言：config 默认档 yolo 时，open-topic 新建分支的会话 meta 与 tab
// 诞生档都是 yolo——心跳/侧栏经此路径新建的会话首轮即 yolo，无 ask 窗口。
func TestOpenTopicNewSessionPersistsConfigApprovalDefaultIntoMeta(t *testing.T) {
	isolateDesktopUserDirs(t)
	seedDesktopDefaultApproval(t, control.ToolApprovalYolo)
	app := NewApp()
	app.ctx = context.Background()
	t.Cleanup(func() { app.shutdown(context.Background()) })

	meta, err := app.OpenGlobalTab("topic_tl1298_seed_yolo")
	if err != nil {
		t.Fatalf("OpenGlobalTab: %v", err)
	}
	sessionPath := singleSessionInDir(t, globalSessionDirForTest(t))
	if got := readSessionApprovalMode(t, sessionPath); got != control.ToolApprovalYolo {
		t.Fatalf("new session meta tool_approval_mode = %q, want yolo (tl1298 leg 1: pin at creation)", got)
	}
	app.mu.RLock()
	tab := app.tabs[meta.ID]
	app.mu.RUnlock()
	if tab == nil {
		t.Fatalf("tab %q not found after open", meta.ID)
	}
	if tab.toolApprovalMode != control.ToolApprovalYolo {
		t.Fatalf("tab toolApprovalMode = %q, want yolo (first turn must run yolo)", tab.toolApprovalMode)
	}
}

// 腿 1 磁盘形状钉：config 默认档 ask 时 meta 不落 approval 键——ask 会话的
// 磁盘形状与 tl1298 之前逐字节一致（persistedToolApprovalMode(ask)=""/缺席键）。
func TestOpenTopicNewSessionAskDefaultKeepsApprovalKeyAbsent(t *testing.T) {
	isolateDesktopUserDirs(t)
	seedDesktopDefaultApproval(t, control.ToolApprovalAsk)
	app := NewApp()
	app.ctx = context.Background()
	t.Cleanup(func() { app.shutdown(context.Background()) })

	if _, err := app.OpenGlobalTab("topic_tl1298_seed_ask"); err != nil {
		t.Fatalf("OpenGlobalTab: %v", err)
	}
	sessionPath := singleSessionInDir(t, globalSessionDirForTest(t))
	if got := readSessionApprovalMode(t, sessionPath); got != "" {
		t.Fatalf("ask-default new session meta tool_approval_mode = %q, want absent key (empty string)", got)
	}
}

// writeGlobalTopicSession 在 global 会话目录造一个带 meta 的既有会话 fixture
// （writeTopicSession 硬编码 project scope，与 global 打开目标不匹配）。
// toolApprovalMode 传 "" 即缺席键形态。
func writeGlobalTopicSession(t *testing.T, dir, name, topicID, topicTitle, toolApprovalMode string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"hello"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	if err := agent.SaveBranchMeta(path, agent.BranchMeta{
		CreatedAt:        time.Now().Add(-time.Minute),
		UpdatedAt:        time.Now(),
		Scope:            "global",
		WorkspaceRoot:    "",
		TopicID:          topicID,
		TopicTitle:       topicTitle,
		ToolApprovalMode: toolApprovalMode,
	}); err != nil {
		t.Fatalf("save branch meta: %v", err)
	}
	return path
}

// 回归钉（普通会话不受影响）：reuse 分支零写——meta 缺席键的既有会话照旧
// 读出 ask、且 open 不往 meta 写任何 approval 值；meta 已有 yolo 的既有会话
// 照旧恢复 yolo（恢复语义不因 tl1298 改变）。
func TestOpenTopicReuseLeavesExistingSessionApprovalMetaUntouched(t *testing.T) {
	isolateDesktopUserDirs(t)
	seedDesktopDefaultApproval(t, control.ToolApprovalYolo)
	dir := globalSessionDirForTest(t)

	// 形态 A：tl1298 之前的旧会话（meta 无 approval 键）——open 后仍 ask，零写。
	legacyPath := writeGlobalTopicSession(t, dir, "20261031-010000.000000000-tl1298-legacy.jsonl",
		"topic_tl1298_legacy", "Legacy session", "")
	metaPath := legacyPath + ".meta"
	if got := readSessionApprovalMode(t, legacyPath); got != "" {
		t.Fatalf("legacy fixture pre-state tool_approval_mode = %q, want absent", got)
	}
	app := NewApp()
	app.ctx = context.Background()
	t.Cleanup(func() { app.shutdown(context.Background()) })
	tabMeta, err := app.OpenGlobalTab("topic_tl1298_legacy")
	if err != nil {
		t.Fatalf("OpenGlobalTab(legacy): %v", err)
	}
	app.mu.RLock()
	tab := app.tabs[tabMeta.ID]
	app.mu.RUnlock()
	if tab == nil {
		t.Fatalf("tab %q not found", tabMeta.ID)
	}
	if tab.toolApprovalMode != control.ToolApprovalAsk {
		t.Fatalf("legacy session (absent meta key) opened as %q, want ask (restore semantics unchanged)", tab.toolApprovalMode)
	}
	if got := readSessionApprovalMode(t, legacyPath); got != "" {
		t.Fatalf("reuse branch wrote tool_approval_mode=%q into legacy meta, want untouched absent key", got)
	}
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("meta sidecar missing after open: %v", err)
	}

	// 形态 B：meta 已有 yolo 的既有会话——open 恢复 yolo（读路径既有行为）。
	yoloPath := writeGlobalTopicSession(t, dir, "20261031-010100.000000000-tl1298-yolo.jsonl",
		"topic_tl1298_yolo", "Yolo session", control.ToolApprovalYolo)
	if err := pinSessionToolApprovalMode(yoloPath, control.ToolApprovalYolo); err != nil {
		t.Fatalf("pinSessionToolApprovalMode: %v", err)
	}
	if _, err := app.OpenGlobalTab("topic_tl1298_yolo"); err != nil {
		t.Fatalf("OpenGlobalTab(yolo): %v", err)
	}
	if got := readSessionApprovalMode(t, yoloPath); got != control.ToolApprovalYolo {
		t.Fatalf("yolo meta mutated by open: %q", got)
	}
	app.mu.RLock()
	var restored *WorkspaceTab
	for _, tt := range app.tabs {
		if tt != nil && strings.HasSuffix(tt.currentSessionPath(), "tl1298-yolo.jsonl") {
			restored = tt
		}
	}
	app.mu.RUnlock()
	if restored == nil || restored.toolApprovalMode != control.ToolApprovalYolo {
		t.Fatalf("stored yolo session restored as %v, want tab yolo", restored)
	}
}

// 腿 2 主断言（首轮读数）：fresh 模式 yolo 任务一轮跑完，tab 与会话 meta 都
// 是 yolo、prompt 已带 yolo 提交——后续重开/重升/重启的会话首轮即 yolo。
func TestHeartbeatFreshRunPersistsTaskApprovalModeIntoSessionMeta(t *testing.T) {
	isolateDesktopUserDirs(t)
	seed := HeartbeatTask{
		ID:                     "tl1298-fresh",
		Title:                  "磁盘卫生六阶段",
		Prompt:                 "run cleanup",
		ApprovalMode:           control.ToolApprovalYolo,
		NewConversationEachRun: true,
		Enabled:                true,
	}
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	app.runtimeEvents.emit = func(context.Context, string, ...any) {}
	engine := &HeartbeatEngine{
		app:           app,
		pendingTopics: map[string]heartbeatPendingTopic{},
	}
	if err := engine.saveTasks([]HeartbeatTask{seed}); err != nil {
		t.Fatalf("seed tasks: %v", err)
	}
	engine.ReloadConfig()

	ctrl := &heartbeatExecuteTaskCtrlStub{}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				app.mu.Lock()
				for _, tab := range app.tabs {
					if tab == nil || tab.Ctrl != nil {
						continue
					}
					tab.removed = true
					if cancel := tab.buildCancel; cancel != nil {
						cancel()
					}
					tab.Ctrl = ctrl
					tab.Ready = true
					tab.StartupErr = ""
					app.advanceSessionRuntimeEpochLocked(tab)
				}
				app.mu.Unlock()
			}
		}
	}()

	got := engine.executeTask(seed)
	if len(ctrl.submitted) == 0 {
		t.Fatal("fresh-mode run submitted no prompt; harness broken (t.Fatal, not skip)")
	}
	if got.ApprovalMode != control.ToolApprovalYolo {
		t.Fatalf("task approval mode after run = %q, want yolo", got.ApprovalMode)
	}
	if ctrl.approvalMode != control.ToolApprovalYolo {
		t.Fatalf("controller approval mode at submit = %q, want yolo (set precedes submit)", ctrl.approvalMode)
	}

	app.mu.RLock()
	var tab *WorkspaceTab
	for _, tt := range app.tabs {
		if tt != nil && strings.HasPrefix(tt.TopicTitle, "Heartbeat: ") {
			tab = tt
		}
	}
	app.mu.RUnlock()
	if tab == nil {
		t.Fatal("heartbeat tab not found after run")
	}
	if tab.toolApprovalMode != control.ToolApprovalYolo {
		t.Fatalf("heartbeat tab toolApprovalMode = %q, want yolo", tab.toolApprovalMode)
	}
	sessionPath := tab.currentSessionPath()
	if strings.TrimSpace(sessionPath) == "" {
		t.Fatal("heartbeat tab has no session path")
	}
	if got := readSessionApprovalMode(t, sessionPath); got != control.ToolApprovalYolo {
		t.Fatalf("fresh session meta tool_approval_mode = %q, want yolo (tl1298 leg 2: persist at set time)", got)
	}
}

// 回归钉（task 437 边界）：非 fresh 心跳（resume/legacy 绑定，往往是用户自己的
// 会话）不把任务档写进 meta——绑定会话的恢复语义保持 snapshot-on-close 不变。
func TestHeartbeatNonFreshRunLeavesSessionApprovalMetaUntouched(t *testing.T) {
	isolateDesktopUserDirs(t)
	seed := HeartbeatTask{
		ID:           "tl1298-legacy",
		Title:        "守护绑定会话",
		Prompt:       "inspect",
		ApprovalMode: control.ToolApprovalYolo,
		Enabled:      true,
	}
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	app.runtimeEvents.emit = func(context.Context, string, ...any) {}
	engine := &HeartbeatEngine{
		app:           app,
		pendingTopics: map[string]heartbeatPendingTopic{},
	}
	if err := engine.saveTasks([]HeartbeatTask{seed}); err != nil {
		t.Fatalf("seed tasks: %v", err)
	}
	engine.ReloadConfig()

	ctrl := &heartbeatExecuteTaskCtrlStub{}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				app.mu.Lock()
				for _, tab := range app.tabs {
					if tab == nil || tab.Ctrl != nil {
						continue
					}
					tab.removed = true
					if cancel := tab.buildCancel; cancel != nil {
						cancel()
					}
					tab.Ctrl = ctrl
					tab.Ready = true
					tab.StartupErr = ""
					app.advanceSessionRuntimeEpochLocked(tab)
				}
				app.mu.Unlock()
			}
		}
	}()

	engine.executeTask(seed)
	if len(ctrl.submitted) == 0 {
		t.Fatal("legacy run submitted no prompt; harness broken (t.Fatal, not skip)")
	}
	app.mu.RLock()
	var tab *WorkspaceTab
	for _, tt := range app.tabs {
		if tt != nil && strings.HasPrefix(tt.TopicTitle, "Heartbeat: ") {
			tab = tt
		}
	}
	app.mu.RUnlock()
	if tab == nil {
		t.Fatal("heartbeat tab not found")
	}
	if tab.toolApprovalMode != control.ToolApprovalYolo {
		t.Fatalf("in-memory tab mode = %q, want yolo (existing pre-submit behavior)", tab.toolApprovalMode)
	}
	sessionPath := tab.currentSessionPath()
	if strings.TrimSpace(sessionPath) == "" {
		t.Fatal("legacy tab has no session path")
	}
	if got := readSessionApprovalMode(t, sessionPath); got == control.ToolApprovalYolo {
		t.Fatalf("non-fresh run persisted task mode yolo into session meta; want untouched (task 437 boundary, tl1298 gate)")
	}
}

// pin 助手单测：yolo/auto 落键，ask 空转（缺席键形态保持），幂等。
func TestPinSessionToolApprovalModeRoundTrip(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := t.TempDir()
	path, err := createEmptySessionFile(dir, "test-model")
	if err != nil {
		t.Fatalf("createEmptySessionFile: %v", err)
	}

	// ask：不落键（缺席 = ask，磁盘形状不变）。
	if err := pinSessionToolApprovalMode(path, control.ToolApprovalAsk); err != nil {
		t.Fatalf("pin ask: %v", err)
	}
	if got := readSessionApprovalMode(t, path); got != "" {
		t.Fatalf("after pin ask: tool_approval_mode = %q, want absent key", got)
	}
	// yolo：落键；重复落同值幂等。
	for range 2 {
		if err := pinSessionToolApprovalMode(path, control.ToolApprovalYolo); err != nil {
			t.Fatalf("pin yolo: %v", err)
		}
	}
	if got := readSessionApprovalMode(t, path); got != control.ToolApprovalYolo {
		t.Fatalf("after pin yolo: tool_approval_mode = %q, want yolo", got)
	}
	// auto：覆写为 auto（心跳任务档与既有值不同时以新意图为准）。
	if err := pinSessionToolApprovalMode(path, control.ToolApprovalAuto); err != nil {
		t.Fatalf("pin auto: %v", err)
	}
	if got := readSessionApprovalMode(t, path); got != control.ToolApprovalAuto {
		t.Fatalf("after pin auto: tool_approval_mode = %q, want auto", got)
	}
	// 空路径安全空转。
	if err := pinSessionToolApprovalMode("", control.ToolApprovalYolo); err != nil {
		t.Fatalf("pin empty path: %v", err)
	}
}
