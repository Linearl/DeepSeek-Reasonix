package main

// tl1560 验收钉：分组右键「迁移到其他分区」。
//
// 第一阶段钉（迁移能力后端整体回滚纪律，返修保持零回退）：
//   ② 组切入目标花名册 + 两侧 GroupsRevision 递增（前端菜单钉
//      在 tl1560-group-migrate.test.tsx）；
//   ③ 成员会话物理搬家 + .meta scope/workspaceRoot 改写 + topic-session 索引
//      失效 + topic 注册（GlobalTopics → Projects[t].Topics）与标题分册随迁；
//   ④ 工作中会话拦截：任一成员有活跃运行时 → 整组拒绝、零写入；
//   ⑤ 失败回滚编排：目标组 id 撞车（注册面失败）与目标目录同名冲突（搬家
//      中途失败）两条路径都必须把磁盘恢复到迁移动前形态（半迁移态为红）；
//   ⑥ 迁移后归位：目标目录可按 topic 命中会话、global 目录不再命中（跨
//      scope 影子条目不出现）、标题可读。
//
// 返修钉（20261112 用户装机实测四问题）：
//   R1 (②) 项目间互迁：project 源 → project 目标整组落盘形态 + meta 改写
//      为目标项目身份；project 源 → global 回迁同一条事务纪律；
//   R2 (①) 名册一致性：meta 指向其他分区的迷路成员显式剔除出迁移名册
//      （SkippedTopics 留痕），注册与组侧都不再把它带去目标分区；
//   R3 (①④) 活口收口：成员可见 tab 打开时迁移先关 tab 再落盘（第一阶段
//      持 sessionRemovalMu 关 tab = closeTabRuntime 重入死锁，成员 tab 打开
//      真跑迁移即挂——本钉在进程内全链跑通即回归证）；关不掉的 tab 整体
//      中止零写入；detached 空闲副本可指认拒绝（errGroupMigrateDetachedHolder）。
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
	"reasonix/internal/jobs"
)

// migrateBusyCtrl is the minimal active-runtime stub: Running=true makes
// hasActiveRuntimeWork() true (tabs.go RuntimeStatus contract).
type migrateBusyCtrl struct {
	stubSessionAPI
	status control.RuntimeStatus
}

func (c *migrateBusyCtrl) RuntimeStatus() control.RuntimeStatus { return c.status }

// migrateIdleCtrl is the minimal idle-runtime stub: RuntimeStatus 全零
// （不活跃）——活跃门与 detached 指认只需要这一个读数。
type migrateIdleCtrl struct {
	stubSessionAPI
}

func (migrateIdleCtrl) RuntimeStatus() control.RuntimeStatus { return control.RuntimeStatus{} }

// migrateUnclosableCtrl refuses the pre-close durable snapshot, so
// CloseTabWithPolicy fails and the migration must abort before any write.
type migrateUnclosableCtrl struct {
	stubSessionAPI
}

func (migrateUnclosableCtrl) RuntimeStatus() control.RuntimeStatus { return control.RuntimeStatus{} }
func (migrateUnclosableCtrl) Snapshot() error                      { return errors.New("snapshot refused") }
func (migrateUnclosableCtrl) Cancel()                              {}
func (migrateUnclosableCtrl) Jobs() []jobs.View                    { return nil }

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
	if err := registerTestProjects(t, app, projectRoot); err != nil {
		t.Fatalf("register project: %v", err)
	}
	globalDir := desktopSessionDir(globalWorkspaceRoot())
	targetDir := desktopSessionDir(projectRoot)
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global dir: %v", err)
	}
	return app, projectRoot, globalDir, targetDir
}

// registerTestProjects registers the given roots in the projects file.
func registerTestProjects(t *testing.T, app *App, roots ...string) error {
	t.Helper()
	_ = app
	return updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		for _, root := range roots {
			f.Projects = append(f.Projects, desktopProject{Root: root, Title: "Proj " + filepath.Base(root)})
		}
		return true, nil
	})
}

// writeMigrateSession writes one transcript plus its meta sidecar (and an
// events-log sidecar to prove stem-prefix moves carry the whole family).
func writeMigrateSession(t *testing.T, dir, name, topicID string) {
	t.Helper()
	writeMigrateSessionIdentity(t, dir, name, topicID, "global", "")
}

