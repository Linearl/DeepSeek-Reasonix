package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/provider"
)

// 任务 451 方案 B：warm page 缓存与 idle 预取的验收测试。
// 验收口径（任务书）：
//   1. 预取后切 tab（冷 tab 的最新页请求）命中缓存，一次 stat 即回；
//   2. 会话推进（追加保存）后不回旧页，回落冷路径；
//   3. 只有最新页形状进缓存，跳页/手动参数语义原样；
//   4. 缓存有界（LRU 8），Error/Stale 不进缓存；
//   5. 预取轮只碰冷候选，前台轮/构建中一律让路。

func warmPageTestSession(t *testing.T, app *App, root, name string, turns int) (*agent.Session, *WorkspaceTab) {
	t.Helper()
	dir := desktopSessionDir(root)
	messages := make([]provider.Message, 0, turns*2)
	for i := 0; i < turns; i++ {
		messages = append(messages,
			provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("warm user turn %d large-session marker", i)},
			provider.Message{Role: provider.RoleAssistant, Content: fmt.Sprintf("warm assistant turn %d large-session marker", i)},
		)
	}
	sess, path := saveHistorySliceSession(t, dir, name, messages)
	tab := &WorkspaceTab{
		ID:            name,
		Scope:         "project",
		WorkspaceRoot: root,
		SessionPath:   path,
		Ready:         false,
		Ctrl:          nil,
	}
	app.mu.Lock()
	app.tabs[tab.ID] = tab
	app.mu.Unlock()
	return sess, tab
}

func warmPageTestApp(t *testing.T) *App {
	t.Helper()
	isolateDesktopUserDirs(t)
	resetHistoryWarmPageCache()
	t.Cleanup(resetHistoryWarmPageCache)
	app := NewApp()
	app.mu.Lock()
	app.tabs = map[string]*WorkspaceTab{}
	app.mu.Unlock()
	return app
}

func newWarmPageReq() HistorySliceRequest {
	return HistorySliceRequest{Turns: historyIdlePrefetchTurns}
}

// 验收 1：预取（首次冷读）后，同形状请求从缓存回页——把盘上文件同尺寸同
// mtime 换成别的字节后仍能拿回首读的条目，证明内容来自缓存而非磁盘。
func TestHistoryWarmPageServesNewestPageFromCache(t *testing.T) {
	root := t.TempDir()
	app := warmPageTestApp(t)
	_, tab := warmPageTestSession(t, app, root, "warm-hit.jsonl", 40)

	req := newWarmPageReq()
	first := app.HistorySliceForTab(tab.ID, req)
	if first.Error != "" || len(first.Entries) == 0 {
		t.Fatalf("first read failed: err=%q entries=%d", first.Error, len(first.Entries))
	}
	if first.Source != "index" && first.Source != "scan" {
		t.Fatalf("first source = %q, want index|scan", first.Source)
	}

	// 同尺寸同 mtime 换内容（缓存命中时永远不该再读到盘）。
	info, err := os.Stat(tab.SessionPath)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(tab.SessionPath)
	if err != nil {
		t.Fatal(err)
	}
	for i := 20; i < len(raw); i++ {
		if raw[i] == 'x' {
			raw[i] = 'y'
		} else {
			raw[i] = 'x'
		}
	}
	if err := os.WriteFile(tab.SessionPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(tab.SessionPath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}

	second := app.HistorySliceForTab(tab.ID, req)
	if second.Source != "warm" {
		t.Fatalf("second source = %q, want warm (cache miss)", second.Source)
	}
	if len(second.Entries) != len(first.Entries) || second.Entries[0].EntryID != first.Entries[0].EntryID {
		t.Fatal("warm page diverges from the cached first read")
	}
}

// 验收 2：会话推进（追加保存，size 增长）后缓存必须失效，回落冷路径拿新页。
func TestHistoryWarmPageInvalidatedBySessionGrowth(t *testing.T) {
	root := t.TempDir()
	app := warmPageTestApp(t)
	sess, tab := warmPageTestSession(t, app, root, "warm-grow.jsonl", 20)

	req := newWarmPageReq()
	if first := app.HistorySliceForTab(tab.ID, req); first.Source == "warm" || first.Error != "" {
		t.Fatalf("unexpected first read: source=%q err=%q", first.Source, first.Error)
	}
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "growth marker appended after warm"})
	if err := sess.Save(tab.SessionPath); err != nil {
		t.Fatal(err)
	}

	second := app.HistorySliceForTab(tab.ID, req)
	if second.Source == "warm" {
		t.Fatal("grown session must not serve the stale warm page")
	}
	if second.Error != "" || len(second.Entries) == 0 {
		t.Fatalf("re-read after growth failed: err=%q entries=%d", second.Error, len(second.Entries))
	}
	found := false
	for _, e := range second.Entries {
		if e.Message.Content == "growth marker appended after warm" {
			found = true
		}
	}
	if !found {
		t.Fatal("re-read after growth misses the appended turn")
	}
}

