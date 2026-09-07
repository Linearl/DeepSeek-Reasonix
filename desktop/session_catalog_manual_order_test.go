package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestManualTopicOrderIsAuthoritativeAcrossCatalogPages(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Ordered project"); err != nil {
		t.Fatal(err)
	}
	dir := desktopSessionDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, topicID := range []string{"a", "b", "c"} {
		writeTopicSession(t, dir, topicID+".jsonl", topicID, strings.ToUpper(topicID), root)
		if err := setTopicTitle(root, topicID, strings.ToUpper(topicID)); err != nil {
			t.Fatal(err)
		}
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.Projects[projectIndexByRoot(f.Projects, root)].Topics = []string{"a", "b", "c"}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	if err := app.ReorderTopics("project", root, []string{"c", "b", "a"}); err != nil {
		t.Fatal(err)
	}
	installSessionCatalogForTest(t, app, dir, "project", root)
	if err := app.syncSessionCatalogMetadata(context.Background(), app.sessionCatalog.Load()); err != nil {
		t.Fatal(err)
	}

	first, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.ListProjectTopics(ProjectTopicPageRequest{
		Scope: "project", WorkspaceRoot: root, Limit: 2, Cursor: first.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || len(second.Items) != 1 || first.NextCursor == "" || second.NextCursor != "" {
		t.Fatalf("manual pages = first %#v second %#v", first, second)
	}
	got := []string{first.Items[0].TopicID, first.Items[1].TopicID, second.Items[0].TopicID}
	want := []string{"c", "b", "a"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("manual catalog order = %v, want %v", got, want)
		}
	}
}

// TestManualOrderFallbackReordersStaleCatalogPage pins the 2026-09-01 定判
// regression: inside the async sync window the SQLite catalog still carries
// the pre-reorder order while desktop-projects.json already has the new one,
// and any ListProjectTopics re-pull rendered the stale catalog order (drag
// auto-reset / startup misorder that self-healed). The json manual order must
// win on manual-sorted, uncursored pages.
func TestManualOrderFallbackReordersStaleCatalogPage(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Fallback project"); err != nil {
		t.Fatal(err)
	}
	dir := desktopSessionDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, topicID := range []string{"a", "b", "c"} {
		writeTopicSession(t, dir, topicID+".jsonl", topicID, strings.ToUpper(topicID), root)
		if err := setTopicTitle(root, topicID, strings.ToUpper(topicID)); err != nil {
			t.Fatal(err)
		}
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.Projects[projectIndexByRoot(f.Projects, root)].Topics = []string{"a", "b", "c"}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	installSessionCatalogForTest(t, app, dir, "project", root)
	if err := app.syncSessionCatalogMetadata(context.Background(), app.sessionCatalog.Load()); err != nil {
		t.Fatal(err)
	}

	// Simulate the sync window: json is updated (c,b,a + manual flag) without
	// any further catalog sync, so the catalog keeps the stale a,b,c order.
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		project := &f.Projects[projectIndexByRoot(f.Projects, root)]
		project.Topics = []string{"c", "b", "a"}
		project.ManualTopicOrder = true
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}

	page, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		got = append(got, item.TopicID)
	}
	want := []string{"c", "b", "a"}
	if len(got) != len(want) {
		t.Fatalf("fallback page = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("fallback order = %v, want %v (stale catalog order must not win)", got, want)
		}
	}
}
