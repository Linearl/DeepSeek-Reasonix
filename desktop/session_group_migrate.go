package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/store"
)

// tl1560 第一阶段：分组右键「迁移到其他项目」——把 global 的一个会话分组
// （连同其全部成员对话）整体迁入目标项目。第二阶段（全局记忆迁移）由各对话
// 自行处理，不归本件。
//
// 编排（决策点：顺序取「能保证组与成员终态一致」的形态）：
//   ① 校验：分组在 global 花名册、目标项目存在、逐成员解析会话文件；
//   ② 活跃拦截：任一成员有活跃运行时工作即整组拒绝（归档 errTopicHasActiveWork
//      同款语义，无 force 通道——迁移不是破坏性操作，不值得开强制口）；
//   ③ 取两把全局变更锁（runtime mutation + session removal），关掉可见的
//      空闲成员 tab（stop_and_close，TrashTopicForce 同款先例：迁移要移动
//      会话文件与 lease 侧车，持有中的 tab 会锁住文件），再复查活跃门；
//   ④ 文件先搬：逐会话移动「转录 + 全部 stem 前缀侧车」（meta/事件日志/
//      lease/恢复副本等，前缀枚举天然覆盖未来新增侧车——tl1442 教训：手工
//      枚举侧车清单必有遗漏），到点后改写 .meta 的 scope/workspaceRoot；
//      逐会话登记回滚日志，任一步失败整体逆向回滚并中止（此时分组数据
//      尚未动过，磁盘终态与迁移动前一致）；
//   ⑤ 组后切：一次 updateProjectsFile 原子事务完成全部分组数据搬家——组
//      从 GlobalGroups 切入 Projects[target].Groups（两侧 GroupsRevision
//      各自乐观递增，并发写者纪律同 SaveSessionGroupsVersioned）、成员
//      topic 从 GlobalTopics/PinnedTopics 搬入目标项目（置顶随迁）、清
//      墓碑（显式登记复活语义）、成员集合与校验期快照不一致即拒绝；
//      事务失败同样回滚第 ④ 步的文件搬家；
//   ⑥ 收尾：topic 标题/来源/创建时间搬入目标 root 分册、global 侧记录
//      注销（标题在 .meta 有副本，失败仅降级显示，不致命）、注册表索引
//      同步、按目录发 catalog reconcile + 树事件。
//
// 失败语义只有一种：整体回滚、返回错误、磁盘回到迁移动前形态（标题分册
// 的先拷后删除外——目标侧拷贝记录在回滚时尽力清除）。

var (
	// errGroupMigrateActiveWork：组内成员仍有活跃运行时。前端在成员 running
	// 时禁用入口，这里是并发窗口的后端兜底；不带 forceable 标记（迁移无
	// force 通道，见上）。
	errGroupMigrateActiveWork = errors.New("a session in this group is still working — wait for it to finish before migrating the group")
	// errGroupMigrateBusy：全局变更锁被占（重建/归档等正在搬会话）。下一步
	// （稍后重试）真实存在：重试入口就是同一菜单项。
	errGroupMigrateBusy = errors.New("another session change is in progress — wait a moment and retry the migration")
)

// SessionGroupMigrationResult is the Wails-facing receipt of one group
// migration. Returned only on full success.
type SessionGroupMigrationResult struct {
	GroupID             string   `json:"groupId"`
	GroupTitle          string   `json:"groupTitle"`
	TargetWorkspaceRoot string   `json:"targetWorkspaceRoot"`
	MigratedTopics      []string `json:"migratedTopics"`
	MovedSessions       int      `json:"movedSessions"`
}

// groupMigrationTarget is one session file planned for the move: its current
// directory and canonical path. Built during validation, consumed by the move
// phase.
type groupMigrationTarget struct {
	dir         string
	sessionPath string
	key         string
}