// 验收 3：只有最新页形状进缓存——手动 turns 参数与带 cursor 的旧页请求
// 语义原样（两次都不走 warm），且不污染最新页缓存条目。
func TestHistoryWarmPageOnlyNewestShape(t *testing.T) {
	root := t.TempDir()
	app := warmPageTestApp(t)
	_, tab := warmPageTestSession(t, app, root, "warm-shape.jsonl", 80)

	if first := app.HistorySliceForTab(tab.ID, newWarmPageReq()); first.Source == "warm" {
		t.Fatal("precondition: first newest-page read must be cold")
	}
	// 手动参数：不服务也不回写。
	for i := 0; i < 2; i++ {
		if page := app.HistorySliceForTab(tab.ID, HistorySliceRequest{Turns: 12}); page.Source == "warm" {
			t.Fatal("manual-turns request must bypass the warm cache")
		}
	}
	// 旧页（带 cursor）：不服务也不回写。
	newest := app.HistorySliceForTab(tab.ID, newWarmPageReq())
	if newest.Source != "warm" {
		t.Fatalf("newest page must hit the cache, got %q", newest.Source)
	}
	if newest.NextCursor == "" {
		t.Fatal("fixture too small to produce an older cursor")
	}
	for i := 0; i < 2; i++ {
		older := app.HistorySliceForTab(tab.ID, HistorySliceRequest{Cursor: newest.NextCursor, Turns: historyIdlePrefetchTurns})
		if older.Source == "warm" || older.Error != "" {
			t.Fatalf("older-page request must bypass the warm cache: source=%q err=%q", older.Source, older.Error)
		}
	}
}

// 验收 4a：LRU 上界——超出 historyWarmPageMaxEntries 后最旧条目逐出。
func TestHistoryWarmPageLRUBound(t *testing.T) {
	app := warmPageTestApp(t)
	_ = app
	dir := t.TempDir()
	req := newWarmPageReq()
	paths := make([]string, 0, historyWarmPageMaxEntries+1)
	for i := 0; i <= historyWarmPageMaxEntries; i++ {
		path := filepath.Join(dir, fmt.Sprintf("warm-lru-%d.jsonl", i))
		if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
		storeHistoryWarmPage(path, req, HistorySlice{Entries: []HistoryEntry{{EntryID: fmt.Sprint(i)}}, Source: "index"})
	}
	if _, ok := lookupHistoryWarmPage(paths[0], req); ok {
		t.Fatal("oldest entry must be evicted past the LRU bound")
	}
	if _, ok := lookupHistoryWarmPage(paths[historyWarmPageMaxEntries], req); !ok {
		t.Fatal("newest entry must survive")
	}
}

