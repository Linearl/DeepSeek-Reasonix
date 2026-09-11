package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/store"
)

// RecoveryCopyView describes one recovery copy sitting beside a conversation.
//
// Listing and measuring are deliberately separate. Measuring means reading and
// comparing whole transcripts, and one copy on this machine is 128 MB, so doing it
// while building the list leaves the panel stuck on a spinner. The list therefore
// carries only what a directory read already knows - name, size, mtime - and the
// counts arrive when the user asks for them.
type RecoveryCopyView struct {
	Path string `json:"path"`
	// Bytes and Modified come from the directory entry, so they cost nothing.
	Bytes    int64  `json:"bytes"`
	Modified string `json:"modified"`
	// Messages, Shared and Unique are only meaningful once Scanned is true.
	// Shared and Unique are the numbers a user needs before merging: a copy that
	// differs only in its opening system prompt is duplication, while one holding
	// a few hundred events of its own is work that would be lost by ignoring it.
	Messages int  `json:"messages"`
	Shared   int  `json:"shared"`
	Unique   int  `json:"unique"`
	Scanned  bool `json:"scanned"`
	// Orphan marks a copy whose canonical transcript is not on disk under the
	// expected name. It is another line rather than a fork of this conversation,
	// so the UI has to say so instead of offering a merge that cannot work.
	Orphan bool `json:"orphan,omitempty"`
}

// RecoveryCopyGroupView is one conversation with recovery copies beside it.
type RecoveryCopyGroupView struct {
	MainPath   string             `json:"mainPath"`
	MainLabel  string             `json:"mainLabel"`
	Directory  string             `json:"directory"`
	MainExists bool               `json:"mainExists"`
	Copies     []RecoveryCopyView `json:"copies"`
}

// ListRecoveryCopyGroups lists conversations that have recovery copies beside them.
//
// It scans the disk rather than asking the catalog on purpose. The point of the
// panel is reclaiming space, so it has to see what is actually on disk - including
// copies the catalog never indexed and copies whose canonical transcript is gone,
// which is precisely the state a cleanup has to be able to describe.
//
// This stays cheap: it reads names and sizes only, never transcripts.
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
		if len(groups[i].Copies) != len(groups[j].Copies) {
			return len(groups[i].Copies) > len(groups[j].Copies)
		}
		return groups[i].MainLabel < groups[j].MainLabel
	})
	return groups, nil
}

// ScanRecoveryCopyGroup measures one conversation's copies: how many messages each
// holds and how much of that the canonical transcript already covers.
//
// Kept out of the listing because it opens every transcript, which is slow enough
// to be worth asking for.
func (a *App) ScanRecoveryCopyGroup(mainPath string) (RecoveryCopyGroupView, error) {
	dir := filepath.Dir(mainPath)
	if group, ok := recoveryCopyGroupFor(dir, mainPath); ok {
		return group, nil
	}
	// The copy may live beside a transcript under a different name than first
	// assumed; fall back to reporting the group as it currently reads.
	return RecoveryCopyGroupView{MainPath: mainPath, Directory: dir, Copies: []RecoveryCopyView{}}, nil
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

// recoveryCopiesByMain groups one directory's recovery copies under their
// canonical transcripts, keeping only names and sizes.
func recoveryCopiesByMain(dir string) (map[string][]RecoveryCopyView, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	byMain := map[string][]RecoveryCopyView{}
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
		view := RecoveryCopyView{Path: full}
		if info, err := entry.Info(); err == nil {
			view.Bytes = info.Size()
			view.Modified = info.ModTime().Format(time.RFC3339)
		}
		byMain[mainPath] = append(byMain[mainPath], view)
	}
	return byMain, order, nil
}

// recoveryCopyGroupsIn lists one directory's groups without opening transcripts.
func recoveryCopyGroupsIn(dir string) ([]RecoveryCopyGroupView, error) {
	byMain, order, err := recoveryCopiesByMain(dir)
	if err != nil {
		return nil, err
	}
	out := []RecoveryCopyGroupView{}
	for _, mainPath := range order {
		group := RecoveryCopyGroupView{
			MainPath:  mainPath,
			Directory: dir,
			Copies:    byMain[mainPath],
		}
		group.MainLabel = recoveryLabel(mainPath)
		if _, err := os.Stat(mainPath); err == nil {
			group.MainExists = true
		}
		out = append(out, group)
	}
	return out, nil
}

// recoveryCopyGroupFor measures one group: message counts plus how much of each
// copy the canonical transcript already holds.
func recoveryCopyGroupFor(dir, mainPath string) (RecoveryCopyGroupView, bool) {
	byMain, _, err := recoveryCopiesByMain(dir)
	if err != nil {
		return RecoveryCopyGroupView{}, false
	}
	copies, ok := byMain[mainPath]
	if !ok {
		return RecoveryCopyGroupView{}, false
	}
	group := RecoveryCopyGroupView{
		MainPath:  mainPath,
		MainLabel: recoveryLabel(mainPath),
		Directory: dir,
		Copies:    make([]RecoveryCopyView, 0, len(copies)),
	}
	if _, err := os.Stat(mainPath); err == nil {
		group.MainExists = true
	}
	for _, copyView := range copies {
		copyView.Scanned = true
		if snapshot, ok := agent.LoadSessionContentSnapshot(copyView.Path); ok {
			copyView.Messages = snapshot.Len()
		}
		if overlap, ok := agent.SessionContentOverlap(mainPath, copyView.Path); ok {
			copyView.Shared = overlap.Shared
			copyView.Unique = overlap.Unique
		} else {
			// No canonical transcript to compare against: the copy stands alone.
			copyView.Orphan = true
			copyView.Unique = copyView.Messages
		}
		group.Copies = append(group.Copies, copyView)
	}
	return group, true
}

// recoveryLabel renders a transcript path as the session id a user recognizes,
// dropping the directory and the extension.
func recoveryLabel(path string) string {
	name := filepath.Base(path)
	return strings.TrimSuffix(name, ".jsonl")
}
