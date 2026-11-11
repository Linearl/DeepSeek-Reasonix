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
	latest, ok := agent.LatestSessionWorkspaceRoot(a.knownSessionDirs())
	return lastSessionWorkspaceInfo(latest, ok, a.recoveredSessionLabelSuffix())
}

func lastSessionWorkspaceInfo(latest agent.LatestSessionWorkspace, ok bool, recoveredSuffix string) LastSessionWorkspaceInfo {
	if !ok {
		return LastSessionWorkspaceInfo{}
	}
	return LastSessionWorkspaceInfo{
		Path:           latest.WorkspaceRoot,
		Usable:         latest.Usable,
		SessionTitle:   latestSessionDisplayTitle(latest.CustomTitle, latest.TopicTitle, latest.Preview, latest.SessionPath, recoveredSuffix, latest.Recovered),
		LastActivityAt: latest.LastActivityAt.UnixMilli(),
	}
}

// latestSessionDisplayTitle picks a best-effort label so the user can confirm
// whose directory they are about to reuse: the user-chosen title, then the
// topic title, then the first-message preview, then a time label carved from
// the transcript name; an empty result means "nothing worth showing" and the
// FE omits the line entirely (tl1330: the raw transcript id never surfaces —
// it reads as a leaked model string).
//
// tl1547 (C2/C3): the chain gains the preview tier and the localized
// recovered suffix — a recovered branch inherits the parent's titles, so two
// rows used to collide into the same unreadable name, exactly the sibling
// collision tl1330 added the sidebar suffix for. Every tier carries the
// suffix, mirroring sessionDisplayLabelForPath.
func latestSessionDisplayTitle(customTitle, topicTitle, preview, sessionPath, recoveredSuffix string, recovered bool) string {
	label := ""
	if title := strings.TrimSpace(customTitle); title != "" {
		label = title
	} else if title := strings.TrimSpace(topicTitle); title != "" {
		label = title
	} else if text := agent.UserPreviewText(strings.TrimSpace(preview)); text != "" {
		label = text
	} else {
		label = sessionStampLabelFromBase(strings.TrimSuffix(filepath.Base(sessionPath), filepath.Ext(sessionPath)))
	}
	if label != "" && recovered {
		label += recoveredSuffix
	}
	return label
}
