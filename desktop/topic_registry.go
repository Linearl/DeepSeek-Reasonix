package main

// topic 注册的写口唯二收点（tl1208，1197 方案获批件第二要素）。
//
// desktop-projects.json 里 topic 注册列表（GlobalTopics / Projects[].Topics /
// 各自 PinnedTopics / DeletedTopics 墓碑 / 分组成员）的全部**成员变更**只从
// 两个收点过：
//
//	registerTopic   —— 登记/更新（新增、复活墓碑、批量补登、置顶翻转）
//	unregisterTopic —— 注销（删除清扫、空壳清理；墓碑可选题）
//
// 既有散布写口（新建会话补登 / fork 补登 / 迁移批量补登 / deleteTopic /
// 空壳清理 / 置顶翻转 / 手动排序）全部改为经由收点；收点在改完权威文件后
// 同步 internal/topicregistry 唯一归属索引（写后即同步，读侧直查不悬空）。
//
// 纪律（源码守卫钉死，见 topic_registry_write_guard_test.go）：
//   - 本文件之外的生产代码零处直接变更 topic 注册列表；
//   - loadProjectsFile/normalizeProjectsFile/applyProjectOrganization 的
//     列表赋值属于「加载视图构建」（去重/顺序投影，不新增成员），守卫按
//     函数名豁免；
//   - reorderTopicOrder 是保序重排（completeTopicOrder 输入输出同集合的
//     置换），不改归属成员，守卫按置换性质单独验证。
//
// 锁序：收点内部持有 desktopProjectsFileMu（updateProjectsFile）做磁盘
// 变更；索引同步发生在磁盘锁释放之后（SyncFromSource 自带包级锁 + 一次
// os.Stat + 一次 JSON 解析，持磁盘锁期间无索引侧磁盘 IO）。

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	"reasonix/internal/topicregistry"
)

// topicRegistryCacheFile 是唯一归属索引的持久化缓存（派生数据，可随时
// 丢弃重建；format 版本门见 internal/topicregistry）。
const topicRegistryCacheFile = "topic-registry.json"

var (
	topicRegistryConfigMu      sync.Mutex
	topicRegistryConfiguredDir string
)

// ensureTopicRegistryConfigured 把唯一索引接到权威源。目录变更时重配
// （测试换临时目录的隔离路径），配置本身廉价（两次路径拼接 + 赋值）。
func ensureTopicRegistryConfigured() {
	dir := desktopConfigDir()
	topicRegistryConfigMu.Lock()
	defer topicRegistryConfigMu.Unlock()
	if topicRegistryConfiguredDir == dir {
		return
	}
	topicregistry.Configure(
		filepath.Join(dir, topicRegistryCacheFile),
		filepath.Join(dir, desktopProjectsFile),
		func() topicregistry.SourceView { return topicRegistrySourceView(loadProjectsFile()) },
	)
	topicRegistryConfiguredDir = dir
}

// topicRegistrySourceView 把 loadProjectsFile 的结果投影成索引重建源。
// root 已由 loadProjectsFile 规整为绝对路径，索引侧只做 Clean 归一。
func topicRegistrySourceView(f desktopProjectFile) topicregistry.SourceView {
	view := topicregistry.SourceView{
		Global:       f.GlobalTopics,
		GlobalPinned: f.GlobalPinnedTopics,
		Deleted:      make(map[string]bool, len(f.DeletedTopics)),
	}
	for _, id := range f.DeletedTopics {
		view.Deleted[id] = true
	}
	for _, p := range f.Projects {
		view.Projects = append(view.Projects, topicregistry.ProjectTopics{
			Root:   p.Root,
			Topics: p.Topics,
			Pinned: p.PinnedTopics,
		})
	}
	return view
}

// syncTopicRegistryIndex 在收点完成磁盘变更后同步唯一归属索引。
// 失败只告警不回滚：索引是派生数据，下次 stamp 过期会自动重建
// （重建先于直查由 OwnerOf/Snapshot 的 ensureFresh 保证）。
func syncTopicRegistryIndex() {
	ensureTopicRegistryConfigured()
	if err := topicregistry.SyncFromSource(); err != nil {
		slog.Warn("desktop: topic registry index sync failed", "err", err)
	}
}

