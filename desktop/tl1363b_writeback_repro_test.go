package main

// tl1363b ① 复现：delete_session 报 trashed=true、组织引用清 0 后，运行态把
// 组织引用写回（用户 20261105 实测：约 1 分钟后回弹 topicOrder[0]，条目改显
// 「10 分钟」继续在栏；重载菜单治不了——引用是真回来了）。
//
// 两个写回方（file:line 见各测试）：
//   A. 心跳引擎的 bound-topic 复用——resolveHeartbeatTopic 对 ReuseSession/
//      legacy 任务无条件返回 t.TopicID（heartbeat.go resolveHeartbeatTopic），
//      不查该 topic 是否已被删除（DeletedTopics 墓碑）。下一次 tick（间隔约
//      1 分钟）用它 openHeartbeatTabInactive → openTopicTabWithActivation 为
//      死 topic 新建会话文件（0 字节空壳制造机）→ tab 构建完成时
//      ensureTopicIndexed（tabs.go 构建完成修复链）清墓碑+前置插回注册表 →
//      saveProjectsFile 镜像 → 组织 sidecar topicOrder[0]。
//   B. tab 构建完成的索引修复——ensureTopicIndexedState 的 wasDeleted 分支
//      故意清墓碑（显式恢复语义），但 tab 构建链的自动修复也走同一入口，
//      于是任何还持有死 topic id 的运行态重建都能把组织引用写回。
//
// 修复判据（验收）：墓碑化 topic 之后——
//   ①-A 心跳绑定不再解析到死 topic（复用模式视为绑定已死，走新建）；
//   ①-B 构建修复链不再清墓碑/不再登记（显式恢复 restoreSessionTopicIndex
//       的清墓碑语义保持不变）；
//   用户不需要再手改 desktop-projects.json / desktop-project-tree-organization.json。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedHeartbeatBoundTopic 造一个「心跳任务绑定 + 有会话文件」的 global topic，
// 返回会话路径——delete 前的真实态。
func seedHeartbeatBoundTopic(t *testing.T, topicID string) string {
	t.Helper()
	paths := seedGlobalConvergeTopic(t, topicID, 1)
	return paths[0]
}

