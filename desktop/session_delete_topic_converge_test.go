package main

// 任务 tl-1144 定向测试：会话级删除路径的注册表收敛。
//
// 覆盖任务书验收点：
//   AC1（核心残留清理断言）topic 的最后一个会话文件被删（delete_session 宿主
//       面 / 桌面单会话 Delete / 延迟回收 goroutine）后，globalTopics 不再残
//       留该 topic——三处真相源（topic-state sqlite / desktop-projects.json
//       注册表与分组 / 标题表）一并收敛，并带墓碑防复活；
//   AC2 回归：多会话 topic 删其中一个，注册表条目原样保留（收敛绝不波及仍
//       持有会话文件的 topic）；
//   AC4 防护：被活 tab 引用的 topic 不收敛；未注册/空 id 幂等无事；
//   既有清理/轮转机制零回归由同包既有测试承载（heartbeat_rotation /
//       trashOrphanHeartbeatTopicsLocked / registry_shell_reconcile /
//       registry_shell_delete_reach 全部不改动、照跑）。

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// seedGlobalConvergeTopic registers one global topic with REAL history (it
// had a session file), a group reference (the 任务书坑② scene), and n session
// files in the global session dir. Returns the session paths.
func seedGlobalConvergeTopic(t *testing.T, topicID string, count int) []string {
	t.Helper()
	if err := ensureTopicIndexedWithCreatedAt("global", "", topicID, "Heartbeat: "+topicID, topicTitleSourceAuto, time.Now().UnixMilli()); err != nil {
		t.Fatalf("seed topic registry: %v", err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.GlobalGroups = append(f.GlobalGroups, desktopGroup{
			ID: "grp-conv-" + topicID, Title: "Converge Group", TopicIDs: []string{topicID},
		})
		return true, nil
	}); err != nil {
		t.Fatalf("seed group reference: %v", err)
	}
	dir := desktopSessionDir("")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir global session dir: %v", err)
	}
	paths := make([]string, 0, count)
	for i := 0; i < count; i++ {
		name := "conv-" + topicID + "-" + strconv.Itoa(i) + ".jsonl"
		path := writeTopicSessionWithPrompt(t, dir, name, topicID, "Heartbeat: "+topicID, "", "hello converge scene", time.Now())
		paths = append(paths, path)
	}
	return paths
}

func assertSessionGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("session file was not trashed: %s (err=%v)", path, err)
	}
}

func assertTopicStillListed(t *testing.T, topicID string) {
	t.Helper()
	f := loadProjectsFile()
	if !containsDesktopString(f.GlobalTopics, topicID) {
		t.Fatalf("topic %q must stay listed in globalTopics", topicID)
	}
	if containsDesktopString(f.DeletedTopics, topicID) {
		t.Fatalf("topic %q must not be tombstoned", topicID)
	}
}

func assertTopicTombstoned(t *testing.T, topicID string) {
	t.Helper()
	if !containsDesktopString(loadProjectsFile().DeletedTopics, topicID) {
		t.Fatalf("converged topic %q must carry a tombstone so repairs cannot resurrect it", topicID)
	}
}

// ── AC1：delete_session 宿主面删掉最后一个会话文件 → 注册表收敛 ──────────

func TestDeleteCollabSessionConvergesRegistryWhenLastSessionTrashed(t *testing.T) {
	isolateDesktopUserDirs(t)
	paths := seedGlobalConvergeTopic(t, "topic-conv-tool", 1)

	app := NewApp()
	_, result, err := app.deleteCollabSession("contact-conv", paths[0], false)
	if err != nil {
		t.Fatalf("deleteCollabSession: %v", err)
	}
	if !result.Trashed {
		t.Fatalf("the host delete must trash the session: %+v", result)
	}
	assertSessionGone(t, paths[0])
	// The residual-cleanup assertion: no shell left behind in any truth source.
	assertTopicGoneFromAllThreeSources(t, "topic-conv-tool")
	assertTopicTombstoned(t, "topic-conv-tool")
}

// ── AC1：桌面单会话 Delete 同契约；多会话 topic 删一个不动注册表（AC2）────

func TestDeleteSessionConvergesOnlyWhenLastSessionTrashed(t *testing.T) {
	isolateDesktopUserDirs(t)
	paths := seedGlobalConvergeTopic(t, "topic-conv-ui", 2)
	app := NewApp()

	// First delete: the topic keeps a session — the registry must not move.
	if err := app.deleteSession(paths[0]); err != nil {
		t.Fatalf("deleteSession(first): %v", err)
	}
	assertSessionGone(t, paths[0])
	if _, err := os.Stat(paths[1]); err != nil {
		t.Fatalf("the remaining session must survive: %v", err)
	}
	assertTopicStillListed(t, "topic-conv-ui")

	// Second delete: the last session leaves — the shell must not linger.
	if err := app.deleteSession(paths[1]); err != nil {
		t.Fatalf("deleteSession(last): %v", err)
	}
	assertSessionGone(t, paths[1])
	assertTopicGoneFromAllThreeSources(t, "topic-conv-ui")
	assertTopicTombstoned(t, "topic-conv-ui")
}

// ── AC1：延迟回收（teardown 超时）分支经 afterTrash 钩子同步收敛 ─────────

func TestDelayedSessionTrashRunsConvergenceHook(t *testing.T) {
	isolateDesktopUserDirs(t)
	paths := seedGlobalConvergeTopic(t, "topic-conv-delayed", 1)
	app := NewApp()

	ran := false
	delayedDesktopSessionTrash(filepath.Dir(paths[0]), paths[0], filepath.Base(paths[0]), nil, func() {
		ran = true
		app.convergeTopicsAfterSessionTrash(map[string]bool{"topic-conv-delayed": true})
	})
	if !ran {
		t.Fatal("the afterTrash hook must run after the delayed trash")
	}
	assertSessionGone(t, paths[0])
	assertTopicGoneFromAllThreeSources(t, "topic-conv-delayed")
	assertTopicTombstoned(t, "topic-conv-delayed")
}

// ── AC4 防护：活 tab 引用的 topic 不收敛 ─────────────────────────────────

func TestConvergeSkipsTabReferencedTopic(t *testing.T) {
	isolateDesktopUserDirs(t)
	if err := ensureTopicIndexedWithCreatedAt("global", "", "topic-conv-held", "Held", topicTitleSourceAuto, time.Now().UnixMilli()); err != nil {
		t.Fatalf("seed topic registry: %v", err)
	}

	app := NewApp()
	app.mu.Lock()
	app.tabs = map[string]*WorkspaceTab{"tab-held": {TopicID: "topic-conv-held"}}
	app.mu.Unlock()

	app.convergeTopicAfterLastSessionTrash("topic-conv-held")
	assertTopicStillListed(t, "topic-conv-held")
}

// ── AC4 防护：未注册 / 空 id → 幂等无事，墓碑零增量 ─────────────────────

func TestConvergeNoopForUnregisteredOrEmptyTopic(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()

	before := len(loadProjectsFile().DeletedTopics)
	app.convergeTopicAfterLastSessionTrash("topic-never-registered")
	app.convergeTopicAfterLastSessionTrash("")
	app.convergeTopicAfterLastSessionTrash("   ")
	after := len(loadProjectsFile().DeletedTopics)

	if before != after {
		t.Fatalf("convergence of unregistered/empty topics must be a no-op (tombstones %d -> %d)", before, after)
	}
}
