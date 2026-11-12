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

// tl1560 分组右键「迁移到其他分区」——把一个会话分组（组壳 + 全部成员对话）
// 在分区之间整体迁移。第二阶段（全局记忆迁移）由各对话自行处理，不归本件。
//
// tl1560 返修（20261112 用户装机实测四问题）在本文件的落点：
//   ② 用户裁决支持项目间互迁——迁移不再限定「迁出 global」：源分区可以是
//      global 或任意项目，目标分区可以是任意项目或 global（root 为空）；
//   ① 成员对话脱离分组——根因之一是落盘名册与实际搬迁清单不一致：meta 已
//      指向其他 scope 的迷路成员 / cleanup-pending / 转录缺失的成员此前被
//      静默跳过却仍随组注册到目标分区，对话就留在旧分区成了「脱离分组的
//      孤儿」。现在这类成员显式剔除出迁移名册（留在源分区注册），并计入
//      结果的 SkippedTopics；根因之二是活口收口不完备，见下；
//   ④ 迁移后压缩分裂出新对话——活口（仍持有旧会话路径的运行时）在迁移后
//      第一次写盘时按旧路径/旧 scope 重建文件或触发 tl1407 无路径倾倒，
//      倾倒副本经 legacySessionTopicID 收养成新 topic（20261112 实测 dump：
//      unsaved-*.unsaved-dump.jsonl → legacy_unsaved-*，尾部与原会话一致）。
//      修复 = 活口收口升级：成员的可见 tab 一律先关（关失败整体中止）、
//      detached 空闲副本可指认拒绝（后台任务中心打开并关 tab 后重试——
//      该下一步真实存在）、持锁状态下复查无新活 tab 才落盘。
//   另修第一阶段编排死锁：closeTabRuntime 自取 runtime-mutation + 
//      sessionRemovalMu 两闸，第一阶段却持着 sessionRemovalMu 去关 tab——
//      成员 tab 打开时真跑迁移即互斥重入死锁（测试无 tab 未暴露）。
//      现在关 tab 一律在两闸之前完成（TrashTopicForce tl1367 缺陷 B 同款
//      顺序纪律），锁内只做复查。
//
// 编排（决策点：顺序取「能保证组与成员终态一致」的形态）：
//   ① 校验：源/目标分区解析、分组在源花名册、逐成员按源身份 (scope,root)
//      解析会话文件；跳过成员显式剔除出迁移名册；
//   ② 活跃拦截：任一成员有活跃运行时工作即整组拒绝（归档 errTopicHasActiveWork
//      同款语义，无 force 通道——迁移不是破坏性操作，不值得开强制口）；
//   ③ 活口收口（两闸之前）：detached 成员副本可指认拒绝；可见 tab 逐个
//      stop_and_close，任一失败即中止（此刻未动任何盘面）；
//   ④ 取两把全局变更锁（runtime mutation + session removal），锁内复查活跃
//      门与活口 tab——并发窗口里新开的 tab / 刚起的 turn 在这里被拦下
//      （turn 准入要过 runtime-mutation 闸，持锁期间起不来）；
//   ⑤ 文件先搬：逐会话移动「转录 + 全部 stem 前缀侧车」（meta/事件日志/
//      lease/恢复副本等，前缀枚举天然覆盖未来新增侧车——tl1442 教训：手工
//      枚举侧车清单必有遗漏），到点后改写 .meta 的 scope/workspaceRoot 为
//      目标身份；逐会话登记回滚日志，任一步失败整体逆向回滚并中止（此时
//      分组数据尚未动过，磁盘终态与迁移动前一致）；
//   ⑥ 组后切：一次 updateProjectsFile 原子事务完成全部分组数据搬家——组
//      从源花名册切入目标花名册（两侧 GroupsRevision 各自乐观递增，并发
//      写者纪律同 SaveSessionGroupsVersioned）、迁移名册成员的 topic 注册
//      与置顶随迁、清墓碑（显式登记复活语义）、成员集合与校验期快照不一致
//      即拒绝；事务失败同样回滚第 ⑤ 步的文件搬家；
//   ⑦ 收尾：topic 标题/来源/创建时间分册在源/目标两册间搬移、global 侧
//      记录注销（标题在 .meta 有副本，失败仅降级显示，不致命）、注册表索引
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
	// errGroupMigrateDetachedHolder：成员的 detached 后台副本仍持有旧会话路径
	// （空闲；活跃形态在活跃门已被拒）。下一步真实存在：任务中心打开该后台
	// 会话（RevealBackgroundRuntime）并关闭其标签页后重试迁移。就地改锚
	// detached 运行时要动 runtime 注册表/lease/sink 全链，收益配不上风险
	// （减法优先），拒绝+指认是最小诚实形态。
	errGroupMigrateDetachedHolder = errors.New("a background copy of one of these sessions is still holding the conversation — open it from the task center, close its tab, then retry the migration")
	// errGroupMigrateSamePartition：源与目标是同一分区。
	errGroupMigrateSamePartition = errors.New("the group already lives in this partition")
)

