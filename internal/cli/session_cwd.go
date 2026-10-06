package cli

import (
	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// resumeWorkspaceRootOverride resolves the workspace root a resumed session's
// runtime should pin (task 545). Precedence:
//
//  1. explicitRoot (--dir): always wins verbatim — the user named the project.
//  2. The lab switch agent.experimental_session_cwd_follow (default OFF): when
//     off, the override is empty so boot keeps its process-cwd / git-root
//     fallback byte-identical to the pre-545 behavior.
//  3. The session's own persisted project root (branch meta workspace_root),
//     resolved by agent.SessionWorkspaceRoot; sessions without a usable root
//     (fresh, global-scope, root deleted) return empty and fall back as before.
//
// The same resolution is the shared semantics task 546 must reuse when a new
// session offers to inherit the latest session's directory.
func resumeWorkspaceRootOverride(explicitRoot, resumePath string, cfg *config.Config) string {
	if explicitRoot != "" {
		return explicitRoot
	}
	if resumePath == "" {
		return ""
	}
	if cfg == nil || !cfg.Agent.ExperimentalSessionCwdFollow {
		return ""
	}
	root, ok := agent.SessionWorkspaceRoot(resumePath)
	if !ok {
		return ""
	}
	return root
}
