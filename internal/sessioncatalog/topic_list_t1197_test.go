package sessioncatalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
)

// tl1197（1074 家族第 3 形态）: 一个 scope 的 topic 行全部由无内容空壳撑着、
// 而同 topic 身份在其它 scope 拥有内容承载者时，本 scope 不出行——
// 「剧情核对」的 global 空壳不再把项目对话画进 Global 未分组区。
func TestListTopicsYieldsCrossScopeShellToContentBearingOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	topicID := "topic_20261025-152234_t1197"

	globalDir := writeCatalogSession(t, "", topicID, "剧情核对", 0)
	projectDir := writeCatalogSession(t, "/ws", topicID, "剧情核对", 7)

	catalog := openT1197Catalog(t, ctx)
	mustReconcile(t, ctx, catalog, globalDir, "global", "")
	mustReconcile(t, ctx, catalog, projectDir, "project", "/ws")

	globalPage, err := catalog.ListTopics(ctx, TopicPageRequest{Scope: "global", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range globalPage.Items {
		if item.TopicID == topicID {
			t.Fatalf("global page still lists the shell-backed topic: %#v", item)
		}
	}

	projectPage, err := catalog.ListTopics(ctx, TopicPageRequest{Scope: "project", WorkspaceRoot: "/ws", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range projectPage.Items {
		if item.TopicID != topicID {
			continue
		}
		found = true
		if len(item.Sessions) != 1 || item.Sessions[0].Turns != 7 {
			t.Fatalf("project sessions = %#v, want the turns-7 owner", item.Sessions)
		}
	}
	if !found {
		t.Fatalf("project page lost the owner topic: %#v", projectPage.Items)
	}
}

// 全世界的会话都无内容（刚新建、还没说话）时照常显示——让位规则只作用于
// 「他 scope 有正主」的场景，不藏新话题。
func TestListTopicsKeepsContentFreeTopicWithoutCrossScopeOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	topicID := "topic_t1197_brand_new"

	globalDir := writeCatalogSession(t, "", topicID, "新话题", 0)
	catalog := openT1197Catalog(t, ctx)
	mustReconcile(t, ctx, catalog, globalDir, "global", "")

	page, err := catalog.ListTopics(ctx, TopicPageRequest{Scope: "global", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range page.Items {
		if item.TopicID == topicID {
			found = true
		}
	}
	if !found {
		t.Fatalf("content-free topic without any owner must stay visible: %#v", page.Items)
	}
}

// 用户手动置顶的空壳行豁免让位——对齐 hasOrdinary 的 pinned 豁免先例。
func TestListTopicsPinnedShellStaysVisible(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	topicID := "topic_t1197_pinned_shell"

	globalDir := writeCatalogSession(t, "", topicID, "置顶空壳", 0)
	projectDir := writeCatalogSession(t, "/ws", topicID, "置顶空壳", 3)

	catalog := openT1197Catalog(t, ctx)
	mustReconcile(t, ctx, catalog, globalDir, "global", "")
	mustReconcile(t, ctx, catalog, projectDir, "project", "/ws")

	if _, err := catalog.db.ExecContext(ctx,
		`UPDATE catalog_topics SET pinned=1 WHERE scope='global' AND topic_id=?`, topicID); err != nil {
		t.Fatal(err)
	}

	page, err := catalog.ListTopics(ctx, TopicPageRequest{Scope: "global", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range page.Items {
		if item.TopicID == topicID {
			found = true
		}
	}
	if !found {
		t.Fatalf("pinned shell row must stay visible: %#v", page.Items)
	}
}

func openT1197Catalog(t *testing.T, ctx context.Context) *Catalog {
	t.Helper()
	catalog, err := Open(ctx, Options{InMemory: true, DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	return catalog
}

func writeCatalogSession(t *testing.T, workspaceRoot, topicID, title string, turns int) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	scope := "global"
	if workspaceRoot != "" {
		scope = "project"
	}
	if err := agent.SaveBranchMeta(path, agent.BranchMeta{
		CreatedAt:     time.Now().Add(-time.Minute),
		UpdatedAt:     time.Now(),
		Scope:         scope,
		WorkspaceRoot: workspaceRoot,
		TopicID:       topicID,
		TopicTitle:    title,
		Turns:         turns,
	}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func mustReconcile(t *testing.T, ctx context.Context, catalog *Catalog, dir, scope, root string) {
	t.Helper()
	if err := catalog.ReconcileDirectory(ctx, DirectoryTarget{
		Path: dir, Scope: scope, WorkspaceRoot: root,
	}); err != nil {
		t.Fatal(err)
	}
}
