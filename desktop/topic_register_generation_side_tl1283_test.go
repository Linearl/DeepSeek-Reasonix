package main

import (
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/topicregistry"
)

// 任务 tl-1283 复现/回归：Global 泄露的**生成侧**（tl1262 修的是存量收敛，
// 本件修生成面）。用户 20261030 22:44 截图：ai-passport 的「剧情核对」「赤途·
// ch02 狩猎者重构」等 collab 会话列在 Global 区（蓝点=运行中心跳子对话），
// tl1262 done 随包 53 后同日复发。
//
// 用户机对账实锤（20261030 23:5x 读数）：
//   - 6 个复发 topic 现登记在 ai-passport 项目侧（desktop-projects.json，
//     mtime 22:59 已收敛），globalTopics/globalPinned/global.groups 零命中；
//   - 复发的生成源是「global 目录空壳误钉件」：projects/<global-slug>/
//     sessions/20261027-082459.297801600-session.jsonl（0 字节空会话），
//     meta 钉 scope=global + topic_id=剧情核对的原 topicId——openTopicTab
//     带 topicID 无 sessionPath 时在调用方 scope 目录 createEmptySessionFile
//     + pinNewEmptySessionBranchMeta 的产物；
//   - 空壳在案期间被活运行时绑定（desktop.log save-lock-key 探针 21:07 起
//     反复命中该路径），tab 修复链路（ensureTopicIndexed 按 tab.Scope）与
//     meta 重钉链路（restoreSessionTopicIndex 按 meta.Scope）每次构建都把
//     项目归属 topic 直灌 globalTopics——tl1262 的 convergeGlobalRegistration
//     只在「project 登记」事务里清 global 侧，覆盖不到这个方向。
//
// 修法（唯一归属判 project 的另一半落写口，与 tl1262 对称）：
//   ① registerTopicPrependMutator 的 global 分支滤除项目已持有 id（写口
//      兜底：tab 修复/meta 重钉/迁移/心跳所有自动化补登面都经收点）；
//   ② ensureTopicIndexedState 与 restoreSessionTopicIndex 共用
//      resolveTopicIndexTarget：global 补登先问注册表唯一归属，项目归属
//      按 owner 改道 project scope（754 绑定纠正同款）——补登与 meta 回写
//      同源，空壳触碰即自愈归位，杜绝「补登落了项目、meta 又钉回 global」
//      的半程收敛。
// 验收 = 新会话不再落 Global + 同一会话只出现在一个位置。

// TestRegisterGlobalRefusedForProjectOwnedTopicTL1283 钉写口拦截主形态：
// 项目已持有的 topic 再走 global 登记收点，globalTopics 不得收进（自动化
// 补登面的口），项目侧与唯一归属不动。报错会触发调用方重试循环，所以拦截
// 必须是静默 no-op（err=nil）。
func TestRegisterGlobalRefusedForProjectOwnedTopicTL1283(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	projectRoot := t.TempDir()
	if err := addProject(projectRoot, "ai-passport"); err != nil {
		t.Fatalf("add project: %v", err)
	}
	topicID := "topic_tl1283_chitu_ch02"
	if err := registerTopic("project", projectRoot, topicRegisterOpts{ensureProject: true}, topicID); err != nil {
		t.Fatalf("seed project registration: %v", err)
	}
	if err := registerTopic("project", projectRoot, topicRegisterOpts{pinUpdate: true, pinned: true}, topicID); err != nil {
		t.Fatalf("seed project pin: %v", err)
	}
	f := loadProjectsFile()
	if containsDesktopString(f.GlobalTopics, topicID) {
		t.Fatalf("precondition broken: topic already in globalTopics before the global registration attempt: %#v", f.GlobalTopics)
	}
	globalCountBefore := len(f.GlobalTopics)

	// 触发：tab 修复 / meta 重钉 / 心跳 global 任务补登的同一收点调用。
	if err := registerTopic("global", "", topicRegisterOpts{}, topicID); err != nil {
		t.Fatalf("global registration must be a silent no-op for project-owned topics (callers retry on error), got: %v", err)
	}

	f = loadProjectsFile()
	if containsDesktopString(f.GlobalTopics, topicID) {
		t.Fatalf("globalTopics took the project-owned topic (生成侧泄露未拦截): %#v", f.GlobalTopics)
	}
	if len(f.GlobalTopics) != globalCountBefore {
		t.Fatalf("globalTopics size changed %d -> %d without cause", globalCountBefore, len(f.GlobalTopics))
	}
	i := projectIndexByRoot(f.Projects, projectRoot)
	if i < 0 || !containsDesktopString(f.Projects[i].Topics, topicID) || !containsDesktopString(f.Projects[i].PinnedTopics, topicID) {
		t.Fatalf("project side disturbed by the refused global registration: %#v", f.Projects)
	}
	owner, ok := topicRegistryOwnerOf(topicID)
	if !ok || owner.Scope != topicregistry.ScopeProject || !sameProjectRoot(owner.Root, projectRoot) {
		t.Fatalf("index owner = %+v ok=%v, want project %q", owner, ok, projectRoot)
	}
	_ = app
}

