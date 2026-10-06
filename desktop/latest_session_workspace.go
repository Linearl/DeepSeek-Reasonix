package main

import (
	"path/filepath"
	"strings"

	"reasonix/internal/agent"
)

// LastSessionWorkspaceInfo is the payload behind the new-session option
// "reuse the latest session's directory" (task 546). An empty Path means no
// recent session declares a project directory and the frontend must not offer
// the option at all. Usable=false means the declared directory no longer
// exists on this machine (deleted, moved, or synced from another device): the
// frontend may still show the entry but must fall back to the default
// workspace with a visible notice — never silently.
type LastSessionWorkspaceInfo struct {
	Path           string `json:"path"`
	Usable         bool   `json:"usable"`
	SessionTitle   string `json:"sessionTitle"`
	LastActivityAt int64  `json:"lastActivityAt"` // unix milliseconds
}

// LatestSessionWorkspace resolves the payload across every session store the
// desktop knows about (legacy config store, global workspace, registered
// projects, open/detached tabs). Remote-device sessions are not part of these
// local stores, so a workspace root is never carried across machines.
func (a *App) LatestSessionWorkspace() LastSessionWorkspaceInfo {
	return lastSessionWorkspaceInfo(agent.LatestSessionWorkspaceRoot(a.knownSessionDirs()))
}

func lastSessionWorkspaceInfo(latest agent.LatestSessionWorkspace, ok bool) LastSessionWorkspaceInfo {
	if !ok {
		return LastSessionWorkspaceInfo{}
	}
	return LastSessionWorkspaceInfo{
		Path:           latest.WorkspaceRoot,
		Usable:         latest.Usable,
		SessionTitle:   latestSessionDisplayTitle(latest.CustomTitle, latest.TopicTitle, latest.SessionPath),
		LastActivityAt: latest.LastActivityAt.UnixMilli(),
	}
}

// latestSessionDisplayTitle picks a best-effort label so the user can confirm
// whose directory they are about to reuse: the user-chosen title, then the
// topic title, then the transcript file name.
func latestSessionDisplayTitle(customTitle, topicTitle, sessionPath string) string {
	if title := strings.TrimSpace(customTitle); title != "" {
		return title
	}
	if title := strings.TrimSpace(topicTitle); title != "" {
		return title
	}
	return strings.TrimSuffix(filepath.Base(sessionPath), filepath.Ext(sessionPath))
}
