package builtin

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitToolMust(t *testing.T, dir string, args ...string) {
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

func TestCreateAndInspectWorktreeTools(t *testing.T) {
	src := t.TempDir()
	managed := t.TempDir()
	gitToolMust(t, src, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(src, "app.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitToolMust(t, src, "add", ".")
	gitToolMust(t, src, "commit", "-m", "seed")

	created, err := createWorktree{workDir: src, managedRoot: managed}.
		Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("create_worktree: %v", err)
	}
	var createdInfo struct {
		WorktreeRoot string `json:"worktreeRoot"`
		Branch       string `json:"branch"`
	}
	if err := json.Unmarshal([]byte(created), &createdInfo); err != nil {
		t.Fatal(err)
	}
	if createdInfo.WorktreeRoot == "" || !strings.HasPrefix(createdInfo.Branch, "reasonix/delivery-") {
		t.Fatalf("created = %s, want managed delivery branch", created)
	}

	// Clean worktree: no changes yet, inspection should be available.
	inspected, err := inspectWorktreeMerge{workDir: createdInfo.WorktreeRoot, managedRoot: managed}.
		Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("inspect_worktree_merge: %v", err)
	}
	var inspection struct {
		Available bool     `json:"available"`
		CanMerge  bool     `json:"canMerge"`
		Blockers  []any    `json:"blockers"`
		ConflictFiles []string `json:"conflictFiles"`
	}
	if err := json.Unmarshal([]byte(inspected), &inspection); err != nil {
		t.Fatal(err)
	}
	if !inspection.Available {
		t.Fatalf("inspection should be available: %s", inspected)
	}

	// Change a file in the worktree and commit so merge has a real diff.
	if err := os.WriteFile(filepath.Join(createdInfo.WorktreeRoot, "app.go"), []byte("package main // changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitToolMust(t, createdInfo.WorktreeRoot, "add", ".")
	gitToolMust(t, createdInfo.WorktreeRoot, "commit", "-m", "work")

	preparedRaw, err := prepareWorktreeMerge{workDir: createdInfo.WorktreeRoot, managedRoot: managed}.
		Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("prepare_worktree_merge: %v", err)
	}
	var prepared struct {
		CanMerge               bool   `json:"canMerge"`
		TargetBranch           string `json:"targetBranch"`
		TargetHead             string `json:"targetHead"`
		WorktreeHead           string `json:"worktreeHead"`
		WorktreeStateToken     string `json:"worktreeStateToken"`
		HasConflicts           bool   `json:"hasConflicts"`
	}
	if err := json.Unmarshal([]byte(preparedRaw), &prepared); err != nil {
		t.Fatal(err)
	}
	if !prepared.CanMerge || prepared.HasConflicts {
		t.Fatalf("clean change should be mergeable: %s", preparedRaw)
	}

	mergedRaw, err := mergeWorktreeBack{workDir: createdInfo.WorktreeRoot, managedRoot: managed}.
		Execute(context.Background(), mustJSON(t, map[string]any{
			"workspace_root":                createdInfo.WorktreeRoot,
			"expected_target_branch":        prepared.TargetBranch,
			"expected_target_head":          prepared.TargetHead,
			"expected_worktree_head":        prepared.WorktreeHead,
			"expected_worktree_state_token": prepared.WorktreeStateToken,
		}))
	if err != nil {
		t.Fatalf("merge_worktree_back: %v", err)
	}
	var merged struct {
		Merged bool `json:"merged"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal([]byte(mergedRaw), &merged); err != nil {
		t.Fatal(err)
	}
	if !merged.Merged || merged.Error != "" {
		t.Fatalf("merge result: %s", mergedRaw)
	}
	// Source branch should now contain the change.
	content, err := os.ReadFile(filepath.Join(src, "app.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "changed") {
		t.Fatalf("source app.go after merge = %q, want the worktree change", content)
	}
}

func TestMergeWorktreeBackRequiresPreparedIdentities(t *testing.T) {
	_, err := mergeWorktreeBack{workDir: t.TempDir(), managedRoot: t.TempDir()}.
		Execute(context.Background(), json.RawMessage(`{"workspace_root":"."}`))
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("missing identities err = %v, want required-field error", err)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