// MigrateSessionGroupToProject moves one global session group — the group
// shell plus every member conversation — into the target project.
//
// The source scope is always global: the entry point exists only on the
// global section's group menu (迁出 global). targetWorkspaceRoot must name an
// existing project.
func (a *App) MigrateSessionGroupToProject(groupID, targetWorkspaceRoot string) (SessionGroupMigrationResult, error) {
	groupID = strings.TrimSpace(groupID)
	targetWorkspaceRoot = normalizeProjectRoot(targetWorkspaceRoot)
	result := SessionGroupMigrationResult{MigratedTopics: []string{}}
	if groupID == "" {
		return result, fmt.Errorf("groupId is required")
	}
	if targetWorkspaceRoot == "" {
		return result, fmt.Errorf("workspaceRoot is required")
	}

	// ① 校验。分组读 raw 文件 + heartbeat 注入视图（定时任务组只存在于注入
	// 视图，磁盘上可能还没有壳——两种形态都要能迁）。
	f := loadProjectsFile()
	ti := projectIndexByRoot(f.Projects, targetWorkspaceRoot)
	if ti < 0 {
		return result, fmt.Errorf("project %q not found", targetWorkspaceRoot)
	}
	injected := a.fileHeartbeatTopics(nonNilGroups(f.GlobalGroups))
	group := desktopGroup{}
	for i := range injected {
		if strings.EqualFold(strings.TrimSpace(injected[i].ID), groupID) {
			group = injected[i]
			break
		}
	}
	if group.ID == "" {
		return result, fmt.Errorf("group %q not found in global groups", groupID)
	}
	members := append([]string(nil), group.TopicIDs...)
	if len(members) > maxSessionGroupTopics {
		return result, fmt.Errorf("group %q has too many topics", groupID)
	}
	result.GroupID = group.ID
	result.GroupTitle = group.Title
	result.TargetWorkspaceRoot = f.Projects[ti].Root

	// 目标项目花名册 id 撞车（uuid 空间里极罕见，但撞上就是静默并组）。
	for _, g := range f.Projects[ti].Groups {
		if strings.EqualFold(strings.TrimSpace(g.ID), group.ID) {
			return result, fmt.Errorf("target project already has a group id %q (%s); dissolve or rename it first", group.ID, g.Title)
		}
	}

	// 逐成员解析待搬会话文件（只认 global 目录；project 域的迷路会话不碰）。
	srcDirs := globalSessionDirs()
	type memberPlan struct {
		topicID string
		targets []groupMigrationTarget
	}
	plans := make([]memberPlan, 0, len(members))
	seenTargets := map[string]bool{}
	for _, topicID := range members {
		topicID = strings.TrimSpace(topicID)
		if topicID == "" {
			continue
		}
		plan := memberPlan{topicID: topicID}
		for _, dir := range srcDirs {
			for _, match := range topicSessionMatches(dir, topicID) {
				sessionPath, key, err := validateSessionPath(dir, match.path)
				if err != nil {
					return result, fmt.Errorf("topic %q: %w", topicID, err)
				}
				id := sessionRuntimeKey(sessionPath)
				if id == "" {
					id = dir + "\x00" + sessionPath
				}
				if seenTargets[id] {
					continue
				}
				meta, ok, err := agent.LoadBranchMeta(sessionPath)
				if err != nil || !ok {
					return result, fmt.Errorf("topic %q: session meta unreadable: %s", topicID, sessionPath)
				}
				if meta.DefaultScope() != "global" {
					// 迷路会话（meta 已指向某项目）：不属于本分组迁移的成员，
					// 跳过并留痕，不阻塞整组。
					slog.Warn("desktop: group migration skips a foreign-scope session",
						"topic", topicID, "scope", meta.DefaultScope(), "root", meta.WorkspaceRoot)
					continue
				}
				if agent.IsCleanupPending(sessionPath) {
					slog.Warn("desktop: group migration skips a cleanup-pending session", "topic", topicID)
					continue
				}
				if sessionTrashTargetFileMissing(sessionPath) {
					// 转录已消失的 meta 残留：搬家只会制造僵尸，留给归档/对账链。
					slog.Warn("desktop: group migration skips a missing-transcript session", "topic", topicID)
					continue
				}
				seenTargets[id] = true
				plan.targets = append(plan.targets, groupMigrationTarget{dir: dir, sessionPath: sessionPath, key: key})
			}
		}
		plans = append(plans, plan)
	}

	// ② 活跃拦截（整组语义：任一成员活跃 → 拒绝）。
	if err := a.groupMigrateActiveGate(members); err != nil {
		return result, err
	}

	// ③ 全局变更锁 + 关空闲可见 tab + 复查活跃门。
	releaseRuntime, ok := a.tryLockRuntimeMutation("migrate-group")
	if !ok {
		return result, errGroupMigrateBusy
	}
	defer releaseRuntime()
	if !a.sessionRemovalMu.TryLock() {
		return result, errGroupMigrateBusy
	}
	defer a.sessionRemovalMu.Unlock()

	for _, topicID := range members {
		a.closeTopicTabsForForceArchive(topicID)
	}
	if err := a.groupMigrateActiveGate(members); err != nil {
		return result, err
	}

	// ④ 文件先搬（带回滚日志）。
	dstDir := desktopSessionDir(result.TargetWorkspaceRoot)
	journal := &groupMigrationJournal{}
	for _, plan := range plans {
		for _, target := range plan.targets {
			if _, err := a.migrateSessionArtifacts(target.dir, dstDir, target.sessionPath, result.TargetWorkspaceRoot, journal); err != nil {
				journal.rollback()
				a.emitMigrationDirs(srcDirs, dstDir)
				return result, fmt.Errorf("topic %q: %w", plan.topicID, err)
			}
			invalidateTopicSessionIndex(target.dir)
			invalidateTopicSessionIndex(dstDir)
			a.removeSessionCatalogPath(target.sessionPath, "group_migrated")
			result.MovedSessions++
		}
		result.MigratedTopics = append(result.MigratedTopics, plan.topicID)
	}

	// ⑤ 组后切：一次原子事务（组切换 + topic 注册搬家 + 墓碑清理 + 置顶随迁）。
	// 成员集合必须与校验期快照一致——并发加成员的迁移是病态场景，宁可整体
	// 回滚让用户重试，也不留下跨 scope 混注册。
	moved := false
	err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		ti := projectIndexByRoot(f.Projects, result.TargetWorkspaceRoot)
		if ti < 0 {
			return false, fmt.Errorf("project %q not found", result.TargetWorkspaceRoot)
		}
		// 心跳托管壳（磁盘上还没有的定时任务组）：目标侧现建。
		ri := -1
		for i := range f.GlobalGroups {
			if strings.EqualFold(strings.TrimSpace(f.GlobalGroups[i].ID), group.ID) {
				ri = i
				break
			}
		}
		for _, g := range f.Projects[ti].Groups {
			if strings.EqualFold(strings.TrimSpace(g.ID), group.ID) {
				return false, fmt.Errorf("target project already has a group id %q (%s)", group.ID, g.Title)
			}
		}
		if ri < 0 {
			f.Projects[ti].Groups = append(f.Projects[ti].Groups, desktopGroup{ID: group.ID, Title: group.Title, TopicIDs: members})
			f.Projects[ti].GroupsRevision++
			moved = true
		} else {
			roster := f.GlobalGroups[ri]
			if !sameStringList(roster.TopicIDs, members) {
				return false, fmt.Errorf("group %q membership changed during migration; retry", group.ID)
			}
			f.GlobalGroups = append(f.GlobalGroups[:ri], f.GlobalGroups[ri+1:]...)
			f.GlobalGroupsRevision++
			f.Projects[ti].Groups = append(f.Projects[ti].Groups, roster)
			f.Projects[ti].GroupsRevision++
			moved = true
		}
		for _, topicID := range members {
			if containsDesktopString(f.GlobalPinnedTopics, topicID) {
				f.GlobalPinnedTopics = removeString(f.GlobalPinnedTopics, topicID)
				f.Projects[ti].PinnedTopics = prependUniqueString(f.Projects[ti].PinnedTopics, topicID)
			}
			f.GlobalTopics = removeString(f.GlobalTopics, topicID)
			// 显式登记复活：清墓碑（registerTopic 前插模式同款语义）。
			f.DeletedTopics = removeString(f.DeletedTopics, topicID)
			f.Projects[ti].Topics = prependUniqueString(f.Projects[ti].Topics, topicID)
		}
		// 落盘前整册校验（SaveSessionGroups 同款纪律）：写坏的花名册不该存在。
		if err := validateSessionGroups(normalizeGroups(f.GlobalGroups)); err != nil {
			return false, err
		}
		if err := validateSessionGroups(normalizeGroups(f.Projects[ti].Groups)); err != nil {
			return false, err
		}
		return true, nil
	})
	if err != nil || !moved {
		journal.rollback()
		a.undoMigratedTopicState(members, result.TargetWorkspaceRoot)
		a.emitMigrationDirs(srcDirs, dstDir)
		if err == nil {
			// moved=false 理论不可达（每个分支都置位或返回错误）；保守防御。
			err = fmt.Errorf("group %q migration did not apply", group.ID)
		}
		return result, err
	}

	// ⑥ 收尾：topic 状态分册（标题/来源/创建时间）global → target，global 侧
	// 注销；注册表同步；目录级 reconcile + 树事件。
	a.moveTopicStateToRoot(members, result.TargetWorkspaceRoot)
	syncTopicRegistryIndex()
	a.emitMigrationDirs(srcDirs, dstDir)

	slog.Info("desktop: session group migrated to project",
		"group", group.ID, "topics", len(result.MigratedTopics),
		"sessions", result.MovedSessions, "target", result.TargetWorkspaceRoot)
	return result, nil
}

