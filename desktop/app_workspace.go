package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

func (a *App) ListWorkspaces() []WorkspaceMeta {
	migrateLegacyWorkspacesIntoProjects()
	activeRoot := ""
	cur, _ := os.Getwd()
	a.mu.RLock()
	if tab := a.activeTabLocked(); tab != nil && tab.WorkspaceRoot != "" {
		activeRoot = normalizeProjectRoot(tab.WorkspaceRoot)
	}
	a.mu.RUnlock()
	if activeRoot == "" {
		activeRoot = normalizeProjectRoot(cur)
	}
	projects := loadProjectsFile().Projects
	out := make([]WorkspaceMeta, 0, len(projects))
	for _, project := range projects {
		out = append(out, WorkspaceMeta{
			Path:    project.Root,
			Name:    projectDisplayName(project),
			Current: activeRoot != "" && sameProjectRoot(project.Root, activeRoot),
		})
	}
	return out
}

func (a *App) RemoveWorkspace(dir string) error {
	if dir == "" {
		return fmt.Errorf("workspace path is required")
	}
	dir = normalizeProjectRoot(dir)

	fallback, err := a.removeWorkspaceSessions(dir)
	if err != nil {
		return err
	}

	// The fallback tab is already linked into a.tabs; its controller build is
	// asynchronous and does not touch removed session files, so it does not
	// need the removal lock.
	if fallback != nil {
		a.startTabControllerBuild(fallback)
	}

	forgetWorkspace(dir)
	if err := removeProject(dir); err != nil {
		return err
	}
	// If the removed workspace was the active one, clear the pointer
	// so we don't leave a stale reference to a deleted project.
	if loadWorkspace() == dir {
		if remaining := loadProjectsFile(); len(remaining.Projects) > 0 {
			// Fall back to the first remaining project
			saveWorkspace(remaining.Projects[0].Root)
		} else {
			// No projects left; clear the active pointer entirely
			clearWorkspace()
		}
	}
	a.emitProjectTreeMetadataChanged()
	return nil
}

func migrateLegacyWorkspacesIntoProjects() {
	legacy := loadWorkspaces()
	if len(legacy) == 0 {
		return
	}
	_ = updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		seen := make(map[string]bool, len(f.Projects)+len(legacy))
		for _, p := range f.Projects {
			seen[projectRootKey(p.Root)] = true
		}
		changed := false
		for _, path := range legacy {
			root := normalizeProjectRoot(path)
			key := projectRootKey(root)
			if root == "" || seen[key] {
				continue
			}
			f.Projects = append(f.Projects, desktopProject{Root: root})
			seen[key] = true
			changed = true
		}
		return changed, nil
	})
}

func workspaceName(path string) string {
	name := filepath.Base(path)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return path
	}
	return name
}

// tabWorkspaceNameForScope resolves the display name for a tab's workspace.
// Callers pass tab.Scope copied under a.mu instead of re-reading the tab.
func tabWorkspaceNameForScope(scope, cwd string) string {
	if scope == "global" {
		return globalProjectTitle()
	}
	return workspaceName(cwd)
}

func (a *App) SwitchWorkspace(dir string) (string, error) {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = home
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", dir)
	}

	// Open a registered topic so the new workspace appears in the project tree
	// immediately instead of only existing as an in-memory tab.
	topic, err := a.CreateTopic("project", dir, "")
	if err != nil {
		return "", err
	}
	var meta TabMeta
	if a.singleSurfaceLayoutEnabled() {
		meta, err = a.ActivateTopic("project", dir, topic.ID, "")
	} else {
		meta, err = a.OpenProjectTab(dir, topic.ID)
	}
	if err != nil {
		return "", err
	}
	return meta.WorkspaceRoot, nil
}

func (a *App) singleSurfaceLayoutEnabled() bool {
	cfg, _, err := a.loadDesktopUserConfigForView()
	if err != nil {
		return true
	}
	return singleSurfaceLayoutStyle(cfg.DesktopLayoutStyle())
}