// TestRegisterGlobalBatchFiltersProjectOwnedTL1283 钉混合批形态：迁移/修复
// 批量补登一批 id（含项目归属题 + 全新题）时，项目归属题被滤除、全新题照
// 常落 global——拦截不能把整个批次一并拒掉（否则存量迁移停摆）。
func TestRegisterGlobalBatchFiltersProjectOwnedTL1283(t *testing.T) {
	isolateDesktopUserDirs(t)
	projectRoot := t.TempDir()
	if err := addProject(projectRoot, "ai-passport"); err != nil {
		t.Fatalf("add project: %v", err)
	}
	projectOwned := "topic_tl1283_plot_check"
	fresh := "topic_tl1283_fresh_global"
	if err := registerTopic("project", projectRoot, topicRegisterOpts{ensureProject: true}, projectOwned); err != nil {
		t.Fatalf("seed project registration: %v", err)
	}

	if err := registerTopic("global", "", topicRegisterOpts{}, projectOwned, fresh); err != nil {
		t.Fatalf("batch register global: %v", err)
	}

	f := loadProjectsFile()
	if containsDesktopString(f.GlobalTopics, projectOwned) {
		t.Fatalf("project-owned topic leaked into globalTopics via batch: %#v", f.GlobalTopics)
	}
	if !containsDesktopString(f.GlobalTopics, fresh) {
		t.Fatalf("fresh topic lost by the batch filter: %#v", f.GlobalTopics)
	}
}

// TestRegisterGlobalFilterChecksProjectPinListTL1283 钉置顶形态：topic 只挂
// 在项目 PinnedTopics（列表字段被测试直写摘除的存量态）时同样拦截——归属
// 面是 topics+pinned 两个列表。
func TestRegisterGlobalFilterChecksProjectPinListTL1283(t *testing.T) {
	isolateDesktopUserDirs(t)
	projectRoot := t.TempDir()
	if err := addProject(projectRoot, "ai-passport"); err != nil {
		t.Fatalf("add project: %v", err)
	}
	topicID := "topic_tl1283_pinned_only"
	if err := registerTopic("project", projectRoot, topicRegisterOpts{ensureProject: true}, topicID); err != nil {
		t.Fatalf("seed project registration: %v", err)
	}
	if err := registerTopic("project", projectRoot, topicRegisterOpts{pinUpdate: true, pinned: true}, topicID); err != nil {
		t.Fatalf("seed project pin: %v", err)
	}
	// 造「只在 PinnedTopics」的存量态：列表字段测试直写摘除（守卫只扫生产文件）。
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		i := projectIndexByRoot(f.Projects, projectRoot)
		if i < 0 {
			t.Fatalf("project not found: %s", projectRoot)
		}
		if !containsDesktopString(f.Projects[i].PinnedTopics, topicID) {
			t.Fatalf("pin seed did not land in project PinnedTopics: %#v", f.Projects[i].PinnedTopics)
		}
		f.Projects[i].Topics = nil
		return true, nil
	}); err != nil {
		t.Fatalf("strip project topics: %v", err)
	}

	if err := registerTopic("global", "", topicRegisterOpts{}, topicID); err != nil {
		t.Fatalf("global registration: %v", err)
	}
	f := loadProjectsFile()
	if containsDesktopString(f.GlobalTopics, topicID) {
		t.Fatalf("topic held only in project PinnedTopics leaked into globalTopics: %#v", f.GlobalTopics)
	}
}

