package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/tool"
)

// Task 128: the session can create an isolated worktree project, get a
// writable root back, and has no empty shell left behind on failure.
func TestOpenIsolatedWorktreeProjectCreatesWritableRoot(t *testing.T) {
	src := t.TempDir()
	managed := t.TempDir()
	gitToolMust(t, src, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(src, "app.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitToolMust(t, src, "add", ".")
	gitToolMust(t, src, "commit", "-m", "seed")

	tl := openIsolatedWorktreeProject{workDir: src, managedRoot: managed}
	decl, err := tl.DeclareWriteAccess(json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("DeclareWriteAccess: %v", err)
	}
	if len(decl.Directories) != 1 || filepath.Clean(decl.Directories[0]) != filepath.Clean(managed) {
		t.Fatalf("declaration = %+v, want managed root", decl)
	}

	out, err := tl.Execute(context.Background(), json.RawMessage(`{"project_name":"wave2"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var result struct {
		ProjectName  string   `json:"projectName"`
		WorktreeRoot string   `json:"worktreeRoot"`
		Branch       string   `json:"branch"`
		WritePaths   []string `json:"writePaths"`
		Next         string   `json:"next"`
		Division     string   `json:"division"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.ProjectName != "wave2" || result.WorktreeRoot == "" {
		t.Fatalf("result = %s", out)
	}
	if !strings.HasPrefix(result.Branch, "reasonix/delivery-") {
		t.Fatalf("branch = %q", result.Branch)
	}
	if len(result.WritePaths) != 1 || result.WritePaths[0] != result.WorktreeRoot {
		t.Fatalf("writePaths = %v", result.WritePaths)
	}
	if !strings.Contains(result.Division, "create_worktree") {
		t.Fatalf("division note missing: %s", result.Division)
	}
	// New root is a real directory the session can write into.
	target := filepath.Join(result.WorktreeRoot, "session_note.txt")
	if err := os.WriteFile(target, []byte("ok"), 0o644); err != nil {
		t.Fatalf("write into new project root: %v", err)
	}
}

func TestOpenIsolatedWorktreeProjectRegisteredBuiltin(t *testing.T) {
	if _, ok := tool.LookupBuiltin("open_isolated_worktree_project"); !ok {
		t.Fatal("open_isolated_worktree_project is not a registered builtin")
	}
}

func TestOpenIsolatedWorktreeProjectInvalidSourceLeavesNoShell(t *testing.T) {
	// Not a git repo: Create fails; managed storage must stay empty.
	src := t.TempDir()
	managed := t.TempDir()
	tl := openIsolatedWorktreeProject{workDir: src, managedRoot: managed}
	if _, err := tl.Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("non-git source must fail")
	}
	entries, err := os.ReadDir(managed)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed create left %d entries in managed storage", len(entries))
	}
}
