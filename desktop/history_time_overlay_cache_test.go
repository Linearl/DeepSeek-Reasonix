package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"reasonix/internal/store"
)

// 任务 451 方案 C 验收测试：page-times 尾读缓存（historyTimeOverlayCache）
// 的身份绑定语义与并发单飞。缓存本体（head+size 身份、追加增量吸收、LRU）
// 是 task 123/2026-09-19 的既有机制，本单补齐并发冷读单飞并把这些语义固
// 定成测试。

func overlayCacheWriteLog(t *testing.T, sessionPath string, lines []string) {
	t.Helper()
	if err := os.WriteFile(store.SessionEventLog(sessionPath), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// overlayCacheUserLine 产出一条 schema-2 消息记录。content 长度参与字节身份，
// 旋转测试用同长度替换制造"同尺寸不同 head"。
func overlayCacheUserLine(t *testing.T, id string, at int64, content string) string {
	b, err := json.Marshal(map[string]any{
		"schema_version": 2,
		"type":           "message",
		"id":             id,
		"at":             "2026-09-18T00:00:00Z",
		"msgs":           []map[string]any{{"role": "user", "created_at": at, "createdAt": at, "content": content}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func overlayCacheStatsReset(t *testing.T) {
	t.Helper()
	resetHistoryTimeOverlayCache()
	t.Cleanup(resetHistoryTimeOverlayCache)
}

func TestPersistedUserTimesCacheServesExactRepeat(t *testing.T) {
	overlayCacheStatsReset(t)
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "s.jsonl")
	overlayCacheWriteLog(t, sessionPath, []string{overlayCacheUserLine(t, "m1", 1000, strings.Repeat("x", 200))})

	first := persistedUserTimesForWindow(sessionPath)
	if first["m1"] != 1000 {
		t.Fatalf("first read should scan the log, got %v", first)
	}
	second := persistedUserTimesForWindow(sessionPath)
	if second["m1"] != 1000 {
		t.Fatalf("repeat read lost the time, got %v", second)
	}
	hits, misses, extends := historyTimeOverlayStats()
	if hits != 1 || misses != 1 || extends != 0 {
		t.Fatalf("stats hits=%d misses=%d extends=%d, want 1/1/0", hits, misses, extends)
	}
}

func TestPersistedUserTimesCacheRotationInvalidatesOnHeadChange(t *testing.T) {
	overlayCacheStatsReset(t)
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "s.jsonl")

	// 同尺寸、不同 head：id/at 同长度替换（m1→m9、1000→9000），content
	// 长度不变 → 文件总尺寸相同，开头 128 字节身份不同（等价日志轮转）。
	lineA := overlayCacheUserLine(t, "m1", 1000, strings.Repeat("x", 200))
	lineB := overlayCacheUserLine(t, "m9", 9000, strings.Repeat("x", 200))
	if len(lineA) != len(lineB) {
		t.Fatalf("rotation fixtures must be equal length: %d vs %d", len(lineA), len(lineB))
	}
	overlayCacheWriteLog(t, sessionPath, []string{lineA})
	first := persistedUserTimesForWindow(sessionPath)
	if first["m1"] != 1000 {
		t.Fatalf("generation A should map m1, got %v", first)
	}

	overlayCacheWriteLog(t, sessionPath, []string{lineB})
	second := persistedUserTimesForWindow(sessionPath)
	if second["m9"] != 9000 {
		t.Fatalf("generation B must be reread after head change, got %v", second)
	}
	if _, found := second["m1"]; found {
		t.Fatalf("stale generation A id must not survive rotation, got %v", second)
	}
	_, misses, _ := historyTimeOverlayStats()
	if misses != 2 {
		t.Fatalf("rotation must cause a second full read, misses = %d", misses)
	}
}

func TestPersistedUserTimesCacheAbsorbsAppendIncrementally(t *testing.T) {
	overlayCacheStatsReset(t)
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "s.jsonl")

	// 首记录拉长（>128 字节），保证追加不影响 head 身份。
	line1 := overlayCacheUserLine(t, "m1", 1000, strings.Repeat("x", 200))
	overlayCacheWriteLog(t, sessionPath, []string{line1})
	first := persistedUserTimesForWindow(sessionPath)
	if first["m1"] != 1000 {
		t.Fatalf("baseline read wrong: %v", first)
	}

	line2 := overlayCacheUserLine(t, "m2", 2000, strings.Repeat("y", 200))
	overlayCacheWriteLog(t, sessionPath, []string{line1, line2})
	second := persistedUserTimesForWindow(sessionPath)
	if second["m1"] != 1000 || second["m2"] != 2000 {
		t.Fatalf("append must extend the map with the new id, got %v", second)
	}
	hits, misses, extends := historyTimeOverlayStats()
	if extends != 1 || misses != 1 {
		t.Fatalf("append must be absorbed as an extension: hits=%d misses=%d extends=%d", hits, misses, extends)
	}
}

func TestPersistedUserTimesCacheLRUBound(t *testing.T) {
	overlayCacheStatsReset(t)
	dir := t.TempDir()
	for i := range historyTimeOverlayCacheEntries + 2 {
		sessionPath := filepath.Join(dir, fmt.Sprintf("s%02d.jsonl", i))
		overlayCacheWriteLog(t, sessionPath, []string{overlayCacheUserLine(t, fmt.Sprintf("m%02d", i), int64(1000+i), "x")})
		persistedUserTimesForWindow(sessionPath)
	}
	historyTimeOverlayCache.mu.Lock()
	defer historyTimeOverlayCache.mu.Unlock()
	if n := len(historyTimeOverlayCache.entries); n > historyTimeOverlayCacheEntries {
		t.Fatalf("overlay cache entries = %d, want <= %d", n, historyTimeOverlayCacheEntries)
	}
}

func TestPersistedUserTimesConcurrentSingleFlight(t *testing.T) {
	overlayCacheStatsReset(t)
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "s.jsonl")

	// 足量记录把冷扫描拉长到几十毫秒，让并发首读必然落在扫描窗口内；
	// 单飞正确时 misses 恒为 1，无单飞时≈并发数。
	lines := make([]string, 0, 6000)
	for i := range 6000 {
		lines = append(lines, overlayCacheUserLine(t, fmt.Sprintf("m%05d", i), int64(1000+i), strings.Repeat("x", 24)))
	}
	overlayCacheWriteLog(t, sessionPath, lines)

	const loaders = 8
	start := make(chan struct{})
	results := make(chan map[string]int64, loaders)
	var wg sync.WaitGroup
	for range loaders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- persistedUserTimesForWindow(sessionPath)
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	for got := range results {
		if got == nil {
			t.Fatal("nil times map from concurrent load")
		}
		if got["m00000"] != 1000 || got["m05999"] != 6999 {
			t.Fatalf("concurrent load got wrong data: first=%v last=%v", got["m00000"], got["m05999"])
		}
	}
	_, misses, _ := historyTimeOverlayStats()
	if misses != 1 {
		t.Fatalf("concurrent cold loads performed %d full tail reads, want 1 (single-flight)", misses)
	}
}
