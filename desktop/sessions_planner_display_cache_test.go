package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 任务 451 方案 A 验收测试：warm 命中零重读（planner-turns 亚毫秒的
// 结构性保证，不设时钟阈值断言）、写后失效、mtime/存在性变更重载、
// 并发单飞、条目上限。

// plannerDisplayCacheTestSwapReader 把读接缝换成可计数实现，返回还原函数与
// 计数指针。slowMs>0 时每次读模拟一次慢盘（单飞测试用）。
func plannerDisplayCacheTestSwapReader(t *testing.T, slowMs int) (restore func(), reads *atomic.Int64) {
	t.Helper()
	reads = &atomic.Int64{}
	original := plannerDisplayReadFile
	plannerDisplayReadFile = func(path string) ([]byte, error) {
		reads.Add(1)
		if slowMs > 0 {
			time.Sleep(time.Duration(slowMs) * time.Millisecond)
		}
		return original(path)
	}
	return func() { plannerDisplayReadFile = original }, reads
}

func plannerDisplayCacheTestWriteSidecar(t *testing.T, dir, marker string) []byte {
	t.Helper()
	m := sessionPlannerDisplayMap{
		"one.jsonl": []plannerDisplayTurn{{
			UserHash: messageDisplayKey("prompt-one"),
			Messages: []HistoryMessage{{Role: "assistant", Content: "answer-one-" + marker}},
		}},
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessionPlannerDisplayPath(dir), b, 0o600); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLoadSessionPlannerDisplaysWarmHitSkipsReread(t *testing.T) {
	resetPlannerDisplayCacheForTest()
	defer resetPlannerDisplayCacheForTest()
	dir := t.TempDir()
	plannerDisplayCacheTestWriteSidecar(t, dir, "v1")
	restore, reads := plannerDisplayCacheTestSwapReader(t, 0)
	defer restore()

	first := loadSessionPlannerDisplays(dir)
	if got := first["one.jsonl"]; len(got) != 1 || got[0].Messages[0].Content != "answer-one-v1" {
		t.Fatalf("first load should come from disk: %+v", got)
	}
	if reads.Load() != 1 {
		t.Fatalf("first load reads = %d, want 1", reads.Load())
	}

	// 不设时钟阈值断言（audit-2 minor：>1ms 断言在负载调度抖动下假挂
	// 2/5，且冗余——零重读由 reads==1 结构性证明：命中路径只有锁+一次
	// stat+比较，完全不读盘）。
	second := loadSessionPlannerDisplays(dir)
	if reads.Load() != 1 {
		t.Fatalf("warm hit must not reread, reads = %d", reads.Load())
	}
	if got := second["one.jsonl"][0].Messages[0].Content; got != "answer-one-v1" {
		t.Fatalf("warm hit content changed: %q", got)
	}
}

func TestLoadSessionPlannerDisplaysReloadsOnMtimeAndExistenceChange(t *testing.T) {
	resetPlannerDisplayCacheForTest()
	defer resetPlannerDisplayCacheForTest()
	dir := t.TempDir()
	path := sessionPlannerDisplayPath(dir)
	plannerDisplayCacheTestWriteSidecar(t, dir, "v1")
	restore, reads := plannerDisplayCacheTestSwapReader(t, 0)
	defer restore()

	if loadSessionPlannerDisplays(dir) == nil {
		t.Fatal("expected non-nil map")
	}
	// mtime+内容变更：外部进程改写的形态，必须重载。
	plannerDisplayCacheTestWriteSidecar(t, dir, "v2")
	if err := os.Chtimes(path, time.Now().Add(2*time.Second), time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	got := loadSessionPlannerDisplays(dir)
	if got["one.jsonl"][0].Messages[0].Content != "answer-one-v2" {
		t.Fatalf("mtime change must reload, got %+v", got["one.jsonl"])
	}
	if reads.Load() != 2 {
		t.Fatalf("reload reads = %d, want 2", reads.Load())
	}

	// 文件缺席 → 创建：负缓存条目必须因 exists 失配重载。
	dir2 := t.TempDir()
	path2 := sessionPlannerDisplayPath(dir2)
	if m := loadSessionPlannerDisplays(dir2); len(m) != 0 {
		t.Fatalf("missing sidecar should read empty, got %#v", m)
	}
	if reads.Load() != 3 {
		t.Fatalf("negative load reads = %d, want 3", reads.Load())
	}
	plannerDisplayCacheTestWriteSidecar(t, dir2, "created")
	created := loadSessionPlannerDisplays(dir2)
	if created["one.jsonl"][0].Messages[0].Content != "answer-one-created" {
		t.Fatalf("created sidecar must invalidate negative entry, got %+v", created["one.jsonl"])
	}
	if reads.Load() != 4 {
		t.Fatalf("created-sidecar load reads = %d, want 4", reads.Load())
	}
	_ = path2
}

func TestLoadSessionPlannerDisplaysWriteInvalidationBeyondMtime(t *testing.T) {
	resetPlannerDisplayCacheForTest()
	defer resetPlannerDisplayCacheForTest()
	dir := t.TempDir()
	path := sessionPlannerDisplayPath(dir)
	// 同长度不同内容：a→b 同字节数；这是"mtime 缓存天然发现不了"的改写。
	sidecarA := []byte(`{"a.jsonl":[]}`)
	sidecarB := []byte(`{"b.jsonl":[]}`)
	if len(sidecarA) != len(sidecarB) {
		t.Fatalf("test fixtures must be equal length")
	}
	if err := os.WriteFile(path, sidecarA, 0o600); err != nil {
		t.Fatal(err)
	}
	restore, reads := plannerDisplayCacheTestSwapReader(t, 0)
	defer restore()
	first := loadSessionPlannerDisplays(dir)
	if _, ok := first["a.jsonl"]; !ok {
		t.Fatalf("expected a.jsonl cached, got %#v", first)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// 同尺寸+同 mtime 的外部改写：mtime/size 校验天然放行，缓存会吐陈值——
	// 这是规格接受的残留风险，先如实断言其存在。
	if err := os.WriteFile(path, sidecarB, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	stale := loadSessionPlannerDisplays(dir)
	if _, ok := stale["a.jsonl"]; !ok {
		t.Fatalf("same mtime+size rewrite is expected to serve stale (documented residual risk), got %#v", stale)
	}
	if reads.Load() != 1 {
		t.Fatalf("stale hit should not reread, reads = %d", reads.Load())
	}

	// 写侧失效不依赖 mtime：删除条目后同一文件立即读出新内容。
	invalidateSessionPlannerDisplayCache(dir)
	fresh := loadSessionPlannerDisplays(dir)
	if _, ok := fresh["b.jsonl"]; !ok {
		t.Fatalf("invalidation must drop the stale entry, got %#v", fresh)
	}
	if reads.Load() != 2 {
		t.Fatalf("post-invalidation load reads = %d, want 2", reads.Load())
	}
}

func TestPlannerDisplaySaveAndRemoveInvalidateCacheEntry(t *testing.T) {
	resetPlannerDisplayCacheForTest()
	defer resetPlannerDisplayCacheForTest()
	dir := t.TempDir()
	plannerDisplayCacheTestWriteSidecar(t, dir, "v1")
	restore, _ := plannerDisplayCacheTestSwapReader(t, 0)
	defer restore()
	loadSessionPlannerDisplays(dir)
	key := plannerDisplayCacheKey(dir)
	if _, ok := plannerDisplayCacheEntries[key]; !ok {
		t.Fatal("entry should be cached after load")
	}

	// save 路径（updateSessionPlannerDisplays 的落盘出口）必须删条目。
	if err := saveSessionPlannerDisplays(dir, sessionPlannerDisplayMap{
		"two.jsonl": []plannerDisplayTurn{{UserHash: messageDisplayKey("p"), Messages: []HistoryMessage{{Role: "assistant", Content: "x"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := plannerDisplayCacheEntries[key]; ok {
		t.Fatal("saveSessionPlannerDisplays must invalidate the cache entry")
	}
	if _, ok := loadSessionPlannerDisplays(dir)["two.jsonl"]; !ok {
		t.Fatal("post-save load must see the new content")
	}

	// remove 分支（清空侧车）同样失效。
	loadSessionPlannerDisplays(dir)
	if err := saveOrRemoveSessionPlannerDisplays(dir, sessionPlannerDisplayMap{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := plannerDisplayCacheEntries[key]; ok {
		t.Fatal("removing the sidecar must invalidate the cache entry")
	}
	if m := loadSessionPlannerDisplays(dir); len(m) != 0 {
		t.Fatalf("post-remove load must be empty, got %#v", m)
	}
}

func TestSessionPlannerDisplayTurnsReflectsRecordAndRemove(t *testing.T) {
	resetPlannerDisplayCacheForTest()
	defer resetPlannerDisplayCacheForTest()
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "one.jsonl")
	if turns := sessionPlannerDisplayTurns(dir, sessionPath); turns != nil {
		t.Fatalf("empty project should have no turns, got %+v", turns)
	}
	if err := recordSessionPlannerDisplay(dir, sessionPath, "prompt", []HistoryMessage{
		{Role: "assistant", Content: "answer"},
	}); err != nil {
		t.Fatal(err)
	}
	if turns := sessionPlannerDisplayTurns(dir, sessionPath); len(turns) != 1 || turns[0].Messages[0].Content != "answer" {
		t.Fatalf("record must be visible through cache, got %+v", turns)
	}
	if err := removeSessionPlannerDisplay(dir, sessionPath); err != nil {
		t.Fatal(err)
	}
	if turns := sessionPlannerDisplayTurns(dir, sessionPath); turns != nil {
		t.Fatalf("remove must be visible through cache, got %+v", turns)
	}
}

func TestLoadSessionPlannerDisplaysConcurrentSingleFlight(t *testing.T) {
	resetPlannerDisplayCacheForTest()
	defer resetPlannerDisplayCacheForTest()
	dir := t.TempDir()
	plannerDisplayCacheTestWriteSidecar(t, dir, "shared")
	restore, reads := plannerDisplayCacheTestSwapReader(t, 40) // 慢读放大竞态窗口
	defer restore()

	const loaders = 32
	start := make(chan struct{})
	results := make(chan sessionPlannerDisplayMap, loaders)
	var wg sync.WaitGroup
	for range loaders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- loadSessionPlannerDisplays(dir)
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	for got := range results {
		if got == nil {
			t.Fatal("nil map from concurrent load")
		}
		turns := got["one.jsonl"]
		if len(turns) != 1 || turns[0].Messages[0].Content != "answer-one-shared" {
			t.Fatalf("concurrent load got wrong data: %+v", turns)
		}
	}
	if n := reads.Load(); n != 1 {
		t.Fatalf("concurrent cold loads performed %d disk reads, want 1 (single-flight)", n)
	}
}

func TestPlannerDisplayCacheEntryCap(t *testing.T) {
	resetPlannerDisplayCacheForTest()
	defer resetPlannerDisplayCacheForTest()
	root := t.TempDir()
	restore, _ := plannerDisplayCacheTestSwapReader(t, 0)
	defer restore()
	for i := range plannerDisplayCacheMaxEntries + 8 {
		dir := filepath.Join(root, fmt.Sprintf("project-%02d", i))
		if m := loadSessionPlannerDisplays(dir); len(m) != 0 {
			t.Fatalf("missing sidecar should read empty, got %#v", m)
		}
	}
	plannerDisplayCacheMu.Lock()
	defer plannerDisplayCacheMu.Unlock()
	if n := len(plannerDisplayCacheEntries); n > plannerDisplayCacheMaxEntries {
		t.Fatalf("cache entries = %d, want <= %d", n, plannerDisplayCacheMaxEntries)
	}
}