// emitMigrationDirs schedules the catalog reconciles and the tree event for
// every directory a migration (or its rollback) touched. Both success and
// failure paths converge here: the sidebar must re-read disk truth either way.
func (a *App) emitMigrationDirs(srcDirs []string, dstDir string) {
	dirs := append([]string(nil), srcDirs...)
	dirs = append(dirs, dstDir)
	a.emitProjectTreeChangedForSessionDirs(dirs...)
}

// groupMigrateActiveGate refuses while any member topic has active runtime
// work (same predicate the archive gate uses).
func (a *App) groupMigrateActiveGate(members []string) error {
	for _, topicID := range members {
		if a.topicHasActiveRuntimeWork(topicID) {
			return errGroupMigrateActiveWork
		}
	}
	return nil
}

// globalSessionDirs enumerates the session directories that can hold
// global-scope sessions (the same pair the catalog targets use), deduped for
// case-insensitive volumes.
func globalSessionDirs() []string {
	out := []string{}
	seen := map[string]bool{}
	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return
		}
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		key := strings.ToLower(dir)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, dir)
	}
	add(config.SessionDir())
	add(desktopSessionDir(globalWorkspaceRoot()))
	return out
}

// groupMigrationJournal records completed artifact moves so a mid-migration
// failure can restore the exact pre-move disk state. Moves are recorded after
// they complete; rollback walks the journal in reverse. A moved .meta carries
// its pre-move scope/workspaceRoot snapshot — the rewrite-to-target happens
// after the move, so a rollback must restore the scope too, not just the file.
type groupMigrationJournal struct {
	entries []groupMigrationEntry
}

