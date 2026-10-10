package main

// Tasks 755 + 754 — the workspace label "Global" is a surface name, not a
// conversation name, and a session open must land in the session's own
// workspace.
//
// 755: a heartbeat conversation whose topic-title registry entry went missing
// (the archive-failure family) used to wear the global-scope display fallback
// "Global", which then leaked into the pinned session sidecar
// (saveTabSessionMeta) and the tab-build index repair — permanently renaming
// the conversation "Global" and, with the lost origin stamp, filing it under
// the plain Global group instead of the managed heartbeat group.
//
// 754: opening a project-bound session from a global-scope entry used to build
// a global tab first and then let the binding apply re-file it under the
// project with a "switched tab from global workspace" warning — a false alarm,
// because the switch was the open's own doing.

import (
	"context"
	"os"
	"testing"
	"time"
)

// ── 755: the workspace label never becomes a conversation name ──────────────

func TestTopicTitleForTabWorkspaceLabelOnlyForTopiclessSurface(t *testing.T) {
	isolateDesktopUserDirs(t)
	globalTopic := "topic_755_label_global"
	projectRoot := t.TempDir()
	projectTopic := "topic_755_label_project"
	if err := setTopicTitle("", globalTopic, "Heartbeat: 论文周报"); err != nil {
		t.Fatalf("seed global topic title: %v", err)
	}
	if err := setTopicTitle(projectRoot, projectTopic, "项目会话"); err != nil {
		t.Fatalf("seed project topic title: %v", err)
	}

	if got := topicTitleForTab("global", "", ""); got != globalWorkspaceTabLabel {
		t.Fatalf("topic-less global surface title = %q, want %q", got, globalWorkspaceTabLabel)
	}
	if got := topicTitleForTab("global", "", globalTopic); got != "Heartbeat: 论文周报" {
		t.Fatalf("global topic title = %q, want registry title", got)
	}
	if got := topicTitleForTab("project", projectRoot, projectTopic); got != "项目会话" {
		t.Fatalf("project topic title = %q, want registry title", got)
	}

	// The 755 shape: a topic-bound tab whose registry entry is missing must
	// fall back to the default conversation title, never the workspace label.
	if got := topicTitleForTab("global", "", "topic_755_orphan"); got != defaultTopicTitle {
		t.Fatalf("title-less global topic fallback = %q, want %q", got, defaultTopicTitle)
	}
	if got := topicTitleForTab("project", projectRoot, "topic_755_orphan_project"); got != defaultTopicTitle {
		t.Fatalf("title-less project topic fallback = %q, want %q", got, defaultTopicTitle)
	}
}

func TestSessionBindingDisplayTitleAuthorityOrder(t *testing.T) {
	isolateDesktopUserDirs(t)
	registry := "topic_755_order_registry"
	if err := setTopicTitle("", registry, "Heartbeat: 周报生成"); err != nil {
		t.Fatalf("seed registry title: %v", err)
	}

	// Registry (718 authority) wins over a stale sidecar cache.
	if got := sessionBindingDisplayTitle("global", "", registry, "过期的侧车标题"); got != "Heartbeat: 周报生成" {
		t.Fatalf("registry title vs sidecar = %q, want registry title", got)
	}
	// A sidecar pinned with the workspace label (the 755 corruption shape)
	// reads as absent: the registry fills the gap when it can.
	if got := sessionBindingDisplayTitle("global", "", registry, globalWorkspaceTabLabel); got != "Heartbeat: 周报生成" {
		t.Fatalf("sidecar workspace label = %q, want registry title", got)
	}
	// Registry corrupted with the label, healthy sidecar → the sidecar heals
	// the display (and the index repair then writes the healed title back).
	if got := sessionBindingDisplayTitle("global", "", "topic_755_corrupted", "Heartbeat: 指令核验"); got != "Heartbeat: 指令核验" {
		t.Fatalf("healthy sidecar over corrupted registry = %q, want sidecar title", got)
	}
	// Nothing anywhere → the default conversation title, never the label.
	if got := sessionBindingDisplayTitle("global", "", "topic_755_empty", ""); got != defaultTopicTitle {
		t.Fatalf("empty sources fallback = %q, want %q", got, defaultTopicTitle)
	}
	if got := sessionBindingDisplayTitle("global", "", "topic_755_empty", globalWorkspaceTabLabel); got != defaultTopicTitle {
		t.Fatalf("label-only sources fallback = %q, want %q", got, defaultTopicTitle)
	}
	// A binding without a topic (legacy session) still shows the surface label.
	if got := sessionBindingDisplayTitle("global", "", "", ""); got != globalWorkspaceTabLabel {
		t.Fatalf("topic-less binding title = %q, want %q", got, globalWorkspaceTabLabel)
	}
}

