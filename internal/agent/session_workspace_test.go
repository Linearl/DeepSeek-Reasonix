package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// Task 545: the session-cwd resolver must return exactly the persisted project
// root, and refuse everything that would silently re-anchor a session to the
// reader's own working directory.
func TestSessionWorkspaceRoot(t *testing.T) {
	project := t.TempDir()
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "20261006_120000_test.jsonl")
	if err := SaveBranchMeta(sessionPath, BranchMeta{ID: BranchID(sessionPath), WorkspaceRoot: project}); err != nil {
		t.Fatal(err)
	}

	got, ok := SessionWorkspaceRoot(sessionPath)
	if !ok || got != filepath.Clean(project) {
		t.Fatalf("SessionWorkspaceRoot = (%q, %v), want (%q, true)", got, ok, filepath.Clean(project))
	}
}

func TestSessionWorkspaceRootMissingMeta(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "20261006_120001_test.jsonl")
	if _, ok := SessionWorkspaceRoot(sessionPath); ok {
		t.Fatal("session without branch meta must not resolve a root")
	}
}

// Adversarial input: the persisted root was deleted or moved after the fact.
// Following a stale root would break every relative path in the session, so a
// nonexistent directory must resolve as "no root".
func TestSessionWorkspaceRootStaleDirectory(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "20261006_120002_test.jsonl")
	if err := SaveBranchMeta(sessionPath, BranchMeta{ID: BranchID(sessionPath), WorkspaceRoot: filepath.Join(dir, "gone")}); err != nil {
		t.Fatal(err)
	}
	if _, ok := SessionWorkspaceRoot(sessionPath); ok {
		t.Fatal("stale (deleted) project root must not resolve")
	}
}

// Global-scope sessions persist an empty workspace_root by convention; they
// have no project to follow.
func TestSessionWorkspaceRootGlobalScopeEmptyRoot(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "20261006_120003_test.jsonl")
	if err := SaveBranchMeta(sessionPath, BranchMeta{ID: BranchID(sessionPath), WorkspaceRoot: ""}); err != nil {
		t.Fatal(err)
	}
	if _, ok := SessionWorkspaceRoot(sessionPath); ok {
		t.Fatal("global-scope session (empty root) must not resolve a root")
	}
}

// The desktop writers only persist absolute roots; a relative value would
// re-anchor to whatever cwd the reader has, which is exactly the bug 545 fixes.
func TestSessionWorkspaceRootRelativeRootRefused(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "20261006_120004_test.jsonl")
	if err := SaveBranchMeta(sessionPath, BranchMeta{ID: BranchID(sessionPath), WorkspaceRoot: "some/relative/dir"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := SessionWorkspaceRoot(sessionPath); ok {
		t.Fatal("relative workspace_root must not resolve")
	}
}

func TestSessionWorkspaceRootEmptyPath(t *testing.T) {
	if _, ok := SessionWorkspaceRoot(""); ok {
		t.Fatal("empty session path must not resolve")
	}
	if _, ok := SessionWorkspaceRoot("   "); ok {
		t.Fatal("blank session path must not resolve")
	}
}

// The root pointing at a FILE (not a directory) must be refused too.
func TestSessionWorkspaceRootFileRootRefused(t *testing.T) {
	dir := t.TempDir()
	fileRoot := filepath.Join(dir, "not-a-dir.txt")
	if err := os.WriteFile(fileRoot, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(dir, "20261006_120005_test.jsonl")
	if err := SaveBranchMeta(sessionPath, BranchMeta{ID: BranchID(sessionPath), WorkspaceRoot: fileRoot}); err != nil {
		t.Fatal(err)
	}
	if _, ok := SessionWorkspaceRoot(sessionPath); ok {
		t.Fatal("file-valued workspace_root must not resolve")
	}
}
