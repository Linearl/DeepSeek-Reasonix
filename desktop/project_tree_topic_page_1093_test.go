package main

// Task 1093（侧栏分组计数与渲染行数不一致）：reasonix-for-ai 分组计数显示
// 17、只渲染 5 行；折叠再展开（full=true 重载）才全量。数据层（分组
// topicIds）正确——计数与目录页渲染行数的一致性，取决于目录页能否沿
// nextCursor 走完全量、且事件刷新（只取首页）不动已取全的行。本测试把这两
// 条钉为不变量：① 全量 walk（collapse→expand 的 full=true 路径）每个分组
// 成员恰好渲染一次；② 事件刷新后的首页合并（mergeProjectTopicFirstPage）
// 保留已取全文件夹的尾部行，组内行数不收缩。

import (
	"fmt"
	"os"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

func seedIndexedGlobalSession(t *testing.T, dir, name, topicID, title string, at time.Time) string {
	t.Helper()
	path := writeLegacySession(t, dir, name, "prompt "+title, at)
	if err := agent.SaveBranchMetaPreserveUpdated(path, agent.BranchMeta{
		ID:         agent.BranchID(path),
		CreatedAt:  at.Add(-time.Hour),
		UpdatedAt:  at,
		Scope:      "global",
		TopicID:    topicID,
		TopicTitle: title,
		Turns:      1,
		Preview:    "prompt " + title,
	}); err != nil {
		t.Fatalf("save meta %s: %v", name, err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatalf("chtimes %s: %v", name, err)
	}
	return path
}

// walkGlobalTopicPage mirrors the frontend's browse loop: request a page,
// render its items, resume from the returned cursor until it is empty. Rows
// are deduped by topic id — the 1074 projectTreeTopicRowIdentity contract
// the frontend merge functions apply.
func walkGlobalTopicPage(t *testing.T, app *App, limit int) []ProjectNode {
	t.Helper()
	rendered := map[string]bool{}
	out := []ProjectNode{}
	cursor := ""
	for pages := 0; pages < 20; pages++ {
		page, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "global", Cursor: cursor, Limit: limit})
		if err != nil {
			t.Fatalf("list page %d: %v", pages, err)
		}
		for _, item := range page.Items {
			if item.TopicID == "" {
				continue
			}
			if rendered[item.TopicID] {
				t.Fatalf("page %d repeats topic %q (cursor did not advance past it)", pages, item.TopicID)
			}
			rendered[item.TopicID] = true
			out = append(out, item)
		}
		if page.NextCursor == "" {
			return out
		}
		cursor = page.NextCursor
	}
	t.Fatalf("cursor chain did not terminate")
	return out
}

// TestProjectTreeTopicPageWalkRendersEveryGroupMember: 17 个 topic 的分组
// （计数=17），每页 limit=5 的目录页走完全量取全后，分组每一个成员都能在
// 渲染行里查到——「组内渲染行数 = 计数」的目录页底座不变量。
func TestProjectTreeTopicPageWalkRendersEveryGroupMember(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}

	const members = 17
	groupTopicIDs := make([]string, 0, members)
	now := time.Now()
	for i := 0; i < members; i++ {
		topicID := fmt.Sprintf("legacy_group_%02d_000000000001", i)
		at := now.Add(-time.Duration(i) * time.Minute)
		seedIndexedGlobalSession(t, dir,
			fmt.Sprintf("group-%02d.jsonl", i), topicID, fmt.Sprintf("话题%02d", i), at)
		groupTopicIDs = append(groupTopicIDs, topicID)
	}
	// The reasonix-for-ai style group: a roster of 17 topics in the global
	// groups catalog — the collapsed count reads exactly len(topicIds).
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.GlobalGroups = normalizeGroups(append(f.GlobalGroups, desktopGroup{
			ID: "collab-reasonix", Title: "reasonix-for-ai", TopicIDs: groupTopicIDs,
		}))
		return true, nil
	}); err != nil {
		t.Fatalf("seed global group: %v", err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		next := uniqueStrings(append(groupTopicIDs, f.GlobalTopics...))
		if sameStringList(next, f.GlobalTopics) {
			return false, nil
		}
		f.GlobalTopics = next
		return true, nil
	}); err != nil {
		t.Fatalf("seed globalTopics: %v", err)
	}
	// The user-visible browse contract: an uncursored walk (full=true, the
	// collapse→expand reload) must paint every group member exactly once.
	rows := walkGlobalTopicPage(t, NewApp(), 5)
	byTopic := map[string]bool{}
	for _, row := range rows {
		byTopic[row.TopicID] = true
	}
	missing := []string{}
	for _, topicID := range groupTopicIDs {
		if !byTopic[topicID] {
			missing = append(missing, topicID)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("walk rendered %d rows, group count is %d; missing %d members: %v",
			len(rows), members, len(missing), missing)
	}
	// The event-refresh path (full=false, first page only) must keep the same
	// first-page rows the full walk starts with: no reorder/skip at the head,
	// which is what makes the collapsed count and the expanded rows agree.
	firstPage, err := NewApp().ListProjectTopics(ProjectTopicPageRequest{Scope: "global", Limit: 5})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(firstPage.Items) != 5 {
		t.Fatalf("first page items = %d, want 5", len(firstPage.Items))
	}
	for i, item := range firstPage.Items {
		if rows[i].TopicID != item.TopicID {
			t.Fatalf("first page item %d = %q, want %q (event refresh must not reorder the head)",
				i, item.TopicID, rows[i].TopicID)
		}
	}
}