type groupMigrationEntry struct {
	from    string
	to      string
	hasMeta bool
	meta    agent.BranchMeta
}

func (j *groupMigrationJournal) record(entry groupMigrationEntry) {
	j.entries = append(j.entries, entry)
}

func (j *groupMigrationJournal) rollback() {
	for i := len(j.entries) - 1; i >= 0; i-- {
		entry := j.entries[i]
		if err := moveFileOrDir(entry.to, entry.from); err != nil {
			slog.Warn("desktop: group migration rollback failed", "from", entry.to, "to", entry.from, "err", err)
			continue
		}
		if entry.hasMeta {
			// entry.from 是 .meta 文件路径；restoreMetaScope 收会话路径
			// （内部推导 <session>.meta）。".meta" 尾换算与 appendMissingFileTrashTargets
			// 同款（SessionMeta 拼在全转录路径之后）。
			owner := strings.TrimSuffix(entry.from, ".meta")
			if err := restoreMetaScope(owner, entry.meta); err != nil {
				slog.Warn("desktop: group migration rollback could not restore meta scope", "to", entry.from, "err", err)
			}
		}
		invalidateTopicSessionIndex(filepath.Dir(entry.to))
		invalidateTopicSessionIndex(filepath.Dir(entry.from))
	}
	j.entries = nil
}

