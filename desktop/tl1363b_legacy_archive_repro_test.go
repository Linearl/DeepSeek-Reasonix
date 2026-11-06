package main

// tl1363b ② 复现：manage_sessions archive 对无 contactId 的 legacy topic 报
// not in the contact directory 直接拒绝（用户 20261105 实测：0 字节空壳试了
// 4 轮全失败，最后关桌面版手改 desktop-projects.json 的 globalTopics 和
// desktop-project-tree-organization.json 的 global.topicOrder 才清掉）。
//
// 根因（file:line）：工具层 internal/agent/manage_sessions_tool.go 的
// forwardArgs 对四个会话 action（rename_session / move_session /
// reorder_session / archive）共用 resolve() = scanAddressable + ResolveTarget，
// 只认磁盘主转录扫描出的身份；注册表空壳（树里有、目录查无）没有可解析
// 身份 → ResolveTarget 报 not in the contact directory。delete_session 的
// 单发与批量路径都有 1145/tl1144 的回落（delete_session_tool.go
// RegistryShellProbe → executeShell），archive 面没有——1144 只修了
// delete_session 的可达路径，archive（及 rename/move/reorder）是漏面。
//
// 修复判据（验收）：与 delete_session 同一身份解析——目录解析失败时走同一
// 个 RegistryShellProbe 回落；空壳经 archive 干净收敛（三处真相源+墓碑），
// 用户不再需要手改两个 json；活会话/有真实历史的残留不受影响（探针终审
// heartbeatTopicIsShell 原语义，不放宽）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/tool"
)

// newManageSessionsShellTool wires the real App probe + host manage face into
// a manage_sessions tool — the same wiring the desktop uses (AgentManageSessions).
func newManageSessionsShellTool(t *testing.T, app *App, root string) tool.Tool {
	t.Helper()
	return agent.NewManageSessionsTool(agent.SessionCollabConfig{
		Enabled:            true,
		SessionDir:         config.SessionDir(),
		WorkspaceRoot:      root,
		RegistryShellProbe: app.resolveRegistryShell,
	}, app.AgentManageSessions)
}

// ── ② 主场景：纯注册表空壳（无会话文件）经 archive 收敛 ─────────────────