// topicRegistryOwnerOf 是读侧直查入口：topicId 的唯一归属行。
// 未配置（headless 冷机）/索引未建时返回 miss，读侧回落既有启发式。
func topicRegistryOwnerOf(topicID string) (topicregistry.Entry, bool) {
	ensureTopicRegistryConfigured()
	return topicregistry.OwnerOf(topicID)
}

// topicRegisterOpts 是 registerTopic 的行为开关。
type topicRegisterOpts struct {
	// respectTombstones=true：批量补登/修复扫描语义——已在 DeletedTopics
	// 墓碑里的 id 不复活（扫描绝不能复活用户删除的话题）。
	respectTombstones bool
	// ensureProject=true：project scope 且项目不存在时补建项目条目
	// （新建会话/隔离 fork 场景是唯一合法的建项目时机）。
	ensureProject bool
	// pinUpdate=true：置顶翻转模式——在位更新置顶态，不前置插入、不改
	// 成员集合；发现逻辑（哪些 scope 持有该 topic）在磁盘锁内按既有
	// SetTopicPinned 语义执行（含标题表探测）。
	pinUpdate bool
	// pinned 在 pinUpdate 模式下是置顶目标态。
	pinned bool
}

// topicUnregisterOpts 是 unregisterTopic 的行为开关。
type topicUnregisterOpts struct {
	// tombstone=true：摘除的同时写墓碑（deleteTopic 语义——防止并发
	// 修复扫描复活）。false：仅摘除（空壳清理语义，历史上不写墓碑）。
	tombstone bool
	// onlyScope 非 nil：只从该 scope 摘除（空壳清理的历史语义——只清
	// 自己 scope 的注册，不动其它 scope）。nil：全 scope 清扫。
	onlyScope *topicScopeRef
}

// topicScopeRef 标识一个注册 scope（global 的 root 为空串）。
type topicScopeRef struct {
	scope string
	root  string
}

// registerTopic 是 topic 注册的收点之一：登记/更新。
//
// 默认（前置插入模式）保持既有单话题 prepend 语义：清墓碑（显式登记
// 复活）、置顶插入（新话题在侧栏顶）；批量 id 一次落盘（迁移路径）。
// pinUpdate 模式在位翻转置顶态。
func registerTopic(scope, workspaceRoot string, opt topicRegisterOpts, topicIDs ...string) error {
	scope = strings.TrimSpace(scope)
	if scope == "global" {
		workspaceRoot = ""
	} else if scope == "project" {
		workspaceRoot = normalizeProjectRoot(workspaceRoot)
		if workspaceRoot == "" {
			return fmt.Errorf("workspaceRoot is required for project scope")
		}
	} else {
		return fmt.Errorf("unsupported scope %q", scope)
	}
	ids := make([]string, 0, len(topicIDs))
	for _, id := range topicIDs {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	if opt.pinUpdate {
		if len(ids) != 1 {
			return fmt.Errorf("pin update takes exactly one topicID, got %d", len(ids))
		}
	}
	ids = uniqueStrings(ids)
	if len(ids) == 0 {
		return nil
	}

	var err error
	if opt.pinUpdate {
		err = updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
			return registerTopicPinMutator(f, workspaceRoot, ids[0], opt.pinned)
		})
	} else {
		err = updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
			return registerTopicPrependMutator(f, workspaceRoot, ids, opt)
		})
	}
	if err != nil {
		return err
	}
	syncTopicRegistryIndex()
	return nil
}

