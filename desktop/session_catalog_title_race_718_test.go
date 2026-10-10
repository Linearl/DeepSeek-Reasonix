package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
)

// setup718TitledProject creates one project topic whose registry title and
// branch-meta title agree, indexes it into a live in-memory catalog, and syncs
// the registry projection — the steady state before a rename.
func setup718TitledProject(t *testing.T, topicID, title string) (*App, string, string) {
	t.Helper()
	// RenameTopic walks the shared projects registry and topic-state store;
	// isolate per test so leftover roots from other tests cannot interfere.
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "718 titled project"); err != nil {
		t.Fatal(err)
	}
	dir := desktopSessionDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := writeTopicSession(t, dir, topicID+".jsonl", topicID, title, root)
	if err := setTopicTitle(root, topicID, title); err != nil {
		t.Fatal(err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.Projects[projectIndexByRoot(f.Projects, root)].Topics = []string{topicID}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	installSessionCatalogForTest(t, app, dir, "project", root)
	if err := app.syncSessionCatalogMetadata(context.Background(), app.sessionCatalog.Load()); err != nil {
		t.Fatal(err)
	}
	return app, root, path
}

func topicLabel718(page ProjectTopicPage, topicID string) string {
	for _, item := range page.Items {
		if item.TopicID == topicID {
			return item.Label
		}
	}
	return ""
}

// Test718RegistryTitleSurvivesStaleSessionMeta pins the acceptance scenario:
// the registry title was written but the branch-meta rewrite is still in flight
// (or was skipped on a busy meta lock). Until task 718 the next directory
// reconcile re-derived the topic title from the stale session row and the
// sidebar showed the old name; the 30s metadata tick healed it, and the next
// activity flipped it back. The registry title must survive every reconcile.
func Test718RegistryTitleSurvivesStaleSessionMeta(t *testing.T) {
	app, root, path := setup718TitledProject(t, "t1", "旧名")

	// The rename lands in the registry; the branch-meta rewrite has NOT happened
	// yet (in-flight persist holds the per-path meta lock).
	if err := setTopicTitle(root, "t1", "手动新名"); err != nil {
		t.Fatal(err)
	}
	if err := app.syncSessionCatalogMetadata(context.Background(), app.sessionCatalog.Load()); err != nil {
		t.Fatal(err)
	}

	// Session activity reconciles the directory while branch meta is stale.
	if err := agent.SaveBranchMetaPreserveUpdated(path, agent.BranchMeta{
		CreatedAt:     time.Now().Add(-time.Minute),
		UpdatedAt:     time.Now(),
		Scope:         "project",
		WorkspaceRoot: root,
		TopicID:       "t1",
		TopicTitle:    "旧名",
	}); err != nil {
		t.Fatal(err)
	}
	reconcileSessionCatalogForTest(t, app, desktopSessionDir(root), "project", root)

	page, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, SortMode: "updated"})
	if err != nil {
		t.Fatal(err)
	}
	if got := topicLabel718(page, "t1"); got != "手动新名" {
		t.Fatalf("sidebar label after reconcile with stale session meta = %q, want registry title %q", got, "手动新名")
	}
}

// Test718RenameReachesSidebarWithoutWaitingForMetadataTick pins the propagation
// half: RenameTopic must publish the registry title to the catalog immediately
// (emitProjectTreeChangedForSessionDirs now pushes metadata), so the sidebar
// shows the new name without waiting for the 30s periodic metadata tick.
func Test718RenameReachesSidebarWithoutWaitingForMetadataTick(t *testing.T) {
	app, root, _ := setup718TitledProject(t, "t1", "改名前")

	if err := app.RenameTopic("t1", "改名后"); err != nil {
		t.Fatal(err)
	}

	// The metadata sync inside the rename propagation is async — poll briefly.
	deadline := time.Now().Add(5 * time.Second)
	label := ""
	for time.Now().Before(deadline) {
		page, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, SortMode: "updated"})
		if err != nil {
			t.Fatal(err)
		}
		if label = topicLabel718(page, "t1"); label == "改名后" {
			// A subsequent reconcile (simulated session activity) must not flip
			// it back now that the registry row is claimed and derivation is gated.
			reconcileSessionCatalogForTest(t, app, desktopSessionDir(root), "project", root)
			page, err = app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, SortMode: "updated"})
			if err != nil {
				t.Fatal(err)
			}
			if after := topicLabel718(page, "t1"); after != "改名后" {
				t.Fatalf("label reverted after reconcile = %q, want %q", after, "改名后")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("sidebar label never reached %q (last %q)", "改名后", label)
}

// Test718RegistryTitleWinsOverSessionDerivedRow covers the fresh-catalog window:
// a reconcile that runs before any metadata sync derives the session-side title,
// and the first metadata sync must still win over it (and stay won).
func Test718RegistryTitleWinsOverSessionDerivedRow(t *testing.T) {
	app, root, _ := setup718TitledProject(t, "t1", "最终名")

	// Branch meta diverged before the app ever saw it (e.g. rename on another
	// window, or a restored backup): session rows say "别的窗口改的名".
	dir := desktopSessionDir(root)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		path := dir + string(os.PathSeparator) + entry.Name()
		if err := agent.SaveBranchMetaPreserveUpdated(path, agent.BranchMeta{
			CreatedAt:     time.Now().Add(-time.Minute),
			UpdatedAt:     time.Now(),
			Scope:         "project",
			WorkspaceRoot: root,
			TopicID:       "t1",
			TopicTitle:    "别的窗口改的名",
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Reconcile first (derives the diverged title), then sync the registry.
	reconcileSessionCatalogForTest(t, app, dir, "project", root)
	if err := app.syncSessionCatalogMetadata(context.Background(), app.sessionCatalog.Load()); err != nil {
		t.Fatal(err)
	}
	// And another reconcile — the pre-718 flip window.
	reconcileSessionCatalogForTest(t, app, dir, "project", root)

	page, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, SortMode: "updated"})
	if err != nil {
		t.Fatal(err)
	}
	if got := topicLabel718(page, "t1"); got != "最终名" {
		t.Fatalf("label after sync+reconcile = %q, want registry title %q", got, "最终名")
	}
}
