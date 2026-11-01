package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/sessioncatalog"
)

// tl1331 G10 喂入侧唯二源码守卫（架构专家裁决③修订形态）。
//
// 背景（tl1302 深查报告 W10/G10）：orderedTopicIDs 旧形态把标题表提升为
// 影子 SSOT——注册列表之外，凡标题表有条目的 topic 都被补全进喂入集合，
// 是 Global 区泄露的读侧真根因。tl1331 收口后喂入集合必须恒等于注册列表
// 集合（标题只查 title 不扩集合）。
//
// 钉死两件事（扫描范式沿 tl1208 写口守卫先例）：
//
//  1. 调用点全集守卫——动态扫描 desktop 包全部生产文件，orderedTopicIDs
//     的调用只许落在白名单 (文件, 函数前缀) 里。架构专家实测调用点 5 处
//     （session_catalog.go 喂入+置顶壳、session_catalog_runtime.go runtime
//     投影、tabs.go 空白页复用两分支）——喂入侧函数体一处收口天然覆盖全
//     5 读路，逐点同改会漏 runtime 变体；本守卫防的是「新增读路漏过收口
//     评审」，新增调用必须先过语义评审再扩白名单（扩名单即评审可见）。
//     锚点地板（白名单每项至少命中一次）+ 原始命中地板（≥6：定义 1 +
//     调用 5）防「扫描失明假绿」。
//  2. 合成反例证伪——检测器是纯函数，直接喂合成违规源码证明它会响、对
//     白名单不响，防守卫自身退化成永远绿。
//
// 喂入集合==注册列表集合断言见文件尾
// TestSyncMetadataFeedSetEqualsRegisteredSetTL1331（行为级，钉复发形态）。

// orderedTopicIDsFeedAllowlist 是允许出现 orderedTopicIDs 调用的
// (文件, 函数前缀) 白名单。定义本体 + 5 个既有读路（2026-11-01 tip
// a0529bc1f 实测全集，行号无关，按函数名锚定——重命名即响，回归响亮）。
var orderedTopicIDsFeedAllowlist = map[string][]string{
	"tabs.go": {
		"func orderedTopicIDs(",                    // 定义本体（喂入收口唯一函数体）
		"func (a *App) indexedBlankTopicIDLocked(", // 空白页复用候选（global/project 两分支）
	},
	"session_catalog.go": {
		"func (a *App) syncSessionCatalogMetadata(", // SyncMetadata 喂入（appendTopics 闭包，global+project 共用）
		"func (a *App) pinnedTopicShells(",          // 元数据快照的置顶壳可用集
	},
	"session_catalog_runtime.go": {
		"func (a *App) metadataProjectTopics(", // runtime 投影喂入（pinnedTopicIDs 包装变体）
	},
}

// orderedTopicIDsGuardRawFloor 是原始命中地板：守卫落地时生产面恰有
// 定义 1 + 调用 5 = 6 处命中；扫描归零说明检测模式断裂，不是代码变干净。
const orderedTopicIDsGuardRawFloor = 6

// orderedTopicIDsCallViolations 提取一个源文件里落在白名单之外的
// orderedTopicIDs 调用点。纯函数：守卫主流程与合成反例共用。
func orderedTopicIDsCallViolations(filename string, src []byte) []string {
	lines := strings.Split(string(src), "\n")
	var violations []string
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}
		if !strings.Contains(line, "orderedTopicIDs(") {
			continue
		}
		start := i
		for start > 0 && !strings.HasPrefix(lines[start], "func ") {
			start--
		}
		fnLine := strings.TrimSpace(lines[start])
		allowed := false
		for _, prefix := range orderedTopicIDsFeedAllowlist[filename] {
			if strings.HasPrefix(fnLine, prefix) {
				allowed = true
				break
			}
		}
		if !allowed {
			violations = append(violations, fmt.Sprintf("%s:%d %s", filename, i+1, fnLine))
		}
	}
	return violations
}

// TestOrderedTopicIDsCallSitesGuardTL1331 钉喂入调用点全集：新读路必须
// 先过「喂入集合==注册列表集合」语义评审，再扩白名单（扩名单即评审可见）。
func TestOrderedTopicIDsCallSitesGuardTL1331(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob desktop package: %v", err)
	}
	rawSites := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		content := string(src)
		for _, line := range strings.Split(content, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if strings.Contains(line, "orderedTopicIDs(") {
				rawSites++
			}
		}
		for _, violation := range orderedTopicIDsCallViolations(name, src) {
			t.Errorf("%s: orderedTopicIDs called outside the feed allowlist: %s (tl1331 G10 喂入侧收口：喂入集合必须==注册列表集合；新读路先评审再扩 orderedTopicIDsFeedAllowlist)", name, violation)
		}
	}
	// 锚点地板：白名单每个 (文件, 函数) 至少命中一次——收口面搬家或改名
	// 时守卫必须响亮失败，而不是静默护住一个空集。
	for filename, prefixes := range orderedTopicIDsFeedAllowlist {
		for _, prefix := range prefixes {
			src, err := os.ReadFile(filename)
			if err != nil {
				t.Fatalf("read allowlisted file %s: %v", filename, err)
			}
			hit := false
			for _, line := range strings.Split(string(src), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), prefix) {
					hit = true
					break
				}
			}
			if !hit {
				t.Errorf("allowlist anchor %q missing from %s — the feed surface moved and the guard scan broke (not the behavior)", prefix, filename)
			}
		}
	}
	// 原始命中地板：防检测模式断裂假绿。
	if rawSites < orderedTopicIDsGuardRawFloor {
		t.Fatalf("guard scanned only %d orderedTopicIDs sites (floor %d) — scan pattern broke?", rawSites, orderedTopicIDsGuardRawFloor)
	}
}