// registerTopicPrependMutator 原 prependTopicsInProjectsFileOpts 的变更体
// （语义逐行保留）：墓碑在磁盘锁内核验/清除，防删除与扫描写竞争复活。
func registerTopicPrependMutator(f *desktopProjectFile, workspaceRoot string, topicIDs []string, opt topicRegisterOpts) (bool, error) {
	live := topicIDs
	changed := false
	if opt.respectTombstones {
		live = make([]string, 0, len(topicIDs))
		for _, id := range topicIDs {
			if !containsDesktopString(f.DeletedTopics, id) {
				live = append(live, id)
			}
		}
		if len(live) == 0 {
			return false, nil
		}
	} else {
		for _, id := range topicIDs {
			if next := removeString(f.DeletedTopics, id); !sameStringList(next, f.DeletedTopics) {
				f.DeletedTopics = next
				changed = true
			}
		}
	}
	if workspaceRoot == "" {
		next := uniqueStrings(append(append([]string(nil), live...), f.GlobalTopics...))
		if sameStringList(next, f.GlobalTopics) {
			return changed, nil
		}
		f.GlobalTopics = next
		return true, nil
	}
	for i, p := range f.Projects {
		if !sameProjectRoot(p.Root, workspaceRoot) {
			continue
		}
		next := uniqueStrings(append(append([]string(nil), live...), p.Topics...))
		if sameStringList(next, p.Topics) {
			return changed, nil
		}
		f.Projects[i].Topics = next
		return true, nil
	}
	if !opt.ensureProject {
		return changed, nil
	}
	// Defensive: the loop above already checked every project with
	// sameProjectRoot, so this is normally unreachable. It stays as a second
	// guard because the append is the only place a new root enters the file.
	if projectRootExists(f, workspaceRoot) {
		return changed, nil
	}
	f.Projects = append(f.Projects, desktopProject{Root: workspaceRoot, Topics: live})
	return true, nil
}

// registerTopicPinMutator 原 SetTopicPinned 变更体（语义逐行保留）：
// 发现逻辑（标题表或注册列表持有该 topic 的第一个 scope）在磁盘锁内。
func registerTopicPinMutator(f *desktopProjectFile, workspaceRoot, topicID string, pinned bool) (bool, error) {
	for i, p := range f.Projects {
		m := loadTopicTitles(p.Root)
		if _, ok := m[topicID]; !ok && !containsDesktopString(p.Topics, topicID) {
			continue
		}
		next := removeString(f.Projects[i].PinnedTopics, topicID)
		if pinned {
			next = prependUniqueString(f.Projects[i].PinnedTopics, topicID)
		}
		if sameStringList(next, f.Projects[i].PinnedTopics) {
			return false, nil
		}
		f.Projects[i].PinnedTopics = next
		return true, nil
	}
	globalTitles := loadTopicTitles("")
	if _, ok := globalTitles[topicID]; !ok && !containsDesktopString(f.GlobalTopics, topicID) {
		return false, fmt.Errorf("topic %q not found", topicID)
	}
	next := removeString(f.GlobalPinnedTopics, topicID)
	if pinned {
		next = prependUniqueString(f.GlobalPinnedTopics, topicID)
	}
	if sameStringList(next, f.GlobalPinnedTopics) {
		return false, nil
	}
	f.GlobalPinnedTopics = next
	return true, nil
}

// unregisterTopic 是 topic 注册的收点之二：注销。
// 默认全 scope 清扫（注册列表/置顶/分组成员）+ 墓碑（可关）。
func unregisterTopic(topicID string, opt topicUnregisterOpts) error {
	topicID = strings.TrimSpace(topicID)
	if topicID == "" {
		return nil
	}
	err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		return unregisterTopicMutator(f, topicID, opt)
	})
	if err != nil {
		return err
	}
	syncTopicRegistryIndex()
	return nil
}

