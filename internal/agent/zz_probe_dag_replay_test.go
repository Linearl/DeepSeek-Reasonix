package agent

// DAG 重放调研探针（2026-10-04，wt-zcode-dag-research 调研分支专用，非产品代码）。
// 运行方式：REASONIX_DAG_PROBE=1 go test ./internal/agent -run 'TestZZProbe' -v -timeout 30m
// 不设该环境变量时全部跳过，CI 与常规测试零影响。

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

func probeEnvFlag() bool { return os.Getenv("REASONIX_DAG_PROBE") == "1" }

func providerRoleForProbe(i int) provider.Role {
	if i%2 == 0 {
		return provider.RoleUser
	}
	return provider.RoleAssistant
}

func providerMessageForProbe(role provider.Role, content string) provider.Message {
	return provider.Message{Role: role, Content: content}
}

// heapSampler 以固定间隔采样 HeapAlloc，报告测量窗口内的峰值。
type heapSampler struct {
	mu     sync.Mutex
	stop   chan struct{}
	done   chan struct{}
	peak   uint64
	before uint64
}

func newHeapSampler() *heapSampler {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	s := &heapSampler{
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
		before: m.HeapAlloc,
		peak:   m.HeapAlloc,
	}
	go func() {
		defer close(s.done)
		tick := time.NewTicker(4 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-tick.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				s.mu.Lock()
				if m.HeapAlloc > s.peak {
					s.peak = m.HeapAlloc
				}
				s.mu.Unlock()
			}
		}
	}()
	return s
}

func (s *heapSampler) finish() (peakGrowth uint64, liveBytes uint64) {
	close(s.stop)
	<-s.done
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	s.mu.Lock()
	peak := s.peak
	s.mu.Unlock()
	if peak < s.before {
		peak = s.before
	}
	return peak - s.before, m.HeapAlloc
}

func probeBuildSession(t *testing.T, targetBytes int64, msgBytes int) (path string, msgs int, logBytes int64) {
	t.Helper()
	s := NewSession("probe-sys")
	chunk := strings.Repeat("x", msgBytes)
	const quota = 16 // 每 16 条消息一轮 user/assistant
	for written := int64(0); written < targetBytes; {
		for i := 0; i < quota && written < targetBytes; i++ {
			role := providerRoleForProbe(i)
			s.Add(providerMessageForProbe(role, chunk))
			written += int64(msgBytes)
			msgs++
		}
	}
	path = t.TempDir() + "/probe-session.jsonl"
	start := time.Now()
	if err := s.Save(path); err != nil {
		t.Fatalf("build save: %v", err)
	}
	t.Logf("BUILD path=%s msgs=%d save_ms=%d", path, msgs, time.Since(start).Milliseconds())
	return path, msgs, probeLogSize(t, path)
}

func probeLogSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(store.SessionEventLog(path))
	if err != nil {
		t.Fatalf("stat event log: %v", err)
	}
	return info.Size()
}

func probeReplayOnce(t *testing.T, label, path string, tailWindow int64) {
	t.Helper()
	ctx := context.Background()
	sampler := newHeapSampler()
	start := time.Now()
	var st *sessionDAGState
	var err error
	if tailWindow > 0 {
		st, err = replaySessionDAGTail(ctx, store.SessionEventLog(path), tailWindow, defaultSessionReplayLimits)
	} else {
		st, err = replaySessionDAG(ctx, store.SessionEventLog(path), defaultSessionReplayLimits)
	}
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("%s replay: %v", label, err)
	}
	matStart := time.Now()
	headID := st.selectedHead()
	msgs, _ := st.materialize(headID)
	matMs := time.Since(matStart).Milliseconds()
	peak, live := sampler.finish()
	runtime.KeepAlive(st)
	runtime.KeepAlive(msgs)
	t.Logf("RESULT label=%s log_mb=%d wall_ms=%d materialize_ms=%d nodes=%d peak_heap_mb=%.1f live_graph_mb=%.1f msgs=%d",
		label, probeLogSize(t, path)>>20, elapsed.Milliseconds(), matMs, len(st.nodes), float64(peak)/(1<<20), float64(live)/(1<<20), len(msgs))
}