// seededTopicSessionPaths lists the session files the seed helper wrote for a
// topic (the trash step needs the real paths).
func seededTopicSessionPaths(t *testing.T, topicID string) []string {
	t.Helper()
	dir := desktopSessionDir("")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read session dir %s: %v", dir, err)
	}
	paths := []string{}
	for _, entry := range entries {
		if entry.IsDir() || !isSessionShapedFileName(entry.Name()) {
			continue
		}
		// The seed names carry the topic id in the file stem (conv-<topic>-0).
		if strings.Contains(entry.Name(), topicID) {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	if len(paths) == 0 {
		t.Fatalf("seeded session file for %q not found under %s", topicID, dir)
	}
	return paths
}

// ── ①-A：墓碑化 topic 的心跳绑定不得再解析回死 topic ────────────────────

func TestDeletedTopicHeartbeatBindingDoesNotResolveBackToTombstonedTopic(t *testing.T) {
	isolateDesktopUserDirs(t)
	topicID := "topic-tl1363b-hb"
	seedHeartbeatBoundTopic(t, topicID)

	app := NewApp()
	path := seededTopicSessionPaths(t, topicID)[0]
	if _, result, err := app.deleteCollabSession("contact-tl1363b", path, false); err != nil {
		t.Fatalf("deleteCollabSession: %v", err)
	} else if !result.Trashed {
		t.Fatalf("delete must trash: %+v", result)
	}
	assertTopicGoneFromAllThreeSources(t, topicID)
	assertTopicTombstoned(t, topicID)

	// 运行态写回方 A：心跳引擎下一次 tick 的绑定解析（ReuseSession 模式，
	// 绑定仍是死 topic id——heartbeat-tasks.json 里的 TopicID 不会自己消失）。
	engine := &HeartbeatEngine{app: app, pendingTopics: map[string]heartbeatPendingTopic{}}
	task := HeartbeatTask{ID: "hb-tl1363b", Title: "Bound", Prompt: "p", Interval: "30m", Enabled: true, ReuseSession: true, TopicID: topicID}
	updated, resolvedTopicID, _, ok := engine.resolveHeartbeatTopic(task, "global", "", "Heartbeat: Bound")
	if !ok {
		t.Fatalf("a tombstoned binding must still let the run proceed (fresh topic), ok=false")
	}
	if resolvedTopicID == topicID {
		t.Fatalf("runtime write-back repro: the tombstoned topic %q was handed back to the run — the next open recreates its session and the tab-build repair re-registers it (topicOrder[0] bounce)", topicID)
	}
	if containsDesktopString(loadProjectsFile().DeletedTopics, resolvedTopicID) {
		t.Fatalf("the replacement topic %q must not itself be tombstoned", resolvedTopicID)
	}
	if updated.TopicID == topicID {
		t.Fatalf("the task binding must move off the tombstoned topic (stays %q)", updated.TopicID)
	}
}

// ── ①-B：构建修复链不得清墓碑（显式恢复语义保持不变）────────────────────

func TestTabIndexRepairDoesNotResurrectTombstonedTopic(t *testing.T) {
	isolateDesktopUserDirs(t)
	topicID := "topic-tl1363b-repair"
	seedHeartbeatBoundTopic(t, topicID)

	app := NewApp()
	for _, path := range seededTopicSessionPaths(t, topicID) {
		if _, result, err := app.deleteCollabSession("contact-tl1363b", path, false); err != nil {
			t.Fatalf("deleteCollabSession(%s): %v", path, err)
		} else if !result.Trashed {
			t.Fatalf("delete must trash: %+v", result)
		}
	}
	assertTopicGoneFromAllThreeSources(t, topicID)
	assertTopicTombstoned(t, topicID)

	// 运行态写回方 B：tab 构建完成链对死 topic 的自动补登，走
	// repairTopicIndexAfterBuild 闸门面（构建链 tabs.go 同款入口）。
	if err := app.repairTopicIndexAfterBuild("global", "", topicID, "Heartbeat: "+topicID, topicTitleSourceAuto); err == nil {
		t.Fatalf("the build repair must refuse a tombstoned topic, not silently pass")
	}
	if containsDesktopString(loadProjectsFile().GlobalTopics, topicID) {
		t.Fatalf("runtime write-back repro: the tab-build repair re-registered tombstoned topic %q into globalTopics", topicID)
	}
	assertTopicTombstoned(t, topicID)

	// 显式恢复（回收站还原）语义保持不变：ensureTopicIndexed 仍清墓碑复活。
	if err := ensureTopicIndexed("global", "", topicID, "Restored", topicTitleSourceManual); err != nil {
		t.Fatalf("explicit restore re-index: %v", err)
	}
	if !containsDesktopString(loadProjectsFile().GlobalTopics, topicID) {
		t.Fatal("explicit restore must still resurrect the topic (restore path semantics unchanged)")
	}
	if containsDesktopString(loadProjectsFile().DeletedTopics, topicID) {
		t.Fatal("explicit restore must clear the tombstone")
	}
}

// ── 端到端：组织 sidecar 的 topicOrder 不回弹 ────────────────────────────

func TestOrganizationTopicOrderStaysCleanAfterRuntimeRebuild(t *testing.T) {
	isolateDesktopUserDirs(t)
	topicID := "topic-tl1363b-org"
	seedHeartbeatBoundTopic(t, topicID)

	app := NewApp()
	path := seededTopicSessionPaths(t, topicID)[0]
	if _, _, err := app.deleteCollabSession("contact-tl1363b", path, false); err != nil {
		t.Fatalf("deleteCollabSession: %v", err)
	}
	org := organizationFromProjectsFile(loadProjectsFile())
	if containsDesktopString(org.Global.TopicOrder, topicID) {
		t.Fatalf("the deleted topic must leave the organization topicOrder (got %+v)", org.Global.TopicOrder)
	}

	// 运行态链重放：心跳绑定解析 + 构建修复补登——两者都不得把引用写回。
	engine := &HeartbeatEngine{app: app, pendingTopics: map[string]heartbeatPendingTopic{}}
	task := HeartbeatTask{ID: "hb-org", Title: "Bound", Prompt: "p", Interval: "30m", Enabled: true, ReuseSession: true, TopicID: topicID}
	_, resolved, _, ok := engine.resolveHeartbeatTopic(task, "global", "", "Heartbeat: Bound")
	if !ok || resolved == topicID {
		t.Fatalf("heartbeat binding must not resolve back to %q (ok=%v resolved=%q)", topicID, ok, resolved)
	}
	if err := app.repairTopicIndexAfterBuild("global", "", topicID, "Heartbeat: "+topicID, topicTitleSourceAuto); err == nil {
		t.Fatalf("the build repair must refuse a tombstoned topic, not silently pass")
	}
	org = organizationFromProjectsFile(loadProjectsFile())
	if containsDesktopString(org.Global.TopicOrder, topicID) {
		t.Fatalf("runtime chain wrote the deleted topic back into topicOrder: %+v", org.Global.TopicOrder)
	}
}
