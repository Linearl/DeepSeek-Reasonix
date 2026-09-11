package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// RelocateProject repoints a project at a different folder on disk (task 47).
//
// A project's identity IS its root path, so a move is not a rewrite of one field: the
// same key is stored in the registry (desktop-projects.json, where PinnedProjects and
// SidebarOrder also hold paths) and in each session's branch-meta sidecar, where it
// decides which project a transcript belongs to. Rewriting only one side leaves a
// tree that disagrees with its own transcripts - sessions that vanish from the project
// or fall back to Global.
//
// Pass order is registry first, sessions second. If the session pass fails, the error
// carries how many already moved, so a retry is an informed decision instead of a
// guess: the registry already points at the new folder and the remaining sidecars
// still carry the old one.
func (a *App) RelocateProject(oldRoot, newRoot string) error {
	from := normalizeProjectRoot(oldRoot)
	to := normalizeProjectRoot(newRoot)
	if from == "" || to == "" {
		return fmt.Errorf("both the current and the new folder are required")
	}
	if sameProjectRoot(from, to) {
		return fmt.Errorf("the project already points at that folder")
	}
	if info, err := os.Stat(to); err != nil || !info.IsDir() {
		return fmt.Errorf("the new folder must exist and be a directory: %s", to)
	}

	// The registry pass carries the two rejections that depend on it: the project must
	// exist, and the new root must not already belong to another project (two projects
	// sharing a key would make every lookup ambiguous).
	var (
		found    bool
		conflict string
	)
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		index := projectIndexByRoot(f.Projects, from)
		if index < 0 {
			return false, nil
		}
		found = true
		if other := projectIndexByRoot(f.Projects, to); other >= 0 {
			// A user-facing refusal, not a write failure: report it through the closure
			// because updateProjectsFile only carries errors.
			conflict = strings.TrimSpace(f.Projects[other].Root)
			return false, nil
		}
		f.Projects[index].Root = to
		if next, changed := replacePathInList(f.PinnedProjects, from, to); changed {
			f.PinnedProjects = next
		}
		if next, changed := replacePathInList(f.SidebarOrder, from, to); changed {
			f.SidebarOrder = next
		}
		return true, nil
	}); err != nil {
		return fmt.Errorf("update the project registry: %w", err)
	}
	if !found {
		return fmt.Errorf("no project is registered at %s", from)
	}
	if conflict != "" {
		return fmt.Errorf("another project already uses that folder: %s", conflict)
	}

	moved, total, err := a.relocateSessionWorkspaceRoots(from, to)
	if err != nil {
		return fmt.Errorf("moved %d of %d session records before failing: %w", moved, total, err)
	}

	// The tree refreshes from the registry; tabs pick the new root up through the
	// existing workspace-root sync on their next load.
	a.emitProjectTreeMetadataChanged()
	return nil
}

// relocateSessionWorkspaceRoots rewrites the workspace root recorded in every
// transcript's branch meta that points at from, and reports how many it changed.
//
// Each sidecar is written on its own so an interrupted run leaves whole records
// behind: the ones already written point at the new folder and the rest still point
// at the old one, which the returned error makes visible.
func (a *App) relocateSessionWorkspaceRoots(from, to string) (moved, total int, err error) {
	dir := config.SessionDir()
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		// No session directory means nothing to rewrite, which is not a failure.
		return 0, 0, nil
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		sessionPath := filepath.Join(dir, entry.Name())
		meta, ok, loadErr := agent.LoadBranchMeta(sessionPath)
		if loadErr != nil || !ok {
			continue
		}
		if !sameProjectRoot(meta.WorkspaceRoot, from) {
			continue
		}
		total++
		meta.WorkspaceRoot = to
		if saveErr := agent.SaveBranchMetaPreserveUpdated(sessionPath, meta); saveErr != nil {
			return moved, total, saveErr
		}
		moved++
	}
	return moved, total, nil
}

// replacePathInList rewrites one root inside a path list, preserving order and every
// entry that does not match. It reports whether anything changed.
func replacePathInList(list []string, from, to string) ([]string, bool) {
	out := make([]string, len(list))
	copy(out, list)
	changed := false
	for i, entry := range list {
		if sameProjectRoot(entry, from) {
			out[i] = to
			changed = true
		}
	}
	return out, changed
}
