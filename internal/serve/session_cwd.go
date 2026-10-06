package serve

import (
	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// sessionCwdFollowConfigForTest is the test seam for the switch lookup (same
// seam style as boot's gitRootWalk); production code never reassigns it.
var sessionCwdFollowConfigForTest = config.Load

// sessionCwdFollowRootOverride resolves the task-545 workspace-root pin for
// binding targetPath (busy /resume replacement builds): non-empty only when
// the lab switch agent.experimental_session_cwd_follow is on and the target
// session carries a valid persisted project root (branch meta workspace_root).
// The empty result keeps the inherited-root behavior byte-identical — the
// switch default is off, so closed-state sessions run exactly as before.
func sessionCwdFollowRootOverride(targetPath string) string {
	if targetPath == "" {
		return ""
	}
	cfg, err := sessionCwdFollowConfigForTest()
	if err != nil || !cfg.Agent.ExperimentalSessionCwdFollow {
		return ""
	}
	root, ok := agent.SessionWorkspaceRoot(targetPath)
	if !ok {
		return ""
	}
	return root
}
