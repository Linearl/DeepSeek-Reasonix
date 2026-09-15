package builtin

import (
	"testing"

	"reasonix/internal/tool"
)

// Task 52 light path: the agent (and therefore sub-agents that inherit the
// parent tool list) must be able to create and merge worktrees through the
// product guards without a desktop-only Wails surface.
func TestWorktreeToolsAreRegisteredBuiltins(t *testing.T) {
	for _, name := range []string{
		"create_worktree",
		"inspect_worktree_merge",
		"prepare_worktree_merge",
		"merge_worktree_back",
	} {
		if _, ok := tool.LookupBuiltin(name); !ok {
			// Fall back to scanning the registry snapshot.
			found := false
			for _, t := range tool.Builtins() {
				if t.Name() == name {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("builtin %q is not registered", name)
			}
		}
	}
}

func TestWorktreeToolsAppearInWorkspaceBinding(t *testing.T) {
	w := Workspace{Dir: t.TempDir()}
	names := map[string]bool{}
	for _, tl := range w.Tools() {
		names[tl.Name()] = true
	}
	for _, name := range []string{
		"create_worktree",
		"inspect_worktree_merge",
		"prepare_worktree_merge",
		"merge_worktree_back",
	} {
		if !names[name] {
			t.Fatalf("workspace-bound tools are missing %q", name)
		}
	}
}
