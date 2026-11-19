package main

import (
	"path/filepath"
	"strings"

	"reasonix/internal/agent"
)

// 本文件是 tl1710 的宿主面：把回收站的「列」与「还原」接给 agent 工具
// trash_list / trash_restore。两条通道都复用 UI Trash 页已有链路，不另起
// 第二套真相源——列表走 ListTrashedSessions（tl1474 装配链），还原走
// restoreSession（冲突预检 + 搬回 + 分组/话题索引恢复 + 树变更广播）。

// agentTrashList assembles the trash entries the trash_list tool reports.
// It rides the SAME listing chain the Trash page renders (ListTrashedSessions
// → listTrashedSessionBuckets), so the agent's view of the trash cannot drift
// from the user's; hasTurn is probed from the bucket transcript itself with
// the same probe delete_session's impact uses.
func (a *App) agentTrashList() ([]agent.TrashEntry, error) {
	metas := a.ListTrashedSessions()
	entries := make([]agent.TrashEntry, 0, len(metas))
	for _, m := range metas {
		title := strings.TrimSpace(m.Title)
		if title == "" {
			// Empty-shell buckets (tl1449 synthesis) carry no title; fall back
			// to the transcript stem so the caller still sees a stable name.
			title = strings.TrimSuffix(filepath.Base(m.Path), filepath.Ext(m.Path))
		}
		entries = append(entries, agent.TrashEntry{
			Title:         title,
			SessionPath:   m.Path,
			HasTurn:       agent.SessionTranscriptHasContent(m.Path),
			DeletedAt:     m.DeletedAt,
			RestoreUntil:  agent.TrashRestoreUntilManual,
			Scope:         m.Scope,
			WorkspaceRoot: m.WorkspaceRoot,
		})
	}
	return entries, nil
}

// agentTrashRestore restores one trashed session through the Trash page's own
// channel (RestoreSession → restoreSession): the destroy/open/cleanup-pending
// guards, the artifact conflict preflight, the topic-index/grouping heal and
// the tree-change broadcast all run — nothing about the restore is re-implemented
// here (task tl1710 件2; non-goal: no file-level bypass of the host).
func (a *App) agentTrashRestore(path string) error {
	return a.RestoreSession(path)
}
