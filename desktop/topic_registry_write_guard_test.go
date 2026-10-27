package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tl1208 写口唯二源码守卫（1197 方案获批件第二要素的可证面）。
//
// 钉死：desktop-projects.json 的 topic 注册列表（GlobalTopics /
// Projects[].Topics / 各自 PinnedTopics / DeletedTopics）的**成员变更**只从
// registerTopic/unregisterTopic 两个收点过（收点体都在 topic_registry.go）。
// 扫描范式沿 299/folded-lazy 守卫先例：
//
//  1. 提取自证——动态扫描 desktop 包全部生产文件，提取每个 topic 注册
//     列表字段的赋值点并定位其所在函数；豁免只按函数名（重命名即失效，
//     回归响亮失败），并设锚点地板与原始命中地板防「扫描失明假绿」。
//  2. 合成反例证伪——检测器是纯函数，本文件直接喂合成违规源码证明它
//     会响（收点文件之外的裸赋值必被抓、垫片文件之外的旧名调用必被抓），
//     防守卫自身退化成永远绿的花瓶。
//
// 豁免面（load 视图构建，不新增成员）：
//   - normalizeProjectsFile / applyProjectOrganization：加载时去重合并与
//     顺序投影（成员集合不变）；
//   - reorderTopicOrder：保序重排——由 TestGuardReorderTopicOrderIsPermutation
//     单独验证置换性质（缺项补尾、未知/重复拒绝）。

// topicRegistryWriteAllowlist 是允许出现注册列表赋值的 (文件, 函数前缀)
// 白名单。topic_registry.go 的收点族整体豁免；其余按函数名逐个豁免。
var topicRegistryWriteAllowlist = map[string][]string{
	"topic_registry.go": {
		"func registerTopic(",              // 收点一（前置插入 + pinUpdate 分派）
		"func registerTopicPrependMutator", // 收点一变更体（原 prependTopicsInProjectsFileOpts）
		"func registerTopicPinMutator",     // 收点一变更体（原 SetTopicPinned 变更）
		"func unregisterTopic(",            // 收点二
		"func unregisterTopicMutator",      // 收点二变更体（原 removeTopicFromProjectsFile）
		"func reorderTopicOrder(",          // 保序重排收口（置换，见置换性质验证）
	},
	"tabs.go": {
		"func normalizeProjectsFile(", // 加载视图去重合并（不新增成员）
	},
	"project_tree_organization_store.go": {
		"func applyProjectOrganization(", // 加载视图顺序投影（成员集合不变）
	},
	// 视图/投影结构体与注册列表同名字段（.Topics）的构建函数：赋值目标是
	// 视图类型（ManageSessionsGroupView / ProjectTreeRuntime 投影），不是
	// desktop-projects.json 的注册列表。按精确函数名豁免——这些函数体内
	// 若出现真正的注册列表赋值属于评审可见的故意行为。
	"manage_sessions.go": {
		"func manageSessionsGroupViews(", // ManageSessionsGroupView 视图投影
	},
	"runtime_state.go": {
		"func (a *App) GetRuntimeStateSnapshot(", // 侧栏运行态投影（cloneRuntimeTopics）
	},
}

// topicRegistryDeprecatedShims 是写口旧名垫片：生产代码禁止在
// topic_registry.go 之外调用（调用即绕过收点命名面）。
var topicRegistryDeprecatedShims = []string{
	"prependTopicInProjectsFile(",
	"prependTopicsInProjectsFile(",
	"removeTopicFromProjectsFile(",
}

// topicRegistryGuardAnchors 是扫描失明哨兵：收点面与直查面必须存在。
var topicRegistryGuardAnchors = []string{
	"func registerTopic(",
	"func unregisterTopic(",
	"func syncTopicRegistryIndex(",
	"func topicRegistryOwnerOf(",
	"func topicRegistryWarmup(",
	"topicregistry.Configure(",
}

// topicRegistryListFields 是注册列表字段赋值的检测模式（含 append 直改）。
var topicRegistryListFields = []string{
	".GlobalTopics =",
	".GlobalPinnedTopics =",
	".DeletedTopics =",
	".Topics =",
	".PinnedTopics =",
}

// topicRegistryWriteViolations 提取一个源文件里的注册列表赋值点，返回
// 非豁免函数名列表（空 = 干净）。纯函数：守卫主流程与合成反例共用。
func topicRegistryWriteViolations(filename string, src []byte) []string {
	lines := strings.Split(string(src), "\n")
	var violations []string
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}
		hit := false
		for _, field := range topicRegistryListFields {
			if strings.Contains(line, field) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		start := i
		for start > 0 && !strings.HasPrefix(lines[start], "func ") {
			start--
		}
		fnLine := strings.TrimSpace(lines[start])
		allowed := false
		for _, prefix := range topicRegistryWriteAllowlist[filename] {
			if strings.HasPrefix(fnLine, prefix) {
				allowed = true
				break
			}
		}
		if !allowed {
			violations = append(violations, filepath.Join(filename, fnLine))
		}
	}
	return violations
}