// TestZZProbeDAGReplayScaling 量化：全量重放 / 尾窗重放（首帧路径）/ 打开（LoadSession）
// 的耗时与内存，按 32/128/256 MiB 三档日志；再量"切走保存"的冷缓存全量重放 vs 热缓存增量。
func TestZZProbeDAGReplayScaling(t *testing.T) {
	if !probeEnabled() {
		t.Skip("set REASONIX_DAG_PROBE=1 to run the DAG replay probe")
	}
	for _, target := range []int64{32 << 20, 128 << 20, 256 << 20} {
		path, _, logBytes := probeBuildSession(t, target, 64<<10)
		t.Logf("LOG target_mb=%d actual_mb=%d", target>>20, logBytes>>20)

		// 1) 首帧路径：LoadSessionTail（>32MiB 走 20MiB 尾窗）。
		probeLoadTail(t, path)

		// 2) 全量重放（无缓存利用，等价 LoadSession/写路径兜底）。
		probeReplayOnce(t, "full-replay", path, 0)

		// 3) 打开路径 LoadSession（重放+物化+规范化，进缓存）。
		probeLoadSession(t, path)

		// 4) 切走保存（cache 命中 → 增量扫描）。
		probeSaveAfterLoad(t, path, true)

		// 5) 切走保存（清缓存 → 锁内全量重放，196 症状）。
		probeSaveAfterLoad(t, path, false)
	}
}

func probeEnabled() bool { return probeEnvFlag() }

func probeLoadTail(t *testing.T, path string) {
	t.Helper()
	sampler := newHeapSampler()
	start := time.Now()
	s, err := LoadSessionTail(path)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("LoadSessionTail: %v", err)
	}
	peak, live := sampler.finish()
	runtime.KeepAlive(s)
	t.Logf("RESULT label=load-tail log_mb=%d wall_ms=%d tail_truncated=%v msgs=%d peak_heap_mb=%.1f live_mb=%.1f",
		probeLogSize(t, path)>>20, elapsed.Milliseconds(), s.TailTruncated(), len(s.Messages), float64(peak)/(1<<20), float64(live)/(1<<20))
}

func probeLoadSession(t *testing.T, path string) {
	t.Helper()
	resetGraphCacheForTest()
	sampler := newHeapSampler()
	start := time.Now()
	s, err := LoadSession(path)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	peak, live := sampler.finish()
	runtime.KeepAlive(s)
	hits, misses, evictions := SessionGraphCacheStats()
	t.Logf("RESULT label=load-full log_mb=%d wall_ms=%d msgs=%d peak_heap_mb=%.1f live_mb=%.1f cache(h/m/e)=%d/%d/%d",
		probeLogSize(t, path)>>20, elapsed.Milliseconds(), len(s.Messages), float64(peak)/(1<<20), float64(live)/(1<<20), hits, misses, evictions)
}

func probeSaveAfterLoad(t *testing.T, path string, warm bool) {
	t.Helper()
	s, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if !warm {
		resetGraphCacheForTest()
	}
	s.Add(providerMessageForProbe(providerRoleForProbe(0), strings.Repeat("y", 8<<10)))
	sampler := newHeapSampler()
	start := time.Now()
	err = s.Save(path)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	peak, _ := sampler.finish()
	label := "save-cold-replay"
	if warm {
		label = "save-warm-extend"
	}
	hits, misses, evictions := SessionGraphCacheStats()
	t.Logf("RESULT label=%s log_mb=%d wall_ms=%d peak_heap_mb=%.1f cache(h/m/e)=%d/%d/%d",
		label, probeLogSize(t, path)>>20, elapsed.Milliseconds(), float64(peak)/(1<<20), hits, misses, evictions)
}

