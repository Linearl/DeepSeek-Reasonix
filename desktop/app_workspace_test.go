package main

import (
	"os"
	"path/filepath"
	"testing"
)

// seedGlobalTabForRemovalTest installs one Global-scope tab so a removal that
// drains the removed workspace's own tabs does not fall into the fallback-tab
// rebuild path: when the tab set empties, RemoveWorkspace starts a full
// controller build for the fallback Global tab — a boot.Build no unit test
// wants to pay for or depend on.
func seedGlobalTabForRemovalTest(app *App) {
	app.tabs["tab_seed_global"] = &WorkspaceTab{
		ID:          "tab_seed_global",
		Scope:       "global",
		Ready:       true,
		disabledMCP: map[string]ServerView{},
	}
	app.tabOrder = []string{"tab_seed_global"}
	app.activeTabID = "tab_seed_global"
}

// assertProjectAbsent checks every removal surface at once: the projects
// registry, the legacy workspace list, and the active workspace pointer. All
// three are pure JSON stores (tabs.go / workspace.go), so a missing disk path
// must simply drop out of them — that is the tl1502 contract.
func assertProjectAbsent(t *testing.T, root string) {
	t.Helper()
	for _, project := range loadProjectsFile().Projects {
		if sameProjectRoot(project.Root, root) {
			t.Fatalf("project %q still registered after removal: %+v", root, loadProjectsFile().Projects)
		}
	}
	for _, path := range loadWorkspaces() {
		if sameProjectRoot(path, root) {
			t.Fatalf("legacy workspace list still holds %q: %v", root, loadWorkspaces())
		}
	}
	if got := loadWorkspace(); sameProjectRoot(got, root) {
		t.Fatalf("active workspace pointer still names %q: %q", root, got)
	}
}

// tl1502: a project whose folder was deleted outside the app (the sidebar row
// survives in desktop-projects.json, showing 无会话) must remove with one
// confirm — removal is a registry eviction, and a missing disk path must not
// block it with a snapshot error.
func TestRemoveWorkspaceMissingRootSucceeds(t *testing.T) {
	isolateDesktopUserDirs(t)
	missing := filepath.Join(t.TempDir(), "镜头脏污检测")
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("test premise: %q should not exist on disk", missing)
	}
	if err := addProject(missing, "镜头脏污检测"); err != nil {
		t.Fatalf("add project: %v", err)
	}

	app := NewApp()
	installNoopRuntimeEvents(app)
	seedGlobalTabForRemovalTest(app)

	if err := app.RemoveWorkspace(missing); err != nil {
		t.Fatalf("RemoveWorkspace on a missing root must succeed, got: %v", err)
	}
	assertProjectAbsent(t, missing)
}

// tl1502: the same missing-root removal must also go through when the project
// still holds an open tab bound to the deleted folder. removeWorkspaceSessions
// skips the pre-unlink snapshot for a root that no longer exists (there is
// nothing left to save it for) instead of failing the whole removal with
// "save current session before removing workspace".
func TestRemoveWorkspaceMissingRootWithBoundTabSucceeds(t *testing.T) {
	isolateDesktopUserDirs(t)
	missing := filepath.Join(t.TempDir(), "gone-on-disk")
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("test premise: %q should not exist on disk", missing)
	}
	if err := addProject(missing, "Gone"); err != nil {
		t.Fatalf("add project: %v", err)
	}

	app := NewApp()
	installNoopRuntimeEvents(app)
	seedGlobalTabForRemovalTest(app)
	app.tabs["tab_bound"] = &WorkspaceTab{
		ID:            "tab_bound",
		Scope:         "project",
		WorkspaceRoot: normalizeProjectRoot(missing),
		TopicID:       "topic_gone",
		Ready:         true,
		disabledMCP:   map[string]ServerView{},
	}
	app.tabOrder = append(app.tabOrder, "tab_bound")

	if err := app.RemoveWorkspace(missing); err != nil {
		t.Fatalf("RemoveWorkspace with a bound tab on a missing root must succeed, got: %v", err)
	}
	assertProjectAbsent(t, missing)
	if _, ok := app.tabs["tab_bound"]; ok {
		t.Fatal("tab bound to the removed workspace is still registered")
	}
}

// Regression: when the folder still exists the removal keeps its established
// success semantics — the registry entry is evicted, the folder itself is
// left untouched.
func TestRemoveWorkspaceExistingRootRemovesRegistryEntry(t *testing.T) {
	isolateDesktopUserDirs(t)
	existing := t.TempDir()
	if err := addProject(existing, "Existing"); err != nil {
		t.Fatalf("add project: %v", err)
	}

	app := NewApp()
	installNoopRuntimeEvents(app)
	seedGlobalTabForRemovalTest(app)

	if err := app.RemoveWorkspace(existing); err != nil {
		t.Fatalf("RemoveWorkspace on an existing root must succeed, got: %v", err)
	}
	assertProjectAbsent(t, existing)
	if _, err := os.Stat(existing); err != nil {
		t.Fatalf("removal must not delete the folder itself: %v", err)
	}
}
