package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/tool"
	"reasonix/internal/worktree"
)

func init() {
	tool.RegisterBuiltin(createWorktree{})
	tool.RegisterBuiltin(inspectWorktreeMerge{})
	tool.RegisterBuiltin(prepareWorktreeMerge{})
	tool.RegisterBuiltin(mergeWorktreeBack{})
}

// createWorktree creates a durable branch-backed isolated worktree under
// Reasonix-managed storage. It never switches or modifies the source checkout.
type createWorktree struct {
	workDir     string
	managedRoot string
}

func (createWorktree) Name() string { return "create_worktree" }

func (createWorktree) Description() string {
	return "Create a durable Git worktree for isolated parallel work. Creates a new branch (reasonix/delivery-*) from the source repository HEAD without modifying the source checkout. Returns the worktree root, branch, and source identity. Use inspect_worktree_merge / merge_worktree_back to merge results back."
}

func (createWorktree) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"workspace_root":{"type":"string","description":"Source workspace root. Defaults to the current workspace."}},"required":[]}`)
}

func (createWorktree) ReadOnly() bool { return false }

func (w createWorktree) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		WorkspaceRoot string `json:"workspace_root"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	root := resolveIn(w.workDir, p.WorkspaceRoot)
	managed := w.managedRoot
	if managed == "" {
		managed = config.DeliveryWorktreeDir()
	}
	result, err := worktree.Create(ctx, root, managed)
	if err != nil {
		return "", err
	}
	out, err := json.MarshalIndent(map[string]any{
		"workspaceRoot":  result.WorkspaceRoot,
		"worktreeRoot":   result.WorktreeRoot,
		"sourceRoot":     result.SourceRoot,
		"branch":         result.Branch,
		"head":           result.Head,
		"sourceDirty":    result.SourceDirty,
	}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// inspectWorktreeMerge reports merge readiness, blockers, and (when present)
// conflict files for a managed worktree against its source branch.
type inspectWorktreeMerge struct {
	workDir     string
	managedRoot string
}

func (inspectWorktreeMerge) Name() string { return "inspect_worktree_merge" }

func (inspectWorktreeMerge) Description() string {
	return "Inspect whether a Reasonix-managed worktree can merge back into its source branch. Reports blockers, dirty state, ahead/behind counts, changed files, and conflict files. Read-only."
}

func (inspectWorktreeMerge) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"workspace_root":{"type":"string","description":"Worktree workspace root to inspect. Defaults to the current workspace."}},"required":[]}`)
}

func (inspectWorktreeMerge) ReadOnly() bool { return true }

func (w inspectWorktreeMerge) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return executeInspectWorktreeMerge(ctx, w.workDir, w.managedRoot, args)
}

// prepareWorktreeMerge is the explicit pre-merge confirmation step. It returns
// the same inspection plus the identities a merge request must echo back.
type prepareWorktreeMerge struct {
	workDir     string
	managedRoot string
}

func (prepareWorktreeMerge) Name() string { return "prepare_worktree_merge" }

func (prepareWorktreeMerge) Description() string {
	return "Run a fresh merge inspection immediately before merging a worktree back. Returns the exact target branch, heads, and state token that merge_worktree_back requires, plus conflict files and blockers. Call this, show the user the result, then pass the identities to merge_worktree_back."
}

func (prepareWorktreeMerge) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"workspace_root":{"type":"string","description":"Worktree workspace root. Defaults to the current workspace."}},"required":[]}`)
}

func (prepareWorktreeMerge) ReadOnly() bool { return true }

func (w prepareWorktreeMerge) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return executeInspectWorktreeMerge(ctx, w.workDir, w.managedRoot, args)
}

func executeInspectWorktreeMerge(ctx context.Context, workDir, managedRoot string, args json.RawMessage) (string, error) {
	var p struct {
		WorkspaceRoot string `json:"workspace_root"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	root := resolveIn(workDir, p.WorkspaceRoot)
	managed := managedRoot
	if managed == "" {
		managed = config.DeliveryWorktreeDir()
	}
	inspection, err := worktree.InspectMerge(ctx, root, managed)
	if err != nil {
		return "", err
	}
	out, err := json.MarshalIndent(inspection, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// mergeWorktreeBack merges a prepared worktree into its source branch. The
// caller must echo the identities from prepare_worktree_merge so a drifted
// HEAD cannot silently become a different merge.
type mergeWorktreeBack struct {
	workDir     string
	managedRoot string
}

func (mergeWorktreeBack) Name() string { return "merge_worktree_back" }

func (mergeWorktreeBack) Description() string {
	return "Merge a Reasonix-managed worktree back into its source branch. Requires the exact identities returned by prepare_worktree_merge (expected_target_branch, expected_target_head, expected_worktree_head, expected_worktree_state_token). Leaves the worktree in place; use the desktop finalize flow or git worktree remove afterwards. Refuses when inspection blockers or conflicts remain."
}

func (mergeWorktreeBack) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
  "workspace_root":{"type":"string","description":"Worktree workspace root."},
  "expected_target_branch":{"type":"string"},
  "expected_target_head":{"type":"string"},
  "expected_worktree_head":{"type":"string"},
  "expected_worktree_state_token":{"type":"string"},
  "auto_commit_dirty":{"type":"boolean","description":"Commit a dirty worktree before merging. Default false."}
},"required":["expected_target_branch","expected_target_head","expected_worktree_head","expected_worktree_state_token"]}`)
}

func (mergeWorktreeBack) ReadOnly() bool { return false }

func (w mergeWorktreeBack) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		WorkspaceRoot             string `json:"workspace_root"`
		ExpectedTargetBranch      string `json:"expected_target_branch"`
		ExpectedTargetHead        string `json:"expected_target_head"`
		ExpectedWorktreeHead      string `json:"expected_worktree_head"`
		ExpectedWorktreeStateToken string `json:"expected_worktree_state_token"`
		AutoCommitDirty           bool   `json:"auto_commit_dirty"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	for name, v := range map[string]string{
		"expected_target_branch":        p.ExpectedTargetBranch,
		"expected_target_head":          p.ExpectedTargetHead,
		"expected_worktree_head":        p.ExpectedWorktreeHead,
		"expected_worktree_state_token": p.ExpectedWorktreeStateToken,
	} {
		if strings.TrimSpace(v) == "" {
			return "", fmt.Errorf("%s is required; run prepare_worktree_merge first", name)
		}
	}
	root := resolveIn(w.workDir, p.WorkspaceRoot)
	managed := w.managedRoot
	if managed == "" {
		managed = config.DeliveryWorktreeDir()
	}
	result, err := worktree.MergeBack(ctx, managed, worktree.MergeRequest{
		WorkspaceRoot:              root,
		ExpectedTargetBranch:       p.ExpectedTargetBranch,
		ExpectedTargetHead:         p.ExpectedTargetHead,
		ExpectedWorktreeHead:       p.ExpectedWorktreeHead,
		ExpectedWorktreeStateToken: p.ExpectedWorktreeStateToken,
		AutoCommitDirty:            p.AutoCommitDirty,
	})
	if err != nil {
		return "", err
	}
	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}