// removeWorkspaceSessions snapshots, unlinks, and closes every tab bound to
// dir under the removal lock, returning the fallback Global tab when the
// removal empties the tab set. Project bookkeeping runs after release.
func (a *App) removeWorkspaceSessions(dir string) (*WorkspaceTab, error) {
	var fallback *WorkspaceTab
	// sessionRemovalMu covers every step that can still touch this workspace's
	// session files; a runtime unlinked from a.tabs/detachedSessions is invisible
	// to DeleteSession/TrashTopic/RestoreSession and must stop writing first.
	if err := func() error {
		defer a.lockRuntimeMutation("remove-workspace")()
		a.sessionRemovalMu.Lock()
		defer a.sessionRemovalMu.Unlock()

		type workspaceTabCandidate struct {
			id  string
			tab *WorkspaceTab
		}

		var closeTabs []*WorkspaceTab
		var closeDetached []*WorkspaceTab
		a.mu.Lock()
		if a.workspaceHasRunningSessions(dir) {
			a.mu.Unlock()
			return fmt.Errorf("workspace has running sessions; stop them before removing")
		}
		candidates := make([]workspaceTabCandidate, 0)
		for id, tab := range a.tabs {
			if !tabInWorkspace(tab, dir) {
				continue
			}
			candidates = append(candidates, workspaceTabCandidate{id: id, tab: tab})
		}
		a.mu.Unlock()

		snapshotted := make(map[string]*WorkspaceTab, len(candidates))
		for _, candidate := range candidates {
			id, tab := candidate.id, candidate.tab
			snapshotted[id] = tab
			if err := a.snapshotTabDurable(tab); err != nil {
				slog.Warn("desktop: snapshot before removing workspace failed", "tab", id, "workspace", dir, "err", err)
				return fmt.Errorf("save current session before removing workspace: %w", err)
			}
		}

		a.mu.Lock()
		if a.workspaceHasRunningSessions(dir) {
			a.mu.Unlock()
			return fmt.Errorf("workspace has running sessions; stop them before removing")
		}
		for id, tab := range a.tabs {
			if tabInWorkspace(tab, dir) && snapshotted[id] != tab {
				a.mu.Unlock()
				return fmt.Errorf("workspace tabs changed while removing; retry")
			}
		}
		for _, candidate := range candidates {
			id, tab := candidate.id, candidate.tab
			if tab == nil || a.tabs[id] != tab || !tabInWorkspace(tab, dir) {
				continue
			}
			a.markTabRemovedLocked(tab)
			closeTabs = append(closeTabs, tab)
			delete(a.tabs, id)
			a.removeTabOrderLocked(id)
			if a.activeTabID == id {
				a.activeTabID = ""
			}
		}
		for key, tab := range a.detachedSessions {
			if !tabInWorkspace(tab, dir) {
				continue
			}
			closeDetached = append(closeDetached, tab)
			delete(a.detachedSessions, key)
		}
		if len(a.tabs) == 0 {
			fallback = a.createTabEntry("global", globalTabWorkspaceRoot(), "")
			fallback.TopicTitle = "Global"
			fallback.sink = &tabEventSink{tabID: fallback.ID, app: a, ctx: a.ctx}
			a.tabs[fallback.ID] = fallback
			a.tabOrder = append(a.tabOrder, fallback.ID)
			a.activeTabID = fallback.ID
		} else if a.activeTabID == "" {
			if ordered := a.orderedTabIDsLocked(); len(ordered) > 0 {
				a.activeTabID = ordered[0]
			}
		}
		a.saveTabsLocked()
		a.mu.Unlock()

		for _, tab := range closeTabs {
			a.closeTabRuntimeAdmissionHeld(tab)
		}
		for _, tab := range closeDetached {
			a.closeTabRuntimeAdmissionHeld(tab)
		}
		return nil
	}(); err != nil {
		return nil, err
	}
	return fallback, nil
}

// workspaceHasRunningSessions reports whether any tab bound to dir still has
// active runtime work. Callers hold a.mu.
func (a *App) workspaceHasRunningSessions(dir string) bool {
	for _, tab := range a.tabs {
		if tabInWorkspace(tab, dir) && tab.hasActiveRuntimeWork() {
			return true
		}
	}
	for _, tab := range a.detachedSessions {
		if tabInWorkspace(tab, dir) && tab.hasActiveRuntimeWork() {
			return true
		}
	}
	return false
}
