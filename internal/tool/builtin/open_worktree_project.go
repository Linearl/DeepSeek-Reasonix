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

func init() { tool.RegisterBuiltin(openIsolatedWorktreeProject{}) }

// openIsolatedWorktreeProject is the task-128 light path for "session creates
// its own project workspace". It is deliberately distinct from create_worktree
// (task 52):
//
//   - create_worktree: low-level durable worktree allocation. No write-access
//     declaration, no usage contract — callers that already hold the managed
//     root (or have parallel full-access on) use this.
//   - open_isolated_worktree_project: create + once write authorization for the
//     managed worktree root + a structured handoff the session can use with
//     fleet write_paths / worktree_root. Prefer this when the session must
//     immediately work inside the new project.
//
// It never opens a desktop tab. Hosts that want UI registration bind a
// WorktreeProjectOpener on the call context; absence is not an error.
type openIsolatedWorktreeProject struct {
	workDir     string
	managedRoot string
}

func (openIsolatedWorktreeProject) Name() string { return "open_isolated_worktree_project" }

func (openIsolatedWorktreeProject) Description() string {
	return "Create an isolated Git worktree project the session can write into immediately. Allocates a reasonix/delivery-* branch worktree under Reasonix-managed storage, requests once write authorization for that storage root, and returns the new workspace path plus fleet handoff fields (write_paths / worktree_root). Prefer this over create_worktree when the session itself will work in the new project; use create_worktree for low-level allocation without write-access setup. Does not open a desktop tab. Failed allocations are rolled back — no empty shell is left behind."
}

func (openIsolatedWorktreeProject) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
  "workspace_root":{"type":"string","description":"Source workspace root. Defaults to the current workspace."},
  "project_name":{"type":"string","description":"Optional label recorded in the result for the session to refer to this project."}
},"required":[]}`)
}

func (openIsolatedWorktreeProject) ReadOnly() bool { return false }

// DeclareWriteAccess asks for the managed worktree storage root once. After
// that grant, every worktree allocated under it is writable without a second
// approval. With task 127 parallel full-access on, confine already trusts this
// root and the declaration is a no-op safety net.
func (w openIsolatedWorktreeProject) DeclareWriteAccess(args json.RawMessage) (tool.WriteAccessDeclaration, error) {
	managed := strings.TrimSpace(w.managedRoot)
	if managed == "" {
		managed = config.DeliveryWorktreeDir()
	}
	if managed == "" {
		return tool.WriteAccessDeclaration{}, fmt.Errorf("Reasonix worktree storage is unavailable")
	}
	return tool.WriteAccessDeclaration{
		Directories:   []string{managed},
		Justification: "create and work inside an isolated delivery worktree project",
	}, nil
}

func (w openIsolatedWorktreeProject) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		WorkspaceRoot string `json:"workspace_root"`
		ProjectName   string `json:"project_name"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	root := resolveIn(w.workDir, p.WorkspaceRoot)
	managed := strings.TrimSpace(w.managedRoot)
	if managed == "" {
		managed = config.DeliveryWorktreeDir()
	}
	created, err := worktree.Create(ctx, root, managed)
	if err != nil {
		// worktree.Create rolls back an exact-clean allocation; a failure here
		// leaves no empty shell for the session to trip over.
		return "", err
	}
	// Optional host hook: desktop may open a tab. Missing hook is fine.
	if opener, ok := tool.WorktreeProjectOpenerFromContext(ctx); ok {
		if regErr := opener.OpenIsolatedWorktreeProject(ctx, created.WorktreeRoot); regErr != nil {
			// The worktree itself succeeded; surface the registration failure
			// as text without destroying the allocation.
			return fmt.Sprintf(
				"created worktree at %s (branch %s) but host registration failed: %v\nUse the path directly; it is already on disk.",
				created.WorktreeRoot, created.Branch, regErr,
			), nil
		}
	}
	name := strings.TrimSpace(p.ProjectName)
	payload := map[string]any{
		"projectName":    name,
		"workspaceRoot":  created.WorkspaceRoot,
		"worktreeRoot":   created.WorktreeRoot,
		"sourceRoot":     created.SourceRoot,
		"branch":         created.Branch,
		"head":           created.Head,
		"sourceDirty":    created.SourceDirty,
		"writableRoot":   created.WorktreeRoot,
		"writePaths":     []string{created.WorktreeRoot},
		"worktreeRootForFleet": created.WorktreeRoot,
		"next": "Write files under worktreeRoot. For parallel sub-agents, declare write_paths/worktree_root pointing at this root. Merge back with prepare_worktree_merge then merge_worktree_back.",
		"division": "open_isolated_worktree_project = create + once write auth + handoff. create_worktree = allocation only.",
	}
	out, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}