func TestApplySessionBindingToTabHealsWorkspaceLabelTitle(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}

	topicID := "topic_755_apply"
	sessionDir := desktopSessionDir(globalWorkspaceRoot())
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir global sessions: %v", err)
	}
	sessionPath := writeTopicSessionWithPrompt(t, sessionDir, "apply-label.jsonl", topicID, globalWorkspaceTabLabel, "", "heartbeat prompt", time.Now())

	tab := &WorkspaceTab{
		ID:            "tab_755_apply",
		Scope:         "global",
		WorkspaceRoot: globalTabWorkspaceRoot(),
		TopicID:       topicID,
		TopicTitle:    globalWorkspaceTabLabel,
		SessionPath:   sessionPath,
		sink:          &tabEventSink{tabID: "tab_755_apply", app: app},
		disabledMCP:   map[string]ServerView{},
	}
	app.tabs[tab.ID] = tab

	binding, ok := app.resolveSessionBinding(sessionPath)
	if !ok {
		t.Fatal("resolveSessionBinding failed for global session")
	}
	app.applySessionBindingToTab(tab, binding)
	if tab.TopicTitle != defaultTopicTitle {
		t.Fatalf("tab title after apply = %q, want %q (workspace label must not survive a binding apply)", tab.TopicTitle, defaultTopicTitle)
	}

	// With a healthy registry entry the registry title (718 authority) wins.
	if err := setTopicTitle("", topicID, "Heartbeat: 指令核验"); err != nil {
		t.Fatalf("seed registry title: %v", err)
	}
	app.applySessionBindingToTab(tab, binding)
	if tab.TopicTitle != "Heartbeat: 指令核验" {
		t.Fatalf("tab title after registry seed = %q, want registry title", tab.TopicTitle)
	}
}

func TestSessionRepairTopicTitleAndHeartbeatOriginRestore(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	if got := sessionRepairTopicTitle(globalWorkspaceTabLabel); got != defaultTopicTitle {
		t.Fatalf("repair title for workspace label = %q, want %q", got, defaultTopicTitle)
	}
	if got := sessionRepairTopicTitle("Heartbeat: 论文周报"); got != "Heartbeat: 论文周报" {
		t.Fatalf("repair title for a real name = %q, want unchanged", got)
	}

	restored := "topic_755_origin_restore"
	restoreHeartbeatTopicOrigin("global", restored, "Heartbeat: 论文周报（轮换 20261010 接任）")
	if origin, stamped := topicOrigin(t, app, restored); !stamped || origin != heartbeatTopicOrigin {
		t.Fatalf("restored origin = %q stamped=%v, want %q", origin, stamped, heartbeatTopicOrigin)
	}

	// A user-named conversation is never re-filed by inference.
	manual := "topic_755_origin_manual"
	restoreHeartbeatTopicOrigin("global", manual, "论文周报主线")
	if _, stamped := topicOrigin(t, app, manual); stamped {
		t.Fatal("non-heartbeat title must not gain an origin stamp")
	}
	// Project-scope indexes are out of the heartbeat filing's reach.
	projected := "topic_755_origin_project"
	restoreHeartbeatTopicOrigin("project", projected, "Heartbeat: 论文周报")
	if _, stamped := topicOrigin(t, app, projected); stamped {
		t.Fatal("project-scope topic must not gain a heartbeat origin stamp")
	}
}

// ── 754: a project-bound session opens in its project, no switch warning ────

func TestOpenTopicSessionGlobalScopeProjectBoundLandsInProject(t *testing.T) {
	isolateDesktopUserDirs(t)
	projectRoot := t.TempDir()
	if err := addProject(projectRoot, "video_compensation"); err != nil {
		t.Fatalf("add project: %v", err)
	}
	topicID := "topic_754_project_bound"
	sessionDir := desktopSessionDir(projectRoot)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir project sessions: %v", err)
	}
	sessionPath := writeTopicSession(t, sessionDir, "project-bound.jsonl", topicID, "镜头配污-基线验证", projectRoot)

	app := NewApp()
	app.ctx = context.Background()
	t.Cleanup(func() { app.shutdown(context.Background()) })

	meta, err := app.OpenTopicSession("global", "", topicID, sessionPath)
	if err != nil {
		t.Fatalf("OpenTopicSession: %v", err)
	}
	if meta.Scope != "project" {
		t.Fatalf("opened tab scope = %q, want project (the binding is the authority on where the session lives)", meta.Scope)
	}
	if !sameProjectRoot(meta.WorkspaceRoot, projectRoot) {
		t.Fatalf("opened tab root = %q, want %q", meta.WorkspaceRoot, projectRoot)
	}
	tabs := app.ListTabs()
	if len(tabs) != 1 || tabs[0].Scope != "project" {
		t.Fatalf("tabs after open = %+v, want a single project tab (no global detour)", tabs)
	}

	// Reopening through the same global-scope entry must reuse the project tab,
	// not spawn a second surface.
	if _, err := app.OpenTopicSession("global", "", topicID, sessionPath); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if tabs := app.ListTabs(); len(tabs) != 1 {
		t.Fatalf("tabs after reopen = %d, want 1 (project tab reuse)", len(tabs))
	}
}

func TestOpenTopicSessionGlobalSessionKeepsGlobalScope(t *testing.T) {
	isolateDesktopUserDirs(t)
	topicID := "topic_754_global"
	sessionDir := desktopSessionDir(globalWorkspaceRoot())
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir global sessions: %v", err)
	}
	sessionPath := writeTopicSessionWithPrompt(t, sessionDir, "global-bound.jsonl", topicID, "全局会话", "", "hello", time.Now())

	app := NewApp()
	app.ctx = context.Background()
	t.Cleanup(func() { app.shutdown(context.Background()) })

	meta, err := app.OpenTopicSession("global", "", topicID, sessionPath)
	if err != nil {
		t.Fatalf("OpenTopicSession: %v", err)
	}
	if meta.Scope != "global" {
		t.Fatalf("global session opened as %q, want global", meta.Scope)
	}
}