// TestOrderedTopicIDsGuardDetectorFires 合成反例证伪：检测器必须对白名单
// 之外的新调用响、对白名单函数不响——防守卫退化成永远绿的花瓶。
func TestOrderedTopicIDsGuardDetectorFires(t *testing.T) {
	violating := `package main

func evilShadowFeedReader(ids []string, titles map[string]string) []string {
	return pinnedTopicIDs(orderedTopicIDs(ids, titles), nil)
}
`
	got := orderedTopicIDsCallViolations("some_other_file.go", []byte(violating))
	if len(got) != 1 {
		t.Fatalf("detector must fire on the shadow feed reader, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "evilShadowFeedReader") {
		t.Fatalf("detector must attribute the violation to the enclosing function: %v", got)
	}

	legal := `package main

func (a *App) metadataProjectTopics(scope, workspaceRoot string) []ProjectNode {
	for sortOrder, topicID := range pinnedTopicIDs(orderedTopicIDs(ids), pinnedIDs) {
		_ = topicID
		_ = sortOrder
	}
	return nil
}
`
	if got := orderedTopicIDsCallViolations("session_catalog_runtime.go", []byte(legal)); len(got) != 0 {
		t.Fatalf("detector must stay silent on allowlisted feed readers, got: %v", got)
	}
}

// TestSyncMetadataFeedSetEqualsRegisteredSetTL1331 行为级断言（深查报告
// §4 防回归门禁②）：SyncMetadata 收到的喂入集合必须恒等于注册列表集合。
// 复发形态：标题表残留条目（未注册的 L1、已归属性 project 的 P1 在 global
// 根标题表的残留、项目根标题表的未注册 L2）——pre-tl1331 会被 orderedTopicIDs
// 标题补全喂进对应 scope，Global 区随之画出泄露行；收口后喂入集合只认注册
// 列表，残留条目不再产生任何 catalog 行。
func TestSyncMetadataFeedSetEqualsRegisteredSetTL1331(t *testing.T) {
	isolateDesktopUserDirs(t)
	projectRoot := t.TempDir()
	if err := addProject(projectRoot, "归属项目"); err != nil {
		t.Fatalf("add project: %v", err)
	}
	globalDir := desktopSessionDir(globalWorkspaceRoot())
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global session dir: %v", err)
	}

	// 注册面（唯一真相源）：global 注册 G1；项目注册 P1。
	globalID := "topic_feed_registered_global"
	if err := setTopicTitle("", globalID, "全局正主"); err != nil {
		t.Fatalf("set global title: %v", err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.GlobalTopics = []string{globalID}
		return true, nil
	}); err != nil {
		t.Fatalf("register global topic: %v", err)
	}
	projectID := "topic_feed_registered_project"
	if err := registerTopic("project", projectRoot, topicRegisterOpts{ensureProject: true}, projectID); err != nil {
		t.Fatalf("register project topic: %v", err)
	}
	if err := setTopicTitle(projectRoot, projectID, "项目正主"); err != nil {
		t.Fatalf("set project title: %v", err)
	}

	// 泄露燃料（W9 存量形态，G9 清扫不在本件——喂入侧必须无视它们）：
	// ① global 根标题表的未注册残留；② project 归属 topic 在 global 根
	// 标题表的残留；③ 项目根标题表的未注册残留。
	leakGlobal := "topic_feed_leak_title_only"
	if err := setTopicTitle("", leakGlobal, "标题表残留"); err != nil {
		t.Fatalf("set leak title: %v", err)
	}
	if err := setTopicTitle("", projectID, "全球侧残留"); err != nil {
		t.Fatalf("set cross-scope leak title: %v", err)
	}
	leakProject := "topic_feed_leak_project_side"
	if err := setTopicTitle(projectRoot, leakProject, "项目侧残留"); err != nil {
		t.Fatalf("set project-side leak title: %v", err)
	}

	app := NewApp()
	installSessionCatalogForTest(t, app, globalDir, "global", "")
	if err := app.syncSessionCatalogMetadata(context.Background(), app.sessionCatalog.Load()); err != nil {
		t.Fatalf("sync session catalog metadata: %v", err)
	}
	catalog := app.sessionCatalog.Load()
	if catalog == nil {
		t.Fatal("session catalog is not installed")
	}
	ctx := context.Background()
	assertFed := func(scope, workspaceRoot, topicID string, want bool, why string) {
		t.Helper()
		_, _, ok, err := catalog.GetTopicWithSessionCount(ctx, sessioncatalog.TopicKey{
			Scope: scope, WorkspaceRoot: workspaceRoot, TopicID: topicID,
		})
		if err != nil {
			t.Fatalf("get topic %s/%s: %v", scope, topicID, err)
		}
		if ok != want {
			t.Fatalf("feed set != registered set: %s fed=%v want=%v (%s)", topicID, ok, want, why)
		}
	}
	assertFed("global", "", globalID, true, "注册列表成员必须喂入")
	assertFed("global", "", leakGlobal, false, "未注册的标题表残留不得喂入（G10 真根因形态）")
	assertFed("global", "", projectID, false, "project 归属 topic 的 global 标题残留不得喂入 Global")
	assertFed("project", projectRoot, projectID, true, "注册列表成员必须喂入")
	assertFed("project", projectRoot, leakProject, false, "项目侧标题残留不得喂入项目区")
}
