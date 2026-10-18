package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/sessioncatalog"
)

// 983：多项目多分组的会话冒泡到 Global 顶层（1137 实测）。
//
// 740 的权威门（applyCatalogFiledIdentity）在快照收集时改写 tab 观点，但有两
// 个 miss 通道让 tab 的 global 观点漏进 Global 页：
//   ①（结构性）tab.SessionPath 为空时门直接 continue 保留 tab 观点——而渲染侧
//      runtimeProjectTopicNodes 一直有 ctrl.SessionPath() 兜底，门没有；
//   ②（间歇态）门瞬时 miss（catalog 未索引/查询失败）期间保留 tab 观点，恢复
//      后下一轮才纠正。
//
// 修复 a：门补 ctrl 路径兜底；Global 页 runtime 行渲染前再核一次 catalog
// filing（filing 在项目下的会话不得冒泡进 Global 顶层——「catalog filing
// wins」在渲染侧的延伸）。miss 期间按 740 合同保留 tab 观点（tab 对自身存
// 在性权威，会话不能从侧栏消失）。

// Test983GlobalPageRefilesTabWithEmptySessionPath 钉住结构性 gap：tab 的
// SessionPath 还没落地、但控制器已持有项目 filing 的会话路径时，Global 页
// 不得出现该行，项目页保持唯一正典行。
func Test983GlobalPageRefilesTabWithEmptySessionPath(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "镜头脏污"); err != nil {
		t.Fatal(err)
	}
	sessionDir := desktopSessionDir(root)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(sessionDir, "mirror-983.jsonl")
	if err := os.WriteFile(sessionPath, []byte(`{"role":"user","content":"hi"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := agent.UpdateBranchMeta(sessionPath, false, func(meta *agent.BranchMeta) error {
		meta.TopicID = "topic-983"
		meta.TopicTitle = "镜头脏污调研"
		meta.WorkspaceRoot = root
		meta.Scope = "project"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	installSessionCatalogForTest(t, app, sessionDir, "project", root)
	// 事故形态的确定性通道：tab 观点是 global、SessionPath 尚空，控制器已经
	// 持有会话文件（渲染侧把 ctrl 当路径权威，权威门此前没有对齐）。
	app.tabs["tab-1"] = &WorkspaceTab{
		ID: "tab-1", Scope: "global",
		TopicID: "topic-983", TopicTitle: "镜头脏污调研",
		Ctrl: &idleReleaseCtrl{sessionPath: sessionPath},
	}

	globalPage, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "global", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range globalPage.Items {
		if item.TopicID == "topic-983" {
			t.Fatalf("global page paints the project-filed conversation: %#v — the 983 bubble is back", item)
		}
	}

	projectPage, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	rows := 0
	for _, item := range projectPage.Items {
		if item.TopicID == "topic-983" {
			rows++
		}
	}
	if rows != 1 {
		t.Fatalf("project page rows for topic-983 = %d, want exactly 1", rows)
	}
}

// Test983LiveTopicFiledElsewhereContract 钉住渲染复核本体的判定合同（方向 a
// 的核心谓词）：catalog 把会话归到别 scope/root → 剔除（true）；归在本页 →
// 保留（false）；catalog 未知 → 保留（740 lag 合同，fresh live session 不得
// 因复核消失）；已收录但无 LogicalTopicID（空 filing，任何页都不渲染普通行）
// → 保留，否则会话从侧栏整体蒸发，违反 740「不得隐藏运行中会话」。
func Test983LiveTopicFiledElsewhereContract(t *testing.T) {
	isolateDesktopUserDirs(t)
	catalog, err := sessioncatalog.Open(context.Background(), sessioncatalog.Options{InMemory: true, DisableRepair: true})
	if err != nil {
		t.Fatalf("open in-memory session catalog: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), sessionCatalogReadTimeout)
		defer cancel()
		_ = catalog.Close(ctx)
	})
	ctx := context.Background()
	projectRoot := t.TempDir()
	filedProject := filepath.Join(projectRoot, "sessions", "filed-project.jsonl")
	filedGlobal := filepath.Join(projectRoot, "sessions", "filed-global.jsonl")
	filedNoTopic := filepath.Join(projectRoot, "sessions", "filed-no-topic.jsonl")
	for _, record := range []sessioncatalog.SessionRecord{
		{Path: filedProject, Directory: filepath.Dir(filedProject), Scope: "project", WorkspaceRoot: projectRoot,
			TopicID: "topic-filed", TopicTitle: "Filed", CreatedAt: 100, LastActivityAt: 100, Turns: 1,
			TurnsState: sessioncatalog.TurnsValid, Health: sessioncatalog.HealthOK},
		{Path: filedGlobal, Directory: filepath.Dir(filedGlobal), Scope: "global",
			TopicID: "topic-global", TopicTitle: "Global", CreatedAt: 100, LastActivityAt: 100, Turns: 1,
			TurnsState: sessioncatalog.TurnsValid, Health: sessioncatalog.HealthOK},
		// 空 filing：既无 TopicID 也无 LogicalTopicID —— catalog 收录了文件但
		// 任何页面都不会为它画普通行。
		{Path: filedNoTopic, Directory: filepath.Dir(filedNoTopic), Scope: "project", WorkspaceRoot: projectRoot,
			CreatedAt: 100, LastActivityAt: 100, Turns: 1, TurnsState: sessioncatalog.TurnsValid, Health: sessioncatalog.HealthOK},
	} {
		if err := catalog.UpsertSession(ctx, record); err != nil {
			t.Fatalf("upsert %s: %v", record.Path, err)
		}
	}
	unknown := filepath.Join(projectRoot, "sessions", "not-indexed-yet.jsonl")

	cases := []struct {
		name     string
		paths    []string
		reqScope string
		reqRoot  string
		want     bool
	}{
		{"project-filed row dropped from global page", []string{filedProject}, "global", "", true},
		{"project-filed row kept on its own project page", []string{filedProject}, "project", projectRoot, false},
		{"project-filed row dropped from a different project page", []string{filedProject}, "project", t.TempDir(), true},
		{"global-filed row kept on global page", []string{filedGlobal}, "global", "", false},
		{"global-filed row dropped from project page", []string{filedGlobal}, "project", projectRoot, true},
		{"catalog-unknown session keeps the row (740 lag contract)", []string{unknown}, "global", "", false},
		{"empty-filing record keeps the row (never hide a live conversation)", []string{filedNoTopic}, "global", "", false},
		{"mixed: one on-page filing keeps the row", []string{filedProject, filedGlobal}, "global", "", false},
		{"no paths keeps the row", nil, "global", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := liveTopicFiledElsewhere(ctx, catalog, tc.paths, tc.reqScope, tc.reqRoot); got != tc.want {
				t.Fatalf("liveTopicFiledElsewhere(%v, %q, %q) = %v, want %v", tc.paths, tc.reqScope, tc.reqRoot, got, tc.want)
			}
		})
	}
	t.Run("nil catalog keeps the row", func(t *testing.T) {
		if liveTopicFiledElsewhere(ctx, nil, []string{filedProject}, "global", "") {
			t.Fatal("nil catalog dropped the row, want keep")
		}
	})
}

// Test983GlobalPageDropsRefiledRowAfterCatalogRecovers 钉住间歇态全程：
// catalog 尚未索引时按 740 合同保留 tab 观点（Global 页可见，会话不得从侧栏
// 消失）；catalog 收录 filing 后，行必须离开 Global 顶层、回到项目页唯一正典
// 行——「冒泡出现、恢复后消失」的往返都可复现且方向正确。
func Test983GlobalPageDropsRefiledRowAfterCatalogRecovers(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "专利调研"); err != nil {
		t.Fatal(err)
	}
	sessionDir := desktopSessionDir(root)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(sessionDir, "patent-983.jsonl")
	if err := os.WriteFile(sessionPath, []byte(`{"role":"user","content":"question"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := agent.UpdateBranchMeta(sessionPath, false, func(meta *agent.BranchMeta) error {
		meta.TopicID = "topic-patent-983"
		meta.TopicTitle = "专利调研"
		meta.WorkspaceRoot = root
		meta.Scope = "project"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	// miss 窗口：catalog 已装但目录为空（尚未索引该项目会话）。
	installSessionCatalogForTest(t, app, t.TempDir(), "global", "")
	app.tabs["tab-1"] = &WorkspaceTab{
		ID: "tab-1", Scope: "global",
		TopicID: "topic-patent-983", TopicTitle: "专利调研", SessionPath: sessionPath,
	}

	// miss 期间：740 合同——tab 对自身存在性权威，Global 页保留该行。
	globalPage, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "global", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range globalPage.Items {
		if item.TopicID == "topic-patent-983" {
			found = true
		}
	}
	if !found {
		t.Fatal("catalog miss window hid the live conversation — the 740 tab-authority contract is broken")
	}

	// 恢复：catalog 收录项目 filing。
	reconcileSessionCatalogForTest(t, app, sessionDir, "project", root)

	globalPage, err = app.ListProjectTopics(ProjectTopicPageRequest{Scope: "global", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range globalPage.Items {
		if item.TopicID == "topic-patent-983" {
			t.Fatalf("global page kept the refiled row after catalog recovery: %#v — the 983 bubble is back", item)
		}
	}

	projectPage, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	rows := 0
	for _, item := range projectPage.Items {
		if item.TopicID == "topic-patent-983" {
			rows++
		}
	}
	if rows != 1 {
		t.Fatalf("project page rows for topic-patent-983 = %d, want exactly 1", rows)
	}
}