// writeMigrateSessionIdentity is writeMigrateSession with an explicit meta
// identity (返修 R1/R2: project-scope 源与迷路成员都要能造出来).
func writeMigrateSessionIdentity(t *testing.T, dir, name, topicID, scope, workspaceRoot string) {
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
		ID:            strings.TrimSuffix(name, ".jsonl"),
		Scope:         scope,
		WorkspaceRoot: workspaceRoot,
		TopicID:       topicID,
		TopicTitle:    "T-" + topicID,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
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

// 验收 ②③⑥：整组迁移落盘形态（global → project，第一阶段钉保持）。
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

	result, err := app.MigrateSessionGroup("global", "", "g-1", projectRoot)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if result.MovedSessions != 2 || len(result.MigratedTopics) != 2 {
		t.Fatalf("receipt movedSessions=%d migratedTopics=%d, want 2/2", result.MovedSessions, len(result.MigratedTopics))
	}
	if len(result.SkippedTopics) != 0 {
		t.Fatalf("unexpected skipped members: %v", result.SkippedTopics)
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

	_, err = app.MigrateSessionGroup("global", "", "g-1", projectRoot)
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

	if _, err := app.MigrateSessionGroup("global", "", "g-1", projectRoot); err == nil {
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

	if _, err := app.MigrateSessionGroup("global", "", "g-1", projectRoot); err == nil {
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
	if _, err := app.MigrateSessionGroup("global", "", heartbeatGroupID, projectRoot); err != nil {
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

// 返修 R1a：project → project 互迁整组落盘形态（② 用户裁决扩面）。
func TestMigrateSessionGroupBetweenProjectsRewritesIdentities(t *testing.T) {
	home := isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	srcRoot := filepath.Join(home, "project-src")
	dstRoot := filepath.Join(home, "project-dst")
	for _, root := range []string{srcRoot, dstRoot} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", root, err)
		}
	}
	if err := registerTestProjects(t, app, srcRoot, dstRoot); err != nil {
		t.Fatalf("register projects: %v", err)
	}
	srcDir := desktopSessionDir(srcRoot)
	dstDir := desktopSessionDir(dstRoot)

	if err := registerTopic("project", srcRoot, topicRegisterOpts{}, "topic-p", "topic-q"); err != nil {
		t.Fatalf("register project topics: %v", err)
	}
	if err := desktopTopicState.setTitle(srcRoot, "topic-p", "T-topic-p", topicTitleSourceManual); err != nil {
		t.Fatalf("seed title topic-p: %v", err)
	}
	writeMigrateSessionIdentity(t, srcDir, "20261112-100000-p.jsonl", "topic-p", "project", srcRoot)
	writeMigrateSessionIdentity(t, srcDir, "20261112-100100-q.jsonl", "topic-q", "project", srcRoot)
	if err := app.SaveSessionGroups("project", srcRoot, []desktopGroup{{ID: "g-2", Title: "G2", TopicIDs: []string{"topic-p", "topic-q"}}}); err != nil {
		t.Fatalf("save source group: %v", err)
	}

	result, err := app.MigrateSessionGroup("project", srcRoot, "g-2", dstRoot)
	if err != nil {
		t.Fatalf("migrate project→project: %v", err)
	}
	if result.MovedSessions != 2 || len(result.MigratedTopics) != 2 || len(result.SkippedTopics) != 0 {
		t.Fatalf("receipt moved=%d topics=%v skipped=%v", result.MovedSessions, result.MigratedTopics, result.SkippedTopics)
	}

	// 组切到目标项目，源项目花名册清空。
	src, err := app.GetProjectGroups("project", srcRoot)
	if err != nil || len(src.Groups) != 0 {
		t.Fatalf("source roster still has groups: %+v err=%v", src.Groups, err)
	}
	dst, err := app.GetProjectGroups("project", dstRoot)
	if err != nil || len(dst.Groups) != 1 || dst.Groups[0].ID != "g-2" {
		t.Fatalf("target roster missing migrated group: %+v err=%v", dst.Groups, err)
	}
	if !sameStringList(dst.Groups[0].TopicIDs, []string{"topic-p", "topic-q"}) {
		t.Fatalf("target members = %v", dst.Groups[0].TopicIDs)
	}

	// meta 改写为目标项目身份；源目录不再命中；目标目录命中且 scope/root 正确。
	for _, topicID := range []string{"topic-p", "topic-q"} {
		if left := topicSessionMatches(srcDir, topicID); len(left) != 0 {
			t.Fatalf("%s still matches in source dir: %v", topicID, left)
		}
		moved := topicSessionMatches(dstDir, topicID)
		if len(moved) != 1 {
			t.Fatalf("%s matches %d in dst dir, want 1", topicID, len(moved))
		}
		if moved[0].scope != "project" || !sameProjectRoot(moved[0].workspaceRoot, dstRoot) {
			t.Fatalf("%s identity = %s/%s, want project/%s", topicID, moved[0].scope, moved[0].workspaceRoot, dstRoot)
		}
	}

	// 注册搬家：源项目 Topics 清、目标项目 Topics 持有、global 侧不收。
	f := loadProjectsFile()
	si := projectIndexByRoot(f.Projects, srcRoot)
	di := projectIndexByRoot(f.Projects, dstRoot)
	if si < 0 || di < 0 {
		t.Fatalf("projects vanished: si=%d di=%d", si, di)
	}
	if containsDesktopString(f.Projects[si].Topics, "topic-p") || containsDesktopString(f.Projects[si].Topics, "topic-q") {
		t.Fatalf("source project still registers members: %v", f.Projects[si].Topics)
	}
	if !containsDesktopString(f.Projects[di].Topics, "topic-p") || !containsDesktopString(f.Projects[di].Topics, "topic-q") {
		t.Fatalf("target project topics missing members: %v", f.Projects[di].Topics)
	}
	if containsDesktopString(f.GlobalTopics, "topic-p") {
		t.Fatalf("project→project migration leaked a member into GlobalTopics")
	}

	// 标题分册随迁（源册 → 目标册）。
	if title := loadTopicTitle(dstRoot, "topic-p"); title != "T-topic-p" {
		t.Fatalf("dst title for topic-p = %q, want T-topic-p", title)
	}
	if title := loadTopicTitle(srcRoot, "topic-p"); title != "" {
		t.Fatalf("src title for topic-p = %q, want empty after move", title)
	}
}

// 返修 R1b：project → global 回迁（互迁的第三向，同一事务纪律）。
func TestMigrateSessionGroupProjectToGlobalRestoresRegistrations(t *testing.T) {
	home := isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	srcRoot := filepath.Join(home, "project-back")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", srcRoot, err)
	}
	if err := registerTestProjects(t, app, srcRoot); err != nil {
		t.Fatalf("register project: %v", err)
	}
	srcDir := desktopSessionDir(srcRoot)
	globalDir := desktopSessionDir(globalWorkspaceRoot())

	if err := registerTopic("project", srcRoot, topicRegisterOpts{}, "topic-r"); err != nil {
		t.Fatalf("register project topic: %v", err)
	}
	writeMigrateSessionIdentity(t, srcDir, "20261112-110000-r.jsonl", "topic-r", "project", srcRoot)
	if err := app.SaveSessionGroups("project", srcRoot, []desktopGroup{{ID: "g-3", Title: "G3", TopicIDs: []string{"topic-r"}}}); err != nil {
		t.Fatalf("save source group: %v", err)
	}

	// 目标 = global：targetWorkspaceRoot 传空。
	result, err := app.MigrateSessionGroup("project", srcRoot, "g-3", "")
	if err != nil {
		t.Fatalf("migrate project→global: %v", err)
	}
	if result.MovedSessions != 1 || len(result.MigratedTopics) != 1 {
		t.Fatalf("receipt moved=%d topics=%v", result.MovedSessions, result.MigratedTopics)
	}

	if groups, err := app.GetProjectGroups("project", srcRoot); err != nil || len(groups.Groups) != 0 {
		t.Fatalf("source roster not empty: %+v err=%v", groups.Groups, err)
	}
	if groups, err := app.GetProjectGroups("global", ""); err != nil || len(groups.Groups) != 1 || groups.Groups[0].ID != "g-3" {
		t.Fatalf("global roster missing migrated group: %+v err=%v", groups.Groups, err)
	}
	if left := topicSessionMatches(srcDir, "topic-r"); len(left) != 0 {
		t.Fatalf("session still matches in project dir: %v", left)
	}
	moved := topicSessionMatches(globalDir, "topic-r")
	if len(moved) != 1 || moved[0].scope != "global" {
		t.Fatalf("moved identity = %+v, want global scope", moved)
	}
	f := loadProjectsFile()
	if !containsDesktopString(f.GlobalTopics, "topic-r") {
		t.Fatalf("topic-r not re-registered in GlobalTopics: %v", f.GlobalTopics)
	}
	if si := projectIndexByRoot(f.Projects, srcRoot); si < 0 || containsDesktopString(f.Projects[si].Topics, "topic-r") {
		t.Fatalf("source project still registers topic-r")
	}
	// 同分区直接拒绝（global→global）。
	if _, err := app.MigrateSessionGroup("global", "", "g-3", ""); !errors.Is(err, errGroupMigrateSamePartition) {
		t.Fatalf("global→global err = %v, want errGroupMigrateSamePartition", err)
	}
}

// 返修 R2：迷路成员（meta 指向其他分区）显式剔除出迁移名册——组与注册
// 都不再把它带去目标分区，SkippedTopics 留痕（①成员对话脱离分组根因 A）。
func TestMigrateSessionGroupLeavesForeignMemberBehind(t *testing.T) {
	home := isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	projectRoot := filepath.Join(home, "project-a")
	foreignRoot := filepath.Join(home, "project-foreign")
	for _, root := range []string{projectRoot, foreignRoot} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", root, err)
		}
	}
	if err := registerTestProjects(t, app, projectRoot, foreignRoot); err != nil {
		t.Fatalf("register projects: %v", err)
	}
	globalDir := desktopSessionDir(globalWorkspaceRoot())
	targetDir := desktopSessionDir(projectRoot)

	if err := registerTopic("global", "", topicRegisterOpts{}, "topic-a", "topic-x"); err != nil {
		t.Fatalf("register topics: %v", err)
	}
	writeMigrateSession(t, globalDir, "20261112-120000-a.jsonl", "topic-a")
	// topic-x 的会话 meta 已指向另一项目（迷路形态）。
	writeMigrateSessionIdentity(t, globalDir, "20261112-120100-x.jsonl", "topic-x", "project", foreignRoot)
	if err := app.SaveSessionGroups("global", "", []desktopGroup{{ID: "g-1", Title: "G1", TopicIDs: []string{"topic-a", "topic-x"}}}); err != nil {
		t.Fatalf("save group: %v", err)
	}

	result, err := app.MigrateSessionGroup("global", "", "g-1", projectRoot)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !sameStringList(result.MigratedTopics, []string{"topic-a"}) {
		t.Fatalf("migratedTopics = %v, want [topic-a]", result.MigratedTopics)
	}
	if !sameStringList(result.SkippedTopics, []string{"topic-x"}) {
		t.Fatalf("skippedTopics = %v, want [topic-x]", result.SkippedTopics)
	}

	// 目标组只含迁移名册成员。
	target, err := app.GetProjectGroups("project", projectRoot)
	if err != nil || len(target.Groups) != 1 || !sameStringList(target.Groups[0].TopicIDs, []string{"topic-a"}) {
		t.Fatalf("target group members = %+v, want [topic-a] only", target.Groups)
	}
	// 迷路成员注册原地（global），其文件原地未动。
	f := loadProjectsFile()
	if !containsDesktopString(f.GlobalTopics, "topic-x") {
		t.Fatalf("foreign member lost its global registration")
	}
	if containsDesktopString(f.GlobalTopics, "topic-a") {
		t.Fatalf("migrated member should have left GlobalTopics")
	}
	if matches := topicSessionMatches(globalDir, "topic-x"); len(matches) != 1 {
		t.Fatalf("foreign member file disturbed: %v", matches)
	}
	if matches := topicSessionMatches(targetDir, "topic-x"); len(matches) != 0 {
		t.Fatalf("foreign member file followed the group to the target dir: %v", matches)
	}
}

