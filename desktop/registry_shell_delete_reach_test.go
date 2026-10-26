package main

// 任务 tl-1144 定向测试：delete_session 触达空壳 topic 的 desktop 终审判据。
//
// 覆盖任务书验收点：
//   AC1 「globalTopics 有登记+会话文件已删」空壳 → delete_session(topic_id)
//       成功，且三处真相源（topic-state sqlite / desktop-projects.json 注册
//       表与分组 / 标题表）均不再含该 topic；
//   AC2 正常（有会话文件）topic 的删除走原宿主文件回收路径，行为与现状
//       一致（回落逻辑绝不碰活会话）；
//   AC4 非空壳的注册表残留（topic-state 有真实历史 CreatedAtMS>0，哪怕文
//       件已不在、目录也查不到）不得被当成空壳删除——判据是
//       heartbeatTopicIsShell 原始语义，不得放宽；
//   AC5 幂等：对已删除 topic 重复清理安静收尾（不报错、不补 tombstone），
//       工具面第二次调用得到干净的「已不存在」。

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/tool"
)

// seedGlobalShellScene registers one global topic that is a TRUE #9614 shell:
// created in the topic-state sqlite with CreatedAtMS==0, listed in
// globalTopics AND inside a global group's topics, with no session file
// anywhere. Returns the isolated project root it also registered (unused by
// the shell, handy for live-topic companions).
func seedGlobalShellScene(t *testing.T, topicID, title string) {
	t.Helper()
	if err := ensureTopicIndexedWithCreatedAt("global", "", topicID, title, topicTitleSourceAuto, 0); err != nil {
		t.Fatalf("seed global shell topic: %v", err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.GlobalGroups = append(f.GlobalGroups, desktopGroup{
			ID: "grp-shell", Title: "Shell Group", TopicIDs: []string{topicID},
		})
		return true, nil
	}); err != nil {
		t.Fatalf("seed group reference: %v", err)
	}
}

// newShellProbeTool wires the real App probe/trash faces into a
// delete_session tool, failing the test if the normal file-trash host
// callback is ever reached (the shell face must never need it).
func newShellProbeTool(t *testing.T, app *App, root string) tool.Tool {
	t.Helper()
	return agent.NewDeleteSessionTool(agent.SessionCollabConfig{
		Enabled:            true,
		SessionDir:         config.SessionDir(),
		WorkspaceRoot:      root,
		RegistryShellProbe: app.resolveRegistryShell,
		RegistryShellTrash: app.trashRegistryShell,
	}, func(contactID, sessionPath string, dryRun bool) (agent.DeleteSessionImpact, agent.DeleteSessionResult, error) {
		t.Fatalf("host file-trash callback must never run on the shell face (got contactID=%q path=%q dryRun=%v)", contactID, sessionPath, dryRun)
		return agent.DeleteSessionImpact{}, agent.DeleteSessionResult{}, nil
	})
}

// assertTopicGoneFromAllThreeSources is the AC1 truth-source sweep: the
// topic-state sqlite, the desktop-projects.json registry (topics + pinned +
// group references) and the title tables must all be free of the topic.
func assertTopicGoneFromAllThreeSources(t *testing.T, topicID string) {
	t.Helper()
	f := loadProjectsFile()
	if containsDesktopString(f.GlobalTopics, topicID) {
		t.Fatal("truth source 1/3: topic still in globalTopics (desktop-projects.json)")
	}
	for _, group := range f.GlobalGroups {
		if containsDesktopString(group.TopicIDs, topicID) {
			t.Fatalf("truth source 2/3: group %q still references the topic", group.ID)
		}
	}
	snap, err := desktopTopicState.snapshot("")
	if err != nil {
		t.Fatalf("snapshot global topic-state: %v", err)
	}
	if _, ok := snap.Records[topicID]; ok {
		t.Fatal("truth source 3/3: topic-state sqlite still holds the record")
	}
	if _, ok := loadTopicTitles("")[topicID]; ok {
		t.Fatal("title table still holds the topic (sqlite mirror)")
	}
}

