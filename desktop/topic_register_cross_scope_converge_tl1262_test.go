package main

import (
	"context"
	"fmt"
	"testing"

	"reasonix/internal/sessioncatalog"
	"reasonix/internal/topicregistry"
)

// 任务 tl-1262 复现/回归：跨 scope 双注册的写侧收敛（会话泄露 Global 的
// 数据正确性缺陷）。用户实测：ai-passport「赤途」系列会话同时出现在项目
// 分组与 Global 区。管道对账（20261030 用户机读数）：
//   - desktop-projects.json 列表管道：ai-passport 侧干净（topics/分组成员
//     与 globalTopics 零交集），同族存量实例 1 例（cooking_action_
//     recognition 的 topic_20261022-074604 双列表）；
//   - catalog 投影管道：同一 topic 双行——(global,"") 与 (project,root)
//     各一条（5 条无 session 空壳行 metadata_present=1 + 1 条有空白 global
//     session 文件的行）。
// 根因：注册写口只加不删——registerTopicPrependMutator 把 topic 登记进
// 项目列表时从不摘除 global 侧注册（754 打开链路的绑定 scope 纠正、自动
// 化按项目根补登都会走这条路），持久真相源从此双行；注册唯一索引重建时
// 「遗留重复判 project」的裁决只救了读侧直查，desktop-projects.json 与
// catalog 投影（SyncMetadata 原样喂两份）不收敛。
// 修法：project 登记在同一事务、同一收点内收敛 global 侧注册（topics/
// pinned/分组成员），裁决规则沿用既有「遗留重复判 project」。验收 =
// 同一会话只出现在一个位置（注册列表层 + catalog 投影层）。

// TestRegisterProjectConvergesGlobalRegistrationTL1262 钉主形态：global
// 已注册（含置顶与分组成员）的 topic 登记进项目后，global 侧三个注册面
// （topics/pinned/分组成员）同事务收敛，唯一归属索引跟手。
func TestRegisterProjectConvergesGlobalRegistrationTL1262(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	projectRoot := t.TempDir()
	if err := addProject(projectRoot, "ai-passport"); err != nil {
		t.Fatalf("add project: %v", err)
	}
	topicID := "topic_tl1262_ch02"

	// 现场：topic 注册在 global（列表 + 置顶 + 分组成员），正是移动/打开
	// 链路发生前的用户态。
	if err := registerTopic("global", "", topicRegisterOpts{}, topicID); err != nil {
		t.Fatalf("register global: %v", err)
	}
	if err := app.SaveSessionGroups("global", "", []desktopGroup{
		{ID: "g-arch", Title: "归档", TopicIDs: []string{topicID}},
	}); err != nil {
		t.Fatalf("seed global group: %v", err)
	}
	if err := registerTopic("global", "", topicRegisterOpts{pinUpdate: true, pinned: true}, topicID); err != nil {
		t.Fatalf("pin global: %v", err)
	}
	f := loadProjectsFile()
	if !containsDesktopString(f.GlobalTopics, topicID) || !containsDesktopString(f.GlobalPinnedTopics, topicID) {
		t.Fatalf("seed incomplete: topics=%#v pinned=%#v", f.GlobalTopics, f.GlobalPinnedTopics)
	}
	groupRevisionBefore := f.GlobalGroupsRevision

	// 触发：登记进项目（754 打开链路 / 自动化补登的同一收点调用）。
	if err := registerTopic("project", projectRoot, topicRegisterOpts{ensureProject: true}, topicID); err != nil {
		t.Fatalf("register project: %v", err)
	}

	// 验收①：项目列表持有该 topic。
	f = loadProjectsFile()
	i := projectIndexByRoot(f.Projects, projectRoot)
	if i < 0 {
		t.Fatalf("projects = %#v, want entry for %q", f.Projects, projectRoot)
	}
	if !containsDesktopString(f.Projects[i].Topics, topicID) {
		t.Fatalf("project topics = %#v, want %q", f.Projects[i].Topics, topicID)
	}
	// 验收②：global 三个注册面全部收敛——同一会话不再挂两处。
	if containsDesktopString(f.GlobalTopics, topicID) {
		t.Fatalf("globalTopics still holds the topic after project registration: %#v (tl1262 泄露未收敛)", f.GlobalTopics)
	}
	if containsDesktopString(f.GlobalPinnedTopics, topicID) {
		t.Fatalf("globalPinnedTopics still holds the topic: %#v", f.GlobalPinnedTopics)
	}
	for _, group := range f.GlobalGroups {
		if groupContainsTopic(group, topicID) {
			t.Fatalf("global group %q still holds the topic: %#v", group.ID, group.TopicIDs)
		}
	}
	if f.GlobalGroupsRevision <= groupRevisionBefore {
		t.Fatalf("global groups revision = %d, want bumped after member removal (was %d)", f.GlobalGroupsRevision, groupRevisionBefore)
	}
	// 验收③：唯一归属索引裁决 = project（与既有「遗留重复判 project」一致）。
	owner, ok := topicRegistryOwnerOf(topicID)
	if !ok || owner.Scope != topicregistry.ScopeProject || !sameProjectRoot(owner.Root, projectRoot) {
		t.Fatalf("index owner = %+v ok=%v, want project %q", owner, ok, projectRoot)
	}
}

