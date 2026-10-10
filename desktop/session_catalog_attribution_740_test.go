package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

// 740：活跃会话双现（项目组 + Global 各一条、都带蓝点）+ 项目栏闪烁。
//
// 根因是归属判定双源：tab 自带的 (scope, workspaceRoot) 只是 tab 对「会话住在
// 哪」的意见，而普通树真正渲染行的位置由 catalog 的归属（session 文件所在目录
// → scope/root/topic）决定。两者分政时，tab 副本被并进 Global 的 runtime-only
// 行、catalog 又在项目组保住正典行——同一会话画两遍；且 runtime 事件与 catalog
// 页刷新交替时 Global 那条时有时无，表现为闪烁。
//
// 修法（718 单一权威范式）：applyCatalogFiledIdentity 把 runtime 快照的归属
// 改写为 catalog 归属。以下三个测试分别钉住：改写生效、catalog 未索引时保留
// tab 意见（tab 对自身存在性权威，同 withLiveTopics 规则）、topicID 重锚到
// catalog 逻辑行（前端按 topicId 合并蓝点的前提）。

func Test740RuntimeAttributionFollowsCatalogFiling(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "镜像提示词"); err != nil {
		t.Fatal(err)
	}
	sessionDir := desktopSessionDir(root)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(sessionDir, "mirror.jsonl")
	if err := os.WriteFile(sessionPath, []byte(`{"role":"user","content":"hi"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := agent.UpdateBranchMeta(sessionPath, false, func(meta *agent.BranchMeta) error {
		meta.TopicID = "topic-740"
		meta.TopicTitle = "镜像提示词汇调研"
		meta.WorkspaceRoot = root
		meta.Scope = "project"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	installSessionCatalogForTest(t, app, sessionDir, "project", root)
	// 事故形态：tab 自己认为它是 global 会话，catalog 却把会话归在项目下。
	app.tabs["tab-1"] = &WorkspaceTab{
		ID: "tab-1", Scope: "global",
		TopicID: "topic-740", TopicTitle: "镜像提示词汇调研", SessionPath: sessionPath,
	}

	// 运行时快照必须改写到 catalog 归属（修复前是 global —— Global 双现源头）。
	snapshot := app.GetProjectTreeRuntimeSnapshot()
	if len(snapshot.Topics) != 1 {
		t.Fatalf("runtime snapshot topics = %d, want 1", len(snapshot.Topics))
	}
	topic := snapshot.Topics[0]
	if topic.Scope != "project" || topic.WorkspaceRoot != root {
		t.Fatalf("runtime topic filed scope=%q root=%q, want the catalog filing project/%s", topic.Scope, topic.WorkspaceRoot, root)
	}

	// Global 页不得再画这条会话（双现复现点）。
	globalPage, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "global", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range globalPage.Items {
		if item.TopicID == "topic-740" {
			t.Fatalf("global page paints the project-filed conversation: %#v — the 740 double row is back", item)
		}
	}

	// 项目页保持唯一正典行。
	projectPage, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	rows := 0
	for _, item := range projectPage.Items {
		if item.TopicID == "topic-740" {
			rows++
		}
	}
	if rows != 1 {
		t.Fatalf("project page rows for topic-740 = %d, want exactly 1", rows)
	}
}

func Test740RuntimeAttributionKeepsTabFilingWhileCatalogLags(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	// catalog 已装但目录为空：全新会话尚未被扫描索引。
	installSessionCatalogForTest(t, app, t.TempDir(), "global", "")
	sessionPath := filepath.Join(desktopSessionDir(globalWorkspaceRoot()), "fresh-740.jsonl")
	app.tabs["tab-1"] = &WorkspaceTab{
		ID: "tab-1", Scope: "global",
		TopicID: "topic-fresh-740", TopicTitle: "Fresh conversation", SessionPath: sessionPath,
	}

	// catalog 不认识这个会话：tab 对自身存在性权威，归属保持 tab 意见，
	// 会话不能因为权威门而消失。
	snapshot := app.GetProjectTreeRuntimeSnapshot()
	if len(snapshot.Topics) != 1 {
		t.Fatalf("runtime snapshot topics = %d, want 1", len(snapshot.Topics))
	}
	if topic := snapshot.Topics[0]; topic.Scope != "global" {
		t.Fatalf("unindexed live topic re-filed to scope=%q, want the tab's own global filing", topic.Scope)
	}
}

func Test740RuntimeAttributionUsesLogicalTopicForReanchoredTab(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := desktopSessionDir(globalWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	save := func(path, topic string) {
		t.Helper()
		session := agent.NewSession("sys")
		session.Add(provider.Message{Role: provider.RoleUser, Content: "question"})
		session.Add(provider.Message{Role: provider.RoleAssistant, Content: "answer"})
		if err := session.Save(path); err != nil {
			t.Fatal(err)
		}
		if err := agent.SaveBranchMetaPreserveUpdated(path, agent.BranchMeta{
			ID: agent.BranchID(path), Scope: "global", TopicID: topic,
			TopicTitle: "镜像提示词专题调研",
		}); err != nil {
			t.Fatal(err)
		}
	}
	rootPath := filepath.Join(dir, "root.jsonl")
	copyPath := filepath.Join(dir, "copy.jsonl")
	save(rootPath, "conversation")
	save(copyPath, "legacy-copy-topic")
	if err := agent.SaveBranchMetaPreserveUpdated(copyPath, agent.BranchMeta{
		ID: "copy", Scope: "global", TopicID: "legacy-copy-topic",
		Recovered: true, ParentID: "root", RecoveryDepth: 1,
	}); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	installSessionCatalogForTest(t, app, dir, "global", "")
	// 恢复链重锚后，tab 仍带旧 topicID：权威门必须连 topicID 一起改写到逻辑行，
	// 前端按 topicId 合并时蓝点才能落在正典行上而不是再开一条 runtime-only 行。
	app.tabs["tab-1"] = &WorkspaceTab{
		ID: "tab-1", Scope: "global",
		TopicID: "legacy-copy-topic", TopicTitle: "镜像提示词专题调研", SessionPath: copyPath,
	}

	snapshot := app.GetProjectTreeRuntimeSnapshot()
	if len(snapshot.Topics) != 1 {
		t.Fatalf("runtime snapshot topics = %d, want 1 logical row", len(snapshot.Topics))
	}
	if got := snapshot.Topics[0].Node.TopicID; got != "conversation" {
		t.Fatalf("runtime topic id = %q, want the catalog logical topic %q", got, "conversation")
	}
}

// Test740AttributionGateWiredAtBothCollectionPoints is the source-level guard
// (task 352 dynamic-scan paradigm): the authority gate only works when EVERY
// runtime-snapshot collection point runs it. catalogRuntimeSnapshots covers
// the overlays and runtime-only page merges; GetRuntimeStateSnapshot builds
// its own bindings slice for the runtime topic grouping. One wiring lost and
// the 740 double row returns through that path alone.
func Test740AttributionGateWiredAtBothCollectionPoints(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) < 10 {
		t.Fatalf("scan floor: glob found %d go files, the scan pattern is broken", len(files))
	}
	const gateMarker = "func (a *App) applyCatalogFiledIdentity"
	type caller struct{ anchor string }
	wantCallers := []caller{
		{"func (a *App) catalogRuntimeSnapshots()"},
		{"func (a *App) GetRuntimeStateSnapshot()"},
	}
	found := map[string]bool{}
	gateFound := false
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue // production surface only; this guard self-matches otherwise
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := string(src)
		if start := strings.Index(text, gateMarker); start >= 0 {
			gateFound = true
			rest := text[start:]
			if end := strings.Index(rest, "\nfunc "); end > 0 {
				rest = rest[:end]
			}
			// Floor: the extracted body is the real gate, not a stub renamer —
			// it queries the catalog per session path and rewrites the filing.
			for _, anchor := range []string{"catalog.GetSession", "LogicalTopicID", "normalizeDesktopTopicScope"} {
				if !strings.Contains(rest, anchor) {
					t.Fatalf("gate body floor failed: %s missing %q — the scan pattern went stale, fix the guard", file, anchor)
				}
			}
			continue
		}
		for _, want := range wantCallers {
			if start := strings.Index(text, want.anchor); start >= 0 {
				rest := text[start:]
				if end := strings.Index(rest, "\nfunc "); end > 0 {
					rest = rest[:end]
				}
				if strings.Contains(rest, "applyCatalogFiledIdentity(") {
					found[want.anchor] = true
				}
			}
		}
	}
	if !gateFound {
		t.Fatalf("authority gate %q not found in any go file — the scan pattern is broken", gateMarker)
	}
	for _, want := range wantCallers {
		if !found[want.anchor] {
			t.Fatalf("740 regression: %s no longer runs applyCatalogFiledIdentity — the tab's scope opinion leaks into that collection path and the double row returns", want.anchor)
		}
	}
}
