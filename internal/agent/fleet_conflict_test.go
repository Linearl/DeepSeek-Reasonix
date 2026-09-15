package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitMust(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// setupConflictWorktrees creates a repo with two linked worktrees that both
// change the same file relative to their merge base.
func setupConflictWorktrees(t *testing.T) (root, wtA, wtB string) {
	t.Helper()
	root = t.TempDir()
	gitMust(t, root, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "shared.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "only_a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "only_b.go"), []byte("package b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitMust(t, root, "add", ".")
	gitMust(t, root, "commit", "-m", "seed")

	wtA = filepath.Join(root, "wt-a")
	wtB = filepath.Join(root, "wt-b")
	gitMust(t, root, "worktree", "add", "-b", "feat-a", wtA, "main")
	gitMust(t, root, "worktree", "add", "-b", "feat-b", wtB, "main")

	// Both branches change shared.go; only_a/only_b stay disjoint.
	if err := os.WriteFile(filepath.Join(wtA, "shared.go"), []byte("package main // a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtA, "only_a.go"), []byte("package a // a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitMust(t, wtA, "add", ".")
	gitMust(t, wtA, "commit", "-m", "a")

	if err := os.WriteFile(filepath.Join(wtB, "shared.go"), []byte("package main // b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtB, "only_b.go"), []byte("package b // b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitMust(t, wtB, "add", ".")
	gitMust(t, wtB, "commit", "-m", "b")
	return root, wtA, wtB
}

func TestDetectWorktreeMergeConflictsReportsOverlappingFiles(t *testing.T) {
	_, wtA, wtB := setupConflictWorktrees(t)
	conflicts, err := DetectWorktreeMergeConflicts(context.Background(), []WorktreeRef{
		{ID: "task-a", Root: wtA},
		{ID: "task-b", Root: wtB},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %+v, want exactly one pair", conflicts)
	}
	c := conflicts[0]
	if c.TaskA != "task-a" || c.TaskB != "task-b" {
		t.Fatalf("pair = %s/%s, want task-a/task-b", c.TaskA, c.TaskB)
	}
	if len(c.Files) != 1 || c.Files[0] != "shared.go" {
		t.Fatalf("files = %v, want [shared.go]", c.Files)
	}
}

func TestDetectWorktreeMergeConflictsIgnoresDisjointBranches(t *testing.T) {
	root := t.TempDir()
	gitMust(t, root, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "base.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitMust(t, root, "add", ".")
	gitMust(t, root, "commit", "-m", "seed")
	wtA := filepath.Join(root, "wt-a")
	wtB := filepath.Join(root, "wt-b")
	gitMust(t, root, "worktree", "add", "-b", "feat-a", wtA, "main")
	gitMust(t, root, "worktree", "add", "-b", "feat-b", wtB, "main")
	if err := os.WriteFile(filepath.Join(wtA, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitMust(t, wtA, "add", ".")
	gitMust(t, wtA, "commit", "-m", "a")
	if err := os.WriteFile(filepath.Join(wtB, "b.go"), []byte("package b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitMust(t, wtB, "add", ".")
	gitMust(t, wtB, "commit", "-m", "b")

	conflicts, err := DetectWorktreeMergeConflicts(context.Background(), []WorktreeRef{
		{ID: "task-a", Root: wtA},
		{ID: "task-b", Root: wtB},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("disjoint branches reported conflicts: %+v", conflicts)
	}
}

func TestFleetPlanValidateConcurrentWorktreeConflicts(t *testing.T) {
	_, wtA, wtB := setupConflictWorktrees(t)
	plan, err := newFleetPlan([]fleetTaskItem{
		{ID: "a", WorktreeRoot: wtA},
		{ID: "b", WorktreeRoot: wtB},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	err = plan.validateConcurrentWorktreeConflicts(context.Background(), []fleetTaskItem{
		{ID: "a", WorktreeRoot: wtA},
		{ID: "b", WorktreeRoot: wtB},
	})
	if err == nil {
		t.Fatal("concurrent worktrees that both change shared.go must fail preflight")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"a"`) || !strings.Contains(msg, `"b"`) || !strings.Contains(msg, "shared.go") {
		t.Fatalf("error must name both tasks and the file, got: %v", err)
	}
}

func TestFleetPlanValidateOrderedWorktreeConflictsAllowed(t *testing.T) {
	_, wtA, wtB := setupConflictWorktrees(t)
	items := []fleetTaskItem{
		{ID: "a", WorktreeRoot: wtA},
		{ID: "b", WorktreeRoot: wtB, DependsOn: []string{"a"}},
	}
	plan, err := newFleetPlan(items, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.validateConcurrentWorktreeConflicts(context.Background(), items); err != nil {
		t.Fatalf("ordered tasks may share files: %v", err)
	}
}