// TestRegisterProjectConvergesPreexistingDoubleRegistrationTL1262 钉存量
// 形态（cooking_action_recognition 实例）：topic 已同时挂在 global 与项目
// 两个列表（遗留双行）时，对项目的再次登记（项目侧前置插入是 no-op）也
// 必须收敛 global 侧——不能因「项目列表未变化」提前返回吞掉收敛写。
func TestRegisterProjectConvergesPreexistingDoubleRegistrationTL1262(t *testing.T) {
	isolateDesktopUserDirs(t)
	projectRoot := t.TempDir()
	if err := addProject(projectRoot, "cooking_action_recognition"); err != nil {
		t.Fatalf("add project: %v", err)
	}
	topicID := "topic_tl1262_legacy_dup"

	// 造遗留双行：global 登记在先，项目列表由收点外的测试直写补上（模拟
	// 旧版本留下的持久态；守卫只扫生产文件，测试直写合法）。
	if err := registerTopic("global", "", topicRegisterOpts{}, topicID); err != nil {
		t.Fatalf("register global: %v", err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		i := projectIndexByRoot(f.Projects, projectRoot)
		if i < 0 {
			return false, fmt.Errorf("test: project not found: %s", projectRoot)
		}
		f.Projects[i].Topics = append([]string{topicID}, f.Projects[i].Topics...)
		return true, nil
	}); err != nil {
		t.Fatalf("seed legacy double registration: %v", err)
	}

	// 触发：对项目的再次登记（项目侧已持有，前置插入无变化）。
	if err := registerTopic("project", projectRoot, topicRegisterOpts{ensureProject: true}, topicID); err != nil {
		t.Fatalf("re-register project: %v", err)
	}

	f := loadProjectsFile()
	if containsDesktopString(f.GlobalTopics, topicID) {
		t.Fatalf("pre-existing global registration survived a project re-registration: %#v (存量双行未收敛)", f.GlobalTopics)
	}
	i := projectIndexByRoot(f.Projects, projectRoot)
	if i < 0 || !containsDesktopString(f.Projects[i].Topics, topicID) {
		t.Fatalf("project topics must keep the topic: index=%d topics=%#v", i, f.Projects)
	}
}

