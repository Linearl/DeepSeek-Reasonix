package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

// setup698OrderedProject writes topics a/b/c into a project workspace and
// persists the manual order c,b,a exactly as a sidebar drag would (ReorderTopics).
// The catalog is synced BEFORE the reorder, so it deliberately keeps the stale
// a,b,c order — the same window a real drag leaves behind until the async
// metadata sync lands.
func setup698OrderedProject(t *testing.T) (*App, string) {
	t.Helper()
	root := t.TempDir()
	if err := addProject(root, "698 ordered project"); err != nil {
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
	// The drag itself: json gets c,b,a + ManualTopicOrder; the catalog is NOT
	// re-synced afterwards, so its sort_order still says a,b,c.
	if err := app.ReorderTopics("project", root, []string{"c", "b", "a"}); err != nil {
		t.Fatal(err)
	}
	return app, root
}

func topicIDOrder698(page ProjectTopicPage) []string {
	out := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		out = append(out, item.TopicID)
	}
	return out
}

func assert698Order(t *testing.T, got []string, want ...string) {
	t.Helper()
	filtered := make([]string, 0, len(got))
	for _, id := range got {
		// Scenario-injected extras (e.g. a fresh topic) may or may not be on
		// the page; only the relative order of the dragged set is pinned.
		for _, w := range want {
			if id == w {
				filtered = append(filtered, id)
			}
		}
	}
	if strings.Join(filtered, ",") != strings.Join(want, ",") {
		t.Fatalf("manual order = %v (filtered %v), want %v", got, filtered, want)
	}
}

// Test698ManualOrderFallbackWinsInFrontendSortModes pins the 698 fix: the
// frontend never sends sortMode "" or "manual" — it sends "created", "updated",
// or "color". The old mode gate made applyManualOrderFallback dead code in
// production, so inside the sync window every re-pull rendered the stale
// catalog order and a dragged order snapped back on screen.
func Test698ManualOrderFallbackWinsInFrontendSortModes(t *testing.T) {
	for _, sortMode := range []string{"updated", "created", "color"} {
		t.Run(sortMode, func(t *testing.T) {
			app, root := setup698OrderedProject(t)
			page, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, SortMode: sortMode})
			if err != nil {
				t.Fatal(err)
			}
			assert698Order(t, topicIDOrder698(page), "c", "b", "a")
		})
	}
}

// Test698ManualOrderSurvivesRestartNewTopicAndGroupMove pins the acceptance
// scenario: after a group-internal drag the order must survive (1) an app
// restart, (2) a new session landing through the prepend write path, and
// (3) a cross-group move, in every case without the stale catalog order
// leaking through.
func Test698ManualOrderSurvivesRestartNewTopicAndGroupMove(t *testing.T) {
	app, root := setup698OrderedProject(t)

	// (1) Restart: a fresh App re-reads desktop-projects.json plus the
	// organization sidecar from disk. With no catalog attached this exercises
	// the metadata page path — the state during the startup window before the
	// catalog opens.
	restarted := NewApp()
	page, err := restarted.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, SortMode: "updated"})
	if err != nil {
		t.Fatal(err)
	}
	assert698Order(t, topicIDOrder698(page), "c", "b", "a")

	// (2) New session: indexNewTopicForSidebar prepends the fresh topic to the
	// projects-file index. The existing members must keep their relative
	// manual order and the catalog must not reshuffle them.
	dir := desktopSessionDir(root)
	writeTopicSession(t, dir, "d.jsonl", "d", "D", root)
	if err := setTopicTitle(root, "d", "D"); err != nil {
		t.Fatal(err)
	}
	if err := prependTopicInProjectsFile(root, "d", true); err != nil {
		t.Fatal(err)
	}
	page, err = app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, SortMode: "updated"})
	if err != nil {
		t.Fatal(err)
	}
	assert698Order(t, topicIDOrder698(page), "c", "b", "a")
	f := loadProjectsFile()
	project := f.Projects[projectIndexByRoot(f.Projects, root)]
	if !project.ManualTopicOrder {
		t.Fatal("prepend write path dropped ManualTopicOrder")
	}

	// (3) Cross-group move: filing b into a session group touches Groups but
	// must never reorder Topics.
	if _, _, _, err := app.MoveTopicToGroup("project", root, "b", "", "研发组"); err != nil {
		t.Fatal(err)
	}
	page, err = app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, SortMode: "updated"})
	if err != nil {
		t.Fatal(err)
	}
	assert698Order(t, topicIDOrder698(page), "c", "b", "a")

	// The group roster itself survives the same restart (sidecar round-trip).
	restartedGroups, err := restarted.GetProjectGroups("project", root)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, group := range restartedGroups.Groups {
		if group.Title == "研发组" {
			found = true
			if len(group.TopicIDs) != 1 || group.TopicIDs[0] != "b" {
				t.Fatalf("group roster after restart = %#v, want [b]", group.TopicIDs)
			}
		}
	}
	if !found {
		t.Fatal("group vanished after restart")
	}
}

// Test698ManualOrderFrontendDragPayloadStaysComplete guards the other half of
// the write path: the drag payload the frontend computes from the rendered
// children must keep every persisted topic. A partially loaded tree must not
// silently reorder topics the client never saw — completeTopicOrder appends
// them, it must not drop or reposition them relative to each other.
func Test698ManualOrderFrontendDragPayloadStaysComplete(t *testing.T) {
	app, root := setup698OrderedProject(t)
	// The client only saw b and c (a was off-page) and dragged c before b.
	if err := app.ReorderTopics("project", root, []string{"c", "b"}); err != nil {
		t.Fatal(err)
	}
	f := loadProjectsFile()
	project := f.Projects[projectIndexByRoot(f.Projects, root)]
	if strings.Join(project.Topics, ",") != "c,b,a" {
		t.Fatalf("topics after partial drag = %v, want [c b a] with a appended", project.Topics)
	}
	if !project.ManualTopicOrder {
		t.Fatal("partial drag lost ManualTopicOrder")
	}
}
