package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// 任务 619：启动 tab 恢复优化。三组验收：
// ① startupBuildSet 只选前台（active-first 首位）+ autopilot 后台 tab 立即构建，
//    其余保持已发布的「未加载」骨架（验收：前台 tab 首帧优先于后台）；
// ② SetActiveTab 点击骨架 tab 触发其 controller 构建，且失败 tab 不静默重建
//    （验收：后台 tab 点击可加载）；
// ③ 骨架 tab 无 controller 也照常持久化，关闭→重开循环 3 次条目不丢不改
//    （验收：关闭→重开循环 3 次行为一致）。

func TestStartupBuildSetForegroundFirstAndAutopilot(t *testing.T) {
	mk := func(id string, autopilot bool) *WorkspaceTab {
		return &WorkspaceTab{ID: id, autopilot: autopilot, disabledMCP: map[string]ServerView{}}
	}
	// Active-first 已排序：active 在首位；bg-autopilot 是必须恢复无人值守的
	// 后台 tab；其余是纯骨架。
	restored := []*WorkspaceTab{
		mk("active", false),
		mk("bg1", false),
		mk("bg-autopilot", true),
		mk("bg2", false),
	}
	got := startupBuildSet(restored)
	if len(got) != 2 {
		t.Fatalf("startup build set len = %d (%v), want 2 (active + bg-autopilot)", len(got), six19TabIDs(got))
	}
	if got[0].ID != "active" || got[1].ID != "bg-autopilot" {
		t.Fatalf("startup build set = %v, want [active bg-autopilot]", six19TabIDs(got))
	}
	// 单 tab（仅 active）退化为原行为。
	got = startupBuildSet([]*WorkspaceTab{mk("only", false)})
	if len(got) != 1 || got[0].ID != "only" {
		t.Fatalf("single-tab build set = %v, want [only]", six19TabIDs(got))
	}
	// nil 条目被跳过，不 panic。
	got = startupBuildSet([]*WorkspaceTab{nil, mk("a", false)})
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("nil-tolerant build set = %v, want [a]", six19TabIDs(got))
	}
}

func six19TabIDs(tabs []*WorkspaceTab) []string {
	out := make([]string, 0, len(tabs))
	for _, tab := range tabs {
		if tab != nil {
			out = append(out, tab.ID)
		}
	}
	return out
}

// TestSetActiveTabKicksLazyTabBuild：点击无 controller 的骨架 tab 必须触发其
// 构建（a.ctx == nil 时走同步构建路径，与 task-405 节流测试同一钩子观测点）。
func TestSetActiveTabKicksLazyTabBuild(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p619-lazy.jsonl")
	a, _ := appWithTab(t, path)
	a.mu.Lock()
	a.tabs["lazy_tab"] = &WorkspaceTab{
		ID:          "lazy_tab",
		Scope:       "global",
		disabledMCP: map[string]ServerView{},
		sink:        &tabEventSink{tabID: "lazy_tab", app: a},
	}
	a.tabOrder = []string{"test_tab", "lazy_tab"}
	a.mu.Unlock()

	var mu sync.Mutex
	var kicks []string
	a.tabBuildStartHook = func(tabID string) {
		mu.Lock()
		kicks = append(kicks, tabID)
		mu.Unlock()
	}

	if err := a.SetActiveTab("lazy_tab"); err != nil {
		t.Fatalf("SetActiveTab on lazy tab: %v", err)
	}
	// a.ctx == nil：构建在 SetActiveTab 内同步启动，返回即已过钩子。
	mu.Lock()
	defer mu.Unlock()
	if len(kicks) != 1 || kicks[0] != "lazy_tab" {
		t.Fatalf("build kicks = %v, want exactly [lazy_tab]", kicks)
	}
}

