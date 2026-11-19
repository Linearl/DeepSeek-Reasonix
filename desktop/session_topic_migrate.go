package main

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"reasonix/internal/agent"
)

// tl1711 会话右键「移动到项目」——把单个会话（topic）在分区之间移动
// （project→project、project→global、global→project 三向）。
//
// 同步模式照抄 tl1560 分组迁移（session_group_migrate.go）的三处一致纪律，
// 收窄到单成员形态：
//   ① 会话文件搬家：转录 + 全部 stem 前缀侧车 + .meta 身份改写
//      （migrateSessionArtifacts 复用，含回滚日志）；
//   ② topic 注册事务：一次 updateProjectsFile 完成源侧摘除（Topics/置顶/
//      源分组名册）/墓碑清理/目标侧前置插入；
//   ③ topic 状态分册（标题/来源/创建时间）源册 → 目标册。
// 失败语义与分组迁移同一条：整体回滚、返回错误、磁盘回到移动前形态。
//
// 与分组迁移的三点刻意差异：
//   - 迷路会话（meta 身份与源分区不符）不静默剔除而是显式拒绝——单会话
//     移动是用户点名操作，落盘名册就是这一个成员，把它留下即整单失败，
//     诚实的报错优于「成功但什么都没搬」；
//   - 无会话文件的组织性成员（注册表有、盘面无）允许纯注册搬家——与
//     分组迁移 resolved=false 成员同语义，搬完开新会话即落在目标分区；
//   - 源分组名册清理：topic 迁出即脱离源分区全部分组（组壳留在源分区），
//     目标分区落为未分组。跨分区沿用源分组归属会复制出同 id 双主，清
//     名册是与 filterKnownGroupMembers 读侧过滤同一事实的写侧收口。
//
// non-goals（任务书）：不做批量迁移；不改会话文件物理存储布局（仅搬
// 目录 + 改归属注册）；不做跨机器迁移。

var (
	// errTopicMigrateActiveWork：会话仍有活跃运行时。前端在行 running 时
	// 禁用入口，这里是并发窗口的后端兜底；迁移无 force 通道（非破坏性
	// 操作，同分组迁移不开强制口）。
	errTopicMigrateActiveWork = errors.New("this session is still working — wait for it to finish before moving it")
	// errTopicMigrateBusy：全局变更锁被占。下一步真实存在：稍后重试同一
	// 菜单项。
	errTopicMigrateBusy = errors.New("another session change is in progress — wait a moment and retry the move")
	// errTopicMigrateDetachedHolder：会话的 detached 后台副本仍持有旧路径
	// （空闲；活跃形态在活跃门已被拒）。下一步真实存在：任务中心打开并
	// 关其标签页后重试（复用 tl1560 同款拒绝语义与文案）。
	errTopicMigrateDetachedHolder = errGroupMigrateDetachedHolder
	// errTopicMigrateSamePartition：源与目标是同一分区。
	errTopicMigrateSamePartition = errors.New("the session already lives in this partition")
	// errTopicMigrateMisrouted：会话文件的 meta 身份与源分区不符（跨区
	// 影子条目）。搬家只会扩大错位，留给对账链。
	errTopicMigrateMisrouted = errors.New("this session's files report a different workspace than the one it is listed under — run the session catalog repair before moving it")
)

// SessionTopicMigrationResult is the Wails-facing receipt of one single-topic
// partition move. Returned only on full success.
type SessionTopicMigrationResult struct {
	TopicID             string `json:"topicId"`
	TargetWorkspaceRoot string `json:"targetWorkspaceRoot"`
	MovedSessions       int    `json:"movedSessions"`
}

