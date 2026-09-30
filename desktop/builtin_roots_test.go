package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/config"
)

// Task 186: the host's own directories must never render as projects, and a stale entry
// must not survive a restart. These tests pin the whitelist itself, the eviction applied
// on load, and the fact that ordinary projects are untouched.
func TestIsBuiltinWorkspaceRootCoversHostDirectories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)

	builtin := map[string]string{
		"global workspace root": filepath.Join(home, "global-workspace"),
		"projects container":    filepath.Join(config.MemoryUserDir(), "projects"),
	}
	// The session directory goes through the same state resolver the app uses, so assert
	// against that value: state roots are redirected under test.
	if dir := config.SessionDir(); dir != "" {
		builtin["session directory"] = dir
	}
	for label, root := range builtin {
		if !isBuiltinWorkspaceRoot(root) {
			t.Fatalf("%s (%s) must be recognised as builtin", label, root)
		}
	}

	ordinary := []string{
		filepath.Join(home, "work", "my-project"),
		filepath.Join(home, "global-workspace-backup"), // prefix, not the root itself
		filepath.Join(home, "sessions-archive"),
		filepath.Join(home, "projects", "slug", "sessions"), // a workspace inside the container
		"__global__", // order token, not a path
		"",
	}
	for _, root := range ordinary {
		if isBuiltinWorkspaceRoot(root) {
			t.Fatalf("%q must not be treated as a builtin root", root)
		}
	}
}

func TestStripBuiltinProjectsRemovesNodeOrderAndPins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	globalRoot := filepath.Join(home, "global-workspace")
	projectsContainer := filepath.Join(config.MemoryUserDir(), "projects")
	userRoot := filepath.Join(home, "work", "my-project")

	f := desktopProjectFile{
		Projects: []desktopProject{
			{Root: globalRoot, Topics: []string{"t-global"}},
			{Root: projectsContainer, Topics: []string{"t-container"}},
			{Root: userRoot, Title: "My Project", Topics: []string{"t-user"}},
		},
		SidebarOrder:   []string{desktopGlobalOrderToken, globalRoot, userRoot},
		PinnedProjects: []string{projectsContainer, userRoot},
	}

	got := stripBuiltinProjects(f)

	if len(got.Projects) != 1 || got.Projects[0].Root != userRoot {
		t.Fatalf("projects after strip = %+v, want only the user project", got.Projects)
	}
	if len(got.Projects[0].Topics) != 1 || got.Projects[0].Topics[0] != "t-user" {
		t.Fatalf("user project topics were altered: %+v", got.Projects[0].Topics)
	}
	if !containsDesktopString(got.SidebarOrder, desktopGlobalOrderToken) {
		t.Fatalf("the Global order token must survive: %v", got.SidebarOrder)
	}
	if containsDesktopString(got.SidebarOrder, globalRoot) {
		t.Fatalf("builtin root kept its sidebar order: %v", got.SidebarOrder)
	}
	if _, ok := containsRoot(got.SidebarOrder, userRoot); !ok {
		t.Fatalf("user project lost its sidebar order: %v", got.SidebarOrder)
	}
	if containsDesktopString(got.PinnedProjects, projectsContainer) {
		t.Fatalf("builtin root stayed pinned: %v", got.PinnedProjects)
	}
	if !containsDesktopString(got.PinnedProjects, userRoot) {
		t.Fatalf("user project lost its pin: %v", got.PinnedProjects)
	}
}

func TestLoadProjectsFileEvictsBuiltinRootsAcrossRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	globalRoot := filepath.Join(home, "global-workspace")
	userRoot := filepath.Join(home, "work", "my-project")

	// A stale registry (the shape task 161 recorded) still holds the global root.
	stale := desktopProjectFile{
		Projects: []desktopProject{
			{Root: globalRoot, Topics: []string{"t-global"}},
			{Root: userRoot, Title: "My Project"},
		},
		SidebarOrder: []string{globalRoot, userRoot},
	}
	if err := saveProjectsFile(stale); err != nil {
		t.Fatalf("seed projects file: %v", err)
	}

	loaded := loadProjectsFile()
	if len(loaded.Projects) != 1 || loaded.Projects[0].Root != userRoot {
		t.Fatalf("loaded projects = %+v, want only the user project", loaded.Projects)
	}
	if containsDesktopString(loaded.SidebarOrder, globalRoot) {
		t.Fatalf("loaded sidebar order still names the builtin root: %v", loaded.SidebarOrder)
	}

	// Writing through the update path must not resurrect it either: the mutator here
	// re-adds the very entry a user just deleted.
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.Projects = append(f.Projects, desktopProject{Root: globalRoot})
		f.SidebarOrder = append(f.SidebarOrder, globalRoot)
		return true, nil
	}); err != nil {
		t.Fatalf("updateProjectsFile: %v", err)
	}

	after := loadProjectsFile()
	if len(after.Projects) != 1 || after.Projects[0].Root != userRoot {
		t.Fatalf("projects after re-add = %+v, want the builtin root still gone", after.Projects)
	}
	if containsDesktopString(after.SidebarOrder, globalRoot) {
		t.Fatalf("sidebar order was resurrected: %v", after.SidebarOrder)
	}
}