// SessionGroupMigrationResult is the Wails-facing receipt of one group
// migration. Returned only on full success.
type SessionGroupMigrationResult struct {
	GroupID             string   `json:"groupId"`
	GroupTitle          string   `json:"groupTitle"`
	TargetWorkspaceRoot string   `json:"targetWorkspaceRoot"`
	MigratedTopics      []string `json:"migratedTopics"`
	MovedSessions       int      `json:"movedSessions"`
	// SkippedTopics 列出未能随迁、已显式留在源分区的成员（meta 指向其他
	// scope 的迷路会话 / cleanup-pending / 转录缺失）。tl1560 返修①：落盘
	// 名册必须与实际搬迁清单一致——宁可少迁并留痕，不可静默制造跨区孤儿。
	SkippedTopics []string `json:"skippedTopics,omitempty"`
}

// groupMigrationTarget is one session file planned for the move: its current
// directory and canonical path. Built during validation, consumed by the move
// phase.
type groupMigrationTarget struct {
	dir         string
	sessionPath string
	key         string
}

// groupMigrationMemberPlan is one roster member's resolved move plan.
type groupMigrationMemberPlan struct {
	topicID string
	targets []groupMigrationTarget
}

// groupMigrationSourceIdentity normalizes the caller-supplied source partition.
type groupMigrationSourceIdentity struct {
	scope string // "global" | "project"
	root  string // project root; empty for global
}

