package main

// tl1363 回归钉：手动「重载会话列表」四步对账的机制测试。
//
// 覆盖任务书验收点：
//   ① 影子条目扫除——零会话绑定的纯注册残留（CreatedAtMS==0 的 tl-1144 空壳
//      形态）reload 后从注册表与侧栏投影消失（验收「残留清除」）；
//   ② legacy_* + topic_* 同题双注册归并（tl1383 同族结论：legacy 有绑定、
//      topic 零绑定——零绑定侧被扫、有绑定侧原样，双条目并一条）；
//   ③ 保护面——活/挂起 tab 引用的 topic 与置顶条目绝不自动移出；
//   ④ scope 隔离——global reload 不动 project 条目，反之亦然；
//   ⑤ 侧栏唯一认的信号——reason="metadata" 的 project-tree:changed-v2 事件
//      （20261009 清理战役判据）。

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seedReloadScene registers one project with:
//   - topic-live:        a real session file on disk, indexed in the registry;
//   - topic-shadow:      a zero-binding registration (CreatedAtMS==0, no file) —
//                        the trash-residue / tl1383 shadow form.
func seedReloadScene(t *testing.T) (app *App, root, dir, livePath string) {
	t.Helper()
	root = t.TempDir()
	if err := addProject(root, "Reload Project"); err != nil {
		t.Fatalf("add project: %v", err)
	}
	dir = desktopSessionDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	livePath = writeTopicSessionWithPrompt(t, dir, "live.jsonl", "topic-live", "Live", root, "reload scene live", time.Now())
	if err := ensureTopicIndexedWithCreatedAt("project", root, "topic-live", "Live", topicTitleSourceManual, time.Now().UnixMilli()); err != nil {
		t.Fatalf("seed live topic: %v", err)
	}
	// 零绑定形态必须是 #9614/tl-1144 的真身：CreatedAtMS==0（注册了但从未落过
	// 真实历史）。CreatedAtMS>0 在 heartbeatTopicIsShell 判据下是「有真实历史」，
	// 不是影子。
	if err := ensureTopicIndexedWithCreatedAt("project", root, "topic-shadow", "Shadow", topicTitleSourceAuto, 0); err != nil {
		t.Fatalf("seed shadow topic: %v", err)
	}
	app = NewApp()
	app.ctx = context.Background()
	return app, root, dir, livePath
}

func projectTopicsForRoot(t *testing.T, root string) []string {
	t.Helper()
	f := loadProjectsFile()
	for i := range f.Projects {
		if sameProjectRoot(f.Projects[i].Root, root) {
			return f.Projects[i].Topics
		}
	}
	t.Fatalf("project %q vanished from the registry", root)
	return nil
}