// unregisterTopicMutator 原 removeTopicFromProjectsFile 变更体的超集：
// onlyScope 把清扫范围收到一个 scope（空壳清理的历史语义）。
func unregisterTopicMutator(f *desktopProjectFile, topicID string, opt topicUnregisterOpts) (bool, error) {
	inScope := func(scope string, root string) bool {
		if opt.onlyScope == nil {
			return true
		}
		if opt.onlyScope.scope != scope {
			return false
		}
		return scope == "global" || sameProjectRoot(opt.onlyScope.root, root)
	}
	changed := false
	if inScope("global", "") {
		if next := removeString(f.GlobalTopics, topicID); !sameStringList(next, f.GlobalTopics) {
			f.GlobalTopics = next
			changed = true
		}
		if next := removeString(f.GlobalPinnedTopics, topicID); !sameStringList(next, f.GlobalPinnedTopics) {
			f.GlobalPinnedTopics = next
			changed = true
		}
		if next, removed := groupsWithoutTopic(f.GlobalGroups, topicID); removed {
			f.GlobalGroups = next
			f.GlobalGroupsRevision++
			changed = true
		}
	}
	if opt.tombstone {
		if next := prependUniqueString(f.DeletedTopics, topicID); !sameStringList(next, f.DeletedTopics) {
			f.DeletedTopics = next
			changed = true
		}
	}
	for i, p := range f.Projects {
		if !inScope("project", p.Root) {
			continue
		}
		if next := removeString(p.Topics, topicID); !sameStringList(next, p.Topics) {
			f.Projects[i].Topics = next
			changed = true
		}
		if next := removeString(p.PinnedTopics, topicID); !sameStringList(next, p.PinnedTopics) {
			f.Projects[i].PinnedTopics = next
			changed = true
		}
		if next, removed := groupsWithoutTopic(p.Groups, topicID); removed {
			f.Projects[i].Groups = next
			f.Projects[i].GroupsRevision++
			changed = true
		}
	}
	return changed, nil
}

// reorderTopicOrder 是保序重排的收口（原 ReorderTopics 变更体，语义逐行
// 保留）：completeTopicOrder 保证输出是输入集合的置换（缺项补尾、未知/
// 重复即拒），归属成员不变。守卫按置换性质单独验证该豁免。
func reorderTopicOrder(scope, workspaceRoot string, orderedTopicIDs []string) error {
	return updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		if scope == "global" {
			next, orderErr := completeTopicOrder(orderedTopicIDs, f.GlobalTopics)
			if orderErr != nil {
				return false, orderErr
			}
			changed := !sameStringList(next, f.GlobalTopics) || !f.GlobalManualTopicOrder
			f.GlobalTopics, f.GlobalManualTopicOrder = next, true
			return changed, nil
		}
		i := projectIndexByRoot(f.Projects, workspaceRoot)
		if i < 0 {
			return false, fmt.Errorf("project %q not found", workspaceRoot)
		}
		next, orderErr := completeTopicOrder(orderedTopicIDs, f.Projects[i].Topics)
		if orderErr != nil {
			return false, orderErr
		}
		changed := !sameStringList(next, f.Projects[i].Topics) || !f.Projects[i].ManualTopicOrder
		f.Projects[i].Topics, f.Projects[i].ManualTopicOrder = next, true
		return changed, nil
	})
}

// --- Deprecated 转发垫片：仅供既有测试沿用旧名。生产代码一律直呼收点，
// 新增调用守卫会拦（topic_registry_write_guard_test.go）。 -------------

// prependTopicInProjectsFile forwards to registerTopic (deprecated shim).
//
// Deprecated: 生产代码改用 registerTopic；垫片仅为既有测试保留。
func prependTopicInProjectsFile(workspaceRoot, topicID string, ensureProject bool) error {
	// 单话题前置是显式登记（话题创建/活跃 tab 补登/回收站恢复）：清墓碑，
	// 让话题完整回归而不是半状态回归（原注释语义，由漏斗默认路径承接）。
	scope := "global"
	if strings.TrimSpace(workspaceRoot) != "" {
		scope = "project"
	}
	return registerTopic(scope, workspaceRoot, topicRegisterOpts{ensureProject: ensureProject}, topicID)
}

// prependTopicsInProjectsFile forwards to registerTopic (deprecated shim).
//
// Deprecated: 生产代码改用 registerTopic（批量 + respectTombstones）；
// 垫片仅为既有测试保留。
func prependTopicsInProjectsFile(workspaceRoot string, topicIDs []string, ensureProject bool) error {
	scope := "global"
	if strings.TrimSpace(workspaceRoot) != "" {
		scope = "project"
	}
	return registerTopic(scope, workspaceRoot, topicRegisterOpts{respectTombstones: true, ensureProject: ensureProject}, topicIDs...)
}

// removeTopicFromProjectsFile forwards to unregisterTopic (deprecated shim).
//
// Deprecated: 生产代码改用 unregisterTopic；垫片仅为既有测试保留。
func removeTopicFromProjectsFile(topicID string) error {
	return unregisterTopic(topicID, topicUnregisterOpts{tombstone: true})
}
