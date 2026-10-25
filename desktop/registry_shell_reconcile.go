package main

// 任务 1145：会话目录 SSOT 统一（desktop 侧收敛）。
//
// 两套真相源的分工在此收口为一条规则：**磁盘上有主转录文件的会话才存在**
// （agent 工具侧 scanAddressable 的判定，经 agent.ScanAddressable 同源调用），
// 桌面注册表（desktop-projects.json 的 globalTopics / project.Topics /
// 分组 / 标题表）降级为「标题与组织缓存」，不再单独决定「是否存在」。
//
// 三个入口共享同一判定：
//  1. reconcileRegistryShells —— Task 550 对账的第四源：注册表有 + 目录无
//     ⇒ shell 失配（可观测）；开关开启时自动移出（复用桌面删除写路径
//     deleteTopic，含 tombstone / 分组 / 标题表收敛）。
//  2. resolveRegistryShell —— delete_session 的空壳识别探针（只认 topic_id，
//     防同名标题误指活会话，任务书 AC6）。
//  3. trashRegistryShell —— delete_session confirm 后的空壳清理，清理前
//     复核仍是空壳（TOCTOU：识别与执行之间可能已有新回合落盘）。
//
// 铁律 2：写路径（自动移出 / 空壳删除）全部挂在
// [agent] session_collab_registry_shell_cleanup（默认关）之下，关闭态行为
// 与既有版本一致（失配种类仍然上报——观测无副作用，Task 550 先例）。
// 开关在 desktop 侧单点判定（registryShellCleanupEnabled，实时读 user
// config），工具探针与对账两个消费方永不分叉。

