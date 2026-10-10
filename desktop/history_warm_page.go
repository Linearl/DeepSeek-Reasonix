package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// 任务 451 方案 B：history 最新页暖缓存（idle 预取的命中面）。
//
// 分相打点实测（desktop.log "history slice timing"，2026-10-08）：冷读一页
// （source=index|scan）常态 0.8-2.2s，大头在 page-cut 段（page-fetch +
// page-times + page-planner + page-convert），而 cold-identity 只占十几毫秒
// ——A/C 两案消掉的只是其中的 planner-turns 与尾读相位。本缓存把整页结果
// 按会话身份存住：命中时一次 stat 即回，切 tab 的首响不再付读盘+换转。
//
// 身份与失效（与方案 A 同一规格取舍）：
//   - 键：filepath.Clean 后的会话路径（进程内调用方都经 resolveSessionPath
//     派生；Windows 大小写别名最坏导致重复条目，正确性由 stat 校验兜底）。
//   - 校验：每次命中先 stat 会话文件，size+mtimeNs 与预取时一致才回；
//     会话推进（追加保存、重写、外部进程改动）都会动 mtime，自然失效回落
//     冷路径。残留风险与一切 mtime 缓存相同：同尺寸同 mtime 的改写不会
//     被发现（方案 A 规格接受，同款注释见 sessions_planner_display_cache.go）。
//     前端还有 revision/digest 指纹核对（task123 reconcile）作第二道防线。
//   - 只缓存最新页形状：cursor 为空 + turns=historyIdlePrefetchTurns + 后端
//     默认 entries/bytes —— 即切换流程（useController loadLatest，turns=60）
//     与本任务预取发出的那一种请求。跳页/手动参数不进缓存，语义保持原样。
//   - Error/Stale 页不缓存；空会话页允许缓存（负缓存，重复打开零成本）。
//   - 共享只读：缓存内 HistorySlice 被多个请求共享，消费方只许序列化，
//     不得改写（与 plannerDisplayCache 同一契约）；Wails 出口每次 JSON
//     序列化，前端拿到的始终是新对象。
//   - 上限 historyWarmPageMaxEntries 条 LRU；单条最坏 ~512KiB 内联预算
//     （defaultHistorySliceBytes），8 条 ≈ 数 MiB 内存封顶。
//
// 与既有缓存面的分工（避免重叠）：
//   - 方案 A plannerDisplayCache：消 planner-turns 相位（读侧车），本缓存
//     命中后该相位根本不发生；未命中冷路径上 A 照常生效，两层互补。
//   - task123 historyTimeOverlayCache（方案 C 单飞）：同上，管 page-times。
//   - 前端 transcript store（448 悬停预取的喂食对象）：前端 LRU 未命中才
//     会打到后端；本缓存让这次后端往返从秒级降到 stat 级。

// historyWarmPageMaxEntries 防御性 LRU 上限：正常用量 = 恢复的冷 tab 数
// （个位数），超限按最旧逐出，重建成本只是一次冷读。
const historyWarmPageMaxEntries = 8

type historyWarmPageEntry struct {
	// req 是入缓存时归一化后的请求形状（可比较结构体，直接 == 判等）。
	req HistorySliceRequest
	// size/mtimeNs 是切页时会话文件的 stat 身份，命中校验用。
	size    int64
	mtimeNs int64
	// slice 是切好的最新页，共享只读。
	slice HistorySlice
}

var historyWarmPageCache = struct {
	mu      sync.Mutex
	entries map[string]historyWarmPageEntry
	order   []string
}{entries: map[string]historyWarmPageEntry{}}

func historyWarmPageKey(sessionPath string) string {
	return filepath.Clean(strings.TrimSpace(sessionPath))
}

// historyWarmPageReqShape reports whether req is exactly the newest-page shape
// the switch flow issues (frontend HISTORY_PAGE_TURNS=60 plus backend
// defaults). Only that shape is cached or served: jump/manual requests keep
// their uncached semantics.
func historyWarmPageReqShape(req HistorySliceRequest) bool {
	n := normalizeHistorySliceRequest(req)
	return n.Cursor == "" && n.Turns == historyIdlePrefetchTurns &&
		n.Entries == defaultHistorySliceEntries && n.Bytes == defaultHistorySliceBytes
}

// lookupHistoryWarmPage returns the cached newest page when the session file's
// stat identity still matches the one the page was cut under.
func lookupHistoryWarmPage(sessionPath string, req HistorySliceRequest) (HistorySlice, bool) {
	if !historyWarmPageReqShape(req) {
		return HistorySlice{}, false
	}
	key := historyWarmPageKey(sessionPath)
	if key == "" || key == "." {
		return HistorySlice{}, false
	}
	info, err := os.Stat(sessionPath)
	if err != nil || info.IsDir() {
		return HistorySlice{}, false
	}
	reqN := normalizeHistorySliceRequest(req)
	historyWarmPageCache.mu.Lock()
	entry, ok := historyWarmPageCache.entries[key]
	match := ok && entry.req == reqN &&
		entry.size == info.Size() && entry.mtimeNs == info.ModTime().UnixNano()
	if match {
		historyWarmPageTouchLocked(key)
	}
	historyWarmPageCache.mu.Unlock()
	if !match {
		return HistorySlice{}, false
	}
	return entry.slice, true
}

// storeHistoryWarmPage caches a freshly cut newest page. Best-effort: any
// stat failure or non-cacheable shape/error just skips the cache.
func storeHistoryWarmPage(sessionPath string, req HistorySliceRequest, slice HistorySlice) {
	if !historyWarmPageReqShape(req) || slice.Error != "" || slice.Stale {
		return
	}
	key := historyWarmPageKey(sessionPath)
	if key == "" || key == "." {
		return
	}
	info, err := os.Stat(sessionPath)
	if err != nil || info.IsDir() {
		return
	}
	historyWarmPageCache.mu.Lock()
	if _, ok := historyWarmPageCache.entries[key]; !ok {
		historyWarmPageCache.order = append(historyWarmPageCache.order, key)
	}
	historyWarmPageCache.entries[key] = historyWarmPageEntry{
		req:     normalizeHistorySliceRequest(req),
		size:    info.Size(),
		mtimeNs: info.ModTime().UnixNano(),
		slice:   slice,
	}
	for len(historyWarmPageCache.order) > historyWarmPageMaxEntries {
		oldest := historyWarmPageCache.order[0]
		historyWarmPageCache.order = historyWarmPageCache.order[1:]
		delete(historyWarmPageCache.entries, oldest)
	}
	historyWarmPageCache.mu.Unlock()
}

func historyWarmPageTouchLocked(key string) {
	for i, candidate := range historyWarmPageCache.order {
		if candidate == key {
			historyWarmPageCache.order = append(historyWarmPageCache.order[:i], historyWarmPageCache.order[i+1:]...)
			break
		}
	}
	historyWarmPageCache.order = append(historyWarmPageCache.order, key)
}

// resetHistoryWarmPageCache clears the cache; tests use it to observe cold
// reads and warm hits deterministically.
func resetHistoryWarmPageCache() {
	historyWarmPageCache.mu.Lock()
	defer historyWarmPageCache.mu.Unlock()
	historyWarmPageCache.entries = map[string]historyWarmPageEntry{}
	historyWarmPageCache.order = nil
}