func TestNormalizeWorkspaceScopeRoutesBuiltinRootsToGlobal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	globalRoot := globalWorkspaceRoot()
	userRoot := filepath.Join(home, "work", "my-project")

	for _, tc := range []struct {
		name          string
		scope         string
		workspaceRoot string
		wantScope     string
		wantRoot      string
	}{
		{name: "project on the global workspace becomes global", scope: "project", workspaceRoot: globalRoot, wantScope: "global", wantRoot: ""},
		{name: "project on the session dir becomes global", scope: "project", workspaceRoot: config.SessionDir(), wantScope: "global", wantRoot: ""},
		{name: "project on the projects container becomes global", scope: "project", workspaceRoot: filepath.Join(config.MemoryUserDir(), "projects"), wantScope: "global", wantRoot: ""},
		{name: "user project is untouched", scope: "project", workspaceRoot: userRoot, wantScope: "project", wantRoot: userRoot},
		{name: "global scope is untouched", scope: "global", workspaceRoot: globalRoot, wantScope: "global", wantRoot: globalRoot},
		{name: "empty root stays a validation problem, not a rewrite", scope: "project", wantScope: "project", wantRoot: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotScope, gotRoot := normalizeWorkspaceScope(tc.scope, tc.workspaceRoot)
			if gotScope != tc.wantScope || gotRoot != tc.wantRoot {
				t.Fatalf("normalizeWorkspaceScope(%q, %q) = (%q, %q), want (%q, %q)",
					tc.scope, tc.workspaceRoot, gotScope, gotRoot, tc.wantScope, tc.wantRoot)
			}
		})
	}
}

// The open paths normalize before anything durable happens: no project entry,
// no workspace pointer, no recent-workspaces residue may name a builtin root.
func TestRegisterProjectRootAndWorkspacePointerSkipBuiltinRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	builtin := globalWorkspaceRoot()
	if err := os.MkdirAll(builtin, 0o755); err != nil {
		t.Fatal(err)
	}
	userRoot := filepath.Join(home, "work", "my-project")

	app := NewApp()
	app.registerProjectRoot(builtin)
	if loaded := loadProjectsFile(); len(loaded.Projects) != 0 {
		t.Fatalf("projects after registering the builtin root = %+v, want none", loaded.Projects)
	}

	app.registerProjectRoot(userRoot)
	loaded := loadProjectsFile()
	if len(loaded.Projects) != 1 || loaded.Projects[0].Root != userRoot {
		t.Fatalf("projects after registering the user root = %+v, want exactly it", loaded.Projects)
	}

	saveWorkspace(builtin)
	if got := loadWorkspace(); got != "" {
		t.Fatalf("workspace pointer = %q, want empty after saving the builtin root", got)
	}
	saveWorkspace(userRoot)
	if got := loadWorkspace(); !sameDesktopPath(got, userRoot) {
		t.Fatalf("workspace pointer = %q, want the user root %q", got, userRoot)
	}

	rememberWorkspace(builtin)
	if list := loadWorkspaces(); len(list) != 0 {
		t.Fatalf("recent workspaces = %v, want empty after remembering the builtin root", list)
	}
	rememberWorkspace(userRoot)
	if list := loadWorkspaces(); len(list) != 1 || !sameDesktopPath(list[0], userRoot) {
		t.Fatalf("recent workspaces = %v, want exactly the user root", list)
	}
}

// TestOpenBuiltinRootTabIsGlobalTab pins the open-path normalization end to end:
// opening a tab that claims project scope on one of the host's own directories
// produces a Global tab whose future turns index into the Global section, and
// leaves no project entry or workspace pointer behind.
func TestOpenBuiltinRootTabIsGlobalTab(t *testing.T) {
	isolateDesktopUserDirs(t)
	builtin := globalWorkspaceRoot()
	if err := os.MkdirAll(builtin, 0o755); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	app.runtimeEvents.emit = func(context.Context, string, ...any) {}

	meta, err := app.openTopicTab("project", builtin, "topic-builtin-open", "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if meta.Scope != "global" {
		t.Fatalf("opened tab scope = %q, want global", meta.Scope)
	}
	if !sameDesktopPath(meta.WorkspaceRoot, builtin) {
		t.Fatalf("opened tab root = %q, want the global workspace %q", meta.WorkspaceRoot, builtin)
	}
	if loaded := loadProjectsFile(); len(loaded.Projects) != 0 {
		t.Fatalf("projects after the open = %+v, want none", loaded.Projects)
	}
	if got := loadWorkspace(); got != "" {
		t.Fatalf("workspace pointer = %q, want empty", got)
	}
}

func containsRoot(values []string, root string) (string, bool) {
	for _, value := range values {
		if sameDesktopPath(value, root) {
			return value, true
		}
	}
	return "", false
}
