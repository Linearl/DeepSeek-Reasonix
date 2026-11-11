package main

// tl1560 第一阶段验收钉：分组右键「迁移到其他项目」（global 分组 → 目标项目）。
//
//   ② 组切入 Projects[target].Groups + 两侧 GroupsRevision 递增（前端菜单钉
//      在 tl1560-group-migrate.test.tsx）；
//   ③ 成员会话物理搬家 + .meta scope/workspaceRoot 改写 + topic-session 索引
//      失效 + topic 注册（GlobalTopics → Projects[t].Topics）与标题分册随迁；
//   ④ 工作中会话拦截：任一成员有活跃运行时 → 整组拒绝、零写入；
//   ⑤ 失败回滚编排：目标组 id 撞车（注册面失败）与目标目录同名冲突（搬家
//      中途失败）两条路径都必须把磁盘恢复到迁移动前形态（半迁移态为红）；
//   ⑥ 迁移后归位：目标目录可按 topic 命中会话、global 目录不再命中（跨
//      scope 影子条目不出现）、标题可读——打开链（openTopicTabWithActivation
//      → findTopicSessionAcrossScopes → 注册表归属）消费的正是这些数据面。
//
// fileHeartbeatTopics 归属纪律补丁（tl1560）：迁出 global 的心跳来源话题不得
// 被 managed filing 拉回 global 定时任务组（跨 scope 影子条目，1074 家族）。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
)

// migrateBusyCtrl is the minimal active-runtime stub: Running=true makes
// hasActiveRuntimeWork() true (tabs.go RuntimeStatus contract).
type migrateBusyCtrl struct {
	stubSessionAPI
	status control.RuntimeStatus
}

func (c *migrateBusyCtrl) RuntimeStatus() control.RuntimeStatus { return c.status }

// newGroupMigrateTestApp isolates user dirs, registers one target project, and
// returns the app plus the global/target session directories.
func newGroupMigrateTestApp(t *testing.T) (*App, string, string, string) {
	t.Helper()
	home := isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	projectRoot := filepath.Join(home, "project-a")
	// 目标项目根真实存在是迁移的合法前提（花名册/标题分册写口都校验根存在）。
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.Projects = append(f.Projects, desktopProject{Root: projectRoot, Title: "Proj A"})
		return true, nil
	}); err != nil {
		t.Fatalf("register project: %v", err)
	}
	globalDir := desktopSessionDir(globalWorkspaceRoot())
	targetDir := desktopSessionDir(projectRoot)
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global dir: %v", err)
	}
	return app, projectRoot, globalDir, targetDir
}