// ① 影子扫除 + 活条目不受影响 + ③ 保护面（tab 引用 / 置顶）。
func TestReloadProjectTreeSessionsSweepsShadowAndProtectsBound(t *testing.T) {
	app, root, dir, livePath := seedReloadScene(t)

	// A pinned zero-binding entry: user-curated state, never auto-swept.
	if err := ensureTopicIndexedWithCreatedAt("project", root, "topic-pinned-shadow", "Pinned Shadow", topicTitleSourceAuto, 0); err != nil {
		t.Fatalf("seed pinned shadow: %v", err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		for i := range f.Projects {
			if sameProjectRoot(f.Projects[i].Root, root) {
				f.Projects[i].Topics = append(f.Projects[i].Topics, "topic-pinned-shadow")
				f.Projects[i].PinnedTopics = append(f.Projects[i].PinnedTopics, "topic-pinned-shadow")
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		t.Fatalf("pin shadow: %v", err)
	}

	// A tab holds another zero-binding topic — a session possibly being created
	// right now; the tab reference must protect it from the sweep.
	app.mu.Lock()
	app.tabs["creating"] = &WorkspaceTab{ID: "creating", Scope: "project", WorkspaceRoot: root,
		TopicID: "topic-tab-shadow", TopicTitle: "Creating"}
	app.mu.Unlock()
	if err := ensureTopicIndexedWithCreatedAt("project", root, "topic-tab-shadow", "Creating", topicTitleSourceAuto, 0); err != nil {
		t.Fatalf("seed tab shadow: %v", err)
	}

	result, err := app.ReloadProjectTreeSessions("project", root)
	if err != nil {
		t.Fatalf("ReloadProjectTreeSessions: %v", err)
	}

	if len(result.RemovedTopicIDs) != 1 || result.RemovedTopicIDs[0] != "topic-shadow" {
		t.Fatalf("removed = %v, want exactly [topic-shadow]", result.RemovedTopicIDs)
	}
	if result.SkippedPinned != 1 {
		t.Fatalf("skippedPinned = %d, want the pinned entry", result.SkippedPinned)
	}
	if result.SkippedReferenced != 1 {
		t.Fatalf("skippedReferenced = %d, want the tab-held entry", result.SkippedReferenced)
	}

	topics := projectTopicsForRoot(t, root)
	has := func(id string) bool {
		for _, topic := range topics {
			if topic == id {
				return true
			}
		}
		return false
	}
	if has("topic-shadow") {
		t.Fatal("shadow entry survived the reload — the sidebar would keep a dead row")
	}
	if !containsDesktopString(loadProjectsFile().DeletedTopics, "topic-shadow") {
		t.Fatal("swept shadow not tombstoned — repair scans could resurrect it")
	}
	if !has("topic-live") {
		t.Fatal("live topic was swept — a topic with a session file must never be touched")
	}
	if !has("topic-pinned-shadow") || !has("topic-tab-shadow") {
		t.Fatalf("protected entries removed (pinned/tab): %v", topics)
	}
	if _, err := os.Stat(livePath); err != nil {
		t.Fatalf("live session file disturbed: %v", err)
	}

	// The sidebar projection no longer carries the shadow row: the metadata
	// projection (catalog-free path) renders exactly the surviving entries.
	page, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, Limit: 200})
	if err != nil {
		t.Fatalf("ListProjectTopics: %v", err)
	}
	rendered := map[string]bool{}
	for _, node := range page.Items {
		rendered[node.TopicID] = true
	}
	if rendered["topic-shadow"] {
		t.Fatal("shadow entry still rendered after the reload")
	}
	if !rendered["topic-live"] {
		t.Fatal("live entry missing from the projection after the reload")
	}
	_ = dir
}

// ② legacy_* + topic_* 同题双注册归并（tl1383 同族结论直接落地）。
func TestReloadProjectTreeSessionsMergesLegacyDoubleRegistration(t *testing.T) {
	app, root, dir, _ := seedReloadScene(t)

	const sharedTitle = "合并专员-新4"
	// The legacy side: a real session file bound to a legacy-minted topic id —
	// tl1383 实锤「legacy 有活会话绑定」。
	legacyPath := writeTopicSessionWithPrompt(t, dir, "legacy.jsonl", "legacy_20261020_merge", sharedTitle, root, "legacy bound conversation", time.Now())
	if err := ensureTopicIndexedWithCreatedAt("project", root, "legacy_20261020_merge", sharedTitle, topicTitleSourceManual, time.Now().UnixMilli()); err != nil {
		t.Fatalf("seed legacy topic: %v", err)
	}
	// The topic_* side: same title, zero binding — the pure registration residue
	// the legacy migration minted without merging (tl1383 成因).
	if err := ensureTopicIndexedWithCreatedAt("project", root, "topic_merge_dupe", sharedTitle, topicTitleSourceAuto, 0); err != nil {
		t.Fatalf("seed dupe topic: %v", err)
	}

	result, err := app.ReloadProjectTreeSessions("project", root)
	if err != nil {
		t.Fatalf("ReloadProjectTreeSessions: %v", err)
	}
	removed := map[string]bool{}
	for _, id := range result.RemovedTopicIDs {
		removed[id] = true
	}
	if !removed["topic_merge_dupe"] {
		t.Fatalf("zero-binding topic_* residue not swept: %v", result.RemovedTopicIDs)
	}
	if removed["legacy_20261020_merge"] || removed["topic-live"] {
		t.Fatalf("bound entries were swept: %v", result.RemovedTopicIDs)
	}

	// The merged view: exactly ONE row carries the shared title, backed by the
	// legacy session — 同题双条目并一条.
	page, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, Limit: 200})
	if err != nil {
		t.Fatalf("ListProjectTopics: %v", err)
	}
	titles := 0
	for _, node := range page.Items {
		if node.Label == sharedTitle {
			titles++
			if node.TopicID != "legacy_20261020_merge" {
				t.Fatalf("merged row backed by %q, want the bound legacy topic", node.TopicID)
			}
		}
	}
	if titles != 1 {
		t.Fatalf("shared title renders %d rows, want exactly one after the merge (items: %d)", titles, len(page.Items))
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("legacy session file disturbed: %v", err)
	}
}