// TestZZProbeGraphCacheHitRate 量化：K 个常驻会话交替保存（桌面 tab 真实模式：
// controller 常驻、Load 只在打开时发生一次）下 capacity 3/6/12 的命中率与每存耗时。
// 日志大小取 4MiB（重放几十 ms，量级可辨）。
func TestZZProbeGraphCacheHitRate(t *testing.T) {
	if !probeEnabled() {
		t.Skip("set REASONIX_DAG_PROBE=1 to run the DAG replay probe")
	}
	type scenario struct {
		sessions int
		capacity int
		rounds   int
	}
	scenarios := []scenario{
		{sessions: 6, capacity: 3, rounds: 4},
		{sessions: 6, capacity: 6, rounds: 4},
		{sessions: 6, capacity: 12, rounds: 4},
		{sessions: 12, capacity: 6, rounds: 3},
		{sessions: 12, capacity: 12, rounds: 3},
	}
	for _, sc := range scenarios {
		sc := sc
		t.Run(fmt.Sprintf("K%d-cap%d", sc.sessions, sc.capacity), func(t *testing.T) {
			SetSessionGraphCacheCapacity(sc.capacity)
			t.Cleanup(func() {
				SetSessionGraphCacheCapacity(sessionGraphCacheCapacityDefault)
				resetGraphCacheForTest()
			})
			paths := make([]string, sc.sessions)
			for i := range paths {
				p, _, _ := probeBuildSession(t, 4<<20, 32<<10)
				paths[i] = p
			}
			resetGraphCacheForTest()
			// 桌面模式：每个 tab 打开时 LoadSession 一次，controller 常驻。
			live := make([]*Session, sc.sessions)
			for i, p := range paths {
				s, err := LoadSession(p)
				if err != nil {
					t.Fatalf("load %s: %v", p, err)
				}
				live[i] = s
			}
			var saves []time.Duration
			hits0, misses0, evict0 := SessionGraphCacheStats()
			runtime.GC()
			var mem0, mem1 runtime.MemStats
			runtime.ReadMemStats(&mem0)
			for r := 0; r < sc.rounds; r++ {
				for i, p := range paths {
					live[i].Add(providerMessageForProbe(providerRoleForProbe(r), "interleave-probe"))
					start := time.Now()
					if err := live[i].Save(p); err != nil {
						t.Fatalf("save %s: %v", p, err)
					}
					saves = append(saves, time.Since(start))
				}
			}
			runtime.GC()
			runtime.ReadMemStats(&mem1)
			hits, misses, evictions := SessionGraphCacheStats()
			hits, misses, evictions = hits-hits0, misses-misses0, evictions-evict0
			total := hits + misses
			rate := 0.0
			if total > 0 {
				rate = float64(hits) / float64(total) * 100
			}
			p50, p95, max := probePercentiles(saves)
			t.Logf("HITRATE K=%d cap=%d rounds=%d saves=%d hits=%d misses=%d evictions=%d hit_rate=%.1f%% save_p50_ms=%d save_p95_ms=%d save_max_ms=%d graph_live_mb=%.1f",
				sc.sessions, sc.capacity, sc.rounds, len(saves), hits, misses, evictions, rate, p50, p95, max, float64(mem1.HeapInuse-mem0.HeapInuse)/(1<<20))
		})
	}
}

func probePercentiles(ds []time.Duration) (p50, p95, max int64) {
	if len(ds) == 0 {
		return 0, 0, 0
	}
	sorted := make([]time.Duration, len(ds))
	copy(sorted, ds)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	pick := func(q float64) int64 {
		idx := int(float64(len(sorted)-1) * q)
		return sorted[idx].Milliseconds()
	}
	return pick(0.5), pick(0.95), sorted[len(sorted)-1].Milliseconds()
}
