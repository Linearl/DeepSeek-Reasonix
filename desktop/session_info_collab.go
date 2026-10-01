package main

// 任务 285（会话信息面工具补强）的宿主侧探针实现：desktop 把分组表、
// recovery 版本谱系与版本切换入口注入 agent 的 SessionCollabConfig，
// agent 工具在调用时求值（boot 快照不冻结）。数据全部复用既有解析器与
// 既有宿主入口——分组读 loadProjectsFile（与侧栏同一事实源），版本读
// GetSessionVersionState（与 UI「查看版本」同一函数），切换走
// SetActiveSessionVersion（既有 recovery 选择路径），不新造存储与机制。

import (
	"fmt"
	"os"
	"strings"

	"reasonix/internal/agent"
)

// sessionGroupTitleForTopic returns the title of the first group containing
// topicID, or "" — the same first-match rule the sidebar uses (normalizeGroups
// already dedups a topic across groups at write time).
func sessionGroupTitleForTopic(groups []desktopGroup, topicID string) string {
	topicID = strings.TrimSpace(topicID)
	if topicID == "" {
		return ""
	}
	for _, group := range groups {
		if group.Title == "" {
			continue
		}
		for _, id := range group.TopicIDs {
			if id == topicID {
				return group.Title
			}
		}
	}
	return ""
}

// collabSessionGroup answers "which sidebar group is this topic in" from the
// same projects file the sidebar renders: global groups first, then every
// project's groups.
func (a *App) collabSessionGroup(topicID string) (string, bool) {
	f := loadProjectsFile()
	if title := sessionGroupTitleForTopic(f.GlobalGroups, topicID); title != "" {
		return title, true
	}
	for _, project := range f.Projects {
		if title := sessionGroupTitleForTopic(project.Groups, topicID); title != "" {
			return title, true
		}
	}
	return "", false
}

// collabSessionVersions reports a conversation's recovery lineage through the
// exact view the UI version viewer consumes (GetSessionVersionState), sized by
// plain stat. ok=false when the host knows no lineage for the topic (catalog
// unavailable, single-version conversation) — the agent tool then says so
// instead of guessing.
func (a *App) collabSessionVersions(scope, workspaceRoot, topicID, sessionPath string) ([]agent.SessionVersionInfo, bool) {
	view := a.GetSessionVersionState(ProjectTopicKey{
		Scope:         scope,
		WorkspaceRoot: workspaceRoot,
		TopicID:       topicID,
		Path:          sessionPath,
	})
	if len(view.Lineage.Members) == 0 {
		return nil, false
	}
	members := view.Lineage.Members
	out := make([]agent.SessionVersionInfo, 0, len(members))
	for _, m := range members {
		info := agent.SessionVersionInfo{
			Path:        m.Path,
			Role:        m.Role,
			VersionKind: m.VersionKind,
			Selected:    m.Selected,
			Canonical:   m.Canonical,
			LastActive:  m.LastActivityAt,
		}
		info.VersionID = m.HeadID
		if info.VersionID == "" {
			info.VersionID = agent.BranchID(m.Path)
		}
		if s, err := os.Stat(m.Path); err == nil {
			info.SizeBytes = s.Size()
		}
		out = append(out, info)
	}
	return out, true
}

// collabAdoptSessionVersion switches the conversation's active version to
// versionID (a version id or exact path from collabSessionVersions) through
// the existing SetActiveSessionVersion path. The agent tool has already
// enforced confirm=true; this probe re-checks the version actually belongs to
// the conversation so a stale id cannot pick an arbitrary file.
func (a *App) collabAdoptSessionVersion(scope, workspaceRoot, topicID, sessionPath, versionID string) error {
	versionID = strings.TrimSpace(versionID)
	key := ProjectTopicKey{Scope: scope, WorkspaceRoot: workspaceRoot, TopicID: topicID, Path: sessionPath}
	view := a.GetSessionVersionState(key)
	for _, m := range view.Lineage.Members {
		id := m.HeadID
		if id == "" {
			id = agent.BranchID(m.Path)
		}
		if id == versionID || strings.EqualFold(strings.TrimSpace(m.Path), versionID) {
			return a.SetActiveSessionVersion(RecoveryPreferenceRequest{
				Scope:         scope,
				WorkspaceRoot: workspaceRoot,
				TopicID:       topicID,
				Path:          m.Path,
				HeadID:        m.HeadID,
			})
		}
	}
	return fmt.Errorf("adopt_session_version: version %q is not in topic %q's lineage (list_session_versions first)", versionID, topicID)
}