// ④ scope 隔离：global reload 不动 project 条目；project reload 不动 global 条目。
func TestReloadProjectTreeSessionsScopedToRequestedScope(t *testing.T) {
	app, root, _, _ := seedReloadScene(t)
	if err := ensureTopicIndexedWithCreatedAt("global", "", "topic-global-shadow", "Global Shadow", topicTitleSourceAuto, 0); err != nil {
		t.Fatalf("seed global shadow: %v", err)
	}

	result, err := app.ReloadProjectTreeSessions("global", "")
	if err != nil {
		t.Fatalf("ReloadProjectTreeSessions(global): %v", err)
	}
	if len(result.RemovedTopicIDs) != 1 || result.RemovedTopicIDs[0] != "topic-global-shadow" {
		t.Fatalf("global reload removed %v, want exactly the global shadow", result.RemovedTopicIDs)
	}
	if topics := projectTopicsForRoot(t, root); !containsDesktopString(topics, "topic-shadow") {
		t.Fatal("global reload swept a project entry — scope isolation broken")
	}

	result, err = app.ReloadProjectTreeSessions("project", root)
	if err != nil {
		t.Fatalf("ReloadProjectTreeSessions(project): %v", err)
	}
	if len(result.RemovedTopicIDs) != 1 || result.RemovedTopicIDs[0] != "topic-shadow" {
		t.Fatalf("project reload removed %v, want exactly the project shadow", result.RemovedTopicIDs)
	}
	if containsDesktopString(loadProjectsFile().GlobalTopics, "topic-global-shadow") {
		t.Fatal("project reload disturbed the global scope")
	}
}

// ⑤ 侧栏唯一认的信号：reload 发 reason="metadata" 的 project-tree:changed-v2。
func TestReloadProjectTreeSessionsEmitsMetadataV2(t *testing.T) {
	app, root, _, _ := seedReloadScene(t)
	events := make(chan runtimeEventEnvelope, 64)
	app.runtimeEvents.emit = func(ctx context.Context, name string, payload ...any) {
		select {
		case events <- runtimeEventEnvelope{ctx: ctx, name: name, payload: append([]any(nil), payload...)}:
		default:
		}
	}

	if _, err := app.ReloadProjectTreeSessions("project", root); err != nil {
		t.Fatalf("ReloadProjectTreeSessions: %v", err)
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-events:
			if event.name != "project-tree:changed-v2" || len(event.payload) == 0 {
				continue
			}
			if v2, ok := event.payload[0].(ProjectTreeChangedV2); ok && v2.Reason == "metadata" {
				return
			}
		case <-deadline:
			t.Fatal("no reason=metadata project-tree:changed-v2 event within 3s — the sidebar would not refresh")
		}
	}
}

// 幂等回执：残留已清的二次 reload 零移除、零报错（重复点击安全）。
func TestReloadProjectTreeSessionsIdempotentSecondRun(t *testing.T) {
	app, root, _, _ := seedReloadScene(t)
	if _, err := app.ReloadProjectTreeSessions("project", root); err != nil {
		t.Fatalf("first reload: %v", err)
	}
	result, err := app.ReloadProjectTreeSessions("project", root)
	if err != nil {
		t.Fatalf("second reload: %v", err)
	}
	if len(result.RemovedTopicIDs) != 0 {
		t.Fatalf("second reload removed %v — the sweep must be idempotent", result.RemovedTopicIDs)
	}
	if topics := projectTopicsForRoot(t, root); !containsDesktopString(topics, "topic-live") {
		t.Fatal("repeated reload disturbed the live entry")
	}
	_ = filepath.Separator
}