// ── AC1：空壳经 delete_session 触达，三处真相源一并收敛 ─────────────

func TestDeleteSessionShellReachConvergesThreeTruthSources(t *testing.T) {
	isolateDesktopUserDirs(t)
	enableRegistryShellCleanupForTest(t)
	root := t.TempDir()
	seedGlobalShellScene(t, "topic-tl1144-shell", "Ghost Heartbeat")

	app := NewApp()
	tool := newShellProbeTool(t, app, root)

	// Dry run: the shell is reachable and nothing moves yet.
	out, err := tool.Execute(t.Context(), []byte(`{"target":"topic-tl1144-shell"}`))
	if err != nil {
		t.Fatal(err)
	}
	var dry struct {
		Status string `json:"status"`
		Impact struct {
			Title string `json:"title"`
			Shell bool   `json:"shell"`
		} `json:"impact"`
	}
	if err := json.Unmarshal([]byte(out), &dry); err != nil {
		t.Fatalf("dry run must return JSON: %v\n%s", err, out)
	}
	if dry.Status != "dry_run" || !dry.Impact.Shell || dry.Impact.Title != "Ghost Heartbeat" {
		t.Fatalf("dry run must report the shell impact: %+v", dry)
	}
	if !containsDesktopString(loadProjectsFile().GlobalTopics, "topic-tl1144-shell") {
		t.Fatal("a dry run must not touch the registry")
	}

	// Confirm: converged — all three truth sources drop the topic.
	out, err = tool.Execute(t.Context(), []byte(`{"target":"topic-tl1144-shell","confirm":true}`))
	if err != nil {
		t.Fatal(err)
	}
	var done struct {
		Status string `json:"status"`
		Result struct {
			ContactID   string `json:"contactId"`
			Trashed     bool   `json:"trashed"`
			SessionPath string `json:"sessionPath"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &done); err != nil {
		t.Fatalf("confirm must return JSON: %v\n%s", err, out)
	}
	if done.Status != "trashed" || !done.Result.Trashed || done.Result.ContactID != "topic-tl1144-shell" || done.Result.SessionPath != "" {
		t.Fatalf("confirm result must report the shell trash: %+v", done)
	}
	assertTopicGoneFromAllThreeSources(t, "topic-tl1144-shell")
	if !containsDesktopString(loadProjectsFile().DeletedTopics, "topic-tl1144-shell") {
		t.Fatal("the converged shell must carry a tombstone so repairs cannot resurrect it")
	}
}

// ── AC4：有真实历史的注册表残留不是空壳——判据不放宽 ─────────────────

func TestDeleteSessionShellVerdictSparesRealHistoryResidue(t *testing.T) {
	isolateDesktopUserDirs(t)
	enableRegistryShellCleanupForTest(t)
	root := t.TempDir()
	// Registered + a topic-state record with REAL history (CreatedAtMS>0) and
	// no session file: the pre-tl-1144 predicate (registry × directory) would
	// have called this a shell; heartbeatTopicIsShell's original semantics
	// must spare it (a real history timestamp is always non-zero).
	if err := ensureTopicIndexedWithCreatedAt("global", "", "topic-real-residue", "Real History", topicTitleSourceAuto, time.Now().UnixMilli()); err != nil {
		t.Fatalf("seed real-history residue: %v", err)
	}

	app := NewApp()
	if _, _, ok := app.resolveRegistryShell("topic-real-residue"); ok {
		t.Fatal("a topic-state record with real history must never be nominated as a shell (AC4)")
	}

	tool := newShellProbeTool(t, app, root)
	_, err := tool.Execute(t.Context(), []byte(`{"target":"topic-real-residue","confirm":true}`))
	if err == nil || !strings.Contains(err.Error(), "is not in the contact directory") {
		t.Fatalf("the spared residue must surface the baseline not-found, got %v", err)
	}
	// The registry entry is untouched — no tombstone, still listed.
	f := loadProjectsFile()
	if !containsDesktopString(f.GlobalTopics, "topic-real-residue") {
		t.Fatal("the residue topic was removed — AC4 forbids deleting it as a shell")
	}
	if containsDesktopString(f.DeletedTopics, "topic-real-residue") {
		t.Fatal("the residue topic was tombstoned — AC4 forbids deleting it as a shell")
	}
}

// ── AC2：活会话（有文件）走原宿主删除路径，行为与现状一致 ───────────

func TestDeleteSessionLiveTopicKeepsNormalTrashPath(t *testing.T) {
	isolateDesktopUserDirs(t)
	enableRegistryShellCleanupForTest(t)
	root, _, livePath := seedShellProjectScene(t)

	app := NewApp()
	shellTrashCalls := 0
	tool := agent.NewDeleteSessionTool(agent.SessionCollabConfig{
		Enabled:            true,
		SessionDir:         config.SessionDir(),
		WorkspaceRoot:      root,
		RegistryShellProbe: app.resolveRegistryShell,
		RegistryShellTrash: func(topicID string, dryRun bool) error {
			shellTrashCalls++
			return nil
		},
	}, func(contactID, sessionPath string, dryRun bool) (agent.DeleteSessionImpact, agent.DeleteSessionResult, error) {
		if dryRun {
			return agent.DeleteSessionImpact{}, agent.DeleteSessionResult{}, nil
		}
		return agent.DeleteSessionImpact{SessionPath: sessionPath}, agent.DeleteSessionResult{
			ContactID: contactID, SessionPath: sessionPath, Trashed: true,
		}, nil
	})

	out, err := tool.Execute(t.Context(), []byte(`{"target":"topic-live","confirm":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if shellTrashCalls != 0 {
		t.Fatalf("the shell trash must never run for a live session (got %d calls)", shellTrashCalls)
	}
	var done struct {
		Status string `json:"status"`
		Result struct {
			SessionPath string `json:"sessionPath"`
			Trashed     bool   `json:"trashed"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &done); err != nil {
		t.Fatalf("confirm must return JSON: %v\n%s", err, out)
	}
	if done.Status != "trashed" || !done.Result.Trashed || done.Result.SessionPath != livePath {
		t.Fatalf("the live session must go through the normal file-trash path: %+v", done)
	}
	// The registry keeps the live topic — the fallback logic never touched it.
	if containsDesktopString(loadProjectsFile().DeletedTopics, "topic-live") {
		t.Fatal("the live topic was tombstoned by the shell face — AC2 regression")
	}
}

// ── AC5：幂等——重复清理安静收尾，不报错、不补 tombstone ────────────

func TestTrashRegistryShellIdempotentRerun(t *testing.T) {
	isolateDesktopUserDirs(t)
	enableRegistryShellCleanupForTest(t)
	seedGlobalShellScene(t, "topic-idem", "Idempotent Ghost")

	app := NewApp()
	if err := app.trashRegistryShell("topic-idem", false); err != nil {
		t.Fatalf("first confirm: %v", err)
	}
	first := loadProjectsFile()

	// Re-run on the already-deleted topic: a clean no-op, never an error.
	if err := app.trashRegistryShell("topic-idem", false); err != nil {
		t.Fatalf("idempotent re-run must not error, got %v", err)
	}
	second := loadProjectsFile()
	if len(first.DeletedTopics) != len(second.DeletedTopics) {
		t.Fatalf("re-run must not churn the tombstone list: %d -> %d", len(first.DeletedTopics), len(second.DeletedTopics))
	}
	assertTopicGoneFromAllThreeSources(t, "topic-idem")

	// The probe face agrees the topic is gone: a delete_session re-call gets
	// the deterministic baseline not-found (「已不存在」), never a crash.
	if _, _, ok := app.resolveRegistryShell("topic-idem"); ok {
		t.Fatal("the deleted topic must no longer be nominated as a shell")
	}
}