func TestManageSessionsArchiveReachesRegistryShell(t *testing.T) {
	isolateDesktopUserDirs(t)
	enableRegistryShellCleanupForTest(t)
	root := t.TempDir()
	seedGlobalShellScene(t, "topic-tl1363b-shell", "Ghost Heartbeat")

	app := NewApp()
	tool := newManageSessionsShellTool(t, app, root)

	// Dry run: reachable, and the note names the metadata-only consequence.
	out, err := tool.Execute(t.Context(), []byte(`{"action":"archive","target":"topic-tl1363b-shell"}`))
	if err != nil {
		t.Fatalf("archive dry run on a registry shell must be reachable (same identity resolution as delete_session), got: %v", err)
	}
	var dry struct {
		Status  string `json:"status"`
		TopicID string `json:"topicId"`
		Sessions []struct {
			SessionPath string `json:"sessionPath"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &dry); err != nil {
		t.Fatalf("dry run must return JSON: %v\n%s", err, out)
	}
	if dry.Status != "dry_run" || dry.TopicID != "topic-tl1363b-shell" {
		t.Fatalf("dry run must report the shell archive plan: %+v", dry)
	}
	if !containsDesktopString(loadProjectsFile().GlobalTopics, "topic-tl1363b-shell") {
		t.Fatal("a dry run must not touch the registry")
	}

	// Confirm: the shell converges — the user never edits the two json files.
	out, err = tool.Execute(t.Context(), []byte(`{"action":"archive","target":"topic-tl1363b-shell","confirm":true}`))
	if err != nil {
		t.Fatalf("archive confirm on a registry shell must succeed, got: %v", err)
	}
	var done struct {
		Status  string `json:"status"`
		TopicID string `json:"topicId"`
	}
	if err := json.Unmarshal([]byte(out), &done); err != nil {
		t.Fatalf("confirm must return JSON: %v\n%s", err, out)
	}
	if done.Status != "archived" || done.TopicID != "topic-tl1363b-shell" {
		t.Fatalf("confirm result must report the archive: %+v", done)
	}
	assertTopicGoneFromAllThreeSources(t, "topic-tl1363b-shell")
	if !containsDesktopString(loadProjectsFile().DeletedTopics, "topic-tl1363b-shell") {
		t.Fatal("the archived shell must carry a tombstone so repairs cannot resurrect it")
	}
	org := organizationFromProjectsFile(loadProjectsFile())
	if containsDesktopString(org.Global.TopicOrder, "topic-tl1363b-shell") {
		t.Fatal("the archived shell must leave the organization topicOrder (no hand-editing desktop-project-tree-organization.json)")
	}
}

// ── ② 变体：0 字节空壳文件（sidecar 无 TopicID）——用户实际手改的形态 ────

func TestManageSessionsArchiveReachesZeroByteLegacyShell(t *testing.T) {
	isolateDesktopUserDirs(t)
	enableRegistryShellCleanupForTest(t)
	root := t.TempDir()
	// 注册在册 + CreatedAtMS=0（无真实历史）+ 一份 0 字节主转录（sidecar 不
	// 携带该 topic 的绑定）——通讯录扫描只能给出无 TopicID 的条目，按 topic_id
	// 解析必失败；heartbeatTopicIsShell 判据（CreatedAtMS==0）仍裁它是空壳。
	seedGlobalShellScene(t, "topic-tl1363b-zero", "Zero-byte Ghost")
	dir := desktopSessionDir("")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	shellPath := filepath.Join(dir, "conv-tl1363b-zero.jsonl")
	if err := os.WriteFile(shellPath, nil, 0o644); err != nil {
		t.Fatalf("write zero-byte shell: %v", err)
	}

	app := NewApp()
	tool := newManageSessionsShellTool(t, app, root)
	out, err := tool.Execute(t.Context(), []byte(`{"action":"archive","target":"topic-tl1363b-zero","confirm":true}`))
	if err != nil {
		t.Fatalf("archive confirm on a zero-byte legacy shell must succeed, got: %v", err)
	}
	var done struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &done); err != nil {
		t.Fatalf("confirm must return JSON: %v\n%s", err, out)
	}
	if done.Status != "archived" {
		t.Fatalf("confirm result must report the archive: %+v", done)
	}
	assertTopicGoneFromAllThreeSources(t, "topic-tl1363b-zero")
}

// ── 同机制入口：rename_session 走同一身份解析（不放宽为通用豁免）────────

func TestManageSessionsRenameUsesSameIdentityResolutionForShell(t *testing.T) {
	isolateDesktopUserDirs(t)
	enableRegistryShellCleanupForTest(t)
	root := t.TempDir()
	seedGlobalShellScene(t, "topic-tl1363b-rename", "Ghost To Rename")

	app := NewApp()
	tool := newManageSessionsShellTool(t, app, root)
	out, err := tool.Execute(t.Context(), []byte(`{"action":"rename_session","target":"topic-tl1363b-rename","title":"Renamed Ghost"}`))
	if err != nil {
		t.Fatalf("rename_session on a registry shell must resolve like delete_session, got: %v", err)
	}
	var done struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &done); err != nil {
		t.Fatalf("rename must return JSON: %v\n%s", err, out)
	}
	if done.Status != "renamed" {
		t.Fatalf("rename result must report success: %s", out)
	}
}

// ── 同机制入口清单（已接线独立工具）：rename_topic / move_topic_to_group ──

func TestStandaloneOrgToolsResolveRegistryShell(t *testing.T) {
	isolateDesktopUserDirs(t)
	enableRegistryShellCleanupForTest(t)
	root := t.TempDir()
	seedGlobalShellScene(t, "topic-tl1363b-standalone", "Ghost Standalone")

	app := NewApp()
	cfg := agent.SessionCollabConfig{
		Enabled:            true,
		SessionDir:         config.SessionDir(),
		WorkspaceRoot:      root,
		RegistryShellProbe: app.resolveRegistryShell,
	}

	// rename_topic: the topic_id face.
	renameTopic := agent.NewRenameTopicTool(cfg, app.renameCollabSession)
	out, err := renameTopic.Execute(t.Context(), []byte(`{"topic_id":"topic-tl1363b-standalone","title":"Standalone Rename"}`))
	if err != nil {
		t.Fatalf("rename_topic on a registry shell must resolve like delete_session, got: %v", err)
	}
	if !strings.Contains(out, `"status":"renamed"`) {
		t.Fatalf("rename_topic must report success: %s", out)
	}

	// move_topic_to_group: grouping keys off the topic id, so the shell moves.
	move := agent.NewMoveTopicToGroupTool(cfg, app.moveCollabTopicToGroup)
	out, err = move.Execute(t.Context(), []byte(`{"target":"topic-tl1363b-standalone","group":"Ghost Group"}`))
	if err != nil {
		t.Fatalf("move_topic_to_group on a registry shell must resolve like delete_session, got: %v", err)
	}
	if !strings.Contains(out, `"status":"moved"`) && !strings.Contains(out, `"status":"already_filed"`) {
		t.Fatalf("move_topic_to_group must report success: %s", out)
	}
	f := loadProjectsFile()
	for _, group := range f.GlobalGroups {
		if group.Title == "Ghost Group" && !containsDesktopString(group.TopicIDs, "topic-tl1363b-standalone") {
			t.Fatalf("the shell must be filed into the target group: %+v", group)
		}
	}
}

// ── 防护回归：目录可寻址的活会话与有真实历史的残留不被空壳面吞掉 ────────

func TestManageSessionsArchiveStillRefusesNonShellResidue(t *testing.T) {
	isolateDesktopUserDirs(t)
	enableRegistryShellCleanupForTest(t)
	root := t.TempDir()
	// 有真实历史（CreatedAtMS>0）的注册表残留：探针终审不放宽（AC4 同款）。
	if err := ensureTopicIndexedWithCreatedAt("global", "", "topic-tl1363b-real", "Real History", topicTitleSourceAuto, time.Now().UnixMilli()); err != nil {
		t.Fatalf("seed real-history residue: %v", err)
	}

	app := NewApp()
	tool := newManageSessionsShellTool(t, app, root)
	_, err := tool.Execute(t.Context(), []byte(`{"action":"archive","target":"topic-tl1363b-real","confirm":true}`))
	if err == nil || !strings.Contains(err.Error(), "not in the contact directory") {
		t.Fatalf("non-shell residue must keep the baseline not-found refusal, got %v", err)
	}
	if !containsDesktopString(loadProjectsFile().GlobalTopics, "topic-tl1363b-real") {
		t.Fatal("the real-history residue must stay listed")
	}
}