// MoveSessionTopic moves one session (topic) between partitions
// (tl1711). (scope, sourceRoot) = 源分区（global 区 root 传空），
// targetWorkspaceRoot 为空表示目标 = global 区。
func (a *App) MoveSessionTopic(scope, sourceRoot, topicID, targetWorkspaceRoot string) (SessionTopicMigrationResult, error) {
	result := SessionTopicMigrationResult{TopicID: strings.TrimSpace(topicID), TargetWorkspaceRoot: ""}
	if result.TopicID == "" {
		return result, fmt.Errorf("topicId is required")
	}
	source := groupMigrationSourceIdentity{scope: strings.TrimSpace(scope)}
	switch source.scope {
	case "global":
		source.root = ""
	case "project":
		source.root = normalizeProjectRoot(sourceRoot)
		if source.root == "" {
			return result, fmt.Errorf("workspaceRoot is required for a project source")
		}
	default:
		return result, fmt.Errorf("scope must be \"global\" or \"project\"")
	}

	targetRoot := ""
	if targetWorkspaceRoot != "" {
		targetRoot = normalizeProjectRoot(targetWorkspaceRoot)
	}
	// 目标分区：root 空 = global；root 非空 = 对应项目。源 = 目标直接拒绝。
	if targetRoot == "" && source.scope == "global" {
		return result, errTopicMigrateSamePartition
	}
	if targetRoot != "" && source.scope == "project" && sameProjectRoot(source.root, targetRoot) {
		return result, errTopicMigrateSamePartition
	}

	// ① 校验。目标项目必须存在（注册写口/标题分册都按存在校验）；
	// 逐会话文件按源身份解析待搬目标（枚举面 = global 两目录 + 源项目
	// 目录，物理落点以 meta 身份为准——desktopSessionDir 回退会把 project
	// 会话落在 config.SessionDir）。
	f := loadProjectsFile()
	if targetRoot != "" && projectIndexByRoot(f.Projects, targetRoot) < 0 {
		return result, fmt.Errorf("project %q not found", targetRoot)
	}
	if source.scope == "project" && projectIndexByRoot(f.Projects, source.root) < 0 {
		return result, fmt.Errorf("project %q not found", source.root)
	}
	srcDirs := globalSessionDirs()
	if source.scope == "project" {
		srcDirs = appendUniqueDir(srcDirs, desktopSessionDir(source.root))
	}
	matchesSourceIdentity := func(meta agent.BranchMeta) bool {
		if source.scope == "project" {
			return meta.DefaultScope() == "project" && sameProjectRoot(meta.WorkspaceRoot, source.root)
		}
		return meta.DefaultScope() == "global"
	}
	plans := []groupMigrationTarget{}
	for _, dir := range srcDirs {
		for _, match := range topicSessionMatches(dir, result.TopicID) {
			sessionPath, key, err := validateSessionPath(dir, match.path)
			if err != nil {
				return result, fmt.Errorf("topic %q: %w", result.TopicID, err)
			}
			meta, ok, err := agent.LoadBranchMeta(sessionPath)
			if err != nil || !ok {
				return result, fmt.Errorf("topic %q: session meta unreadable: %s", result.TopicID, sessionPath)
			}
			if !matchesSourceIdentity(meta) {
				// 迷路会话（meta 指向其他分区）：单会话移动不静默剔除——
				// 用户点名要搬的就是这个会话，报错比假成功诚实。
				slog.Warn("desktop: topic move refuses a foreign-partition session file",
					"topic", result.TopicID, "path", sessionPath,
					"scope", meta.DefaultScope(), "root", meta.WorkspaceRoot)
				return result, errTopicMigrateMisrouted
			}
			id := sessionRuntimeKey(sessionPath)
			if id == "" {
				id = dir + "\x00" + sessionPath
			}
			seen := false
			for _, planned := range plans {
				if planned.dir == dir && planned.sessionPath == sessionPath {
					seen = true
					break
				}
			}
			if seen {
				continue
			}
			plans = append(plans, groupMigrationTarget{dir: dir, sessionPath: sessionPath, key: key})
		}
	}

	// ② 活跃拦截。
	if a.topicHasActiveRuntimeWork(result.TopicID) {
		return result, errTopicMigrateActiveWork
	}

	// ③ 活口收口（两闸之前，tl1367 缺陷 B 同款顺序纪律）。detached 空闲
	// 副本可指认拒绝；可见 tab 逐个 stop_and_close，任一失败即中止（此刻
	// 未动任何盘面）。辅助件直接复用 tl1560 的成员面函数（单成员 =
	// members 切片长度 1 的退化形态）。
	plannedPaths := planPaths([]groupMigrationMemberPlan{{topicID: result.TopicID, targets: plans}})
	if err := a.refuseGroupMigrationDetachedHolders([]string{result.TopicID}, plannedPaths); err != nil {
		return result, err
	}
	if err := a.closeGroupMigrationTabs([]string{result.TopicID}, plannedPaths); err != nil {
		return result, err
	}

	// ④ 全局变更锁 + 锁内复查（活跃门 + 活口 tab）。
	releaseRuntime, ok := a.tryLockRuntimeMutation("move-topic")
	if !ok {
		return result, errTopicMigrateBusy
	}
	defer releaseRuntime()
	if !a.sessionRemovalMu.TryLock() {
		return result, errTopicMigrateBusy
	}
	defer a.sessionRemovalMu.Unlock()

	if a.topicHasActiveRuntimeWork(result.TopicID) {
		return result, errTopicMigrateActiveWork
	}
	if bound := a.groupMigrationBoundTabs([]string{result.TopicID}, plannedPaths); len(bound) > 0 {
		slog.Warn("desktop: topic move found a tab rebound during the lock window", "topic", result.TopicID, "tabs", len(bound))
		return result, errTopicMigrateBusy
	}

	// ⑤ 文件先搬（带回滚日志；目标身份与目标目录一次定死）。
	dstDir := desktopSessionDir(targetRoot)
	targetScope := "project"
	if targetRoot == "" {
		targetScope = "global"
	}
	journal := &groupMigrationJournal{}
	for _, target := range plans {
		if _, err := a.migrateSessionArtifacts(target.dir, dstDir, target.sessionPath, targetScope, targetRoot, journal); err != nil {
			journal.rollback()
			a.emitMigrationDirs(srcDirs, dstDir)
			return result, fmt.Errorf("topic %q: %w", result.TopicID, err)
		}
		invalidateTopicSessionIndex(target.dir)
		invalidateTopicSessionIndex(dstDir)
		a.removeSessionCatalogPath(target.sessionPath, "topic_migrated")
		result.MovedSessions++
	}

	// ⑥ 注册事务：源侧摘除（Topics + 置顶 + 源分组名册）+ 墓碑清理 +
	//    目标侧前置插入，一次原子落盘。失败整体回滚第 ⑤ 步的文件搬家。
	moved := false
	err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		si := -1
		if source.scope == "project" {
			si = projectIndexByRoot(f.Projects, source.root)
			if si < 0 {
				return false, fmt.Errorf("project %q not found", source.root)
			}
		}
		ti := -1
		if targetRoot != "" {
			ti = projectIndexByRoot(f.Projects, targetRoot)
			if ti < 0 {
				return false, fmt.Errorf("project %q not found", targetRoot)
			}
		}
		// 源侧注册与置顶摘除；置顶随迁（源侧有置顶 → 目标侧前置插入）。
		pinned := false
		if source.scope == "global" {
			pinned = containsDesktopString(f.GlobalPinnedTopics, result.TopicID)
			if pinned {
				f.GlobalPinnedTopics = removeString(f.GlobalPinnedTopics, result.TopicID)
			}
			f.GlobalTopics = removeString(f.GlobalTopics, result.TopicID)
		} else {
			pinned = containsDesktopString(f.Projects[si].PinnedTopics, result.TopicID)
			if pinned {
				f.Projects[si].PinnedTopics = removeString(f.Projects[si].PinnedTopics, result.TopicID)
			}
			f.Projects[si].Topics = removeString(f.Projects[si].Topics, result.TopicID)
			// 源分组名册清理：topic 迁出即脱离源分区全部分组（组壳留在
			// 源分区，GroupsRevision 只在名册真变时递增）。
			rosterChanged := false
			for gi := range f.Projects[si].Groups {
				next := removeString(f.Projects[si].Groups[gi].TopicIDs, result.TopicID)
				if len(next) != len(f.Projects[si].Groups[gi].TopicIDs) {
					f.Projects[si].Groups[gi].TopicIDs = next
					rosterChanged = true
				}
			}
			if rosterChanged {
				f.Projects[si].GroupsRevision++
			}
		}
		if source.scope == "global" {
			// global 源的分组名册同样清理（组壳留在 global）。
			rosterChanged := false
			for gi := range f.GlobalGroups {
				next := removeString(f.GlobalGroups[gi].TopicIDs, result.TopicID)
				if len(next) != len(f.GlobalGroups[gi].TopicIDs) {
					f.GlobalGroups[gi].TopicIDs = next
					rosterChanged = true
				}
			}
			if rosterChanged {
				f.GlobalGroupsRevision++
			}
		}
		// 显式登记复活：清墓碑（registerTopic 前插模式同款语义）。
		f.DeletedTopics = removeString(f.DeletedTopics, result.TopicID)
		if targetRoot != "" {
			if pinned {
				f.Projects[ti].PinnedTopics = prependUniqueString(f.Projects[ti].PinnedTopics, result.TopicID)
			}
			f.Projects[ti].Topics = prependUniqueString(f.Projects[ti].Topics, result.TopicID)
			if err := validateSessionGroups(normalizeGroups(f.Projects[ti].Groups)); err != nil {
				return false, err
			}
		} else {
			if pinned {
				f.GlobalPinnedTopics = prependUniqueString(f.GlobalPinnedTopics, result.TopicID)
			}
			f.GlobalTopics = prependUniqueString(f.GlobalTopics, result.TopicID)
		}
		// 落盘前整册校验（SaveSessionGroups 同款纪律）：写坏的花名册不该存在。
		if source.scope == "project" {
			if err := validateSessionGroups(normalizeGroups(f.Projects[si].Groups)); err != nil {
				return false, err
			}
		} else if err := validateSessionGroups(normalizeGroups(f.GlobalGroups)); err != nil {
			return false, err
		}
		moved = true
		return true, nil
	})
	if err != nil || !moved {
		journal.rollback()
		a.undoMigratedTopicState([]string{result.TopicID}, targetRoot)
		a.emitMigrationDirs(srcDirs, dstDir)
		if err == nil {
			// moved=false 理论不可达（每个分支都置位或返回错误）；保守防御。
			err = fmt.Errorf("topic %q move did not apply", result.TopicID)
		}
		return result, err
	}

	// ⑦ 收尾：topic 状态分册源册 → 目标册（标题/来源/创建时间；失败降级
	//    为可重命名行，.meta 有标题副本）、注册表索引同步、目录级
	//    reconcile + 树事件。
	a.moveTopicStateBetweenRoots([]string{result.TopicID}, source.root, targetRoot)
	syncTopicRegistryIndex()
	a.emitMigrationDirs(srcDirs, dstDir)

	slog.Info("desktop: session topic moved between partitions",
		"topic", result.TopicID, "source_scope", source.scope, "source_root", source.root,
		"target_root", targetRoot, "sessions", result.MovedSessions)
	return result, nil
}
