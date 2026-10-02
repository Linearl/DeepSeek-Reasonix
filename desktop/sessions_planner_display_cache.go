package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// 任务 451 方案 A：planner 展示侧车缓存。
//
// 项目级 .planner-display.json 随项目全体会话的 planner 展示轮增长（实测
// 12MB），而 history 切片、旧版 History API 与检查点路径每次都整读+重新
// JSON 解析，打点实测占 history 阶段 38-41%（desktop.log "history slice
// timing" 的 planner-turns 相位）。本缓存按项目 dir 保留解析结果，消除
// 这份读放大：
//   - 校验：每次装载先 stat，mtime+size 与缓存一致才命中；外部进程改写
//     文件时自然失效重载。残留风险与一切 mtime 缓存相同：外部同尺寸同
//     mtime 的改写不会被发现（规格接受）。
//   - 写侧失效：本进程的一切写（saveSessionPlannerDisplays 与
//     saveOrRemoveSessionPlannerDisplays 的保存/删除分支）成功后立即删除
//     对应条目，不依赖 mtime 粒度。
//   - 单飞：同一 dir 的并发冷装载只发生一次磁盘读+解析，其余等待共享。
//   - 上限：条目按项目计数量小，防御性上限 plannerDisplayCacheMaxEntries，
//     满了随机逐出一条（重建成本只是一次重读）。
//
// 键用 filepath.Clean 后的 dir：进程内所有调用方都经 resolveSessionPath
// 派生，拼写一致；Windows 大小写别名最坏导致重复条目，正确性仍由
// mtime+size 校验兜底（pathidentity.Canonical 的 EvalSymlinks 每请求
// 系统调用不值得进热路径）。
//
// 返回的 map 为共享只读：现有调用方只做查键，planner 轮数据在
// sessionPlannerDisplayTurns 处照旧逐轮克隆；未来调用方若要改写，必须先
// 自行克隆，不得动缓存内的对象。

// plannerDisplayCacheMaxEntries 防御目录条目无限增长：每条目对应一个项目
// 目录，正常用量个位数，超限视为异常路径抖动。
const plannerDisplayCacheMaxEntries = 64

type plannerDisplayCacheEntry struct {
	// data 是整个项目级侧车的解析结果，共享只读。
	data sessionPlannerDisplayMap
	// exists 区分"文件存在"与"文件缺席（负缓存）"两种条目：缺席条目在
	// 文件被创建后由 exists 失配触发重载。
	exists bool
	// mtimeNs/size 是装载时的文件身份， Uniform stat 校验用。
	mtimeNs int64
	size    int64
}

// plannerDisplayInflight 承接同一 dir 的并发冷装载：先到者装载，后到者等
// done 关闭后共享 data。
type plannerDisplayInflight struct {
	done chan struct{}
	data sessionPlannerDisplayMap
}

var (
	plannerDisplayCacheMu       sync.Mutex
	plannerDisplayCacheEntries  = map[string]plannerDisplayCacheEntry{}
	plannerDisplayCacheInflight = map[string]*plannerDisplayInflight{}
)

// plannerDisplayReadFile 是测试接缝（仿 sessionPlannerDisplayUpdateAfterLoad
// 先例）：生产指向 readFileUTF8；测试替换为可计数/慢速实现，用于验证命中
// 路径零读盘与并发单飞。
var plannerDisplayReadFile = readFileUTF8

func plannerDisplayCacheKey(dir string) string {
	return filepath.Clean(strings.TrimSpace(dir))
}

// loadCachedSessionPlannerDisplays 是 loadSessionPlannerDisplays 的缓存实现。
// 对外语义与原实现一致：文件缺席、读失败、解析失败一律回非 nil 空 map。
func loadCachedSessionPlannerDisplays(dir string) sessionPlannerDisplayMap {
	empty := sessionPlannerDisplayMap{}
	key := plannerDisplayCacheKey(dir)
	if key == "" || key == "." {
		return empty
	}
	path := sessionPlannerDisplayPath(key)
	fi, statErr := os.Stat(path)

	plannerDisplayCacheMu.Lock()
	if statErr == nil {
		if e, ok := plannerDisplayCacheEntries[key]; ok && e.exists &&
			e.mtimeNs == fi.ModTime().UnixNano() && e.size == fi.Size() {
			data := e.data
			plannerDisplayCacheMu.Unlock()
			return data
		}
	} else if os.IsNotExist(statErr) {
		if e, ok := plannerDisplayCacheEntries[key]; ok && !e.exists {
			data := e.data
			plannerDisplayCacheMu.Unlock()
			return data
		}
	} else {
		// stat 因其它原因失败（权限等）：绕过缓存，保持原语义。
		plannerDisplayCacheMu.Unlock()
		return readSessionPlannerDisplaysUnchecked(path)
	}
	if fl, ok := plannerDisplayCacheInflight[key]; ok {
		plannerDisplayCacheMu.Unlock()
		<-fl.done
		return fl.data
	}
	fl := &plannerDisplayInflight{done: make(chan struct{})}
	plannerDisplayCacheInflight[key] = fl
	plannerDisplayCacheMu.Unlock()

	data := readSessionPlannerDisplaysUnchecked(path)

	entry := plannerDisplayCacheEntry{data: data}
	if statErr == nil {
		entry.exists = true
		entry.mtimeNs = fi.ModTime().UnixNano()
		entry.size = fi.Size()
	}
	plannerDisplayCacheMu.Lock()
	fl.data = data // 发布给单飞跟随者（done 关闭后可见）
	if _, ok := plannerDisplayCacheEntries[key]; !ok && len(plannerDisplayCacheEntries) >= plannerDisplayCacheMaxEntries {
		for k := range plannerDisplayCacheEntries {
			delete(plannerDisplayCacheEntries, k)
			break
		}
	}
	plannerDisplayCacheEntries[key] = entry
	delete(plannerDisplayCacheInflight, key)
	close(fl.done)
	plannerDisplayCacheMu.Unlock()
	return data
}

// readSessionPlannerDisplaysUnchecked 保持原 loadSessionPlannerDisplays 的
// 容错语义：读失败或解析失败都回空 map，不向上报错。
func readSessionPlannerDisplaysUnchecked(path string) sessionPlannerDisplayMap {
	m := sessionPlannerDisplayMap{}
	b, err := plannerDisplayReadFile(path)
	if err != nil {
		return m
	}
	_ = json.Unmarshal(b, &m)
	return m
}

// invalidateSessionPlannerDisplayCache 在写侧成功落盘/删除后调用，删除该
// 项目目录的缓存条目；下一次装载按冷路径重读。
func invalidateSessionPlannerDisplayCache(dir string) {
	key := plannerDisplayCacheKey(dir)
	if key == "" || key == "." {
		return
	}
	plannerDisplayCacheMu.Lock()
	delete(plannerDisplayCacheEntries, key)
	plannerDisplayCacheMu.Unlock()
}

// resetPlannerDisplayCacheForTest 清空缓存与单飞表；测试用它观察冷装载。
// 已在等待的单飞跟随者持有自身指针，不受清表影响。
func resetPlannerDisplayCacheForTest() {
	plannerDisplayCacheMu.Lock()
	defer plannerDisplayCacheMu.Unlock()
	plannerDisplayCacheEntries = map[string]plannerDisplayCacheEntry{}
	plannerDisplayCacheInflight = map[string]*plannerDisplayInflight{}
}
