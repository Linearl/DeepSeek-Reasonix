package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Task 128: the desktop opener must refuse to pile up duplicate registrations.
// A visible project tab already on the worktree root short-circuits the open,
// so repeated tool calls cannot accumulate empty shell topics.
func TestVisibleProjectTabIDForRootDeduplicates(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"wt":   {ID: "wt", Scope: "project", WorkspaceRoot: root},
			"proj": {ID: "proj", Scope: "project", WorkspaceRoot: other},
			"glob": {ID: "glob", Scope: "global", WorkspaceRoot: root},
		},
	}
	if got := app.visibleProjectTabIDForRoot(root); got != "wt" {
		t.Fatalf("visibleProjectTabIDForRoot(root) = %q, want wt", got)
	}
	// Spelling-insensitive: an equivalent path with a trailing separator still
	// matches the open tab.
	if got := app.visibleProjectTabIDForRoot(root + string(filepath.Separator)); got != "wt" {
		t.Fatalf("trailing-separator root = %q, want wt", got)
	}
	if got := app.visibleProjectTabIDForRoot(filepath.Join(root, "nested")); got != "" {
		t.Fatalf("unrelated root = %q, want empty", got)
	}
	// Global-scope tabs on the same directory are not project registrations.
	globalOnly := &App{
		tabs: map[string]*WorkspaceTab{
			"glob": {ID: "glob", Scope: "global", WorkspaceRoot: root},
		},
	}
	if got := globalOnly.visibleProjectTabIDForRoot(root); got != "" {
		t.Fatalf("global-only tab matched as project: %q", got)
	}
}

// The opener validates the root before touching registration: a missing or
// non-directory path is an error, an empty path is an error.
func TestOpenIsolatedWorktreeProjectValidation(t *testing.T) {
	opener := appWorktreeProjectOpener{app: &App{}}
	if err := opener.OpenIsolatedWorktreeProject(context.Background(), ""); err == nil {
		t.Fatal("empty root must be refused")
	}
	if err := opener.OpenIsolatedWorktreeProject(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing root must be refused")
	}
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := opener.OpenIsolatedWorktreeProject(context.Background(), file); err == nil {
		t.Fatal("non-directory root must be refused")
	}
	if err := (appWorktreeProjectOpener{}).OpenIsolatedWorktreeProject(context.Background(), "non-empty"); err == nil {
		t.Fatal("nil app must be refused")
	}
}