import (
	"fmt"
	"log/slog"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// Topic-inventory mismatch kinds added by task 1145. Stable identifiers, like
// the task-550 kinds in topic_inventory.go.
const (
	topicInventoryRegistryTopicWithoutDirectory = "registry_topic_without_directory_entry"
	topicInventoryDirectorySessionWithoutRegistry = "directory_session_without_registry_entry"

	topicInventoryHandlingShellObserved = "开启 [agent] session_collab_registry_shell_cleanup（重启生效）后由对账自动移出，或用 delete_session(target=<topic_id>) 清理（1145 空壳触达）"
	topicInventoryHandlingShellRepaired = "已从注册表移出（树/分组/标题表同步收敛，复用桌面删除写路径）"
	topicInventoryHandlingShellFailed   = "自动移出失败（条目保留，下轮对账重试）："
	topicInventoryHandlingShellPinned   = "置顶条目不自动移出（留人处理）：可取消置顶后重跑对账，或 delete_session(target=<topic_id>) 清理"
	topicInventoryHandlingUnregistered  = "观测类：会话仍可经 agent 工具寻址；树侧登记由会话目录扫描/迁移补登，本对账不自动登记（只列失配）"
)

// registryShellCleanupEnabled is the single switch read for every write path
// of the task-1145 convergence. Real-time user-config read: the tool probes
// and the reconcile ask the same question through this one function, so a
// flip applies to both without a restart ordering hazard. Read errors fail
// closed (switch treated as off — 宁紧勿松).
func registryShellCleanupEnabled() bool {
	cfg, err := config.LoadUserConfigReadOnly()
	if err != nil {
		slog.Warn("desktop: read registry-shell cleanup switch failed; treating as off", "err", err)
		return false
	}
	return cfg.Agent.SessionCollabRegistryShellCleanup
}

// directoryTopicIDs is the SSOT view: which topics the agent tools can
// address right now, from the ONE scan the tools themselves use. Topic ids
// compare case-insensitively (registry ids and meta sidecars have drifted
// case in the wild).
func directoryTopicIDs() map[string]bool {
	out := map[string]bool{}
	for _, id := range agent.ScanAddressable(config.SessionDir(), globalWorkspaceRoot()) {
		if topicID := strings.TrimSpace(id.TopicID); topicID != "" {
			out[strings.ToLower(topicID)] = true
		}
	}
	return out
}

// registryShellRefs snapshots the desktop registry's displayable topic
// entries (global + every project) with their scope/root/pinned state.
// Tombstoned ids (deletedTopics) are not displayable and never shell entries.
func registryShellRefs() map[string]registryShellRef {
	f := loadProjectsFile()
	deleted := map[string]bool{}
	for _, id := range f.DeletedTopics {
		deleted[id] = true
	}
	refs := map[string]registryShellRef{}
	for _, id := range f.GlobalTopics {
		if id = strings.TrimSpace(id); id == "" || deleted[id] {
			continue
		}
		refs[id] = registryShellRef{scope: "global", pinned: containsDesktopString(f.GlobalPinnedTopics, id)}
	}
	for _, p := range f.Projects {
		root := normalizeProjectRoot(p.Root)
		for _, id := range p.Topics {
			if id = strings.TrimSpace(id); id == "" || deleted[id] {
				continue
			}
			if _, dup := refs[id]; dup {
				continue // global registration wins; one entry per topic
			}
			refs[id] = registryShellRef{scope: "project", root: root, pinned: containsDesktopString(p.PinnedTopics, id)}
		}
	}
	return refs
}

type registryShellRef struct {
	scope  string
	root   string
	pinned bool
}

// tabReferencedTopicIDs collects every topic a live or detached tab holds —
// the protection that keeps a session being created right now (registered
// before its first transcript write) from being swept as a shell.
func (a *App) tabReferencedTopicIDs() map[string]bool {
	out := map[string]bool{}
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, tabs := range []map[string]*WorkspaceTab{a.tabs, a.detachedSessions} {
		for _, tab := range tabs {
			if tab == nil {
				continue
			}
			if topicID := strings.TrimSpace(tab.TopicID); topicID != "" {
				out[topicID] = true
			}
		}
	}
	return out
}

// reconcileRegistryShells is the task-1145 fourth source of the task-550
// inventory reconcile. Observation (both mismatch kinds) runs always; the
// registry removal runs only when the switch is on. The scan is skipped
// entirely for an empty registry — nothing to converge.
func (a *App) reconcileRegistryShells(result *TopicInventoryReconcileResult) {
	refs := registryShellRefs()
	if len(refs) == 0 {
		return
	}
	cleanup := registryShellCleanupEnabled()
	directory := directoryTopicIDs()
	tabTopics := a.tabReferencedTopicIDs()

	// Shell class (AC4): registered in the tree, invisible to the directory.
	for topicID, ref := range refs {
		if tabTopics[topicID] || directory[strings.ToLower(topicID)] {
			continue
		}
		mismatch := TopicInventoryMismatch{
			Kind:    topicInventoryRegistryTopicWithoutDirectory,
			Scope:   ref.scope,
			TopicID: topicID,
			Detail:  "注册表登记过（树里显示）但 agent 目录（磁盘主转录扫描）查无：两套真相源分叉的空壳（1145）",
		}
		switch {
		case !cleanup:
			mismatch.Handling = topicInventoryHandlingShellObserved
		case ref.pinned:
			mismatch.Handling = topicInventoryHandlingShellPinned
		default:
			if err := a.deleteTopic(topicID); err != nil {
				slog.Warn("desktop: registry shell removal failed", "topic", topicID, "err", err)
				mismatch.Handling = topicInventoryHandlingShellFailed + err.Error()
			} else {
				mismatch.Repaired = true
				mismatch.Handling = topicInventoryHandlingShellRepaired
			}
		}
		result.Mismatches = append(result.Mismatches, mismatch)
	}

	// Unregistered class (AC4): a live main transcript the directory can
	// address but the tree does not list. Archived sessions are the normal
	// post-delete state (not listed by design); sessions under unregistered
	// project roots belong to an unregistered project, not to this class.
	registeredRoots := map[string]bool{}
	f := loadProjectsFile()
	for _, p := range f.Projects {
		registeredRoots[normalizeProjectRoot(p.Root)] = true
	}
	for _, id := range agent.ScanAddressable(config.SessionDir(), globalWorkspaceRoot()) {
		topicID := strings.TrimSpace(id.TopicID)
		if topicID == "" || id.Archived {
			continue
		}
		if _, inRegistry := refs[topicID]; inRegistry {
			continue
		}
		if tabTopics[topicID] {
			continue
		}
		if _, onDisk := directory[strings.ToLower(topicID)]; !onDisk {
			continue
		}
		if id.Scope == "project" && !registeredRoots[normalizeProjectRoot(id.Workspace)] {
			continue
		}
		result.Mismatches = append(result.Mismatches, TopicInventoryMismatch{
			Kind:        topicInventoryDirectorySessionWithoutRegistry,
			Scope:       id.Scope,
			WorkspaceRoot: id.Workspace,
			TopicID:     topicID,
			SessionPath: id.SessionPath,
			Detail:      "磁盘主转录存在、agent 目录可寻址，但桌面注册表（树）无条目：漏登类（1145）",
			Handling:    topicInventoryHandlingUnregistered,
		})
	}
}

// resolveRegistryShell is the delete_session probe face (task 1145): answers
// whether ref names a displayable registry topic that the directory cannot
// see (a shell). Only exact topic ids match — a title must never nominate a
// shell while a live conversation with the same title exists (AC6).
func (a *App) resolveRegistryShell(ref string) (topicID, title string, ok bool) {
	if !registryShellCleanupEnabled() {
		return "", "", false
	}
	topicID = strings.TrimSpace(ref)
	if topicID == "" {
		return "", "", false
	}
	if _, inRegistry := registryShellRefs()[topicID]; !inRegistry {
		return "", "", false
	}
	if a.tabReferencedTopicIDs()[topicID] {
		return "", "", false
	}
	if directoryTopicIDs()[strings.ToLower(topicID)] {
		return "", "", false
	}
	return topicID, a.registryTopicTitle(topicID), true
}

// registryTopicTitle resolves the display title the tree shows for a registry
// topic (title table first, default fallback otherwise) — the same title the
// directory would have shown for a live session.
func (a *App) registryTopicTitle(topicID string) string {
	if title := strings.TrimSpace(loadTopicTitles("")[topicID]); title != "" {
		return title
	}
	f := loadProjectsFile()
	for _, p := range f.Projects {
		if title := strings.TrimSpace(loadTopicTitles(normalizeProjectRoot(p.Root))[topicID]); title != "" {
			return title
		}
	}
	return topicID
}

// trashRegistryShell is the delete_session confirm face (task 1145). It
// re-verifies the shell verdict immediately before writing (the model turn
// between probe and confirm is enough time for a first transcript to land),
// then converges the registry through the desktop's own delete path
// (tombstone + groups + title table + tree refresh).
func (a *App) trashRegistryShell(topicID string, dryRun bool) error {
	topicID = strings.TrimSpace(topicID)
	if topicID == "" {
		return fmt.Errorf("topicID is required")
	}
	if a.tabReferencedTopicIDs()[topicID] || directoryTopicIDs()[strings.ToLower(topicID)] {
		return fmt.Errorf("topic %q is no longer a registry shell (a tab references it, or a session file exists on disk) — refusing the shell cleanup", topicID)
	}
	if dryRun {
		return nil
	}
	return a.deleteTopic(topicID)
}