// TestRegisterGlobalKeepsProjectOwnerTL1262 钉契约边界（非对称性）：global
// 登记永不剥离项目归属——心跳/自动化/回收站恢复的全局登记不得抢走项目侧
// 的既有注册（与「遗留重复判 project」的裁决方向一致，读侧唯一索引兜底）。
func TestRegisterGlobalKeepsProjectOwnerTL1262(t *testing.T) {
	isolateDesktopUserDirs(t)
	projectRoot := t.TempDir()
	if err := addProject(projectRoot, "owner-project"); err != nil {
		t.Fatalf("add project: %v", err)
	}
	topicID := "topic_tl1262_reverse"

	if err := registerTopic("project", projectRoot, topicRegisterOpts{ensureProject: true}, topicID); err != nil {
		t.Fatalf("register project: %v", err)
	}
	if err := registerTopic("global", "", topicRegisterOpts{}, topicID); err != nil {
		t.Fatalf("register global: %v", err)
	}

	f := loadProjectsFile()
	i := projectIndexByRoot(f.Projects, projectRoot)
	if i < 0 || !containsDesktopString(f.Projects[i].Topics, topicID) {
		t.Fatalf("project ownership must survive a global registration: index=%d topics=%#v", i, f.Projects)
	}
	// 唯一归属索引维持 project 裁决（读侧单行不变）。
	owner, ok := topicRegistryOwnerOf(topicID)
	if !ok || owner.Scope != topicregistry.ScopeProject {
		t.Fatalf("index owner = %+v ok=%v, want project (adjudication unchanged)", owner, ok)
	}
}

// TestTL1262CatalogProjectionConvergesToEndToEnd 验收端到端读数：收敛前的
// SyncMetadata 会给 global 造出 catalog 投影行（用户态的 Global 区行）；
// 收敛后的 SyncMetadata 必须把该投影行删掉——同一会话在 catalog 投影层
// 也只出现在项目一侧。
func TestTL1262CatalogProjectionConvergesToEndToEnd(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	projectRoot := t.TempDir()
	if err := addProject(projectRoot, "ai-passport"); err != nil {
		t.Fatalf("add project: %v", err)
	}
	topicID := "topic_tl1262_projection"

	catalog, err := sessioncatalog.Open(context.Background(), sessioncatalog.Options{InMemory: true, DisableRepair: true})
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	app.sessionCatalog.Store(catalog)
	t.Cleanup(func() { app.stopSessionCatalog(0) })

	// 第一幕（事故形态）：topic 只注册在 global，SyncMetadata 投影出
	// (global,"") 行——这就是用户 Global 区看到的那一行。
	if err := registerTopic("global", "", topicRegisterOpts{}, topicID); err != nil {
		t.Fatalf("register global: %v", err)
	}
	if err := app.syncSessionCatalogMetadata(context.Background(), catalog); err != nil {
		t.Fatalf("sync metadata (global-only): %v", err)
	}
	// GetTopic 对零 session 行有墓碑遮罩；存在性断言用 WithSessionCount
	// 读法（task 550 对账同款）。
	if _, count, found, err := catalog.GetTopicWithSessionCount(context.Background(), sessioncatalog.TopicKey{Scope: "global", TopicID: topicID}); err != nil || !found {
		t.Fatalf("precondition: catalog global row must exist before convergence (err=%v found=%v count=%d)", err, found, count)
	}

	// 第二幕（修复）：登记进项目 → 列表收敛 → SyncMetadata 重建投影：
	// global 行被删（无 session 支撑的空壳行走既有 metadata_present 清扫），
	// 项目行出现。
	if err := registerTopic("project", projectRoot, topicRegisterOpts{ensureProject: true}, topicID); err != nil {
		t.Fatalf("register project: %v", err)
	}
	if err := app.syncSessionCatalogMetadata(context.Background(), catalog); err != nil {
		t.Fatalf("sync metadata (after convergence): %v", err)
	}
	if _, _, found, err := catalog.GetTopicWithSessionCount(context.Background(), sessioncatalog.TopicKey{Scope: "global", TopicID: topicID}); err != nil || found {
		t.Fatalf("catalog global row must be gone after convergence (err=%v found=%v) — 泄露会话未收敛", err, found)
	}
	if _, _, found, err := catalog.GetTopicWithSessionCount(context.Background(), sessioncatalog.TopicKey{Scope: "project", WorkspaceRoot: projectRoot, TopicID: topicID}); err != nil || !found {
		t.Fatalf("catalog project row must exist after convergence (err=%v found=%v)", err, found)
	}
}
