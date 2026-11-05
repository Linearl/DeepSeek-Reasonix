package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1403 回归钉：recovery fork / 延迟转正后，打开的 tab surface 必须跟随
// 写入面（2026-11-05 13:21~14:12 早停现场）。两个方向各一枚：
//  1. fork：handleTabSessionRecovered 在换路径的同时 bump SessionGeneration、
//     重绑 sink generation、并推送 tab:meta（前端换代标记接收的唯一可信源）；
//  2. promote：handleTabSessionPromoted 把租约/写权/路径/generation 随迁回
//     主文件并推送 tab:meta——没有这枚，fork→promote→fork 循环里 surface
//     第二次分家（前端挂副本、controller 写主文件）。

func tabMetaEventFor(t *testing.T, rec *runtimeEventRecorder, wantPath string) Meta {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range rec.snapshot() {
			if e.name != tabMetaRefreshEventChannel {
				continue
			}
			refresh, ok := e.args[0].(TabMetaRefreshEvent)
			if !ok {
				continue
			}
			if refresh.Meta.SessionPath == wantPath {
				return refresh.Meta
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no tab:meta push carrying sessionPath %q within 5s", wantPath)
	return Meta{}
}

func Test1403RecoveredHandlerMovesSurfaceIdentity(t *testing.T) {
	t.Setenv(agent.SessionLogSchemaEnv, "v1")
	isolateDesktopUserDirs(t)

	root := globalTabWorkspaceRoot()
	dir := desktopSessionDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	originalPath := filepath.Join(dir, "session.jsonl")
	topic := "topic_tl1403_fork"
	if err := setTopicTitle("", topic, "TL1403 Fork"); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	disk := agent.NewSession("sys")
	disk.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	disk.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	disk.Add(provider.Message{Role: provider.RoleUser, Content: "disk second"})
	if err := disk.Save(originalPath); err != nil {
		t.Fatalf("Save disk: %v", err)
	}
	if err := agent.SaveBranchMeta(originalPath, agent.BranchMeta{
		Scope: "global", TopicID: topic, TopicTitle: "TL1403 Fork",
		Preview: "first", Turns: 2, SchemaVersion: agent.BranchMetaCountsVersion,
	}); err != nil {
		t.Fatalf("SaveBranchMeta: %v", err)
	}

	stale := agent.NewSession("sys")
	stale.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	stale.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	stale.Add(provider.Message{Role: provider.RoleUser, Content: "local second"})
	exec := agent.New(stubProvider{}, tool.NewRegistry(), stale, agent.Options{}, event.Discard)
	rec := &runtimeEventRecorder{}
	app := &App{
		tabs:             map[string]*WorkspaceTab{},
		detachedSessions: map[string]*WorkspaceTab{},
		activeTabID:      "tl1403_tab",
		ctx:              context.Background(),
	}
	app.runtimeEvents.emit = rec.record
	tab := &WorkspaceTab{
		ID:            "tl1403_tab",
		Scope:         "global",
		WorkspaceRoot: root,
		TopicID:       topic,
		TopicTitle:    "TL1403 Fork",
		SessionPath:   originalPath,
		Ready:         true,
		model:         "test-model",
		disabledMCP:   map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	tab.Ctrl = control.New(control.Options{
		Executor:            exec,
		SessionDir:          dir,
		SessionPath:         originalPath,
		Label:               "test",
		Sink:                tab.sink,
		SessionRecoveryMeta: app.tabSessionRecoveryMeta(tab),
		OnSessionRecovered:  app.handleTabSessionRecovered(tab),
	})
	app.tabs[tab.ID] = tab

	if err := tab.Ctrl.Snapshot(); err != nil {
		t.Fatalf("Snapshot (conflict fork expected inside): %v", err)
	}
	recoveryPath := tab.Ctrl.SessionPath()
	if recoveryPath == "" || recoveryPath == originalPath {
		t.Fatalf("recovery path = %q, want a distinct fork path", recoveryPath)
	}
	if tab.SessionPath != recoveryPath {
		t.Fatalf("tab session path = %q, want recovery path %q", tab.SessionPath, recoveryPath)
	}
	if tab.SessionGeneration != 1 {
		t.Fatalf("tab session generation = %d, want 1 after the fork move", tab.SessionGeneration)
	}
	if got := tab.sink.sessionGenerationSnapshot(); got != tab.SessionGeneration {
		t.Fatalf("sink session generation = %d, want %d (wire events must stay inside the frontend fence)", got, tab.SessionGeneration)
	}
	meta := tabMetaEventFor(t, rec, recoveryPath)
	if meta.SessionGeneration != 1 {
		t.Fatalf("pushed meta generation = %d, want 1 (the surface-move marker)", meta.SessionGeneration)
	}
}

func Test1403PromotedHandlerFollowsMainPath(t *testing.T) {
	t.Setenv(agent.SessionLogSchemaEnv, "v1")
	isolateDesktopUserDirs(t)

	root := globalTabWorkspaceRoot()
	dir := desktopSessionDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	mainPath := filepath.Join(dir, "main.jsonl")
	copyPath := filepath.Join(dir, "main-recovery-abc123.jsonl")
	topic := "topic_tl1403_promote"
	main := agent.NewSession("sys")
	main.Add(provider.Message{Role: provider.RoleUser, Content: "main content"})
	if err := main.Save(mainPath); err != nil {
		t.Fatalf("Save main: %v", err)
	}
	if err := agent.SaveBranchMeta(mainPath, agent.BranchMeta{Scope: "global", TopicID: topic, TopicTitle: "TL1403 Promote"}); err != nil {
		t.Fatalf("SaveBranchMeta main: %v", err)
	}
	copySess := agent.NewSession("sys")
	copySess.Add(provider.Message{Role: provider.RoleUser, Content: "copy content"})
	if err := copySess.Save(copyPath); err != nil {
		t.Fatalf("Save copy: %v", err)
	}

	rec := &runtimeEventRecorder{}
	app := &App{
		tabs:             map[string]*WorkspaceTab{},
		detachedSessions: map[string]*WorkspaceTab{},
		activeTabID:      "tl1403_tab",
		ctx:              context.Background(),
	}
	app.runtimeEvents.emit = rec.record
	tab := &WorkspaceTab{
		ID:                "tl1403_tab",
		Scope:             "global",
		WorkspaceRoot:     root,
		TopicID:           topic,
		TopicTitle:        "TL1403 Promote",
		SessionPath:       copyPath,
		SessionGeneration: 3,
		Ready:             true,
		disabledMCP:       map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	app.tabs[tab.ID] = tab
	// fork 时 detached 注册表挂在副本键上（handleTabSessionRecovered 的既有行为）。
	copyKey := sessionRuntimeKey(copyPath)
	app.detachedSessions[copyKey] = tab

	if err := app.handleTabSessionPromoted(tab)(control.SessionPromotionInfo{CopyPath: copyPath, MainPath: mainPath}); err != nil {
		t.Fatalf("handleTabSessionPromoted: %v", err)
	}
	if tab.SessionPath != mainPath {
		t.Fatalf("tab session path = %q, want main path %q", tab.SessionPath, mainPath)
	}
	if tab.SessionGeneration != 4 {
		t.Fatalf("tab session generation = %d, want 4 after the promote move", tab.SessionGeneration)
	}
	if got := tab.sink.sessionGenerationSnapshot(); got != 4 {
		t.Fatalf("sink session generation = %d, want 4", got)
	}
	mainKey := sessionRuntimeKey(mainPath)
	if app.detachedSessions[copyKey] == tab || app.detachedSessions[mainKey] != tab {
		t.Fatalf("detached registry did not rekey from copy to main (copy=%v main=%v)", app.detachedSessions[copyKey], app.detachedSessions[mainKey])
	}
	if tab.sessionLeaseRuntimeKey() != mainKey {
		t.Fatalf("tab lease runtime key = %q, want main key %q (write authority must follow the promotion)", tab.sessionLeaseRuntimeKey(), mainKey)
	}
	meta := tabMetaEventFor(t, rec, mainPath)
	if meta.SessionGeneration != 4 {
		t.Fatalf("pushed meta generation = %d, want 4", meta.SessionGeneration)
	}
	saved := loadTabsFile()
	if len(saved.Tabs) != 1 || saved.Tabs[0].SessionPath != mainPath {
		t.Fatalf("saved tabs = %+v, want the tab persistently on %q", saved.Tabs, mainPath)
	}
}

func Test1403PromotedHandlerDeclinesWhenControllerMovedOn(t *testing.T) {
	t.Setenv(agent.SessionLogSchemaEnv, "v1")
	isolateDesktopUserDirs(t)

	root := globalTabWorkspaceRoot()
	dir := desktopSessionDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	mainPath := filepath.Join(dir, "main.jsonl")
	main := agent.NewSession("sys")
	main.Add(provider.Message{Role: provider.RoleUser, Content: "main"})
	if err := main.Save(mainPath); err != nil {
		t.Fatalf("Save main: %v", err)
	}
	app := &App{
		tabs:             map[string]*WorkspaceTab{},
		detachedSessions: map[string]*WorkspaceTab{},
		activeTabID:      "tl1403_tab",
		ctx:              context.Background(),
	}
	app.runtimeEvents.emit = (&runtimeEventRecorder{}).record
	// 现场形态：通知在路上时控制器又被下一轮 fork 换道（不再住在被通知的主路径）。
	// 用一个假控制器模拟 controller.SessionPath() ≠ mainPath。
	tab := &WorkspaceTab{
		ID: "tl1403_tab", Scope: "global", WorkspaceRoot: root,
		SessionPath: filepath.Join(dir, "copy.jsonl"), Ready: true,
		disabledMCP: map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	otherPath := filepath.Join(dir, "newer-fork.jsonl")
	stub := control.New(control.Options{SessionDir: dir, SessionPath: otherPath, Sink: event.Discard})
	tab.Ctrl = stub
	app.tabs[tab.ID] = tab

	if err := app.handleTabSessionPromoted(tab)(control.SessionPromotionInfo{CopyPath: tab.SessionPath, MainPath: mainPath}); err != nil {
		t.Fatalf("handleTabSessionPromoted: %v", err)
	}
	if tab.SessionPath != filepath.Join(dir, "copy.jsonl") {
		t.Fatalf("tab session path changed to %q behind a moved-on controller; want untouched", tab.SessionPath)
	}
}

// 验收①Go 侧（连续 3 轮冲突）：每轮真实分歧后断言「surface 身份 == 写入路径」
// 零失联——fork 轮 surface 必须跟挂新分叉且 generation 严格前进；刹车窗内的
// adopt 轮路径本就不变，同样必须一致。第 1 轮必 fork（刹车窗新鲜+配额 0），
// 2/3 轮落在 2 分钟刹车窗内按 tl1352 设计转 adopt（现场 13:55/13:59/14:10 的
// 连续 fork 形态由跨窗时距保证，测试不伪造时间）。FE 侧「3 轮零 stale
// reconcile」由 tl1403-watchdog-move-exemption 场景 3 钉。
func Test1403ThreeConflictRoundsKeepSurfaceOnWriterPath(t *testing.T) {
	t.Setenv(agent.SessionLogSchemaEnv, "v1")
	isolateDesktopUserDirs(t)

	root := globalTabWorkspaceRoot()
	dir := desktopSessionDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	livePath := filepath.Join(dir, "rounds.jsonl")
	topic := "topic_tl1403_rounds"
	if err := setTopicTitle("", topic, "TL1403 Rounds"); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	disk := agent.NewSession("sys")
	disk.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	disk.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	disk.Add(provider.Message{Role: provider.RoleUser, Content: "disk second"})
	if err := disk.Save(livePath); err != nil {
		t.Fatalf("Save disk: %v", err)
	}
	if err := agent.SaveBranchMeta(livePath, agent.BranchMeta{
		Scope: "global", TopicID: topic, TopicTitle: "TL1403 Rounds",
		Preview: "first", Turns: 2, SchemaVersion: agent.BranchMetaCountsVersion,
	}); err != nil {
		t.Fatalf("SaveBranchMeta: %v", err)
	}

	local := agent.NewSession("sys")
	local.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	local.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	local.Add(provider.Message{Role: provider.RoleUser, Content: "local second"})
	exec := agent.New(stubProvider{}, tool.NewRegistry(), local, agent.Options{}, event.Discard)
	app := &App{
		tabs:             map[string]*WorkspaceTab{},
		detachedSessions: map[string]*WorkspaceTab{},
		activeTabID:      "tl1403_rounds",
		ctx:              context.Background(),
	}
	app.runtimeEvents.emit = (&runtimeEventRecorder{}).record
	tab := &WorkspaceTab{
		ID: "tl1403_rounds", Scope: "global", WorkspaceRoot: root,
		TopicID: topic, TopicTitle: "TL1403 Rounds", SessionPath: livePath,
		Ready: true, model: "test-model", disabledMCP: map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	tab.Ctrl = control.New(control.Options{
		Executor:            exec,
		SessionDir:          dir,
		SessionPath:         livePath,
		Label:               "test",
		Sink:                tab.sink,
		SessionRecoveryMeta: app.tabSessionRecoveryMeta(tab),
		OnSessionRecovered:  app.handleTabSessionRecovered(tab),
	})
	app.tabs[tab.ID] = tab

	moves := 0
	for round := 1; round <= 3; round += 1 {
		before := tab.Ctrl.SessionPath()
		beforeGeneration := tab.SessionGeneration
		// 内存侧追加本轮回合（不落盘），盘侧幽灵写者从盘上基线追加独立回合：
		// 两条线互不为前缀 = 真分歧。
		local.Add(provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("tab round %d", round)})
		ghost, err := agent.LoadSession(before)
		if err != nil {
			t.Fatalf("round %d: load %s: %v", round, before, err)
		}
		ghost.Add(provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("ghost round %d", round)})
		ghost.Add(provider.Message{Role: provider.RoleAssistant, Content: fmt.Sprintf("ghost ack %d", round)})
		if err := ghost.Save(before); err != nil {
			t.Fatalf("round %d: ghost write %s: %v", round, before, err)
		}
		if err := tab.Ctrl.Snapshot(); err != nil {
			t.Fatalf("round %d: Snapshot (conflict expected and handled inside): %v", round, err)
		}
		after := tab.Ctrl.SessionPath()
		// 核心不变量：桌面 surface 身份必须始终等于控制器写入路径（监测面/写入面
		// 零失联），无论本轮走 fork 还是 adopt。
		if sessionRuntimeKey(tab.SessionPath) != sessionRuntimeKey(after) {
			t.Fatalf("round %d: surface %s split from writer %s", round, tab.SessionPath, after)
		}
		if after != before {
			// fork 轮：generation 必须严格前进且 sink 同步，前端才换面。
			moves++
			if tab.SessionGeneration != beforeGeneration+1 {
				t.Fatalf("round %d: generation = %d, want %d after the fork move", round, tab.SessionGeneration, beforeGeneration+1)
			}
			if got := tab.sink.sessionGenerationSnapshot(); got != tab.SessionGeneration {
				t.Fatalf("round %d: sink generation = %d, want %d", round, got, tab.SessionGeneration)
			}
		}
	}
	if moves < 1 {
		t.Fatalf("no fork followed the surface in 3 rounds; expected at least the first round to fork")
	}
}
