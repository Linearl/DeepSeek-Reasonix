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