// TestEnsureTopicIndexedRetargetsGlobalRepairToProjectOwnerTL1283 钉 ensure
// 改道：tab 修复链路（ensureTopicIndexed 按 tab.Scope="global" 补登）遇到
// 项目归属 topic 时按 owner 改道——标题状态写项目侧 title root，注册落项目
// 侧，global 侧零写入。
func TestEnsureTopicIndexedRetargetsGlobalRepairToProjectOwnerTL1283(t *testing.T) {
	isolateDesktopUserDirs(t)
	projectRoot := t.TempDir()
	if err := addProject(projectRoot, "ai-passport"); err != nil {
		t.Fatalf("add project: %v", err)
	}
	topicID := "topic_tl1283_tab_repair"
	title := "赤途·ch02狩猎者重构"
	if err := registerTopic("project", projectRoot, topicRegisterOpts{ensureProject: true}, topicID); err != nil {
		t.Fatalf("seed project registration: %v", err)
	}

	// 触发：tab.Scope=global 的修复补登（desktop/tabs.go 控制器构建修复面的
	// 同一入口）。
	if err := ensureTopicIndexed("global", "", topicID, title, topicTitleSourceManual); err != nil {
		t.Fatalf("ensure topic indexed: %v", err)
	}

	f := loadProjectsFile()
	if containsDesktopString(f.GlobalTopics, topicID) {
		t.Fatalf("ensure repair pulled the project topic into globalTopics: %#v", f.GlobalTopics)
	}
	i := projectIndexByRoot(f.Projects, projectRoot)
	if i < 0 || !containsDesktopString(f.Projects[i].Topics, topicID) {
		t.Fatalf("ensure repair did not keep the project registration: %#v", f.Projects)
	}
	if got := loadTopicTitles(projectRoot)[topicID]; got != title {
		t.Fatalf("project title root holds %q, want %q (改道后标题状态必须落归属侧)", got, title)
	}
	if got := loadTopicTitles("")[topicID]; got == title {
		t.Fatalf("global title root picked up the project topic title (半程收敛)")
	}
}

