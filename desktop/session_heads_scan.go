package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

// 任务 1019：面板级 heads 读数的限流与缓存。
//
// 事故形态（20261020 ai-passport 会话，8 heads / 47 MB events 日志 /
// 物化断层 476 条）：设置页「工作阶段版本与副本」与「工作阶段版本与修复」
// 两个分区背后，每展开一个会话都要 agent.ListSessionHeads 全量重放事件日
// 志——实测单次 2 秒起（每个 head 的 headRecord 都整链 materialize，8 heads
// × ~7k 节点 ≈ 5 万次消息转换），且此前零缓存：展开→收起→再展开、切换选中
// 头、面板重开，每一次都重付全额。桌面端进程同时还在跑会话保存（每秒级
// save 持锁 100-400ms），重放与活写争锁，设置页整体被拖到秒级乃至用户观感
// 「必然加载失败」。
//
// 修法（纯数据链，UI 重构另派）：
//  1. 身份键缓存——path|size|mtime 键，文件没变就不再重放（轮换/保存/修复
//     改了文件自然失效）；LRU 上限 4 条与 previewFeedCache 同档。
//  2. 面板级重放预算——超过 budget 的事件日志不做全量重放，降级返回（调用
//     方得到 degraded 标记，列表仍有该会话行与 meta 层读数，不再整页挂死）。
//     budget 默认 256 MiB，远高于实测事故样本（47 MB），只拦病态大文件；
//     测试可收窄以验证降级路径。
//
// 写路径（cleanTopicHeads 清理、修复动作）不经过这里：它们要么自带服务端
// 复核，要么要求读数绝对新鲜，必须直连全量重放。

// panelHeadsReplayMaxBytes 是面板路径愿意为一次 heads 全量重放付出的磁盘
// 上限。var 而非 const：测试收窄它来验证降级路径。
var panelHeadsReplayMaxBytes int64 = 256 << 20

// panelHeadsCacheMax 与 previewFeedCache 同档：面板同时细看的会话屈指可数，
// 4 条足够覆盖「展开→切换→切回来」的往返。
const panelHeadsCacheMax = 4

type panelHeadsEntry struct {
	heads    []agent.SessionHead
	degraded bool
}

// panelHeadsCache 键是 path|size|mtimeNs：任一变化（保存/轮换/修复写回）即
// 自然失效，无需主动失效通路。
var panelHeadsCache = struct {
	sync.Mutex
	entries map[string]panelHeadsEntry
	order   []string
	hits    int64
}{entries: map[string]panelHeadsEntry{}}

// panelHeadsCacheKey 是主转录文件的身份键（events 日志缺失时的退路）。
func panelHeadsCacheKey(path string) (string, int64, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return path + "|?", 0, false
	}
	return fmt.Sprintf("%s|%d|%d", path, info.Size(), info.ModTime().UnixNano()), info.Size(), true
}

// panelHeadsReplaySize 量的是重放真正要读的文件——schema-2 的 events 日志
// （物化断层形态下它比主转录大得多，事故样本 47 MB vs 25 MB），量主文件
// 会放行真正贵的那一侧。读不到 events 日志时退回主文件大小。
func panelHeadsReplaySize(path string) (string, int64, bool) {
	if eventLog := store.SessionEventLog(path); eventLog != "" {
		if info, err := os.Stat(eventLog); err == nil {
			return fmt.Sprintf("%s|%d|%d", path, info.Size(), info.ModTime().UnixNano()), info.Size(), true
		}
	}
	return panelHeadsCacheKey(path)
}

func panelHeadsCacheGet(key string) (panelHeadsEntry, bool) {
	panelHeadsCache.Lock()
	defer panelHeadsCache.Unlock()
	e, ok := panelHeadsCache.entries[key]
	if ok {
		panelHeadsCache.hits++
	}
	return e, ok
}

func panelHeadsCacheStore(key string, e panelHeadsEntry) {
	panelHeadsCache.Lock()
	defer panelHeadsCache.Unlock()
	if _, ok := panelHeadsCache.entries[key]; !ok {
		panelHeadsCache.order = append(panelHeadsCache.order, key)
		for len(panelHeadsCache.order) > panelHeadsCacheMax {
			oldest := panelHeadsCache.order[0]
			panelHeadsCache.order = panelHeadsCache.order[1:]
			delete(panelHeadsCache.entries, oldest)
		}
	}
	panelHeadsCache.entries[key] = e
}

// panelSessionHeads 是面板路径的 heads 读数入口：缓存命中零重放；events 日
// 志在预算内走全量重放并缓存；超预算降级（degraded=true，heads=nil）——调
// 用方保留该会话的行，但不为它付出无界重放。返回的切片归缓存所有，只读。
func panelSessionHeads(path string) (heads []agent.SessionHead, degraded bool, err error) {
	key, size, ok := panelHeadsReplaySize(path)
	if ok {
		if e, hit := panelHeadsCacheGet(key); hit {
			return e.heads, e.degraded, nil
		}
	}
	if ok && size > panelHeadsReplayMaxBytes {
		// 病态大日志：面板降级而不是把设置页挂在重放上。meta 层读数
		// （headCount/选中头）由 ListSessionVersionGroups 单独携带，这里
		// 不重放也不读全文。
		e := panelHeadsEntry{degraded: true}
		panelHeadsCacheStore(key, e)
		return e.heads, e.degraded, nil
	}
	heads, err = agent.ListSessionHeads(path)
	if err != nil {
		// 任务 1039：重放超预算（SessionReplayLimitError，含 limitsForSessionLog
		// 自适应额度顶到 1 GiB 硬顶仍不够的病态日志）不再是整页错误——与上面
		// 超面板预算的降级同语义：保留该会话行、计数全零、不重放。1019 的降级
		// 只拦「文件大小 > 面板预算」这一层；真正的硬拒发生在重放内部，两层
		// 之间漏了这条：49 MB 事件日志过面板预算、却在重放里撞硬顶，错误一路
		// 传到前端整页「设置加载失败」（20261021 GC开发专员条目实测）。
		if errors.Is(err, agent.ErrSessionReplayLimitExceeded) {
			e := panelHeadsEntry{degraded: true}
			if ok {
				panelHeadsCacheStore(key, e)
			}
			return e.heads, e.degraded, nil
		}
		// 其余重放失败不缓存：文件下次变化或调用重试时按原样再试。
		return nil, false, err
	}
	panelHeadsCacheStore(key, panelHeadsEntry{heads: heads})
	return heads, false, nil
}
