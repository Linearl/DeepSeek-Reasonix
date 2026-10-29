package main

// 任务 tl-1144：会话级删除路径的注册表收敛（globalTopics 空壳残留的产生链补清）。
//
// 产生链（20261024 缺陷上报：21 条 go-build 心跳会话 15 条空壳）：会话级删除
// 路径——桌面单会话 Delete（deleteSession）与 delete_session 工具的宿主面
// （deleteCollabSession）——trash 会话文件后只清会话目录条目
// （removeSessionCatalogPath），从不调 deleteTopic。topic 的最后一个会话文件
// 被删后，desktop-projects.json 的 globalTopics/project.Topics 条目（连同分组
// 成员与标题表）残留成空壳：树照常渲染，delete_session 按通讯录（磁盘主转录
// 扫描）解析报 not found——「树里显示可交互 → 删时说不在目录」死循环。
//
// 对照：topic 级归档（trashTopic → commitTopicArchive，topic_archive.go）在
// 同一位置调 a.deleteTopic 收敛注册表。本修复把会话级路径补齐到同一契约——
// 是既有写路径上漏掉的那一步，不是第二套清理机制；既有清理面（任务移除级联
// trashOrphanHeartbeatTopicsLocked、1145 对账 reconcileRegistryShells、
// delete_session 空壳面 resolve/trashRegistryShell）职责不变，仍是各自触发器
// 下的兜底，与本补清无时序冲突（全部经同一条 deleteTopic 写路径，幂等）。
//
// 与心跳轮转（Task 500）的交互：轮转只做交接继任，明确永不删会话文件
// （heartbeat_rotation.go 头注「the old session itself is NEVER archived or
// trashed by this mechanism」），与本收敛无交集。
//
// 防护（全部继承既有判定，不再造）：
//   1. 活 tab / 分离 tab 仍持有该 topic ⇒ 不动（同 resolveRegistryShell 的
//      「可能正在创建」保护，tabReferencedTopicIDs）；
//   2. topic 在任一已知会话目录仍有主转录 ⇒ 不动（多会话 topic 删其中一个
//      是常态，topicHasAnySessionFileOnApp 与 heartbeatTopicIsShell 文件半
//      判据同源）；
//   3. topic 未注册（注册表无条目）⇒ 无事可做；
//   4. 收敛走既有 deleteTopic：unregisterTopic 墓碑 + 全 scope 注册列表/置顶/
//      分组成员剔除 + 标题表清理 + 树刷新——任务书坑②「被分组 topicIds 引用」
//      由该写路径即时满足（unregisterTopicMutator 的 groupsWithoutTopic）；
//   5. 失败只告警不回滚删除（与 topic_archive 的「cleanup remains pending」
//      同姿态）：文件已进回收站，注册表收敛由下一次 deleteTopic 重试或 1145
//      对账（开关开启时）兜底。
//
// 证人（witness）：被删会话属于哪个注册表 topic，必须在文件移动「前」取证——
// 之后文件已不在。两个证人按权威排序：被拆除的活 tab 绑定（重建 tab 可能已把
// 会话收养到与分支元数据不同的 topic 之下），其次文件自身的 branch meta
// sidecar（未打开的会话只有这一个证人）。

import (
	"log/slog"
	"strings"

	"reasonix/internal/agent"
)

// convergeTopicsAfterSessionTrash converges every registry topic in the set
// whose last session file just left (see the file header). The set shape
// mirrors the deleted-session topic witnesses so callers can pass the map
// straight through, including into the delayed-trash goroutine (after the map
// has stopped changing — reads only from there on).
func (a *App) convergeTopicsAfterSessionTrash(topics map[string]bool) {
	for topicID := range topics {
		a.convergeTopicAfterLastSessionTrash(topicID)
	}
}

// convergeTopicAfterLastSessionTrash removes topicID from the desktop registry
// when its last session file is gone and nothing still holds it. Every guard
// is a no-op return — convergence must never be observable for a topic that is
// still alive, still referenced, or was never registered.
func (a *App) convergeTopicAfterLastSessionTrash(topicID string) {
	topicID = strings.TrimSpace(topicID)
	if topicID == "" {
		return
	}
	if a.tabReferencedTopicIDs()[topicID] {
		slog.Debug("desktop: skip topic registry convergence", "topic", topicID,
			"reason", "a tab references the topic (possibly being used right now)")
		return
	}
	if topicHasAnySessionFileOnApp(a, topicID) {
		return // the topic still holds a real session — a normal partial delete
	}
	if !topicRegisteredInRegistry(topicID) {
		return // nothing registered to converge
	}
	slog.Info("desktop: converging topic registry after its last session was trashed", "topic", topicID,
		"path", "deleteTopic (tombstone + registry/pins/groups + title tables + tree refresh)")
	if err := a.deleteTopic(topicID); err != nil {
		slog.Warn("desktop: topic registry cleanup after session delete remains pending", "topic", topicID, "err", err)
	}
}

// topicWitnessesFromRuntimeBindings merges the topic ids of the runtime
// bindings being torn down into the witness set (the primary witness: a tab
// binding is what the tree actually rendered for this session).
func topicWitnessesFromRuntimeBindings(topics map[string]bool, removed []removedSessionRuntime) {
	for _, item := range removed {
		if id := strings.TrimSpace(item.topicID); id != "" {
			topics[id] = true
		}
	}
}

// branchMetaTopicWitness returns the topic id recorded in the session's branch
// meta sidecar — the only witness for a session no tab ever opened ("" when
// the sidecar is absent or unreadable).
func branchMetaTopicWitness(sessionPath string) string {
	meta, ok, err := agent.LoadBranchMeta(sessionPath)
	if err != nil || !ok {
		return ""
	}
	return strings.TrimSpace(meta.TopicID)
}
