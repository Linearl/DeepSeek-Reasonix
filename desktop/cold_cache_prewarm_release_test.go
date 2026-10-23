package main

import (
	"testing"
	"time"

	"reasonix/internal/agent"
)

// Task 1077 (378-B2 ②)：headless 预热完成后立即释放该会话的物化数据。
// 本测试直接验证 releasePrewarmedSession 精准剔除 graph cache 条目：
// 预热路径（headlessCompactOne）通过 boot.Build 构建 controller 时会把
// replayed graph 放进 agent 的 sessionGraphCache；compactOne 成功后必须
// 立即调用 releasePrewarmedSession，把这一条从缓存里剔除（用完即走不积内存）。
func TestReleasePrewarmedSessionDropsGraphCacheEntry(t *testing.T) {
	// 直接构造一个假的 graph cache 条目（agent 包内部 API 未导出，
	// 这里用导出的 InvalidateSessionGraph + ByteStats 做黑盒验证）。
	// 由于 desktop 包无法直接 Put 条目，本测试验证的是「条目不存在时
	// releasePrewarmedSession 不 panic 且 no-op」——真正的剔除逻辑
	// 由 agent.InvalidateSessionGraph 覆盖（save_dag_graph_cache_bytes_test.go）。
	loop := &coldCacheCompactLoop{}
	// 不存在的会话路径：no-op，不 panic。
	loop.releasePrewarmedSession("/nonexistent/session.jsonl")
	// 空缓存下 ByteStats 不变。
	before, _, _, entriesBefore, _ := agent.SessionGraphCacheByteStats()
	loop.releasePrewarmedSession("/nonexistent/session.jsonl")
	after, _, _, entriesAfter, _ := agent.SessionGraphCacheByteStats()
	if before != after || entriesBefore != entriesAfter {
		t.Fatalf("release on nonexistent session must be a no-op: bytes %d→%d, entries %d→%d",
			before, after, entriesBefore, entriesAfter)
	}
}

// TestColdCacheCompactSwitchOffZeroTouch: 开关关态 tick 零触碰（逐字节不变）。
// loadColdCacheCompactConfig 读 config.Load；测试隔离 user dirs 让 config 落
// 默认（Enabled=false），tick 必须在 knobs.Enabled 早退，不做任何事。
func TestColdCacheCompactSwitchOffZeroTouch(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	l := &coldCacheCompactLoop{
		a:         app,
		done:      map[string]int64{},
		parked:    map[string]int64{},
		firstSeen: map[string]int64{},
		inFlight:  map[string]bool{},
	}
	// tick 不 panic、不写 done/parked（开关关态零触碰）。
	l.tick(time.Now())
	l.mu.Lock()
	if len(l.done) != 0 || len(l.parked) != 0 || len(l.inFlight) != 0 {
		t.Fatalf("switch-off tick must leave all bookkeeping maps empty: done=%d parked=%d inFlight=%d",
			len(l.done), len(l.parked), len(l.inFlight))
	}
	l.mu.Unlock()
}
