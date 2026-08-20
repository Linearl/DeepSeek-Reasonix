package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/sandbox"
)

// Authorized write-directory management (desktop surface for #9167).
//
// These back the "authorized write directories" panel: querying what a project
// and the active session are currently allowed to write, and adding/removing
// entries. Project-scope writes persist into the project config
// [sandbox].allow_write (via SetProjectWriteAccess); session-scope writes
// apply to the active controller's in-memory session roots.

// WriteDirScope mirrors sandbox.ApprovalScope over the IPC integer boundary.
// Frontend sends 0 for project-level, 1 for session-level.
const (
	writeDirScopeProject = 0
	writeDirScopeSession = 1
)

// QueryAuthorizedWriteDirs returns the currently authorized write directories
// for the active workspace, split by scope: project (config allow_write) and
// session (active controller's session roots).
func (a *App) QueryAuthorizedWriteDirs() (project, session []string) {
	project = []string{}
	session = []string{}
	if a == nil {
		return project, session
	}
	_, ctrl := a.activeTabAndCtrl()
	if c, ok := ctrl.(*control.Controller); ok {
		p, s := c.QueryAuthorizedWriteDirs()
		project = nonNilSlice(p)
		session = nonNilSlice(s)
		return project, session
	}
	// No live controller: fall back to reading the project config allow_write.
	if root := a.activeWorkspaceRoot(); root != "" {
		if cfg, err := config.LoadForRootReadOnly(root); err == nil && cfg != nil {
			project = nonNilSlice(cfg.AllowWriteRoots())
		}
	}
	return project, session
}

// AddAuthorizedWriteDir adds a writable directory at the given scope
// (0=project, 1=session) for the active workspace/session.
func (a *App) AddAuthorizedWriteDir(scope int, dir string) error {
	if a == nil {
		return fmt.Errorf("no active app")
	}
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("directory is required")
	}
	_, ctrl := a.activeTabAndCtrl()
	if c, ok := ctrl.(*control.Controller); ok {
		s := approvalScopeFromWriteDir(scope)
		return c.AddAuthorizedWriteDir(s, dir)
	}
	// No controller: project-only fallback using the active workspace root.
	if scope != writeDirScopeProject {
		return fmt.Errorf("no active session for session-scope write directory")
	}
	root := a.activeWorkspaceRoot()
	if root == "" {
		return fmt.Errorf("no active workspace")
	}
	path := projectConfigPathForWriteAccess(root)
	project := []string{dir}
	if cfg, err := config.LoadForRootReadOnly(root); err == nil && cfg != nil {
		project = nonNilSlice(cfg.AllowWriteRoots())
	}
	if !containsWriteDir(project, dir) {
		project = append(project, dir)
	}
	return config.SetProjectWriteAccess(path, project, "")
}

// RemoveAuthorizedWriteDir removes a writable directory at the given scope
// (0=project, 1=session) for the active workspace/session.
func (a *App) RemoveAuthorizedWriteDir(scope int, dir string) error {
	if a == nil {
		return fmt.Errorf("no active app")
	}
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("directory is required")
	}
	_, ctrl := a.activeTabAndCtrl()
	if c, ok := ctrl.(*control.Controller); ok {
		s := approvalScopeFromWriteDir(scope)
		return c.RemoveAuthorizedWriteDir(s, dir)
	}
	if scope != writeDirScopeProject {
		return fmt.Errorf("no active session for session-scope write directory")
	}
	root := a.activeWorkspaceRoot()
	if root == "" {
		return fmt.Errorf("no active workspace")
	}
	path := projectConfigPathForWriteAccess(root)
	kept := []string{}
	if cfg, err := config.LoadForRootReadOnly(root); err == nil && cfg != nil {
		for _, p := range cfg.AllowWriteRoots() {
			if writeDirEqual(p, dir) {
				continue
			}
			kept = append(kept, p)
		}
	}
	return config.SetProjectWriteAccess(path, kept, "")
}

func approvalScopeFromWriteDir(scope int) sandbox.ApprovalScope {
	if scope == writeDirScopeSession {
		return sandbox.ApprovalScopeSession
	}
	return sandbox.ApprovalScopeProject
}

func nonNilSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// projectConfigPathForWriteAccess matches control's helper: workspaceRoot/
// reasonix.toml when a workspace is active, else user config.
func projectConfigPathForWriteAccess(workspaceRoot string) string {
	if workspaceRoot != "" {
		return filepath.Join(workspaceRoot, "reasonix.toml")
	}
	path := config.SourcePath()
	if path == "" {
		path = "reasonix.toml"
	}
	return path
}

func containsWriteDir(roots []string, target string) bool {
	for _, r := range roots {
		if writeDirEqual(r, target) {
			return true
		}
	}
	return false
}

func writeDirEqual(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return a == b
	}
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA == nil && errB == nil {
		return absA == absB
	}
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}