// 验收 4b：Error 页与 stat 失败不进缓存。
func TestHistoryWarmPageErrorNotCached(t *testing.T) {
	app := warmPageTestApp(t)
	_ = app
	req := newWarmPageReq()
	missing := filepath.Join(t.TempDir(), "missing.jsonl")
	storeHistoryWarmPage(missing, req, HistorySlice{Source: "index"})
	if _, ok := lookupHistoryWarmPage(missing, req); ok {
		t.Fatal("stat failure must not be cached")
	}
	existing := filepath.Join(t.TempDir(), "broken.jsonl")
	if err := os.WriteFile(existing, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	storeHistoryWarmPage(existing, req, HistorySlice{Error: "boom"})
	if _, ok := lookupHistoryWarmPage(existing, req); ok {
		t.Fatal("error slices must not be cached")
	}
}

// 验收 5：预取轮把冷候选的最新页切好；随后同形状请求命中 warm。
func TestHistoryIdlePrefetchRoundWarmsColdTabs(t *testing.T) {
	root := t.TempDir()
	app := warmPageTestApp(t)
	_, tabA := warmPageTestSession(t, app, root, "prefetch-a.jsonl", 20)
	_, tabB := warmPageTestSession(t, app, root, "prefetch-b.jsonl", 20)
	app.mu.Lock()
	app.activeTabID = "some-other-tab"
	app.mu.Unlock()

	app.historyIdlePrefetchRound(context.Background())

	for _, tab := range []*WorkspaceTab{tabA, tabB} {
		page := app.HistorySliceForTab(tab.ID, newWarmPageReq())
		if page.Source != "warm" || page.Error != "" {
			t.Fatalf("tab %s not warmed by prefetch round: source=%q err=%q", tab.ID, page.Source, page.Error)
		}
	}
}

// 验收 5b：前台轮在跑（Running）时预取轮整轮让路，一个候选都不碰。
func TestHistoryIdlePrefetchRoundYieldsToRuntimeWork(t *testing.T) {
	root := t.TempDir()
	app := warmPageTestApp(t)
	_, tab := warmPageTestSession(t, app, root, "prefetch-busy.jsonl", 20)
	busy := &idlePrefetchBusyCtrl{status: control.RuntimeStatus{Running: true}}
	app.mu.Lock()
	app.tabs["busy"] = &WorkspaceTab{ID: "busy", Scope: "global", Ready: true, Ctrl: busy}
	app.mu.Unlock()

	app.historyIdlePrefetchRound(context.Background())

	if page := app.HistorySliceForTab(tab.ID, newWarmPageReq()); page.Source == "warm" {
		t.Fatal("prefetch round must not run while a foreground turn is active")
	}
}

// 验收 5c：活跃 tab 与构建中的 tab 不进候选。
func TestHistoryIdlePrefetchRoundSkipsActiveAndBuildingTabs(t *testing.T) {
	root := t.TempDir()
	app := warmPageTestApp(t)
	_, active := warmPageTestSession(t, app, root, "prefetch-active.jsonl", 20)
	_, building := warmPageTestSession(t, app, root, "prefetch-building.jsonl", 20)
	app.mu.Lock()
	app.activeTabID = active.ID
	app.tabs[building.ID].buildDone = make(chan struct{})
	app.mu.Unlock()

	app.historyIdlePrefetchRound(context.Background())

	for _, tab := range []*WorkspaceTab{active, building} {
		if page := app.HistorySliceForTab(tab.ID, newWarmPageReq()); page.Source == "warm" {
			t.Fatalf("tab %s must stay outside prefetch candidates", tab.ID)
		}
	}
}

// 冒烟：武装幂等、kick 不炸、stop 收口；测试 App 无 tab，防抖轮为空转。
func TestHistoryIdlePrefetchStartStopSmoke(t *testing.T) {
	app := historySliceTestApp(t)
	ctx, cancel := context.WithCancel(app.ctx)
	app.ctx = ctx
	t.Cleanup(cancel)
	app.startHistoryIdlePrefetch()
	app.startHistoryIdlePrefetch() // 幂等
	app.kickHistoryIdlePrefetch()
	app.stopHistoryIdlePrefetch()
	// 给武装/退出的 goroutine 一点调度余地；断言只求不挂不炸。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type idlePrefetchBusyCtrl struct {
	stubSessionAPI
	status control.RuntimeStatus
}

func (c *idlePrefetchBusyCtrl) RuntimeStatus() control.RuntimeStatus { return c.status }