// restoreMetaScope puts one moved-back .meta back to its pre-move
// scope/workspaceRoot (the same locked read-modify-write the forward
// rewrite uses).
func restoreMetaScope(ownerPath string, snapshot agent.BranchMeta) error {
	unlock, lockErr := agent.LockSessionMetaPath(ownerPath)
	if lockErr != nil {
		return lockErr
	}
	defer unlock()
	meta, ok, loadErr := agent.LoadBranchMeta(ownerPath)
	if loadErr != nil {
		return loadErr
	}
	if !ok {
		return nil
	}
	meta.Scope = snapshot.Scope
	meta.WorkspaceRoot = snapshot.WorkspaceRoot
	return agent.SaveBranchMetaPreserveUpdatedLocked(ownerPath, meta)
}

// migrateSessionArtifacts moves one session's transcript plus every
// stem-prefixed sidecar (meta, event logs, leases, recovery copies, guardian
// companions, image packs — the whole family, including forms added later)
// from srcDir to dstDir, then rewrites every moved .meta to the target scope.
// Completed moves are recorded in the journal. Returns the new transcript path.
func (a *App) migrateSessionArtifacts(srcDir, dstDir, sessionPath, targetRoot string, journal *groupMigrationJournal) (string, error) {
	base := filepath.Base(sessionPath)
	stem := strings.TrimSuffix(base, store.SessionTranscriptSuffix)
	prefix := stem + "."
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return "", err
	}
	names := []string{base}
	for _, entry := range entries {
		name := entry.Name()
		if name != base && strings.HasPrefix(name, prefix) && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return "", err
	}
	for _, name := range names {
		from := filepath.Join(srcDir, name)
		to := filepath.Join(dstDir, name)
		if _, err := os.Lstat(to); err == nil {
			return "", fmt.Errorf("target already exists: %s", to)
		} else if !os.IsNotExist(err) {
			return "", err
		}
		// 回滚所需：meta 在搬移前的 scope/workspaceRoot 快照（搬移后立即被
		// 改写为目标 scope，回滚只搬文件不够——半迁移态测试钉的就是这个）。
		entry := groupMigrationEntry{from: from, to: to}
		if strings.HasSuffix(name, store.SessionMetaFileSuffix) {
			ownerFrom := filepath.Join(srcDir, strings.TrimSuffix(name, ".meta"))
			if meta, ok, err := agent.LoadBranchMeta(ownerFrom); err == nil && ok {
				entry.hasMeta = true
				entry.meta = meta
			}
		}
		if err := moveFileOrDir(from, to); err != nil {
			return "", err
		}
		journal.record(entry)
	}
	// 侧车 scope 改写：主转录与全体随迁侧车（guardian/子代理各自的 .meta）
	// 一并指向目标 scope——留下 global 元数据就是跨 scope 影子条目的种子。
	// meta 读改写走会话路径（Lock/Load/Save 内部推导 <session>.meta），
	// updateTopicSessionTitles 同款持锁纪律。
	newPath := filepath.Join(dstDir, base)
	for _, name := range names {
		if !strings.HasSuffix(name, store.SessionMetaFileSuffix) {
			continue
		}
		// <id>.jsonl.meta → 会话路径 = 去掉 ".meta" 尾（appendMissingFileTrashTargets
		// 同款换算：SessionMeta 拼在全转录路径之后）。
		ownerPath := filepath.Join(dstDir, strings.TrimSuffix(name, ".meta"))
		unlock, lockErr := agent.LockSessionMetaPath(ownerPath)
		if lockErr != nil {
			return "", lockErr
		}
		meta, ok, loadErr := agent.LoadBranchMeta(ownerPath)
		if loadErr != nil {
			unlock()
			return "", loadErr
		}
		if ok {
			meta.Scope = "project"
			meta.WorkspaceRoot = targetRoot
			if saveErr := agent.SaveBranchMetaPreserveUpdatedLocked(ownerPath, meta); saveErr != nil {
				unlock()
				return "", saveErr
			}
		}
		unlock()
	}
	return newPath, nil
}

