package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/store"
)

// RecoveryCopyView describes one recovery copy sitting beside a conversation.
//
// Shared and Unique are the numbers a user needs before merging: a copy that
// differs only in its opening system prompt is duplication, while one holding a
// few hundred events of its own is work that would be lost by ignoring it. The
// earlier investigation found exactly that spread - one copy held 284 unique
// events while most differed by a single line.
type RecoveryCopyView struct {
	Path     string `json:"path"`
	Messages int    `json:"messages"`
	Shared   int    `json:"shared"`
	Unique   int    `json:"unique"`
	// Orphan marks a copy whose canonical transcript is not on disk under the
	// expected name. It is another line rather than a fork of this conversation,
	// so the UI has to say so instead of offering a merge that cannot work.
	Orphan bool `json:"orphan,omitempty"`
}

// RecoveryCopyGroupView is one conversation with recovery copies beside it.
type RecoveryCopyGroupView struct {
	MainPath     string             `json:"mainPath"`
	Directory    string             `json:"directory"`
	MainMessages int                `json:"mainMessages"`
	Copies       []RecoveryCopyView `json:"copies"`
}

// ListRecoveryCopyGroups scans the session directories for conversations that have
// recovery copies beside them.
//
// It scans the disk rather than asking the catalog on purpose. The point of the
// panel is reclaiming space, so it has to see what is actually on disk - including
// copies the catalog never indexed and copies whose canonical transcript is gone,
// which is precisely the state a cleanup has to be able to describe.
func (a *App) ListRecoveryCopyGroups() ([]RecoveryCopyGroupView, error) {
	groups := []RecoveryCopyGroupView{}
	for _, dir := range recoveryScanDirectories() {
		found, err := recoveryCopyGroupsIn(dir)
		if err != nil {
			// A directory that cannot be read must not fail the whole panel: the
			// other projects still have something to show.
			continue
		}
		groups = append(groups, found...)
	}
	sort.Slice(groups, func(i, j int) bool {
		return len(groups[i].Copies) > len(groups[j].Copies)
	})
	return groups, nil
}

// recoveryScanDirectories lists the session directories that can hold copies: the
// shared one plus one per project. A missing parent is not an error.
func recoveryScanDirectories() []string {
	seen := map[string]struct{}{}
	out := []string{}
	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return
		}
		if _, ok := seen[dir]; ok {
			return
		}
		seen[dir] = struct{}{}
		out = append(out, dir)
	}
	add(config.SessionDir())
	if root := strings.TrimSpace(config.MemoryUserDir()); root != "" {
		entries, err := os.ReadDir(filepath.Join(root, "projects"))
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					add(filepath.Join(root, "projects", entry.Name(), "sessions"))
				}
			}
		}
	}
	return out
}

// recoveryCopyGroupsIn groups one directory's recovery copies under their
// canonical transcripts.
func recoveryCopyGroupsIn(dir string) ([]RecoveryCopyGroupView, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	byMain := map[string][]string{}
	order := []string{}
	for _, entry := range entries {
		if entry.IsDir() || !store.IsSessionTranscriptName(entry.Name()) {
			continue
		}
		full := filepath.Join(dir, entry.Name())
		if !agent.LooksLikeRecoveryFilename(full) {
			continue
		}
		parentID, ok := agent.RecoveryFilenameParentID(full)
		if !ok || strings.TrimSpace(parentID) == "" {
			continue
		}
		mainPath := filepath.Join(dir, parentID+".jsonl")
		if _, seen := byMain[mainPath]; !seen {
			order = append(order, mainPath)
		}
		byMain[mainPath] = append(byMain[mainPath], full)
	}

	out := []RecoveryCopyGroupView{}
	for _, mainPath := range order {
		group := RecoveryCopyGroupView{
			MainPath:  mainPath,
			Directory: dir,
			Copies:    []RecoveryCopyView{},
		}
		if main, ok := agent.LoadSessionContentSnapshot(mainPath); ok {
			group.MainMessages = main.Len()
		}
		for _, copyPath := range byMain[mainPath] {
			view := RecoveryCopyView{Path: copyPath}
			if snapshot, ok := agent.LoadSessionContentSnapshot(copyPath); ok {
				view.Messages = snapshot.Len()
			}
			if overlap, ok := agent.SessionContentOverlap(mainPath, copyPath); ok {
				view.Shared = overlap.Shared
				view.Unique = overlap.Unique
			} else {
				// No canonical transcript to compare against: the copy stands alone.
				view.Orphan = true
				view.Unique = view.Messages
			}
			group.Copies = append(group.Copies, view)
		}
		out = append(out, group)
	}
	return out, nil
}