// MigrateSessionGroup moves one session group — the group shell plus every
// member conversation — between partitions (tl1560 返修②：项目间互迁，
// global→project、project→project、project→global 三向同一条事务纪律；
// 原名 MigrateSessionGroupToProject 只覆盖 global→project 一向，扩面后
// 更名。targetWorkspaceRoot 为空表示目标 = global 区).
func (a *App) MigrateSessionGroup(scope, sourceRoot, groupID, targetWorkspaceRoot string) (SessionGroupMigrationResult, error) {
	groupID = strings.TrimSpace(groupID)
	result := SessionGroupMigrationResult{MigratedTopics: []string{}, SkippedTopics: []string{}}
	if groupID == "" {
		return result, fmt.Errorf("groupId is required")
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
		return result, errGroupMigrateSamePartition
	}
	if targetRoot != "" && source.scope == "project" && sameProjectRoot(source.root, targetRoot) {
		return result, errGroupMigrateSamePartition
	}

	// ① 校验。分组读 raw 文件；global 组还要叠 heartbeat 注入视图（定时任务
	// 组只存在于注入视图，磁盘上可能还没有壳——两种形态都要能迁）。project
	// 组直接读盘面（注入视图只服务 global 定时任务组）。
	f := loadProjectsFile()
	group := desktopGroup{}
	sourceIndex := -1
	switch source.scope {
	case "global":
		injected := a.fileHeartbeatTopics(nonNilGroups(f.GlobalGroups))
		for i := range injected {
			if strings.EqualFold(strings.TrimSpace(injected[i].ID), groupID) {
				group = injected[i]
				break
			}
		}
		if group.ID == "" {
			return result, fmt.Errorf("group %q not found in global groups", groupID)
		}
	case "project":
		sourceIndex = projectIndexByRoot(f.Projects, source.root)
		if sourceIndex < 0 {
			return result, fmt.Errorf("project %q not found", source.root)
		}
		for i := range f.Projects[sourceIndex].Groups {
			if strings.EqualFold(strings.TrimSpace(f.Projects[sourceIndex].Groups[i].ID), groupID) {
				group = f.Projects[sourceIndex].Groups[i]
				break
			}
		}
		if group.ID == "" {
			return result, fmt.Errorf("group %q not found in project %q", groupID, source.root)
		}
	}
	if targetRoot != "" && projectIndexByRoot(f.Projects, targetRoot) < 0 {
		return result, fmt.Errorf("project %q not found", targetRoot)
	}

	members := append([]string(nil), group.TopicIDs...)
	if len(members) > maxSessionGroupTopics {
		return result, fmt.Errorf("group %q has too many topics", groupID)
	}
	result.GroupID = group.ID
	result.GroupTitle = group.Title
	result.TargetWorkspaceRoot = targetRoot

	// 目标花名册 id 撞车（uuid 空间里极罕见，但撞上就是静默并组）。
	targetGroups := f.GlobalGroups
	if targetRoot != "" {
		targetGroups = f.Projects[projectIndexByRoot(f.Projects, targetRoot)].Groups
	}
	for _, g := range targetGroups {
		if strings.EqualFold(strings.TrimSpace(g.ID), group.ID) {
			return result, fmt.Errorf("target partition already has a group id %q (%s); dissolve or rename it first", group.ID, g.Title)
		}
	}

	// 逐成员解析待搬会话文件（只认源身份匹配；跨区迷路会话不碰）。
	// 枚举面 = global 两目录 + 源项目目录（物理落点以 meta 身份为准，目录
	// 只是扫描面——desktopSessionDir 回退会把 project 会话落在 config.SessionDir）。
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
	type memberPlan = groupMigrationMemberPlan
	plans := make([]memberPlan, 0, len(members))
	skipped := map[string]bool{}
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
				if !matchesSourceIdentity(meta) {
					// 迷路会话（meta 已指向其他分区）：tl1560 返修①——显式剔除
					// 出迁移名册并留痕，成员连同其注册一起留在源分区，不再
					// 随组静默迁出制造跨区孤儿。
					slog.Warn("desktop: group migration leaves a foreign-partition member behind",
						"topic", topicID, "scope", meta.DefaultScope(), "root", meta.WorkspaceRoot)
					skipped[topicID] = true
					continue
				}
				if agent.IsCleanupPending(sessionPath) {
					slog.Warn("desktop: group migration skips a cleanup-pending session", "topic", topicID)
					skipped[topicID] = true
					continue
				}
				if sessionTrashTargetFileMissing(sessionPath) {
					// 转录已消失的 meta 残留：搬家只会制造僵尸，留给归档/对账链。
					slog.Warn("desktop: group migration skips a missing-transcript session", "topic", topicID)
					skipped[topicID] = true
					continue
				}
				seenTargets[id] = true
				plan.targets = append(plan.targets, groupMigrationTarget{dir: dir, sessionPath: sessionPath, key: key})
			}
		}
		// resolved=false：成员在本机没有任何会话文件（组织性成员/心跳壳/空
		// 话题）——保留在迁移名册里随组走，注册搬家后开新会话即落在目标分区。
		plans = append(plans, plan)
	}
	movedMembers := make([]string, 0, len(members))
	for _, plan := range plans {
		if skipped[plan.topicID] {
			result.SkippedTopics = append(result.SkippedTopics, plan.topicID)
			continue
		}
		movedMembers = append(movedMembers, plan.topicID)
	}
	sort.Strings(result.SkippedTopics)

	// ② 活跃拦截（整组语义：任一成员活跃 → 拒绝）。
	if err := a.groupMigrateActiveGate(members); err != nil {
		return result, err
	}

	// ③ 活口收口（两闸之前——closeTabRuntime 自取 runtime-mutation 与
	//    sessionRemovalMu 两闸，持锁关 tab 即互斥重入死锁；tl1367 缺陷 B
	//    同款顺序纪律：先关 tab 后取闸）。detached 副本可指认拒绝（下一步
	//    真实存在：任务中心打开并关其标签页）。
	if err := a.refuseGroupMigrationDetachedHolders(members, planPaths(plans)); err != nil {
		return result, err
	}
	if err := a.closeGroupMigrationTabs(members, planPaths(plans)); err != nil {
		return result, err
	}

	// ④ 全局变更锁 + 锁内复查（活跃门 + 活口 tab）。并发窗口里新开的 tab
	// 在这里被拦下；turn 准入要过 runtime-mutation 闸，持锁期间起不来。
	releaseRuntime, ok := a.tryLockRuntimeMutation("migrate-group")
	if !ok {
		return result, errGroupMigrateBusy
	}
	defer releaseRuntime()
	if !a.sessionRemovalMu.TryLock() {
		return result, errGroupMigrateBusy
	}
	defer a.sessionRemovalMu.Unlock()

	if err := a.groupMigrateActiveGate(members); err != nil {
		return result, err
	}
	if bound := a.groupMigrationBoundTabs(members, planPaths(plans)); len(bound) > 0 {
		slog.Warn("desktop: group migration found a tab rebound to a member during the lock window", "tabs", len(bound))
		return result, errGroupMigrateBusy
	}

	// ⑤ 文件先搬（带回滚日志）。
	dstDir := desktopSessionDir(targetRoot)
	targetScope := "project"
	if targetRoot == "" {
		targetScope = "global"
	}
	journal := &groupMigrationJournal{}
	for _, plan := range plans {
		if skipped[plan.topicID] {
			continue
		}
		for _, target := range plan.targets {
			if _, err := a.migrateSessionArtifacts(target.dir, dstDir, target.sessionPath, targetScope, targetRoot, journal); err != nil {
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

	// ⑥ 组后切：一次原子事务（组切换 + 迁移名册的 topic 注册搬家 + 墓碑清理
	//    + 置顶随迁）。成员集合必须与校验期快照一致——并发加成员的迁移是
	//    病态场景，宁可整体回滚让用户重试，也不留下跨分区混注册。
	moved := false
	err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		// 源组定位（global=磁盘壳可缺席；project=壳必在）。
		ri := -1
		if source.scope == "global" {
			for i := range f.GlobalGroups {
				if strings.EqualFold(strings.TrimSpace(f.GlobalGroups[i].ID), group.ID) {
					ri = i
					break
				}
			}
		} else {
			si := projectIndexByRoot(f.Projects, source.root)
			if si < 0 {
				return false, fmt.Errorf("project %q not found", source.root)
			}
			sourceIndex = si
			for i := range f.Projects[si].Groups {
				if strings.EqualFold(strings.TrimSpace(f.Projects[si].Groups[i].ID), group.ID) {
					ri = i
					break
				}
			}
			if ri < 0 {
				return false, fmt.Errorf("group %q vanished from project %q during migration", group.ID, source.root)
			}
		}
		// 目标定位与撞车复查。
		ti := -1
		if targetRoot != "" {
			ti = projectIndexByRoot(f.Projects, targetRoot)
			if ti < 0 {
				return false, fmt.Errorf("project %q not found", targetRoot)
			}
			for _, g := range f.Projects[ti].Groups {
				if strings.EqualFold(strings.TrimSpace(g.ID), group.ID) {
					return false, fmt.Errorf("target project already has a group id %q (%s)", group.ID, g.Title)
				}
			}
		} else {
			for _, g := range f.GlobalGroups {
				if strings.EqualFold(strings.TrimSpace(g.ID), group.ID) {
					return false, fmt.Errorf("global already has a group id %q (%s)", group.ID, g.Title)
				}
			}
		}
		// 成员一致性：磁盘壳存在时，其名册必须与校验期快照一致。
		var roster desktopGroup
		if ri >= 0 {
			if source.scope == "project" {
				roster = f.Projects[sourceIndex].Groups[ri]
			} else {
				roster = f.GlobalGroups[ri]
			}
			if !sameStringList(roster.TopicIDs, members) {
				return false, fmt.Errorf("group %q membership changed during migration; retry", group.ID)
			}
		}
		// 源侧移除 + 修订递增。目标壳整壳随迁（parent/nesting 等字段不丢），
		// 仅 TopicIDs 换成实际搬迁的迁移名册；磁盘壳缺席（心跳托管壳）时
		// 以校验期解析到的组壳现建。
		next := roster
		if ri < 0 {
			next = desktopGroup{ID: group.ID, Title: group.Title}
		}
		next.TopicIDs = movedMembers
		switch {
		case ri < 0:
			// global 心跳托管壳（磁盘上还没有的定时任务组）：目标侧现建。
		case source.scope == "global":
			f.GlobalGroups = append(f.GlobalGroups[:ri], f.GlobalGroups[ri+1:]...)
			f.GlobalGroupsRevision++
		default:
			f.Projects[sourceIndex].Groups = append(f.Projects[sourceIndex].Groups[:ri], f.Projects[sourceIndex].Groups[ri+1:]...)
			f.Projects[sourceIndex].GroupsRevision++
		}
		// 目标侧加入 + 修订递增。
		if targetRoot != "" {
			f.Projects[ti].Groups = append(f.Projects[ti].Groups, next)
			f.Projects[ti].GroupsRevision++
		} else {
			f.GlobalGroups = append(f.GlobalGroups, next)
			f.GlobalGroupsRevision++
		}
		// 迁移名册成员的注册/置顶/墓碑搬家；跳过成员一律不动（留在源分区）。
		for _, topicID := range movedMembers {
			if targetRoot != "" {
				if source.scope == "global" {
					if containsDesktopString(f.GlobalPinnedTopics, topicID) {
						f.GlobalPinnedTopics = removeString(f.GlobalPinnedTopics, topicID)
						f.Projects[ti].PinnedTopics = prependUniqueString(f.Projects[ti].PinnedTopics, topicID)
					}
					f.GlobalTopics = removeString(f.GlobalTopics, topicID)
				} else {
					if containsDesktopString(f.Projects[sourceIndex].PinnedTopics, topicID) {
						f.Projects[sourceIndex].PinnedTopics = removeString(f.Projects[sourceIndex].PinnedTopics, topicID)
						f.Projects[ti].PinnedTopics = prependUniqueString(f.Projects[ti].PinnedTopics, topicID)
					}
					f.Projects[sourceIndex].Topics = removeString(f.Projects[sourceIndex].Topics, topicID)
				}
				// 显式登记复活：清墓碑（registerTopic 前插模式同款语义）。
				f.DeletedTopics = removeString(f.DeletedTopics, topicID)
				f.Projects[ti].Topics = prependUniqueString(f.Projects[ti].Topics, topicID)
			} else {
				// 目标 = global：project 成员的注册收编回 global 侧。
				if source.scope == "project" {
					if containsDesktopString(f.Projects[sourceIndex].PinnedTopics, topicID) {
						f.Projects[sourceIndex].PinnedTopics = removeString(f.Projects[sourceIndex].PinnedTopics, topicID)
						f.GlobalPinnedTopics = prependUniqueString(f.GlobalPinnedTopics, topicID)
					}
					f.Projects[sourceIndex].Topics = removeString(f.Projects[sourceIndex].Topics, topicID)
				}
				f.DeletedTopics = removeString(f.DeletedTopics, topicID)
				f.GlobalTopics = prependUniqueString(f.GlobalTopics, topicID)
			}
		}
		// 落盘前整册校验（SaveSessionGroups 同款纪律）：写坏的花名册不该存在。
		if err := validateSessionGroups(normalizeGroups(f.GlobalGroups)); err != nil {
			return false, err
		}
		if targetRoot != "" {
			if err := validateSessionGroups(normalizeGroups(f.Projects[ti].Groups)); err != nil {
				return false, err
			}
		}
		if source.scope == "project" {
			if err := validateSessionGroups(normalizeGroups(f.Projects[sourceIndex].Groups)); err != nil {
				return false, err
			}
		}
		moved = true
		return true, nil
	})
	if err != nil || !moved {
		journal.rollback()
		a.undoMigratedTopicState(movedMembers, targetRoot)
		a.emitMigrationDirs(srcDirs, dstDir)
		if err == nil {
			// moved=false 理论不可达（每个分支都置位或返回错误）；保守防御。
			err = fmt.Errorf("group %q migration did not apply", group.ID)
		}
		return result, err
	}

	// ⑦ 收尾：topic 状态分册（标题/来源/创建时间）源册 → 目标册（copyTopic
	//    StateMaps 的 sourceRoot 参数即为本返修的互迁泛化预留），源侧注销；
	//    注册表同步；目录级 reconcile + 树事件。
	a.moveTopicStateBetweenRoots(movedMembers, source.root, targetRoot)
	syncTopicRegistryIndex()
	a.emitMigrationDirs(srcDirs, dstDir)

	slog.Info("desktop: session group migrated between partitions",
		"group", group.ID, "source_scope", source.scope, "source_root", source.root,
		"target_root", targetRoot, "topics", len(result.MigratedTopics),
		"skipped", len(result.SkippedTopics),
		"sessions", result.MovedSessions)
	return result, nil
}