// TestTopicRegistryWriteGuardSolo 钉写口唯二：注册列表成员变更只出现在
// topic_registry.go 收点族与 load 视图豁免函数里。
func TestTopicRegistryWriteGuardSolo(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob desktop package: %v", err)
	}
	anchorHits := map[string]int{}
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
		for _, anchor := range topicRegistryGuardAnchors {
			if strings.Contains(content, anchor) {
				anchorHits[anchor]++
			}
		}
		for _, line := range strings.Split(content, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, field := range topicRegistryListFields {
				if strings.Contains(line, field) {
					rawSites++
					break
				}
			}
		}
		for _, violation := range topicRegistryWriteViolations(name, src) {
			t.Errorf("%s: topic registration list mutated outside the write funnels: %s (tl1208 写口唯二)", name, violation)
		}
	}
	for _, anchor := range topicRegistryGuardAnchors {
		if anchorHits[anchor] == 0 {
			t.Fatalf("anchor %q missing from desktop production files — the funnel surface moved and the guard scan broke (not the behavior)", anchor)
		}
	}
	// 原始命中地板：守卫落地时生产面有 ≥10 处列表赋值（收点体 + load 豁免）；
	// 扫描归零说明检测模式断裂，不是代码变干净。
	if rawSites < 10 {
		t.Fatalf("guard scanned only %d topic-list assignment sites — scan pattern broke?", rawSites)
	}
}

// TestTopicRegistryDeprecatedShimCallers 钉：写口旧名垫片在生产代码里零
// 调用（topic_registry.go 垫片定义体自身除外）。
func TestTopicRegistryDeprecatedShimCallers(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob desktop package: %v", err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		lines := strings.Split(string(src), "\n")
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, shim := range topicRegistryDeprecatedShims {
				if !strings.Contains(line, shim) {
					continue
				}
				if name == "topic_registry.go" {
					// 垫片定义与其转发体合法。
					continue
				}
				t.Errorf("%s:%d: deprecated write shim %q called outside topic_registry.go — use registerTopic/unregisterTopic (tl1208 写口唯二)", name, i+1, shim)
			}
		}
	}
}

// TestTopicRegistryGuardDetectorFires 合成反例证伪：检测器必须对「收点之外
// 的裸赋值」响，对合法豁免不响——防守卫退化成永远绿。
func TestTopicRegistryGuardDetectorFires(t *testing.T) {
	violating := `package main

func evilShadowRegister(f *desktopProjectFile) {
	f.GlobalTopics = append(f.GlobalTopics, "shadow")
	f.DeletedTopics = f.DeletedTopics[:0]
}

func evilShadowUnregister(p *desktopProject) {
	p.Topics = nil
	p.PinnedTopics = nil
}
`
	got := topicRegistryWriteViolations("some_other_file.go", []byte(violating))
	if len(got) != 4 {
		t.Fatalf("detector must fire on all 4 shadow assignments, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "evilShadowRegister") || !strings.Contains(got[2], "evilShadowUnregister") {
		t.Fatalf("detector must attribute violations to the enclosing function: %v", got)
	}

	legal := `package main

func registerTopicPrependMutator(f *desktopProjectFile, workspaceRoot string, topicIDs []string, opt topicRegisterOpts) (bool, error) {
	f.GlobalTopics = uniqueStrings(append(live, f.GlobalTopics...))
	f.Projects[0].Topics = next
	return true, nil
}
`
	if got := topicRegistryWriteViolations("topic_registry.go", []byte(legal)); len(got) != 0 {
		t.Fatalf("funnel-file assignments must be allowlisted, got %v", got)
	}

	loadView := `package main

func normalizeProjectsFile(f desktopProjectFile) desktopProjectFile {
	p.Topics = uniqueStrings(p.Topics)
	return out
}
`
	if got := topicRegistryWriteViolations("tabs.go", []byte(loadView)); len(got) != 0 {
		t.Fatalf("load-view exemptions must hold, got %v", got)
	}

	// 同一裸赋值放进非豁免文件的同类函数里必须响（豁免按 (文件,函数) 双键，
	// 换个文件就失效——防豁免面被悄悄放大）。
	if got := topicRegistryWriteViolations("some_other_file.go", []byte(loadView)); len(got) != 1 {
		t.Fatalf("load-view exemption must be file-scoped, got %v", got)
	}
}

// TestGuardReorderTopicOrderIsPermutation 钉重排豁免的置换性质：
// completeTopicOrder 输出必须是输入集合的置换（缺项补尾、未知/重复即拒），
// 因此 reorderTopicOrder 不改注册成员——这是它在守卫白名单里的证明。
func TestGuardReorderTopicOrderIsPermutation(t *testing.T) {
	previous := []string{"a", "b", "c"}
	next, err := completeTopicOrder([]string{"c", "a"}, previous)
	if err != nil {
		t.Fatalf("reorder: %v", err)
	}
	if len(next) != len(previous) {
		t.Fatalf("permutation must preserve length: %v -> %v", previous, next)
	}
	seen := map[string]bool{}
	for _, id := range next {
		if seen[id] {
			t.Fatalf("permutation produced duplicate %q: %v", id, next)
		}
		seen[id] = true
	}
	for _, id := range previous {
		if !seen[id] {
			t.Fatalf("permutation dropped %q: %v", id, next)
		}
	}
	if next[0] != "c" || next[1] != "a" || next[2] != "b" {
		t.Fatalf("omitted ids must be re-appended in order: %v", next)
	}
	if _, err := completeTopicOrder([]string{"unknown"}, previous); err == nil {
		t.Fatal("unknown id must be rejected (would change membership)")
	}
	if _, err := completeTopicOrder([]string{"a", "a"}, previous); err == nil {
		t.Fatal("duplicate id must be rejected (would change membership)")
	}
}
