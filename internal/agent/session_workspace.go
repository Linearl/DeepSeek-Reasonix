package agent

import (
	"os"
	"path/filepath"
	"strings"
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
