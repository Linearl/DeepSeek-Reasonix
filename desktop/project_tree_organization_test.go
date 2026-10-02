package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"reasonix/internal/agent"
)

func TestCompleteTopicOrderPreservesTopicsMissingFromPartialClient(t *testing.T) {
	got, err := completeTopicOrder([]string{"c", "a"}, []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"c", "a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if _, err := completeTopicOrder([]string{"a", "a"}, []string{"a", "b"}); err == nil {
		t.Fatal("duplicate topic order must fail")
	}
	if _, err := completeTopicOrder([]string{"unknown"}, []string{"a", "b"}); err == nil {
		t.Fatal("unknown topic must not be persisted")
	}
}

func TestReorderTopicsEnablesManualOrderOnlyAfterExplicitDrag(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Project"); err != nil {
		t.Fatal(err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		i := projectIndexByRoot(f.Projects, root)
		f.Projects[i].Topics = []string{"a", "b", "c"}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	for _, topic := range app.metadataProjectTopics("project", root) {
		if topic.SortOrder != -1 {
			t.Fatalf("preference-free sortOrder = %d, want -1", topic.SortOrder)
		}
	}
	if err := app.ReorderTopics("project", root, []string{"c", "a"}); err != nil {
		t.Fatal(err)
	}
	project := loadProjectsFile().Projects[projectIndexByRoot(loadProjectsFile().Projects, root)]
	if want := []string{"c", "a", "b"}; !reflect.DeepEqual(project.Topics, want) {
		t.Fatalf("topics = %v, want %v", project.Topics, want)
	}
	if !project.ManualTopicOrder {
		t.Fatal("manual topic order flag was not persisted")
	}
	for index, topic := range app.metadataProjectTopics("project", root) {
		if topic.SortOrder != index {
			t.Fatalf("topic %q sortOrder = %d, want %d", topic.TopicID, topic.SortOrder, index)
		}
	}
}

func TestSessionGroupsPersistExclusiveMembership(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Project"); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	groups := []desktopGroup{
		{ID: "one", Title: "One", TopicIDs: []string{"a", "b"}},
		{ID: "two", Title: "Two", TopicIDs: []string{"c"}},
	}
	if err := app.SaveSessionGroups("project", root, groups); err != nil {
		t.Fatal(err)
	}
	got, err := app.ListProjectGroups("project", root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, groups) {
		t.Fatalf("groups = %#v, want %#v", got, groups)
	}
	if err := removeTopicFromProjectsFile("b"); err != nil {
		t.Fatal(err)
	}
	got, err = app.ListProjectGroups("project", root)
	if err != nil || len(got) != 2 || !reflect.DeepEqual(got[0].TopicIDs, []string{"a"}) {
		t.Fatalf("groups after topic deletion = %#v, err=%v", got, err)
	}
	if err := app.SaveSessionGroups("project", root, []desktopGroup{
		{ID: "one", Title: "One", TopicIDs: []string{"same"}},
		{ID: "two", Title: "Two", TopicIDs: []string{"same"}},
	}); err == nil {
		t.Fatal("a topic cannot belong to multiple groups")
	}
	if _, err := app.ListProjectGroups("other", root); err == nil {
		t.Fatal("unsupported scope must fail")
	}
}

func TestProjectOrganizationSurvivesOlderBuildRoundTrip(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Project"); err != nil {
		t.Fatal(err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.Projects[projectIndexByRoot(f.Projects, root)].Topics = []string{"a", "b", "c"}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	if err := app.ReorderTopics("project", root, []string{"c", "a", "b"}); err != nil {
		t.Fatal(err)
	}
	wantGroups := []desktopGroup{{ID: "important", Title: "Important", TopicIDs: []string{"a", "c"}}}
	if err := app.SaveSessionGroups("project", root, wantGroups); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(desktopConfigDir(), desktopProjectOrganizationFile)); err != nil {
		t.Fatalf("organization sidecar missing: %v", err)
	}

	// Model an older binary's typed decode + save: it preserves the topic list
	// but drops every organization field it does not know.
	projectsPath := filepath.Join(desktopConfigDir(), desktopProjectsFile)
	b, err := os.ReadFile(projectsPath)
	if err != nil {
		t.Fatal(err)
	}
	var old map[string]any
	if err := json.Unmarshal(b, &old); err != nil {
		t.Fatal(err)
	}
	delete(old, "globalManualTopicOrder")
	delete(old, "globalGroups")
	for _, value := range old["projects"].([]any) {
		project := value.(map[string]any)
		delete(project, "manualTopicOrder")
		delete(project, "groups")
	}
	b, err = json.MarshalIndent(old, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectsPath, b, 0o644); err != nil {
		t.Fatal(err)
	}

	loaded := loadProjectsFile()
	project := loaded.Projects[projectIndexByRoot(loaded.Projects, root)]
	if !project.ManualTopicOrder || !reflect.DeepEqual(project.Topics, []string{"c", "a", "b"}) {
		t.Fatalf("manual organization after downgrade = flag %v topics %v", project.ManualTopicOrder, project.Topics)
	}
	if !reflect.DeepEqual(project.Groups, wantGroups) {
		t.Fatalf("groups after downgrade = %#v, want %#v", project.Groups, wantGroups)
	}
}

