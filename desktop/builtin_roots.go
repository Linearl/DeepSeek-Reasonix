package main

import (
	"path/filepath"
	"strings"

	"reasonix/internal/config"
)

// Task 186: the host owns a few directories of its own — the global workspace root, the
// session directory, and the per-project session container. They are not user projects:
// nothing registers them, yet global-scope sessions living inside them made the sidebar
// render a project node for the global workspace, and deleting that node brought it back
// on the next run (the ensure/scan path recreates the directory every boot).
//
// They are therefore a whitelist: never rendered, and evicted on load so a stale entry
// cannot survive a restart. Their sessions stay reachable through the built-in Global
// folder, which is the one node that legitimately carries the global root.
func builtinWorkspaceRoots() []string {
	roots := make([]string, 0, 3)
	if root := strings.TrimSpace(globalWorkspaceRoot()); root != "" {
		roots = append(roots, root)
	}
	if dir := strings.TrimSpace(config.SessionDir()); dir != "" {
		roots = append(roots, dir)
	}
	// <user-support>/projects is the container for per-workspace session directories
	// (config.ProjectSessionDir resolves its base the same way), not a workspace anyone
	// works in. Same resolver as the session dir above, so the two never drift apart
	// when the state root is redirected.
	if base := strings.TrimSpace(config.MemoryUserDir()); base != "" {
		roots = append(roots, filepath.Join(base, "projects"))
	}
	return roots
}

// isBuiltinWorkspaceRoot reports whether root is one of the host's own directories.
// The comparison is path-normalised and case-insensitive on Windows (sameDesktopPath),
// so spellings that differ only by separator or case still match.
func isBuiltinWorkspaceRoot(root string) bool {
	root = strings.TrimSpace(root)
	if root == "" {
		return false
	}
	for _, candidate := range builtinWorkspaceRoots() {
		if sameDesktopPath(root, candidate) {
			return true
		}
	}
	return false
}

// stripBuiltinProjects removes every project entry that points at a builtin root, plus the
// sidebar-order and pinned entries that named it. Session data is deliberately untouched:
// only the project node disappears, so the conversations remain reachable from the Global
// folder (and nothing has to be migrated or deleted).
func stripBuiltinProjects(f desktopProjectFile) desktopProjectFile {
	if len(f.Projects) > 0 {
		kept := make([]desktopProject, 0, len(f.Projects))
		for _, project := range f.Projects {
			if isBuiltinWorkspaceRoot(project.Root) {
				continue
			}
			kept = append(kept, project)
		}
		f.Projects = kept
	}
	f.SidebarOrder = stripBuiltinRootStrings(f.SidebarOrder)
	f.PinnedProjects = stripBuiltinRootStrings(f.PinnedProjects)
	return f
}

func stripBuiltinRootStrings(values []string) []string {
	if len(values) == 0 {
		return values
	}
	kept := make([]string, 0, len(values))
	for _, value := range values {
		// "_global__" and friends are order tokens, not paths; isBuiltinWorkspaceRoot
		// only matches real directories, so tokens pass through untouched.
		if isBuiltinWorkspaceRoot(value) {
			continue
		}
		kept = append(kept, value)
	}
	return kept
}