// TestRestoreSessionTopicIndexHealsGlobalShellTL1283 钉本批复发的端到端
// 形态：global 目录空壳会话（0 字节 + meta 钉 scope=global + 项目归属
// topicId）被触碰时，restoreSessionTopicIndex 把 meta 重钉回项目归属、
// 补登落项目侧、globalTopics 保持净——同一会话只出现在一个位置。
func TestRestoreSessionTopicIndexHealsGlobalShellTL1283(t *testing.T) {
	isolateDesktopUserDirs(t)
	projectRoot := t.TempDir()
	if err := addProject(projectRoot, "ai-passport"); err != nil {
		t.Fatalf("add project: %v", err)
	}
	topicID := "topic_tl1283_shell_plot_check"
	title := "剧情核对"
	if err := registerTopic("project", projectRoot, topicRegisterOpts{ensureProject: true}, topicID); err != nil {
		t.Fatalf("seed project registration: %v", err)
	}

	// 造复发现场：global 目录空壳 + pinNewEmptySessionBranchMeta 钉 global
	// 归属（与用户机 20261027-082459.297801600-session.jsonl 同构）。
	globalDir := desktopSessionDir(globalWorkspaceRoot())
	shellPath, err := createEmptySessionFile(globalDir, "")
	if err != nil {
		t.Fatalf("create shell session: %v", err)
	}
	if err := pinNewEmptySessionBranchMeta(shellPath, "global", "", topicID, title, ""); err != nil {
		t.Fatalf("pin shell meta: %v", err)
	}

	// 触发：会话文件触碰链路的同一入口。
	if err := restoreSessionTopicIndex(globalDir, shellPath); err != nil {
		t.Fatalf("restore session topic index: %v", err)
	}

	// 验收①：空壳 meta 被重钉回项目归属（自愈，不只是登记面绕过）。
	meta, ok, err := agent.LoadBranchMeta(shellPath)
	if err != nil || !ok {
		t.Fatalf("reload shell meta: ok=%v err=%v", ok, err)
	}
	if meta.Scope != "project" || !sameProjectRoot(meta.WorkspaceRoot, projectRoot) {
		t.Fatalf("shell meta not healed: scope=%q root=%q, want project %q", meta.Scope, meta.WorkspaceRoot, projectRoot)
	}
	// 验收②：注册面单一居所——项目侧持有，global 侧零命中。
	f := loadProjectsFile()
	if containsDesktopString(f.GlobalTopics, topicID) {
		t.Fatalf("globalTopics took the shell topic (同一会话双显): %#v", f.GlobalTopics)
	}
	i := projectIndexByRoot(f.Projects, projectRoot)
	if i < 0 || !containsDesktopString(f.Projects[i].Topics, topicID) {
		t.Fatalf("project registration lost during the heal: %#v", f.Projects)
	}
	// 验收③：唯一归属索引裁决 = project。
	owner, ownerOK := topicRegistryOwnerOf(topicID)
	if !ownerOK || owner.Scope != topicregistry.ScopeProject || !sameProjectRoot(owner.Root, projectRoot) {
		t.Fatalf("index owner = %+v ok=%v, want project %q", owner, ownerOK, projectRoot)
	}
}

// TestResolveTopicIndexTargetAsymmetricTL1283 钉改道的非对称边界：global
// 归属不反向改道——project 调用撞 global 归属 topic 保持原路径（归属裁决
// 判 project，但 project 侧显式调用是更具体的意图，改道只服务 global 面
// 的自动化补登）。
func TestResolveTopicIndexTargetAsymmetricTL1283(t *testing.T) {
	isolateDesktopUserDirs(t)
	globalTopic := "topic_tl1283_global_owner"
	if err := registerTopic("global", "", topicRegisterOpts{}, globalTopic); err != nil {
		t.Fatalf("seed global registration: %v", err)
	}

	gotScope, gotRoot := resolveTopicIndexTarget("project", filepath.Join(t.TempDir(), "proj"), globalTopic)
	if gotScope != "project" {
		t.Fatalf("project-scope caller was retargeted (scope=%q) — 改道只服务 global 补登面", gotScope)
	}
	if gotRoot == "" {
		t.Fatalf("project-scope caller lost its root")
	}

	// global 调用 + global 归属 = 原路径不动。
	gotScope, gotRoot = resolveTopicIndexTarget("global", "", globalTopic)
	if gotScope != "global" || gotRoot != "" {
		t.Fatalf("global-owned topic was retargeted: scope=%q root=%q", gotScope, gotRoot)
	}

	// 读侧 miss（未注册 topic）= 原路径不动。
	orphan := "topic_tl1283_orphan"
	gotScope, gotRoot = resolveTopicIndexTarget("global", "", orphan)
	if gotScope != "global" || gotRoot != "" {
		t.Fatalf("unregistered topic was retargeted: scope=%q root=%q", gotScope, gotRoot)
	}
}