// TestSetActiveTabDoesNotRekickFailedLazyTab：带 StartupErr 的骨架 tab 保留
// 显式重试面，点击不静默重建。
func TestSetActiveTabDoesNotRekickFailedLazyTab(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p619-failed.jsonl")
	a, _ := appWithTab(t, path)
	a.mu.Lock()
	a.tabs["failed_tab"] = &WorkspaceTab{
		ID:          "failed_tab",
		Scope:       "global",
		StartupErr:  "boom",
		disabledMCP: map[string]ServerView{},
		sink:        &tabEventSink{tabID: "failed_tab", app: a},
	}
	a.tabOrder = []string{"test_tab", "failed_tab"}
	a.mu.Unlock()

	var mu sync.Mutex
	var kicks []string
	a.tabBuildStartHook = func(tabID string) {
		mu.Lock()
		kicks = append(kicks, tabID)
		mu.Unlock()
	}

	if err := a.SetActiveTab("failed_tab"); err != nil {
		t.Fatalf("SetActiveTab on failed tab: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(kicks) != 0 {
		t.Fatalf("failed tab rebuilt on click: %v, want no kicks", kicks)
	}
}

// TestLazyTabSkeletonPersistsThreeCycles：骨架 tab（无 controller）经
// saveTabs 落盘后，load→save 循环 3 次条目不丢、active 不漂——对应验收
// 「关闭→重开循环 3 次行为一致」的持久化半边；构建半边由上面的点击触发
// 测试覆盖。
func TestLazyTabSkeletonPersistsThreeCycles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)

	seed := []byte(`{"tabs":[` +
		`{"id":"tab-1","scope":"global","topicId":"topic_a","sessionPath":"` + filepath.ToSlash(filepath.Join(home, "s1.jsonl")) + `"},` +
		`{"id":"tab-2","scope":"global","topicId":"topic_b","sessionPath":"` + filepath.ToSlash(filepath.Join(home, "s2.jsonl")) + `"}],` +
		`"activeTab":"tab-1","tabOrder":["tab-1","tab-2"]}`)
	if err := os.WriteFile(filepath.Join(home, tabsFileName), seed, 0o644); err != nil {
		t.Fatalf("seed desktop-tabs.json: %v", err)
	}

	a := &App{tabs: map[string]*WorkspaceTab{}}
	for cycle := 1; cycle <= 3; cycle++ {
		f := loadTabsFile()
		if len(f.Tabs) != 2 {
			t.Fatalf("cycle %d: loaded %d tabs, want 2", cycle, len(f.Tabs))
		}
		// 模拟恢复循环：全部发布，tab-2 保持骨架（无 Ctrl）。
		a.mu.Lock()
		a.tabs = map[string]*WorkspaceTab{}
		a.tabOrder = nil
		a.activeTabID = f.ActiveTab
		for _, entry := range f.Tabs {
			tab := &WorkspaceTab{
				ID:          entry.ID,
				Scope:       entry.Scope,
				TopicID:     entry.TopicID,
				SessionPath: entry.SessionPath,
				TopicTitle:  "topic-" + entry.TopicID,
				disabledMCP: map[string]ServerView{},
			}
			a.tabs[tab.ID] = tab
			a.tabOrder = append(a.tabOrder, tab.ID)
		}
		// 骨架 tab（tab-2 无 Ctrl）必须进快照——failedStartupTabLocked 不因
		// 无 runtime 而剔除。
		dir, entries, activeID, version := a.saveTabsCollectLocked()
		a.mu.Unlock()
		if len(entries) != 2 {
			t.Fatalf("cycle %d: snapshot kept %d entries, want 2 (lazy skeleton dropped?)", cycle, len(entries))
		}
		if activeID != "tab-1" {
			t.Fatalf("cycle %d: active = %q, want tab-1", cycle, activeID)
		}
		a.saveTabsWrite(dir, entries, activeID, version)
		// 落盘核验：两条 id 都在。
		blob, err := os.ReadFile(filepath.Join(home, tabsFileName))
		if err != nil {
			t.Fatalf("cycle %d: read back: %v", cycle, err)
		}
		for _, want := range []string{`"tab-1"`, `"tab-2"`} {
			if !bytes.Contains(blob, []byte(want)) {
				t.Fatalf("cycle %d: saved file missing %s: %s", cycle, want, blob)
			}
		}
	}
}