func TestVersionedSessionGroupsRejectStaleFullState(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Project"); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	if err := app.SaveSessionGroups("project", root, []desktopGroup{{ID: "one", Title: "One", TopicIDs: []string{"a"}}}); err != nil {
		t.Fatal(err)
	}
	base, err := app.GetProjectGroups("project", root)
	if err != nil {
		t.Fatal(err)
	}
	first, err := app.SaveSessionGroupsVersioned("project", root, base.Revision, []desktopGroup{{ID: "one", Title: "Renamed", TopicIDs: []string{"a"}}})
	if err != nil || !first.Applied {
		t.Fatalf("first CAS = %#v, err=%v", first, err)
	}
	stale, err := app.SaveSessionGroupsVersioned("project", root, base.Revision, []desktopGroup{
		{ID: "one", Title: "One", TopicIDs: []string{"a"}},
		{ID: "two", Title: "Two"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if stale.Applied || stale.Revision != first.Revision || len(stale.Groups) != 1 || stale.Groups[0].Title != "Renamed" {
		t.Fatalf("stale CAS = %#v, want current renamed state", stale)
	}
	rebased := append(nonNilGroups(stale.Groups), desktopGroup{ID: "two", Title: "Two"})
	merged, err := app.SaveSessionGroupsVersioned("project", root, stale.Revision, rebased)
	if err != nil || !merged.Applied || len(merged.Groups) != 2 || merged.Groups[0].Title != "Renamed" {
		t.Fatalf("rebased CAS = %#v, err=%v", merged, err)
	}

	beforeArchive, _ := app.GetProjectGroups("project", root)
	if err := removeTopicFromProjectsFile("a"); err != nil {
		t.Fatal(err)
	}
	resurrect, err := app.SaveSessionGroupsVersioned("project", root, beforeArchive.Revision, beforeArchive.Groups)
	if err != nil {
		t.Fatal(err)
	}
	if resurrect.Applied || len(resurrect.Groups[0].TopicIDs) != 0 {
		t.Fatalf("archive removal was overwritten by stale save: %#v", resurrect)
	}
	if err := app.SaveSessionGroups("project", root, beforeArchive.Groups); err != nil {
		t.Fatal(err)
	}
	legacy, _ := app.GetProjectGroups("project", root)
	if len(legacy.Groups[0].TopicIDs) != 0 {
		t.Fatalf("legacy full-state save resurrected archived membership: %#v", legacy)
	}
}

func TestListProjectGroupsMissingProjectReturnsJSONArray(t *testing.T) {
	isolateDesktopUserDirs(t)
	groups, err := NewApp().ListProjectGroups("project", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(groups)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "[]" {
		t.Fatalf("missing project groups JSON = %s, want []", b)
	}
}

func TestGetProjectGroupsFiltersStaleMemberIDs(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Grouped"); err != nil {
		t.Fatal(err)
	}
	sessionDir := desktopSessionDir(root)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	livePath := filepath.Join(sessionDir, "live.jsonl")
	if err := os.WriteFile(livePath, []byte(`{"role":"user","content":"hi"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := agent.UpdateBranchMeta(livePath, false, func(meta *agent.BranchMeta) error {
		meta.TopicID = "topic-live"
		meta.TopicTitle = "Live Topic"
		meta.WorkspaceRoot = root
		meta.Scope = "project"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	installSessionCatalogForTest(t, app, sessionDir, "project", root)

	groups := []desktopGroup{
		{ID: "g1", Title: "Live", TopicIDs: []string{"topic-live", "topic-gone", "topic-archived"}},
		{ID: "g2", Title: "Empty", TopicIDs: nil},
	}
	if err := app.SaveSessionGroups("project", root, groups); err != nil {
		t.Fatal(err)
	}
	snapshot, err := app.GetProjectGroups("project", root)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Groups) != 2 {
		t.Fatalf("groups = %d, want 2 (empty groups stay)", len(snapshot.Groups))
	}
	if want := []string{"topic-live"}; !reflect.DeepEqual(snapshot.Groups[0].TopicIDs, want) {
		t.Fatalf("live group members = %v, want %v", snapshot.Groups[0].TopicIDs, want)
	}
	listed, err := app.ListProjectGroups("project", root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"topic-live"}; !reflect.DeepEqual(listed[0].TopicIDs, want) {
		t.Fatalf("listed live group members = %v, want %v", listed[0].TopicIDs, want)
	}
}

// Task 170: moving a session between groups must leave it in exactly one group.
// AddTopicToGroup only appends, and a topic may belong to a single group, so an
// append-only move is refused by validation and the session never moves - which
// is why the agent tool goes through MoveTopicToGroup.
func TestMoveTopicToGroupLeavesThePreviousGroup(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Project"); err != nil {
		t.Fatal(err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.Projects[projectIndexByRoot(f.Projects, root)].Topics = []string{"a", "b"}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	if err := app.SaveSessionGroups("project", root, []desktopGroup{
		{ID: "grp-old", Title: "Old", TopicIDs: []string{"a"}},
		{ID: "grp-two", Title: "Two", TopicIDs: []string{"b"}},
	}); err != nil {
		t.Fatal(err)
	}

	// The append-only path cannot move an already-grouped topic.
	if err := app.AddTopicToGroup("project", root, "a", "", "Two"); err == nil {
		t.Fatal("append-only filing of a grouped topic must be refused")
	}

	group, removed, alreadyFiled, err := app.MoveTopicToGroup("project", root, "a", "", "Two")
	if err != nil {
		t.Fatalf("MoveTopicToGroup: %v", err)
	}
	if !reflect.DeepEqual(removed, []string{"Old"}) {
		t.Fatalf("removed = %v, want [Old]", removed)
	}
	if group.ID != "grp-two" || group.Title != "Two" || alreadyFiled {
		t.Fatalf("target group = %#v alreadyFiled=%v", group, alreadyFiled)
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
	if !reflect.DeepEqual(membership["a"], []string{"Two"}) {
		t.Fatalf("topic a belongs to %v, want [Two]", membership["a"])
	}
	if !reflect.DeepEqual(membership["b"], []string{"Two"}) {
		t.Fatalf("topic b belongs to %v, want [Two]", membership["b"])
	}

	// Moving again into the same group is a no-op, not a duplicate membership.
	_, removedAgain, alreadyFiledAgain, err := app.MoveTopicToGroup("project", root, "a", "", "Two")
	if err != nil {
		t.Fatalf("second MoveTopicToGroup: %v", err)
	}
	if !alreadyFiledAgain || len(removedAgain) != 0 {
		t.Fatalf("second move = alreadyFiled %v removed %v, want true/[]", alreadyFiledAgain, removedAgain)
	}
	groups, err = app.ListProjectGroups("project", root)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range groups {
		count := 0
		for _, topicID := range g.TopicIDs {
			if topicID == "a" {
				count++
			}
		}
		if count > 1 {
			t.Fatalf("group %q lists the topic %d times", g.Title, count)
		}
	}
}

// A move into a group that does not exist yet creates it, matching the
// "drag into a new group" behaviour the sidebar offers.
func TestMoveTopicToGroupCreatesMissingGroup(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Project"); err != nil {
		t.Fatal(err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.Projects[projectIndexByRoot(f.Projects, root)].Topics = []string{"a"}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	group, removed, alreadyFiled, err := app.MoveTopicToGroup("project", root, "a", "", "Fresh team")
	if err != nil {
		t.Fatalf("MoveTopicToGroup: %v", err)
	}
	if group.Title != "Fresh team" || group.ID == "" {
		t.Fatalf("created group = %#v", group)
	}
	if len(removed) != 0 || alreadyFiled {
		t.Fatalf("fresh move = removed %v alreadyFiled %v", removed, alreadyFiled)
	}
	groups, err := app.ListProjectGroups("project", root)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || !reflect.DeepEqual(groups[0].TopicIDs, []string{"a"}) {
		t.Fatalf("groups = %#v", groups)
	}
}


// Task 350: session group hierarchy (parent pointers).
func TestSessionGroupParentAdditiveMigration(t *testing.T) {
	// Old flat JSON (no parent field) still unmarshals; the new field stays empty.
	var groups []desktopGroup
	if err := json.Unmarshal([]byte(`[{"id":"g1","title":"Team","topicIds":["a"]}]`), &groups); err != nil {
		t.Fatalf("old json: %v", err)
	}
	if groups[0].Parent != "" {
		t.Fatalf("old data gained a parent: %q", groups[0].Parent)
	}
	// A legacy reader (struct without the field, modeled as a raw map) keeps the
	// unknown key in the payload — new data is inert for old binaries.
	var legacy []map[string]any
	if err := json.Unmarshal([]byte(`[{"id":"g1","title":"Team","parent":"g0"}]`), &legacy); err != nil {
		t.Fatalf("new json into legacy reader: %v", err)
	}
	if _, has := legacy[0]["parent"]; !has {
		t.Fatal("legacy reader dropped the parent key")
	}
	if err := json.Unmarshal([]byte(`[{"id":"g1","title":"Team","parent":"g0"}]`), &groups); err != nil {
		t.Fatalf("new json: %v", err)
	}
	if groups[0].Parent != "g0" {
		t.Fatalf("parent = %q, want g0", groups[0].Parent)
	}
}

func TestSessionGroupHierarchyValidation(t *testing.T) {
	ok := []desktopGroup{
		{ID: "top", Title: "Top"},
		{ID: "child", Title: "Child", Parent: "top"},
	}
	if err := validateSessionGroups(ok); err != nil {
		t.Fatalf("valid hierarchy rejected: %v", err)
	}
	cases := []struct {
		name   string
		groups []desktopGroup
	}{
		{"unknown parent", []desktopGroup{{ID: "a", Title: "A", Parent: "ghost"}}},
		{"self parent", []desktopGroup{{ID: "a", Title: "A", Parent: "a"}}},
		{"nested parent (depth 3)", []desktopGroup{
			{ID: "a", Title: "A"},
			{ID: "b", Title: "B", Parent: "a"},
			{ID: "c", Title: "C", Parent: "b"},
		}},
		{"two-node cycle", []desktopGroup{
			{ID: "a", Title: "A", Parent: "b"},
			{ID: "b", Title: "B", Parent: "a"},
		}},
	}
	for _, tc := range cases {
		if err := validateSessionGroups(tc.groups); err == nil {
			t.Fatalf("%s: expected rejection, got nil", tc.name)
		}
	}
}

// TestSessionGroupHierarchyDisplayOnly is the 层级≠指挥权 guard (task 350, the
// design rule fixed by the 0928 decision): nesting a group under another must
// not touch ids, titles, or membership, and addressing must stay flat — filing
// a session into a child group by id works exactly like before the hierarchy
// existed, and a parent-pointer-only change still advances the CAS revision.
func TestSessionGroupHierarchyDisplayOnly(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Project"); err != nil {
		t.Fatal(err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		i := projectIndexByRoot(f.Projects, root)
		f.Projects[i].Topics = []string{"a", "b", "c"}
		f.Projects[i].Groups = []desktopGroup{
			{ID: "team-a", Title: "Team A", TopicIDs: []string{"a"}},
			{ID: "team-b", Title: "Team B", TopicIDs: []string{"b"}},
		}
		f.Projects[i].GroupsRevision = 1
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	before, err := app.ListProjectGroups("project", root)
	if err != nil {
		t.Fatal(err)
	}

	// Nest Team B under Team A (the same mutation the sidebar drag performs).
	nested := append([]desktopGroup(nil), before...)
	for i := range nested {
		if nested[i].ID == "team-b" {
			nested[i].Parent = "team-a"
		}
	}
	snapshot, err := app.SaveSessionGroupsVersioned("project", root, 1, nested)
	if err != nil || !snapshot.Applied {
		t.Fatalf("save nested: %v applied=%v", err, snapshot.Applied)
	}
	after, err := app.ListProjectGroups("project", root)
	if err != nil {
		t.Fatal(err)
	}
	// Hierarchy is display only: membership, ids and titles are untouched.
	for _, was := range before {
		for _, now := range after {
			if was.ID != now.ID {
				continue
			}
			if was.Title != now.Title || !reflect.DeepEqual(was.TopicIDs, now.TopicIDs) {
				t.Fatalf("nesting mutated group %q: %#v -> %#v", was.ID, was, now)
			}
		}
	}

	// Addressing stays flat: filing into the child group by id ignores the tree.
	if err := app.AddTopicToGroup("project", root, "c", "team-b", ""); err != nil {
		t.Fatalf("AddTopicToGroup into nested group: %v", err)
	}
	groups, err := app.ListProjectGroups("project", root)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		if group.ID != "team-b" {
			continue
		}
		if !reflect.DeepEqual(group.TopicIDs, []string{"b", "c"}) {
			t.Fatalf("child group membership = %#v", group.TopicIDs)
		}
	}

	// A parent-pointer-only change must bump the revision: equalGroups compares
	// Parent, so a pure re-nesting is never mistaken for a no-op.
	current, err := app.GetProjectGroups("project", root)
	if err != nil {
		t.Fatal(err)
	}
	detached := append([]desktopGroup(nil), current.Groups...)
	for i := range detached {
		detached[i].Parent = ""
	}
	next, err := app.SaveSessionGroupsVersioned("project", root, current.Revision, detached)
	if err != nil {
		t.Fatalf("detach: %v", err)
	}
	if !next.Applied || next.Revision <= current.Revision {
		t.Fatalf("parent-only change did not advance revision: applied=%v %d->%d", next.Applied, current.Revision, next.Revision)
	}
}