// writeMigrateSession writes one transcript plus its meta sidecar (and an
// events-log sidecar to prove stem-prefix moves carry the whole family).
func writeMigrateSession(t *testing.T, dir, name, topicID string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("{\"marker\": \""+topicID+"\"}\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	events := strings.TrimSuffix(path, ".jsonl") + ".events.jsonl"
	if err := os.WriteFile(events, []byte("event-log\n"), 0o644); err != nil {
		t.Fatalf("write events sidecar: %v", err)
	}
	meta := agent.BranchMeta{
		ID:         strings.TrimSuffix(name, ".jsonl"),
		Scope:      "global",
		TopicID:    topicID,
		TopicTitle: "T-" + topicID,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := agent.SaveBranchMeta(path, meta); err != nil {
		t.Fatalf("write meta: %v", err)
	}
}

// migrateFixture registers two global topics with one session each and files
// both into group g-1. Titles live in the per-root topic-state book (the .meta
// TopicTitle is only a sidecar copy), so the fixture writes both.
func migrateFixture(t *testing.T, app *App, globalDir string) {
	t.Helper()
	if err := registerTopic("global", "", topicRegisterOpts{}, "topic-a", "topic-b"); err != nil {
		t.Fatalf("register topics: %v", err)
	}
	if err := desktopTopicState.setTitle("", "topic-a", "T-topic-a", topicTitleSourceManual); err != nil {
		t.Fatalf("seed title topic-a: %v", err)
	}
	if err := desktopTopicState.setTitle("", "topic-b", "T-topic-b", topicTitleSourceManual); err != nil {
		t.Fatalf("seed title topic-b: %v", err)
	}
	writeMigrateSession(t, globalDir, "20261111-090000-a.jsonl", "topic-a")
	writeMigrateSession(t, globalDir, "20261111-090100-b.jsonl", "topic-b")
	if err := app.SaveSessionGroups("global", "", []desktopGroup{{ID: "g-1", Title: "G1", TopicIDs: []string{"topic-a", "topic-b"}}}); err != nil {
		t.Fatalf("save group: %v", err)
	}
}

// 验收 ②③⑥：整组迁移落盘形态。
func TestMigrateSessionGroupMovesRosterMembersAndMeta(t *testing.T) {
	app, projectRoot, globalDir, targetDir := newGroupMigrateTestApp(t)
	migrateFixture(t, app, globalDir)

	before, err := app.GetProjectGroups("global", "")
	if err != nil || len(before.Groups) != 1 || before.Groups[0].ID != "g-1" {
		t.Fatalf("fixture roster missing g-1: %+v err=%v", before.Groups, err)
	}
	if before.Revision != 1 {
		t.Fatalf("fixture global revision = %d, want 1 (one SaveSessionGroups write)", before.Revision)
	}

	result, err := app.MigrateSessionGroupToProject("g-1", projectRoot)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if result.MovedSessions != 2 || len(result.MigratedTopics) != 2 {
		t.Fatalf("receipt movedSessions=%d migratedTopics=%d, want 2/2", result.MovedSessions, len(result.MigratedTopics))
	}

	// ② 组切到目标项目 + 两侧 revision 各自递增。
	after, err := app.GetProjectGroups("global", "")
	if err != nil {
		t.Fatalf("read global roster: %v", err)
	}
	if len(after.Groups) != 0 {
		t.Fatalf("global roster still has %d groups, want 0", len(after.Groups))
	}
	if after.Revision != before.Revision+1 {
		t.Fatalf("GlobalGroupsRevision %d -> %d, want +1", before.Revision, after.Revision)
	}
	target, err := app.GetProjectGroups("project", projectRoot)
	if err != nil {
		t.Fatalf("read target roster: %v", err)
	}
	if len(target.Groups) != 1 || target.Groups[0].ID != "g-1" {
		t.Fatalf("target roster missing migrated group: %+v", target.Groups)
	}
	if !sameStringList(target.Groups[0].TopicIDs, []string{"topic-a", "topic-b"}) {
		t.Fatalf("target group members = %v, want [topic-a topic-b]", target.Groups[0].TopicIDs)
	}
	if target.Revision != 1 {
		t.Fatalf("target GroupsRevision = %d, want 1", target.Revision)
	}

	// ③ 会话物理搬家 + meta 改写 + 索引失效。
	for _, topicID := range []string{"topic-a", "topic-b"} {
		left := topicSessionMatches(globalDir, topicID)
		if len(left) != 0 {
			t.Fatalf("%s still matches in global dir after migration: %v", topicID, left)
		}
		moved := topicSessionMatches(targetDir, topicID)
		if len(moved) != 1 {
			t.Fatalf("%s matches %d sessions in target dir, want 1: %v", topicID, len(moved), moved)
		}
		if moved[0].scope != "project" || !sameProjectRoot(moved[0].workspaceRoot, projectRoot) {
			t.Fatalf("%s meta scope/root = %s/%s, want project/%s", topicID, moved[0].scope, moved[0].workspaceRoot, projectRoot)
		}
	}

	// ③ topic 注册搬家：GlobalTopics 清空、目标项目 Topics 持有成员。
	f := loadProjectsFile()
	if len(f.GlobalTopics) != 0 {
		t.Fatalf("GlobalTopics = %v, want empty", f.GlobalTopics)
	}
	i := projectIndexByRoot(f.Projects, projectRoot)
	if i < 0 {
		t.Fatalf("target project vanished from projects file")
	}
	if !containsDesktopString(f.Projects[i].Topics, "topic-a") || !containsDesktopString(f.Projects[i].Topics, "topic-b") {
		t.Fatalf("target project topics = %v, want both members", f.Projects[i].Topics)
	}

	// ③ 标题分册随迁（显示名不丢）。
	if title := loadTopicTitle(projectRoot, "topic-a"); title != "T-topic-a" {
		t.Fatalf("target title for topic-a = %q, want T-topic-a", title)
	}
	if title := loadTopicTitle("", "topic-b"); title != "" {
		t.Fatalf("global title for topic-b = %q, want empty after move", title)
	}

	// ⑥ stem 前缀侧车全家随迁：events log 不留源目录、已在目标目录。
	if _, err := os.Stat(filepath.Join(targetDir, "20261111-090000-a.events.jsonl")); err != nil {
		t.Fatalf("events sidecar not moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(globalDir, "20261111-090000-a.events.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("events sidecar still in global dir (err=%v)", err)
	}
}

// 验收 ④：成员活跃 → 整组拒绝，零写入。
func TestMigrateSessionGroupRefusesWhileMemberActive(t *testing.T) {
	app, projectRoot, globalDir, _ := newGroupMigrateTestApp(t)
	migrateFixture(t, app, globalDir)
	before, err := os.ReadFile(filepath.Join(globalDir, "20261111-090000-a.jsonl"))
	if err != nil {
		t.Fatalf("read source transcript: %v", err)
	}

	app.mu.Lock()
	app.tabs = map[string]*WorkspaceTab{
		"t1": {ID: "t1", TopicID: "topic-a", Ctrl: &migrateBusyCtrl{status: control.RuntimeStatus{Running: true}}},
	}
	app.mu.Unlock()

	_, err = app.MigrateSessionGroupToProject("g-1", projectRoot)
	if !errors.Is(err, errGroupMigrateActiveWork) {
		t.Fatalf("err = %v, want errGroupMigrateActiveWork", err)
	}

	// 磁盘零变化：文件原地、组原地、topic 注册原地。
	after, err := os.ReadFile(filepath.Join(globalDir, "20261111-090000-a.jsonl"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("source transcript changed during refused migration (err=%v)", err)
	}
	roster, err := app.GetProjectGroups("global", "")
	if err != nil || len(roster.Groups) != 1 || roster.Groups[0].ID != "g-1" {
		t.Fatalf("group moved despite refusal: %+v err=%v", roster.Groups, err)
	}
	if topicSessionMatches(desktopSessionDir(projectRoot), "topic-a") != nil {
		t.Fatalf("session appeared in target dir despite refusal")
	}
}

// 验收 ⑤a：目标项目已有同 id 分组 → 注册面拒绝 + 搬家整体回滚。
func TestMigrateSessionGroupRollsBackOnTargetGroupCollision(t *testing.T) {
	app, projectRoot, globalDir, targetDir := newGroupMigrateTestApp(t)
	migrateFixture(t, app, globalDir)
	if err := app.SaveSessionGroups("project", projectRoot, []desktopGroup{{ID: "g-1", Title: "Target twin"}}); err != nil {
		t.Fatalf("plant target collision: %v", err)
	}

	if _, err := app.MigrateSessionGroupToProject("g-1", projectRoot); err == nil {
		t.Fatalf("migration succeeded despite target group id collision")
	}

	// 磁盘回到迁移动前：会话文件在源目录、组在 global、topic 注册在 global。
	if topicSessionMatches(globalDir, "topic-a") == nil || topicSessionMatches(globalDir, "topic-b") == nil {
		t.Fatalf("sessions not restored to global dir after rollback")
	}
	if entries, err := os.ReadDir(targetDir); err == nil && len(entries) != 0 {
		t.Fatalf("target dir not empty after rollback: %d entries", len(entries))
	}
	roster, err := app.GetProjectGroups("global", "")
	if err != nil || len(roster.Groups) != 1 || roster.Groups[0].ID != "g-1" {
		t.Fatalf("group left global despite rollback: %+v err=%v", roster.Groups, err)
	}
	if roster.Revision != 1 {
		t.Fatalf("GlobalGroupsRevision = %d after failed migration, want unchanged 1", roster.Revision)
	}
	f := loadProjectsFile()
	if !containsDesktopString(f.GlobalTopics, "topic-a") {
		t.Fatalf("topic-a left GlobalTopics despite rollback")
	}
}

// 验收 ⑤b：搬家第二站同名冲突 → 半迁移态必须整体回收（日志逆向）。
func TestMigrateSessionGroupRollsBackOnMidMoveFailure(t *testing.T) {
	app, projectRoot, globalDir, targetDir := newGroupMigrateTestApp(t)
	migrateFixture(t, app, globalDir)
	// 第二个会话的同名转录已占据目标目录 → 该会话搬家在动第一个文件之前
	// 失败；第一个会话已搬完，只能靠回滚日志恢复。
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "20261111-090100-b.jsonl"), []byte("obstacle\n"), 0o644); err != nil {
		t.Fatalf("plant collision: %v", err)
	}

	if _, err := app.MigrateSessionGroupToProject("g-1", projectRoot); err == nil {
		t.Fatalf("migration succeeded despite a destination name collision")
	}

	// 第一站已搬走的会话被日志逆向搬回；两站会话都在源目录且内容未损。
	data, err := os.ReadFile(filepath.Join(globalDir, "20261111-090000-a.jsonl"))
	if err != nil || !strings.Contains(string(data), "topic-a") {
		t.Fatalf("session-a not restored to global dir (err=%v data=%q)", err, string(data))
	}
	meta, ok, err := agent.LoadBranchMeta(filepath.Join(globalDir, "20261111-090000-a.jsonl"))
	if err != nil || !ok || meta.DefaultScope() != "global" {
		t.Fatalf("session-a meta not restored to global scope: ok=%v scope=%q err=%v", ok, meta.Scope, err)
	}
	if topicSessionMatches(globalDir, "topic-a") == nil || topicSessionMatches(globalDir, "topic-b") == nil {
		t.Fatalf("topic-session index lost members after rollback")
	}
	// 障碍物未被动过（移动链永不覆盖既有目标）。
	obstacle, err := os.ReadFile(filepath.Join(targetDir, "20261111-090100-b.jsonl"))
	if err != nil || string(obstacle) != "obstacle\n" {
		t.Fatalf("pre-planted obstacle was clobbered (err=%v data=%q)", err, string(obstacle))
	}
	roster, err := app.GetProjectGroups("global", "")
	if err != nil || len(roster.Groups) != 1 || roster.Groups[0].ID != "g-1" {
		t.Fatalf("group left global despite rollback: %+v err=%v", roster.Groups, err)
	}
}

// fileHeartbeatTopics 归属纪律：迁出 global 的心跳话题不再被拉回定时任务组。
func TestMigratedHeartbeatTopicNotRefiledIntoGlobalHeartbeatGroup(t *testing.T) {
	app, projectRoot, globalDir, _ := newGroupMigrateTestApp(t)
	if err := registerTopic("global", "", topicRegisterOpts{}, "topic-hb"); err != nil {
		t.Fatalf("register heartbeat topic: %v", err)
	}
	if err := desktopTopicState.markTopicOrigin("", "topic-hb", heartbeatTopicOrigin, ""); err != nil {
		t.Fatalf("mark heartbeat origin: %v", err)
	}

	// 迁移动前：managed filing 会建/填定时任务组（原行为锚点）。
	filed := app.fileHeartbeatTopics([]desktopGroup{})
	found := false
	for _, group := range filed {
		if group.ID == heartbeatGroupID && groupContainsTopic(group, "topic-hb") {
			found = true
		}
	}
	if !found {
		t.Fatalf("fixture broken: heartbeat topic not filed before migration")
	}

	// 迁移：托管组在磁盘上还没有壳（注入视图形态），迁移必须照样成立。
	writeMigrateSession(t, globalDir, "20261111-091000-hb.jsonl", "topic-hb")
	if _, err := app.MigrateSessionGroupToProject(heartbeatGroupID, projectRoot); err != nil {
		t.Fatalf("migrate managed heartbeat group: %v", err)
	}

	// 迁移动后：话题注册归属已在项目，filing 不得再拉回 global。
	for _, group := range app.fileHeartbeatTopics([]desktopGroup{}) {
		if groupContainsTopic(group, "topic-hb") {
			t.Fatalf("migrated heartbeat topic re-filed into global roster: %+v", group)
		}
	}
	roster, err := app.GetProjectGroups("global", "")
	if err != nil {
		t.Fatalf("read global roster: %v", err)
	}
	for _, group := range roster.Groups {
		if groupContainsTopic(group, "topic-hb") {
			t.Fatalf("migrated heartbeat topic visible in global roster: %+v", group)
		}
	}
	target, err := app.GetProjectGroups("project", projectRoot)
	if err != nil || len(target.Groups) != 1 || target.Groups[0].ID != heartbeatGroupID {
		t.Fatalf("heartbeat group not landed in target project: %+v err=%v", target.Groups, err)
	}
}