// moveFileOrDir moves src to dst, refusing an existing destination (the
// collision pre-check in migrateSessionArtifacts already filters the common
// case; this is the belt to those braces). The heavy lifting reuses the
// trash chain's move primitive: rename first, copy+remove fallback for
// cross-volume moves (global sessions under the home directory routinely
// migrate to projects on another drive) and Windows handle races.
func moveFileOrDir(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("target already exists: %s", dst)
	}
	return movePathIfExists(src, dst)
}

// moveTopicStateToRoot copies the migrated topics' per-root state (titles,
// sources, created-at stamps) from the global book into the target project's
// book, then drops the global records. Copy failures degrade to a renameable
// row (the .meta sidecar carries a title copy) and never fail the migration.
func (a *App) moveTopicStateToRoot(members []string, targetRoot string) {
	member := map[string]bool{}
	for _, id := range members {
		member[id] = true
	}
	if !copyTopicStateMaps(member, "", targetRoot) {
		slog.Warn("desktop: group migration could not move topic display state; titles fall back to session sidecars")
	}
}

// copyTopicStateMaps reads the source root's books, merges the members into
// the target root's books, removes the members from the source books, and
// persists all three stores per root. Reports whether every write landed.
func copyTopicStateMaps(member map[string]bool, sourceRoot, targetRoot string) bool {
	step := "load titles"
	titles, err := loadTopicTitlesForUpdate(sourceRoot)
	if err != nil {
		slog.Warn("desktop: group migration topic state step failed", "step", step, "err", err)
		return false
	}
	targetTitles := loadTopicTitles(targetRoot)
	step = "load title sources"
	sources, err := loadTopicTitleSourcesForUpdate(sourceRoot)
	if err != nil {
		slog.Warn("desktop: group migration topic state step failed", "step", step, "err", err)
		return false
	}
	targetSources := loadTopicTitleSources(targetRoot)
	createdAts := loadTopicCreatedAts(sourceRoot)
	targetCreatedAts := loadTopicCreatedAts(targetRoot)
	changed := false
	for id := range member {
		if title := titles[id]; title != "" {
			targetTitles[id] = title
			changed = true
		}
		if source := sources[id]; source != "" {
			targetSources[id] = source
			changed = true
		}
		if createdAt := createdAts[id]; createdAt > 0 {
			targetCreatedAts[id] = createdAt
			changed = true
		}
		delete(titles, id)
		delete(sources, id)
		delete(createdAts, id)
		changed = true
	}
	if !changed {
		return true
	}
	step = "save target titles"
	if err := saveTopicTitles(targetRoot, targetTitles); err != nil {
		slog.Warn("desktop: group migration topic state step failed", "step", step, "err", err)
		return false
	}
	step = "save target title sources"
	if err := saveTopicTitleSources(targetRoot, targetSources); err != nil {
		slog.Warn("desktop: group migration topic state step failed", "step", step, "err", err)
		return false
	}
	step = "save target created-ats"
	if err := saveTopicCreatedAts(targetRoot, targetCreatedAts); err != nil {
		slog.Warn("desktop: group migration topic state step failed", "step", step, "err", err)
		return false
	}
	step = "save source titles"
	if err := saveTopicTitles(sourceRoot, titles); err != nil {
		slog.Warn("desktop: group migration topic state step failed", "step", step, "err", err)
		return false
	}
	step = "save source title sources"
	if err := saveTopicTitleSources(sourceRoot, sources); err != nil {
		slog.Warn("desktop: group migration topic state step failed", "step", step, "err", err)
		return false
	}
	step = "save source created-ats"
	if err := saveTopicCreatedAts(sourceRoot, createdAts); err != nil {
		slog.Warn("desktop: group migration topic state step failed", "step", step, "err", err)
		return false
	}
	return true
}

// undoMigratedTopicState removes the target-root topic records a failed
// migration had already copied (best effort; the global records were never
// touched before the registry write).
func (a *App) undoMigratedTopicState(members []string, targetRoot string) {
	for _, id := range members {
		if err := deleteTopicState(targetRoot, id); err != nil {
			slog.Warn("desktop: group migration state rollback failed", "topic", id, "err", err)
		}
	}
}
