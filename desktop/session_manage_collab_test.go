package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"reasonix/internal/agent"
)

// Task 170: the host half of the collaboration "改" tools. These tests pin the
// two things the agent cannot verify for itself - that a rename reaches every
// listing surface, and that a move leaves the sidebar with something to reload.

// rename_session must land the new title in the surfaces the sidebar, search and
// the contact directory read: the topic-title store (sidebar + catalog-only
// topics) and the branch-meta sidecar (directory + search).
func TestRenameCollabSessionUpdatesEveryListing(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Project"); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	meta, err := app.CreateTopic("project", root, "Old name")
	if err != nil {
		t.Fatal(err)
	}
	dir := desktopSessionDir(root)
	sessionPath, err := createEmptySessionFile(dir, "collab")
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.UpdateBranchMeta(sessionPath, false, func(m *agent.BranchMeta) error {
		m.TopicID = meta.ID
		m.TopicTitle = meta.Title
		m.Scope = "project"
		m.WorkspaceRoot = root
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	result, err := app.renameCollabSession(meta.ID, sessionPath, "New name")
	if err != nil {
		t.Fatalf("renameCollabSession: %v", err)
	}
	if !result.ListingSynced || result.Title != "New name" {
		t.Fatalf("result = %#v", result)
	}
	if result.Previous != "Old name" {
		t.Fatalf("previous title = %q, want Old name", result.Previous)
	}
	if titles := loadTopicTitles(root); titles[meta.ID] != "New name" {
		t.Fatalf("topic-title store = %q, want New name", titles[meta.ID])
	}
	stored, ok, err := agent.LoadBranchMeta(sessionPath)
	if err != nil || !ok {
		t.Fatalf("branch meta: ok=%v err=%v", ok, err)
	}
	if stored.TopicTitle != "New name" {
		t.Fatalf("sidecar title = %q, want New name", stored.TopicTitle)
	}
}

// A session that was never filed as a topic still has a transcript: the rename
// must follow it by path rather than failing.
func TestRenameCollabSessionFallsBackToTranscriptPath(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Project"); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	dir := desktopSessionDir(root)
	sessionPath, err := createEmptySessionFile(dir, "plain")
	if err != nil {
		t.Fatal(err)
	}
	result, err := app.renameCollabSession("", sessionPath, "Renamed by path")
	if err != nil {
		t.Fatalf("renameCollabSession: %v", err)
	}
	if !result.ListingSynced {
		t.Fatalf("result = %#v", result)
	}
	stored, ok, err := agent.LoadBranchMeta(sessionPath)
	if err != nil || !ok {
		t.Fatalf("branch meta: ok=%v err=%v", ok, err)
	}
	if stored.CustomTitle != "Renamed by path" {
		t.Fatalf("sidecar custom title = %q, want the new title", stored.CustomTitle)
	}
}

// move_topic_to_group must leave the session in exactly one group AND publish the
// catalog metadata revision: the sidebar reloads its groups on that signal
// (ProjectTree: reason "metadata"), so without it the moved row keeps showing up
// under the old group until something else refreshes the tree.
func TestMoveCollabTopicToGroupMovesAndPublishesRevision(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Project"); err != nil {
		t.Fatal(err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.Projects[projectIndexByRoot(f.Projects, root)].Topics = []string{"topic-a"}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	app.ctx = context.Background()
	if err := app.SaveSessionGroups("project", root, []desktopGroup{
		{ID: "grp-old", Title: "Old", TopicIDs: []string{"topic-a"}},
	}); err != nil {
		t.Fatal(err)
	}
	events := make(chan runtimeEventEnvelope, 8)
	app.runtimeEvents.emit = func(ctx context.Context, name string, payload ...any) {
		events <- runtimeEventEnvelope{ctx: ctx, name: name, payload: append([]any(nil), payload...)}
	}

	result, err := app.moveCollabTopicToGroup("topic-a", "", "project", root, "", "Team")
	if err != nil {
		t.Fatalf("moveCollabTopicToGroup: %v", err)
	}
	if result.Group != "Team" || !reflect.DeepEqual(result.RemovedFrom, []string{"Old"}) {
		t.Fatalf("result = %#v", result)
	}
	if result.Scope != "project" || result.GroupID == "" {
		t.Fatalf("result scope/id = %#v", result)
	}

	found := false
	deadline := time.After(2 * time.Second)
	for !found {
		select {
		case event := <-events:
			if event.name == "project-tree:changed" {
				found = true
			}
		case <-deadline:
			t.Fatal("a group move published no tree revision; the sidebar would keep the old group")
		}
	}

	groups, err := app.ListProjectGroups("project", root)
	if err != nil {
		t.Fatal(err)
	}
	membership := map[string][]string{}
	for _, g := range groups {
		for _, topicID := range g.TopicIDs {
			membership[topicID] = append(membership[topicID], g.Title)
		}
	}
	if !reflect.DeepEqual(membership["topic-a"], []string{"Team"}) {
		t.Fatalf("topic-a belongs to %v, want [Team]", membership["topic-a"])
	}
}

// A caller that does not know the session's scope (a secretary moving a session
// it only sees through the directory) must still land in the right groups store.
func TestMoveCollabTopicToGroupResolvesScopeFromTopicLocation(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Project"); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.Projects[projectIndexByRoot(f.Projects, root)].Topics = []string{"topic-a"}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := app.moveCollabTopicToGroup("topic-a", "", "", "", "", "Team")
	if err != nil {
		t.Fatalf("moveCollabTopicToGroup: %v", err)
	}
	if result.Scope != "project" || !sameProjectRoot(result.ProjectRoot, root) {
		t.Fatalf("resolved target = %#v, want the topic's own project", result)
	}
	groups, err := app.ListProjectGroups("project", root)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || !reflect.DeepEqual(groups[0].TopicIDs, []string{"topic-a"}) {
		t.Fatalf("groups = %#v", groups)
	}
}
