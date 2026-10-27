package sessioncatalog

import (
	"context"
	"testing"
)

// tl1208 三读口直查（目录列表侧）：跨 scope 空壳让位先问注册表唯一归属
// 钩子；钩子未命中回落 1197 内容承载查询（行为逐字节不变）。
// 复用 t1197 的目录写盘/对账助手（writeCatalogSession/mustReconcile）。

func openTL1208Catalog(t *testing.T, ctx context.Context, owner func(string) (string, string, bool)) *Catalog {
	t.Helper()
	catalog, err := Open(ctx, Options{Path: "", InMemory: true, DisableRepair: true, TopicOwner: owner})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(ctx) })
	return catalog
}

func listTL1208TopicIDs(t *testing.T, catalog *Catalog, scope, root string) map[string]bool {
	t.Helper()
	page, err := catalog.ListTopics(context.Background(), TopicPageRequest{Scope: scope, WorkspaceRoot: root, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, item := range page.Items {
		got[item.TopicID] = true
	}
	return got
}

// TestListTopicsOwnerHookYieldsForeignShell 钉：注册表归属在 project 时，
// global 侧的空壳行让位——即便全球没有一处内容承载（直查裁决独立于目录
// 内容查询，这正是它与 1197 内容承载规则的分工面）。
func TestListTopicsOwnerHookYieldsForeignShell(t *testing.T) {
	ctx := context.Background()
	catalog := openTL1208Catalog(t, ctx, func(topicID string) (string, string, bool) {
		if topicID == "topic_tl1208_foreign" {
			return "project", "/ws", true
		}
		return "", "", false
	})
	globalDir := writeCatalogSession(t, "", "topic_tl1208_foreign", "空壳", 0)
	orphanDir := writeCatalogSession(t, "", "topic_tl1208_orphan", "孤儿", 0)
	mustReconcile(t, ctx, catalog, globalDir, "global", "")
	mustReconcile(t, ctx, catalog, orphanDir, "global", "")

	got := listTL1208TopicIDs(t, catalog, "global", "")
	if got["topic_tl1208_foreign"] {
		t.Fatalf("global shell must yield to the registry owner: %v", got)
	}
	// 孤儿空壳：钩子查无条目 → 回落内容承载查询（无别处内容）→ 照常显示。
	if !got["topic_tl1208_orphan"] {
		t.Fatalf("ownerless shell with no content anywhere must stay visible: %v", got)
	}
}

// TestListTopicsOwnerHookKeepsOwnedShell 钉：归属在本 scope 时注册表权威
// 保留空壳行（即便别 scope 有内容承载——保留方向永不藏行，风险单边）。
func TestListTopicsOwnerHookKeepsOwnedShell(t *testing.T) {
	ctx := context.Background()
	catalog := openTL1208Catalog(t, ctx, func(topicID string) (string, string, bool) {
		if topicID == "topic_tl1208_owned" {
			return "global", "", true
		}
		return "", "", false
	})
	globalDir := writeCatalogSession(t, "", "topic_tl1208_owned", "壳在本scope", 0)
	projectDir := writeCatalogSession(t, "/ws", "topic_tl1208_owned", "正主在别处", 7)
	mustReconcile(t, ctx, catalog, globalDir, "global", "")
	mustReconcile(t, ctx, catalog, projectDir, "project", "/ws")

	got := listTL1208TopicIDs(t, catalog, "global", "")
	if !got["topic_tl1208_owned"] {
		t.Fatalf("owner-scoped shell must be kept by the registry verdict: %v", got)
	}
}

// TestListTopicsNilHookKeepsT1197Behavior 钉：serve/headless 不注入钩子时
// 走 1197 内容承载查询——有内容正主在别处时 global 空壳仍让位（回归钉）。
func TestListTopicsNilHookKeepsT1197Behavior(t *testing.T) {
	ctx := context.Background()
	catalog, err := Open(ctx, Options{Path: "", InMemory: true, DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(ctx) })
	globalDir := writeCatalogSession(t, "", "topic_tl1208_fallback", "空壳", 0)
	projectDir := writeCatalogSession(t, "/ws", "topic_tl1208_fallback", "正主", 5)
	mustReconcile(t, ctx, catalog, globalDir, "global", "")
	mustReconcile(t, ctx, catalog, projectDir, "project", "/ws")

	got := listTL1208TopicIDs(t, catalog, "global", "")
	if got["topic_tl1208_fallback"] {
		t.Fatalf("nil hook must fall back to the 1197 content-bearing yield: %v", got)
	}
}
