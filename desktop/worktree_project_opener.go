package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Task 128 (会话自主创建新项目): the desktop half of the
// open_isolated_worktree_project agent tool. The tool allocates the worktree;
// this opener registers it as a first-class project (sidebar entry + topic) and
// opens a BACKGROUND tab for it, which is the "new session binds the new root"
// half of the task: the tab's session boots with the worktree as its workspace
// root, so its write baseline is the worktree itself. The calling session keeps
// its own binding and reaches the new root through the write-access approval
// chain (task 127 switches auto-trust this managed storage when enabled).
//
// Product ruling (recorded, not user-prompted): the worktree IS registered as a
// formal project topic. Not registering would leave the new project invisible
// in the sidebar, defeating the task goal; registration is reversible through
// the existing project removal and worktree finalize/cleanup flows.
//
// This file deliberately stays out of tabs.go / builtin_roots.go (another
// branch owns those surfaces); it only calls existing tab APIs.

// appWorktreeProjectOpener implements tool.WorktreeProjectOpener for the
// desktop host. It is a value type, so wiring it into boot.Options is
// allocation-free.
type appWorktreeProjectOpener struct {
	app *App
}

// OpenIsolatedWorktreeProject registers an existing worktree directory as a
// project and opens an inactive tab for it (no focus steal during the calling
// session's live turn). Idempotent per root: a visible tab already on this
// workspace short-circuits instead of piling up empty topics.
func (o appWorktreeProjectOpener) OpenIsolatedWorktreeProject(_ context.Context, worktreeRoot string) error {
	if o.app == nil {
		return fmt.Errorf("desktop app is not available")
	}
	root, err := normalizeWorktreeProjectRoot(worktreeRoot)
	if err != nil {
		return err
	}
	if existing := o.app.visibleProjectTabIDForRoot(root); existing != "" {
		// Already registered and visible — a repeated tool call must not
		// accumulate shell topics for the same project.
		return nil
	}
	topic, err := o.app.CreateTopic("project", root, "")
	if err != nil {
		return fmt.Errorf("register worktree project: %w", err)
	}
	// Inactive on purpose: the agent that created this project is mid-turn in
	// another tab; yanking focus would fight the user. The sidebar entry plus
	// background tab make the project reachable immediately.
	if _, err := o.app.openProjectTabInactive(root, topic.ID); err != nil {
		return fmt.Errorf("open worktree project tab: %w", err)
	}
	return nil
}

// normalizeWorktreeProjectRoot validates the directory the tool just created.
func normalizeWorktreeProjectRoot(worktreeRoot string) (string, error) {
	root := strings.TrimSpace(worktreeRoot)
	if root == "" {
		return "", fmt.Errorf("worktree root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve worktree root: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("worktree root is missing: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", abs)
	}
	return abs, nil
}

// visibleProjectTabIDForRoot returns the id of an open project tab whose
// workspace is root (canonical-spelling-insensitive), or "" when none is.
// Reads the tab map directly: the remote/enrichment pass of ListTabs is
// irrelevant for the duplicate guard and needs more live state.
func (a *App) visibleProjectTabIDForRoot(root string) string {
	if a == nil || strings.TrimSpace(root) == "" {
		return ""
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, tab := range a.tabs {
		if tab == nil || tab.Scope != "project" {
			continue
		}
		if sameProjectRoot(tab.WorkspaceRoot, root) {
			return tab.ID
		}
	}
	return ""
}
