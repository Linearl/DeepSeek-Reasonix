package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Task 1077 (378-B2 ①)：折叠默认懒守护断言。
//
// 阶段 A 归因（docs/report/内存与性能/内存数据量归因-378阶段A-20261005.md）确认
// 「折叠也全载」只剩两条真枪：记忆召回索引（B1 已改懒构建，internal/memory/memory.go）
// 与冷缓存压缩循环（cold_cache_compact.go，开关默认关）。DAG/转录是「点开即全量
// 物化」——折叠态本就不载。本测试用 299 动态扫描范式把这一现状钉死：
//
//   1. 冷缓存循环的 headless 预热路径必须经过开关判定（默认关态零加载），
//      且预热完成后必须立即释放物化数据（378-B2 ② 载后即卸）；
//   2. desktop 包生产文件中每一个 `agent.LoadSession(` 调用点必须在豁免
//      白名单内（按函数名豁免，勿写死文件行号）——新增的启动期全量物化
//      调用点会被同一断言捕获（299 同款自匹配排除 + 锚点 floor 防扫描失明）。
//
// 勿写死清单：扫描面是整个 desktop 包生产文件；白名单只按函数名豁免，
// 函数被重命名/移动到别的文件时豁免失效，回归会响亮失败。

// foldedLazyGuardAnchorFloor 是扫描失明的哨兵：生产文件中必须能定位到这些
// 锚点，否则扫描面已断裂（文件改名/包移动），测试必须响亮失败而非假绿。
var foldedLazyGuardAnchorFloor = []string{
	"coldCacheCompactDecision", // 冷缓存决策函数（开关判定锚点）
	"coldCacheCompactLoop",     // 冷缓存循环类型
	"startColdCacheCompactLoop",
	"releasePrewarmedSession", // 378-B2 ② 载后即卸锚点
}

// foldedLazySessionLoadAllowlist 是「点开才载」豁免名单：这些函数的
// LoadSession 调用是用户动作（打开会话/搜索/建议/历史预览）触发的按需物化，
// 不属于「启动后未点开即载」。按函数名豁免，勿按文件行号——重命名即失效。
var foldedLazySessionLoadAllowlist = []string{
	"previewSessionMessages(",                              // 历史预览（打开会话触发）
	"previewSessionPage(",                                  // 历史分页（打开会话触发）
	"historyHitFromCandidate(",                             // 历史搜索收集（显式搜索触发）
	"loadSuggestionSessions(",                              // 记忆建议（显式建议请求触发）
	"promptHistory",                                        // 提示历史面板（打开面板触发）
	"resumeWithFreshSystemPrompt(",                         // 会话恢复续载（恢复动作触发）
	"silentLoadSessionSnapshot(",                           // 静默加载 helper（所有调用方均为按需）
	"loadSessionDisplays(",                                 // 提示历史会话列表（打开面板触发）
	"historyTime",                                          // 时间叠层/回填（打开历史视图触发）
	"liveSessionContentDiscardable(",                       // 删除确认对话框（用户删除动作触发）
	"sessionFileHasConversationContent(",                   // 删除确认对话框（用户删除动作触发）
	"runtimeSessionTranscriptBlank(",                       // 运行时转录空白判定（打开/重启路径触发）
	"sessionPathHasNoContent(",                             // 打开会话前的内容校验（打开动作触发）
	"restoredSessionTopicTitle(",                           // 恢复 pinned tab 标题（窗口恢复触发，单会话）
	"loadPinnedTabSessionWithPreloadAndMigrationFallback(", // pinned tab 加载（打开/恢复触发）
}

// TestFoldedSessionsStayLazy pins the 378-B2 status quo: folded sessions are
// never fully materialized by a background/startup path. The rule is dynamic —
// it enumerates every production file in this package and rejects any
// `agent.LoadSession(` call site that is NOT in the on-demand allowlist.
func TestFoldedSessionsStayLazy(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob desktop package: %v", err)
	}

	anchorHits := map[string]int{}
	rawSites := 0
	sites := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		content := string(src)
		for _, anchor := range foldedLazyGuardAnchorFloor {
			if strings.Contains(content, anchor) {
				anchorHits[anchor]++
			}
		}

		lines := strings.Split(content, "\n")
		for i, line := range lines {
			if !strings.Contains(line, "agent.LoadSession(") {
				continue
			}
			// Skip comment/doc lines mentioning the call (not real call sites).
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
				continue
			}
			rawSites++
			// Find the enclosing top-level function.
			start := i
			for start > 0 && !strings.HasPrefix(lines[start], "func ") {
				start--
			}
			fnLine := lines[start]
			allowed := false
			for _, name := range foldedLazySessionLoadAllowlist {
				if strings.Contains(fnLine, name) {
					allowed = true
					break
				}
			}
			if allowed {
				continue
			}
			sites++
			t.Errorf("%s:%d: %q fully materializes a session outside the on-demand allowlist — folded sessions must stay lazy (378-B2)",
				name, i+1, strings.TrimSpace(fnLine))
		}
	}

	// Anchor floor: the cold-cache surface and the 378-B2 ② release hook must
	// still exist so the guard below is exercising real code.
	for _, anchor := range foldedLazyGuardAnchorFloor {
		if anchorHits[anchor] == 0 {
			t.Fatalf("anchor %q missing from desktop production files — did the cold-cache surface move? (guard scan broke, not the behavior)", anchor)
		}
	}
	// Call-site floor: the production surface at guard time had ≥10 LoadSession
	// call sites; a zero-hit scan means the pattern broke, not that the code
	// got lazier. (At guard time all call sites are allowlisted — the floor
	// counts raw hits, not violations, so it stays green only while the scan
	// pattern itself is intact.)
	if rawSites == 0 {
		t.Fatal("guard scanned only 0 LoadSession call sites — did the scan pattern break?")
	}
}

// TestColdCacheCompactHeadlessPathGatesOnSwitch pins the 378-B2 ② status quo:
// the headless prewarm path (the only path that materializes a folded session
// in the background) is unreachable when the lab switch is OFF, and the
// materialized data is released immediately after the prewarm completes.
func TestColdCacheCompactHeadlessPathGatesOnSwitch(t *testing.T) {
	src, err := os.ReadFile("cold_cache_compact.go")
	if err != nil {
		t.Fatalf("read cold_cache_compact.go: %v", err)
	}
	content := string(src)

	if !strings.Contains(content, "func (l *coldCacheCompactLoop) compactOne(") {
		t.Fatal("compactOne missing from cold_cache_compact.go")
	}
	if !strings.Contains(content, "func (l *coldCacheCompactLoop) headlessCompactOne(") {
		t.Fatal("headlessCompactOne missing from cold_cache_compact.go")
	}
	// The tick loop must early-return on !knobs.Enabled before iterating
	// sessions (the OFF zero-touch path).
	if !strings.Contains(content, "if !knobs.Enabled {\n\t\treturn\n\t}") {
		t.Fatal("cold-cache tick loop does not early-return when the switch is off — OFF state would touch sessions")
	}
	// 378-B2 ②: post-prewarm release must be present and called on the headless
	// path (载后即卸). A refactor that drops it breaks the assertion.
	if !strings.Contains(content, "releasePrewarmedSession(") {
		t.Fatal("post-prewarm release (releasePrewarmedSession) missing — headless prewarm would retain materialized session data (378-B2 ②)")
	}
}