// 返修 R3a：成员可见 tab 打开时迁移先关 tab 再落盘（全链进程内跑通）。
// 第一阶段持 sessionRemovalMu 调 closeTabRuntime（其内部再取同一把锁）——
// 成员 tab 打开时真跑迁移即互斥重入死锁，本钉以 10s 看门狗回归该形态。
func TestMigrateSessionGroupClosesOpenMemberTabAndProceeds(t *testing.T) {
	app, projectRoot, globalDir, _ := newGroupMigrateTestApp(t)
	migrateFixture(t, app, globalDir)
	app.mu.Lock()
	app.tabs = map[string]*WorkspaceTab{
		// Ctrl=nil：closeTabRuntime 全链对 nil ctrl 有守卫（snapshot/teardown
		// 都短路），等价「空闲且可关」的真实 tab 形态。
		"t1": {ID: "t1", TopicID: "topic-a"},
	}
	app.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, err := app.MigrateSessionGroup("global", "", "g-1", projectRoot)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("migrate with an open member tab: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("migration deadlocked while closing the member tab (phase-1 regression)")
	}
	app.mu.RLock()
	stillBound := 0
	for _, tab := range app.tabs {
		if tab != nil && tab.TopicID == "topic-a" {
			stillBound++
		}
	}
	app.mu.RUnlock()
	if stillBound != 0 {
		t.Fatalf("member tab survived the migration: %d bound tabs", stillBound)
	}
	if topicSessionMatches(desktopSessionDir(projectRoot), "topic-a") == nil {
		t.Fatalf("session not moved despite successful tab close")
	}
}