// planPaths flattens the planned source session paths (活口 tab 的 path 键匹配面).
func planPaths(plans []groupMigrationMemberPlan) []string {
	paths := make([]string, 0, len(plans))
	for _, plan := range plans {
		for _, target := range plan.targets {
			paths = append(paths, target.sessionPath)
		}
	}
	return paths
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

// appendUniqueDir appends dir to dirs unless an equivalent path is already
// present (case-insensitive, the same normalization globalSessionDirs uses).
func appendUniqueDir(dirs []string, dir string) []string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return dirs
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	key := strings.ToLower(dir)
	for _, existing := range dirs {
		if strings.ToLower(existing) == key {
			return dirs
		}
	}
	return append(dirs, dir)
}

// groupMigrationBoundTabs lists the live tabs (visible + detached) bound to a
// member topic — by TopicID or by one of the planned source session paths.
// SessionPath 匹配兜住 TopicID 为空/被改写的持有者（路径才是文件的真实锁）。
func (a *App) groupMigrationBoundTabs(members []string, plannedPaths []string) []*WorkspaceTab {
	bound := map[string]bool{}
	for _, id := range members {
		bound[strings.TrimSpace(id)] = true
	}
	paths := map[string]bool{}
	for _, path := range plannedPaths {
		if trimmed := strings.TrimSpace(path); trimmed != "" {
			paths[strings.ToLower(trimmed)] = true
		}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := []*WorkspaceTab{}
	for _, tabs := range []map[string]*WorkspaceTab{a.tabs, a.detachedSessions} {
		for _, tab := range tabs {
			if tab == nil {
				continue
			}
			if bound[strings.TrimSpace(tab.TopicID)] {
				out = append(out, tab)
				continue
			}
			if path := strings.TrimSpace(tab.currentSessionPath()); path != "" && paths[strings.ToLower(path)] {
				out = append(out, tab)
			}
		}
	}
	return out
}

// closeGroupMigrationTabs closes every visible tab bound to a member ahead of
// the move (stop_and_close: snapshot save → cancel owned work → bounded wait →
// close). Runs WITHOUT the migration locks (closeTabRuntime takes both global
// gates itself — holding sessionRemovalMu here would self-deadlock, the
// tl1367 defect-B ordering). Any failure aborts the migration before the first
// disk mutation: a tab we cannot close is exactly the stale-binding活口 that
// later writes through the old path (返修④).
func (a *App) closeGroupMigrationTabs(members []string, plannedPaths []string) error {
	for _, tab := range a.groupMigrationBoundTabs(members, plannedPaths) {
		if a.isDetachedSessionTab(tab) {
			// detached 的关闭原语不存在（可见 tab 专属）；统一交给
			// refuseGroupMigrationDetachedHolders 的可指认拒绝。
			continue
		}
		if err := a.CloseTabWithPolicy(tab.ID, "stop_and_close"); err != nil {
			slog.Warn("desktop: group migration could not close a member tab", "tab", tab.ID, "err", err)
			return fmt.Errorf("close the session's tab before migrating the group: %w", err)
		}
		slog.Info("desktop: group migration closed the member tab before the move", "tab", tab.ID)
	}
	return nil
}

// refuseGroupMigrationDetachedHolders rejects the migration while an idle
// detached copy of a member still holds its session. Active detached members
// never reach here (the active gate refuses first); this catches the idle
// keep-alive form whose next write would resurrect the old path (返修④).
func (a *App) refuseGroupMigrationDetachedHolders(members []string, plannedPaths []string) error {
	bound := a.groupMigrationBoundTabs(members, plannedPaths)
	for _, tab := range bound {
		if a.isDetachedSessionTab(tab) {
			return errGroupMigrateDetachedHolder
		}
	}
	return nil
}

// isDetachedSessionTab reports whether tab is registered as a detached
// background session. a.detachedSessions is keyed by session runtime key,
// not tab id — membership is a pointer-identity match against the values.
func (a *App) isDetachedSessionTab(tab *WorkspaceTab) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, detached := range a.detachedSessions {
		if detached == tab {
			return true
		}
	}
	return false
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
// from srcDir to dstDir, then rewrites every moved .meta to the target
// identity (scope + workspaceRoot). Completed moves are recorded in the
// journal. Returns the new transcript path.
func (a *App) migrateSessionArtifacts(srcDir, dstDir, sessionPath, targetScope, targetRoot string, journal *groupMigrationJournal) (string, error) {
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
		// 改写为目标身份，回滚只搬文件不够——半迁移态测试钉的就是这个）。
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
	// 侧车身份改写：主转录与全体随迁侧车（guardian/子代理各自的 .meta）
	// 一并指向目标分区——留下旧分区元数据就是跨区影子条目的种子。
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
			meta.Scope = targetScope
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

// moveTopicStateBetweenRoots copies the migrated topics' per-root state
// (titles, sources, created-at stamps) from the source book into the target
// book, then drops the source records. Copy failures degrade to a renameable
// row (the .meta sidecar carries a title copy) and never fail the migration.
// Empty roots denote the global books — the 互迁返修 generalizes what phase 1
// hardcoded as source="" only.
func (a *App) moveTopicStateBetweenRoots(members []string, sourceRoot, targetRoot string) {
	member := map[string]bool{}
	for _, id := range members {
		member[id] = true
	}
	if !copyTopicStateMaps(member, sourceRoot, targetRoot) {
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
// migration had already copied (best effort; the source records were never
// touched before the registry write).
func (a *App) undoMigratedTopicState(members []string, targetRoot string) {
	for _, id := range members {
		if err := deleteTopicState(targetRoot, id); err != nil {
			slog.Warn("desktop: group migration state rollback failed", "topic", id, "err", err)
		}
	}
}
