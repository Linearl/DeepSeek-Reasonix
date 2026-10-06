package agent

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SessionWorkspaceRoot resolves the project directory a saved session belongs
// to, from the branch meta the frontends persist alongside the transcript
// (desktop tabs write meta.WorkspaceRoot on every topic binding; global-scope
// sessions deliberately carry an empty root).
//
// The result is the session-cwd base for task 545: callers pin the runtime
// workspace root to it so relative paths and git commands resolve against the
// session's own project instead of the process working directory. Task 546
// (new-session "reuse the latest session's directory") must share this exact
// resolution — two cwd semantics would repeat task 36's parallel-mechanisms
// mistake.
//
// The second return is false when the session carries no usable root: missing
// meta, empty or relative workspace_root (global scope / legacy transcripts),
// or a root that no longer exists on disk. Callers fall back to their previous
// resolution in that case.
func SessionWorkspaceRoot(sessionPath string) (string, bool) {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return "", false
	}
	meta, ok, err := LoadBranchMeta(sessionPath)
	if err != nil || !ok {
		return "", false
	}
	root := strings.TrimSpace(meta.WorkspaceRoot)
	if root == "" {
		return "", false
	}
	// The desktop writers only persist absolute roots; a relative value would
	// silently re-anchor to whatever cwd the reader happens to have.
	if !filepath.IsAbs(root) {
		return "", false
	}
	root = filepath.Clean(root)
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return "", false
	}
	return root, true
}

// LatestSessionWorkspace describes the most recently active session that
// declares a project workspace root. Task 546 (the desktop new-session option
// "reuse the latest session's directory") consumes it, and the usability
// verdict comes from SessionWorkspaceRoot above — the same single cwd
// semantics, not a parallel mechanism.
type LatestSessionWorkspace struct {
	SessionPath    string
	WorkspaceRoot  string // as declared by the session meta, cleaned; may point at a vanished directory
	Usable         bool   // the declared root still exists on this machine
	LastActivityAt time.Time
	CustomTitle    string
	TopicTitle     string
}

// LatestSessionWorkspaceRoot scans the given session stores (each a
// per-project or global session directory) and picks the most recently active
// session that declares a project root.
//
// Selection criteria, fixed for task 546:
//   - Recency is SessionOrderInfo.LastActivityAt: the branch meta's UpdatedAt
//     when persisted, otherwise the newer of the transcript checkpoint mtime
//     and the event-log mtime. This folds "event log last write" into the
//     ordering without decoding any transcript.
//   - Sessions whose meta declares no workspace root (global scope by
//     convention, or legacy transcripts without meta) are skipped: they have
//     no project directory to reuse, so the scan continues to the next most
//     recent session instead of settling on the global workspace.
//   - The first session with a declared root wins, even when that directory
//     no longer exists: Usable=false reports the stale root and the caller
//     must fall back visibly (task 546 forbids a silent fallback). Directories
//     that vanished typically are deletions/moves or sessions synced from
//     another machine — either way the path is not reusable on this machine.
//   - Remote (GC) sessions never appear here: callers only pass local stores.
func LatestSessionWorkspaceRoot(dirs []string) (LatestSessionWorkspace, bool) {
	var merged []SessionOrderInfo
	seen := make(map[string]bool)
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		infos, err := ListSessionOrder(dir)
		if err != nil {
			continue // unreadable store: skip it, the remaining stores may still resolve
		}
		for _, info := range infos {
			if seen[info.Path] {
				continue
			}
			seen[info.Path] = true
			merged = append(merged, info)
		}
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].LastActivityAt.Equal(merged[j].LastActivityAt) {
			return merged[i].Path < merged[j].Path
		}
		return merged[i].LastActivityAt.After(merged[j].LastActivityAt)
	})
	for _, info := range merged {
		declared := strings.TrimSpace(info.WorkspaceRoot)
		if declared == "" {
			continue
		}
		_, usable := SessionWorkspaceRoot(info.Path)
		return LatestSessionWorkspace{
			SessionPath:    info.Path,
			WorkspaceRoot:  filepath.Clean(declared),
			Usable:         usable,
			LastActivityAt: info.LastActivityAt,
			CustomTitle:    info.CustomTitle,
			TopicTitle:     info.TopicTitle,
		}, true
	}
	return LatestSessionWorkspace{}, false
}
