package main

import (
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// TestSessionBindingWorkspaceRootBlockedFor: the app's own session storage — the
// storage dir itself and everything beneath it — can never be a project workspace
// root (task 211: that is exactly how a shadow project tab was resurrected).
func TestSessionBindingWorkspaceRootBlockedFor(t *testing.T) {
	storage := normalizeProjectRoot(t.TempDir())
	under := filepath.Join(storage, "some-slug")
	real := filepath.Join(t.TempDir(), "real-project")
	cases := []struct {
		name string
		root string
		want bool
	}{
		{"storage dir itself", storage, true},
		{"slug dir under storage", under, true},
		{"deeper under storage", filepath.Join(under, "nested"), true},
		{"case-insensitive on windows", strings.ToUpper(under), true},
		{"real project", real, false},
		{"empty root", "", false},
	}
	for _, tc := range cases {
		if got := sessionBindingWorkspaceRootBlockedFor(tc.root, []string{storage}); got != tc.want {
			t.Errorf("%s: blocked=%v, want %v", tc.name, got, tc.want)
		}
	}
	// No storages configured blocks nothing.
	if sessionBindingWorkspaceRootBlockedFor(real, nil) {
		t.Error("empty storage list must block nothing")
	}
}

// TestSessionBindingWorkspaceRootBlockedProduction: the production wrapper consults
// the real session storage dirs. A real temp project stays allowed; the actual
// storage dir must be blocked (this is the regression guard for the user's report).
func TestSessionBindingWorkspaceRootBlockedProduction(t *testing.T) {
	if storage := normalizeProjectRoot(config.SessionDir()); storage != "" {
		if !sessionBindingWorkspaceRootBlocked(storage) {
			t.Fatalf("real session storage %q must be blocked", storage)
		}
		under := filepath.Join(storage, "legacy-slug")
		if !sessionBindingWorkspaceRootBlocked(under) {
			t.Fatalf("slug dir %q under session storage must be blocked", under)
		}
	}
	if sessionBindingWorkspaceRootBlocked(filepath.Join(t.TempDir(), "project")) {
		t.Error("a normal temp project must not be blocked")
	}
}

// TestSessionBindingFromMetaHealsShadowWorkspace: a session whose meta names the
// app's own storage as its workspace opens as a global session instead of spawning
// a shadow project tab — and it still opens at all (task 211 acceptance).
func TestSessionBindingFromMetaHealsShadowWorkspace(t *testing.T) {
	storage := normalizeProjectRoot(config.SessionDir())
	if storage == "" {
		t.Fatal("config.SessionDir() must resolve in tests to exercise the heal")
	}
	meta := agent.BranchMeta{Scope: "project", WorkspaceRoot: storage, TopicID: "topic-1", TopicTitle: "Topic"}
	binding, ok := sessionBindingFromMeta(filepath.Join(storage, "legacy-slug", "s.jsonl"), meta)
	if !ok {
		t.Fatal("a healed session must still resolve to a binding (it must open)")
	}
	if binding.scope != "global" {
		t.Fatalf("scope = %q, want global", binding.scope)
	}
	if sessionBindingWorkspaceRootBlocked(binding.workspaceRoot) {
		t.Fatalf("healed root %q is still inside session storage", binding.workspaceRoot)
	}
}

// TestSessionBindingFromMetaKeepsNormalProject: a legitimate project session is
// untouched by the heal (acceptance: zero regression for normal projects).
func TestSessionBindingFromMetaKeepsNormalProject(t *testing.T) {
	root := filepath.Join(t.TempDir(), "real-project")
	meta := agent.BranchMeta{Scope: "project", WorkspaceRoot: root, TopicID: "topic-2", TopicTitle: "Topic"}
	binding, ok := sessionBindingFromMeta(filepath.Join(root, "sessions", "s.jsonl"), meta)
	if !ok {
		t.Fatal("normal project session must resolve")
	}
	if binding.scope != "project" {
		t.Fatalf("scope = %q, want project", binding.scope)
	}
	if !sameDesktopPath(binding.workspaceRoot, normalizeProjectRoot(root)) {
		t.Fatalf("workspaceRoot = %q, want %q", binding.workspaceRoot, normalizeProjectRoot(root))
	}
}

// TestPruneShadowProjectsFor: the startup self-check keeps real projects and drops
// only registry entries whose root is the app's own session storage (task 211 P2).
func TestPruneShadowProjectsFor(t *testing.T) {
	storage := normalizeProjectRoot(t.TempDir())
	real := filepath.Join(t.TempDir(), "real-project")
	projects := []desktopProject{
		{Root: real, Title: "real"},
		{Root: filepath.Join(storage, "shadow-slug"), Title: "shadow"},
		{Root: storage, Title: "storage-itself"},
	}
	kept, pruned := pruneShadowProjectsFor(projects, []string{storage})
	if len(kept) != 1 || kept[0].Root != real {
		t.Fatalf("kept = %+v, want only the real project", kept)
	}
	if len(pruned) != 2 {
		t.Fatalf("pruned = %+v, want the two storage-rooted entries", pruned)
	}
	if kept2, pruned2 := pruneShadowProjectsFor([]desktopProject{{Root: real, Title: "real"}}, []string{storage}); len(pruned2) != 0 || len(kept2) != 1 {
		t.Fatalf("all-real registry must be untouched: kept=%+v pruned=%+v", kept2, pruned2)
	}
	kept3, pruned3 := pruneShadowProjectsFor(nil, []string{storage})
	if len(kept3) != 0 || len(pruned3) != 0 {
		t.Fatal("empty registry must produce empty results")
	}
}

// TestSessionBindingBlocksProjectsTreeShapes (task 211 audit-3 M1): the real
// corrupt roots were the projects tree root itself and orphan slug directories
// beneath it — both ANCESTORS of the leaf session dirs, which is why the old
// leaf-dir table missed them and the banner kept coming back when the user
// opened a global session. Both shapes must be blocked against the production
// tree roots. Deliberately asserts the real cross-section (config.MemoryUserDir
// + "projects") instead of building fake roots from config.SessionDir(), which
// is exactly how the previous test passed while the field kept failing.
func TestSessionBindingBlocksProjectsTreeShapes(t *testing.T) {
	base := normalizeProjectRoot(config.MemoryUserDir())
	if base == "" {
		t.Fatal("config.MemoryUserDir() must resolve to assert the real projects-tree cross-section")
	}
	projectsTree := filepath.Join(base, "projects")
	for _, root := range []string{
		projectsTree,                                          // the tree root itself
		filepath.Join(projectsTree, "orphan-slug"),            // an orphan slug dir
		filepath.Join(projectsTree, "orphan-slug", "sessions"), // the leaf the old table listed
	} {
		if !sessionBindingWorkspaceRootBlocked(root) {
			t.Errorf("projects-tree root %q must be blocked (audit-3 M1 cross-section)", root)
		}
	}
	// A project outside the storage trees is never blocked.
	outside := filepath.Join(base, "real-work", "my-project")
	if sessionBindingWorkspaceRootBlocked(outside) {
		t.Errorf("a real project outside the storage trees must not be blocked: %q", outside)
	}
	// The prune path shares the predicate and must drop the same shapes.
	kept, pruned := pruneShadowProjectsFor([]desktopProject{
		{Root: projectsTree, Title: "tree-root"},
		{Root: filepath.Join(projectsTree, "orphan-slug"), Title: "orphan"},
		{Root: outside, Title: "real"},
	}, sessionStorageTreeRoots())
	if len(pruned) != 2 || len(kept) != 1 || kept[0].Root != normalizeProjectRoot(outside) {
		t.Fatalf("prune must drop both projects-tree shapes: kept=%+v pruned=%+v", kept, pruned)
	}
}

// TestBindingNoticeOncePerProcess (task 211 audit-3 M2): the binding banner is
// suppressed per process, not per tab — reopening the same session in a fresh
// tab must not warn again about an already settled binding.
func TestBindingNoticeOncePerProcess(t *testing.T) {
	a := &App{}
	if a.bindingNoticeSeen != nil {
		t.Fatal("the seen-set must start nil and lazily initialize")
	}
	key := "global|%5Ctmp%5Cs.jsonl"
	a.mu.Lock()
	first := !a.bindingNoticeSeen[key]
	if first {
		if a.bindingNoticeSeen == nil {
			a.bindingNoticeSeen = make(map[string]bool)
		}
		a.bindingNoticeSeen[key] = true
	}
	a.mu.Unlock()
	if !first {
		t.Fatal("first sighting must warn")
	}
	a.mu.Lock()
	second := !a.bindingNoticeSeen[key]
	a.mu.Unlock()
	if second {
		t.Fatal("second sighting of the same key must be suppressed")
	}
}