// 返修 R3b：关不掉的 tab（快照写口拒绝）→ 整体中止、零写入。
func TestMigrateSessionGroupAbortsWhenMemberTabCannotClose(t *testing.T) {
	app, projectRoot, globalDir, _ := newGroupMigrateTestApp(t)
	migrateFixture(t, app, globalDir)
	before, err := os.ReadFile(filepath.Join(globalDir, "20261111-090000-a.jsonl"))
	if err != nil {
		t.Fatalf("read source transcript: %v", err)
	}
	app.mu.Lock()
	app.tabs = map[string]*WorkspaceTab{
		"t1": {ID: "t1", TopicID: "topic-a", Ctrl: migrateUnclosableCtrl{}},
	}
	app.mu.Unlock()

	if _, err := app.MigrateSessionGroup("global", "", "g-1", projectRoot); err == nil {
		t.Fatalf("migration succeeded despite an unclosable member tab")
	}
	after, err := os.ReadFile(filepath.Join(globalDir, "20261111-090000-a.jsonl"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("source transcript changed during aborted migration (err=%v)", err)
	}
	roster, err := app.GetProjectGroups("global", "")
	if err != nil || len(roster.Groups) != 1 || roster.Groups[0].ID != "g-1" {
		t.Fatalf("group moved despite aborted migration: %+v err=%v", roster.Groups, err)
	}
	if topicSessionMatches(desktopSessionDir(projectRoot), "topic-a") != nil {
		t.Fatalf("session moved despite aborted migration")
	}
}

// 返修 R3c：成员的 detached 空闲副本 → 可指认拒绝（下一步真实存在：
// 任务中心打开该后台会话并关闭其标签页）。
func TestMigrateSessionGroupRefusesDetachedHolder(t *testing.T) {
	app, projectRoot, globalDir, _ := newGroupMigrateTestApp(t)
	migrateFixture(t, app, globalDir)
	app.mu.Lock()
	app.detachedSessions = map[string]*WorkspaceTab{
		sessionRuntimeKey(filepath.Join(globalDir, "20261111-090000-a.jsonl")): {
			ID: "d1", TopicID: "topic-a", Ctrl: migrateIdleCtrl{},
		},
	}
	app.mu.Unlock()

	_, err := app.MigrateSessionGroup("global", "", "g-1", projectRoot)
	if !errors.Is(err, errGroupMigrateDetachedHolder) {
		t.Fatalf("err = %v, want errGroupMigrateDetachedHolder", err)
	}
	roster, err := app.GetProjectGroups("global", "")
	if err != nil || len(roster.Groups) != 1 || roster.Groups[0].ID != "g-1" {
		t.Fatalf("group moved despite detached refusal: %+v err=%v", roster.Groups, err)
	}
	if topicSessionMatches(desktopSessionDir(projectRoot), "topic-a") != nil {
		t.Fatalf("session moved despite detached refusal")
	}
}
