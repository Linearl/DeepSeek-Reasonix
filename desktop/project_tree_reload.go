package main

// tl1363：手动「重载会话列表」（用户 20261102 拍板：项目/Global 右键菜单、
// 「复制路径」正上方）。应用此前没有重载对话列表的出口——后台归档（如
// Heartbeat 一次性会话轮换）摘掉 sidecar 引用后，前端驻留行与注册残留只
// 能靠重启收敛；本入口一次点击完成四步对账：
//
//  1. 影子条目扫除——零会话绑定的纯注册残留（tl1383 同族结论：注册在
//     desktop-projects.json 但磁盘无主转录、topic-state 无真实历史）。
//     判据沿用 1145 家族唯一裁决体 heartbeatTopicIsShellOnApp，保护面同族：
//     活/挂起 tab 引用的 topic 绝不动（会话可能正在创建），置顶条目不自动
//     移出（留人处理，可走既有归档菜单）。写路径走 deleteTopic 既有收敛
//     （注册+墓碑+分组+标题表一次清），墓碑保证修复扫描不复活。
//     legacy_* + topic_* 同题双注册（tl1383 实锤：legacy 有绑定、topic 零
//     绑定）在扫除后自然合并为一条——零绑定的 topic_* 残留被移除，有绑定
//     的 legacy 行原样保留。
//  2. 注册投影同步——清完的 sidecar 一次有界同步进 catalog 元数据投影，
//     让点击后第一页拉取就是真实态（不等 30s 元数据 tick）。
//  3. 重挂活会话——本 scope 的每个 catalog 目录走既有单飞对账
//     （requestSessionCatalogReconcile：legacy 迁移补登 + ReconcileDirectory
//     + 副本清扫，与归档路径同一原语），磁盘上的活会话重新挂上树。
//  4. 发侧栏唯一认的信号——reason="metadata" 的 project-tree:changed-v2
//     事件（20261009 清理战役实证：侧栏刷新只认它）。
//
// scope 语义与右键菜单对齐：点 Global 行清 Global 注册，点项目行清该项目
// 注册——逐文件夹手动对账，不做全库扫除（blast radius 与菜单语义一致）。

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"reasonix/internal/sessioncatalog"
)

// ProjectTreeReloadResult is the reload receipt: what was swept, what was
// protected, and how many directories re-mounted. RemovedTopicIDs is sorted
// for stable tests and logs.
type ProjectTreeReloadResult struct {
	Scope             string   `json:"scope"`
	WorkspaceRoot     string   `json:"workspaceRoot,omitempty"`
	RemovedTopicIDs   []string `json:"removedTopicIds"`
	SkippedPinned     int      `json:"skippedPinned"`
	SkippedReferenced int      `json:"skippedReferenced"`
	ReconciledDirs    int      `json:"reconciledDirs"`
}

// ReloadProjectTreeSessions is the manual refresh exit behind the sidebar's
// 「重载会话列表」 menu item. Errors from individual removals never abort the
// reload: one wedged topic must not keep the other residues from converging —
// each failure logs and stays for the next reload.
func (a *App) ReloadProjectTreeSessions(scope, workspaceRoot string) (ProjectTreeReloadResult, error) {
	scope, workspaceRoot, err := normalizeOrganizationTarget(scope, workspaceRoot)
	if err != nil {
		return ProjectTreeReloadResult{}, err
	}
	result := ProjectTreeReloadResult{Scope: scope, WorkspaceRoot: workspaceRoot, RemovedTopicIDs: []string{}}

	// Phase 1 — shadow sweep (zero-binding registration residue).
	referenced := a.tabReferencedTopicIDs()
	for topicID, ref := range registryShellRefs() {
		if !reloadScopeMatchesRef(scope, workspaceRoot, ref) {
			continue
		}
		if referenced[topicID] {
			// A live or detached tab holds this topic — the session may be
			// mid-creation with no transcript yet. Never swept.
			result.SkippedReferenced++
			continue
		}
		if ref.pinned {
			// Same protection as the 1145 auto reconcile: a pinned entry is
			// user-curated state; the existing archive menu is its removal path.
			result.SkippedPinned++
			continue
		}
		if !heartbeatTopicIsShellOnApp(a, topicID, registryShellScopeRoot(ref)) {
			continue // real history or a session file exists — keep
		}
		if err := a.deleteTopic(topicID); err != nil {
			slog.Warn("desktop: session-list reload could not remove a shadow registration",
				"topic", topicID, "scope", ref.scope, "err", err)
			continue
		}
		result.RemovedTopicIDs = append(result.RemovedTopicIDs, topicID)
	}

	// Phase 2 — push the now-clean sidecar into the catalog metadata projection
	// synchronously (bounded), so the first page pull after this call already
	// reflects the truth instead of waiting for the 30s metadata tick.
	catalog := a.sessionCatalog.Load()
	if catalog != nil && !a.shuttingDown.Load() {
		ctx, cancel := context.WithTimeout(a.bootContext(), sessionCatalogMetadataSyncTimeout)
		if syncErr := a.syncSessionCatalogMetadata(ctx, catalog); syncErr != nil && !errors.Is(syncErr, context.Canceled) {
			slog.Debug("desktop: session-list reload metadata sync", "err", syncErr)
		}
		cancel()
	}

	// Phase 3 — remount live sessions: the existing single-flight reconcile per
	// catalog directory of this scope (legacy migration + directory scan +
	// copy sweep; the same primitives the archive path rides).
	for _, target := range a.sessionCatalogTargets() {
		if !reloadScopeMatchesTarget(scope, workspaceRoot, target) {
			continue
		}
		if a.requestSessionCatalogReconcile(target.Path) {
			result.ReconciledDirs++
		}
	}

	// Phase 4 — the one signal the sidebar honors: a reason="metadata" v2 tree
	// event (same contract as every manage_sessions write).
	revision := uint64(0)
	if catalog != nil {
		revision = catalog.Status().Revision
	}
	a.emitProjectTreeChangedV2(revision, nil, "metadata")
	a.requestSessionCatalogMetadataSync()
	return result, nil
}

// reloadScopeMatchesRef reports whether a registration entry belongs to the
// reloaded scope. Global reloads sweep only global entries; a project reload
// sweeps only that project's entries.
func reloadScopeMatchesRef(scope, workspaceRoot string, ref registryShellRef) bool {
	if scope == "global" {
		return ref.scope == "global"
	}
	return ref.scope == "project" && sameProjectRoot(strings.TrimSpace(ref.root), workspaceRoot)
}

// reloadScopeMatchesTarget is the directory-target half of the same scoping.
func reloadScopeMatchesTarget(scope, workspaceRoot string, target sessioncatalog.DirectoryTarget) bool {
	if scope == "global" {
		return target.Scope == "global"
	}
	return target.Scope == "project" && sameProjectRoot(strings.TrimSpace(target.WorkspaceRoot), workspaceRoot)
}
