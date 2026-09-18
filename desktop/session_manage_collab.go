package main

import (
	"fmt"
	"strings"

	"reasonix/internal/agent"
)

// Session management for the collaboration tool set (task 170).
//
// Both operations are metadata-only: they write the topic-title store and the
// branch-meta sidecar, never the transcript, so they are safe for a session that
// is mid-turn (no writer lease is touched) and need no "close it first" dance.
// Grouping keys off the topic id, so renaming never moves a session out of its
// group and a move never changes how the session is addressed.

// renameCollabSession is the host half of the agent's rename_session tool. It
// reuses the desktop's own rename paths, so every listing surface follows:
//
//   - RenameTopic owns the topic-title store (it also covers catalog-only topics
//     that have no transcript and no open tab) and projects the title into the
//     open tab and the branch-meta sidecar;
//   - RenameSession is the transcript-path entry, used when the session was
//     never filed as a topic.
//
// Both emit the project-tree change, which is what makes the sidebar, search and
// the contact directory agree without a manual refresh.
func (a *App) renameCollabSession(topicID, sessionPath, title string) (agent.RenameSessionResult, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return agent.RenameSessionResult{}, fmt.Errorf("title is required")
	}
	topicID = strings.TrimSpace(topicID)
	sessionPath = strings.TrimSpace(sessionPath)
	result := agent.RenameSessionResult{
		TopicID:     topicID,
		SessionPath: sessionPath,
		Previous:    collabSessionTitle(sessionPath),
		Title:       title,
	}
	if topicID != "" {
		if err := a.RenameTopic(topicID, title); err == nil {
			result.ListingSynced = true
			return result, nil
		}
		// Fall through: the topic entry may be gone while the transcript lives on.
	}
	if sessionPath == "" {
		return result, fmt.Errorf("session %q is neither a known topic nor a transcript path", strings.TrimSpace(topicID))
	}
	if err := a.RenameSession(sessionPath, title); err != nil {
		return result, err
	}
	result.ListingSynced = true
	return result, nil
}

// collabSessionTitle reads a session's current display title from the
// branch-meta sidecar - the same source the contact directory lists - so a
// rename can report what it replaced.
func collabSessionTitle(sessionPath string) string {
	if strings.TrimSpace(sessionPath) == "" {
		return ""
	}
	meta, ok, err := agent.LoadBranchMeta(sessionPath)
	if err != nil || !ok {
		return ""
	}
	return strings.TrimSpace(meta.TopicTitle)
}

// moveCollabTopicToGroup is the host half of the agent's move_topic_to_group
// tool. It delegates to MoveTopicToGroup, which removes the session from its
// previous group before filing it into the target one, then publishes the
// metadata revision so the sidebar drops the old row and shows the new one
// immediately (the same signal RenameTopic relies on).
func (a *App) moveCollabTopicToGroup(topicID, sessionPath, scope, workspaceRoot, groupID, groupTitle string) (agent.MoveTopicToGroupResult, error) {
	topicID = strings.TrimSpace(topicID)
	if topicID == "" {
		return agent.MoveTopicToGroupResult{}, fmt.Errorf("session is not filed as a topic, so it cannot be grouped")
	}
	scope, root, err := a.collabGroupingTarget(topicID, scope, workspaceRoot)
	if err != nil {
		return agent.MoveTopicToGroupResult{}, err
	}
	group, removed, alreadyFiled, err := a.MoveTopicToGroup(scope, root, topicID, groupID, groupTitle)
	if err != nil {
		return agent.MoveTopicToGroupResult{}, err
	}
	// The sidebar keeps its groups from GetProjectGroups and reloads them when the
	// catalog publishes a metadata revision (reason "metadata"). Emitting the
	// metadata change - not just the tree change - is what makes the moved row
	// leave the old group and appear in the target group without a manual refresh.
	a.emitProjectTreeMetadataChanged()
	return agent.MoveTopicToGroupResult{
		TopicID:      topicID,
		SessionPath:  strings.TrimSpace(sessionPath),
		Scope:        scope,
		ProjectRoot:  root,
		GroupID:      group.ID,
		Group:        group.Title,
		RemovedFrom:  removed,
		AlreadyFiled: alreadyFiled,
	}, nil
}

// collabGroupingTarget normalizes the scope/root a session's groups live under.
// A caller talking about another session may not know either, so the topic's own
// location is used as the fallback; an unfiled location resolves to global, which
// is where the desktop files ungrouped collab sessions.
func (a *App) collabGroupingTarget(topicID, scope, workspaceRoot string) (string, string, error) {
	scope = strings.TrimSpace(scope)
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if scope == "" {
		if locatedScope, locatedRoot, ok := a.findTopicLocation(topicID); ok {
			scope, workspaceRoot = locatedScope, locatedRoot
		} else if registeredScope, registeredRoot, ok := topicScopeFromProjectsFile(topicID); ok {
			scope, workspaceRoot = registeredScope, registeredRoot
		} else {
			scope = "global"
		}
	}
	return normalizeOrganizationTarget(scope, workspaceRoot)
}

// topicScopeFromProjectsFile locates a topic that has neither a transcript nor an
// open tab: a catalog-only topic lives in the projects file alone, and that is
// exactly when the caller has no scope of its own to report. findTopicLocation
// cannot see it (it looks at tabs and the session directory), so without this
// step a move would file the session under Global instead of its project.
func topicScopeFromProjectsFile(topicID string) (string, string, bool) {
	topicID = strings.TrimSpace(topicID)
	if topicID == "" {
		return "", "", false
	}
	f := loadProjectsFile()
	for _, id := range f.GlobalTopics {
		if strings.TrimSpace(id) == topicID {
			return "global", "", true
		}
	}
	for _, project := range f.Projects {
		for _, id := range project.Topics {
			if strings.TrimSpace(id) == topicID {
				return "project", project.Root, true
			}
		}
	}
	return "", "", false
}
