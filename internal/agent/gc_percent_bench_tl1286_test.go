package agent

// tl1286 三档重测仪器：GOGC 50/100/150 下「会话全量解码」负载的 RSS + GC CPU
// 对照。负载入口 = loadSessionMessagesFromJSONLContext（checkpoint .jsonl 逐行
// 解码——tl1279 实测的 literalStore 主分配路径，内存旋钮真正作用的工作面）。
// 每档一个独立子进程（GOGC env 在 runtime 初始化生效，与 debug.SetGCPercent
// 同一机制同一效果），档位间互不污染 GC 状态；窗口内指标 = MemStats 前后差 +
// GetProcessTimes/GCCPUFraction 推导的 GC CPU + psapi 采样的工作集序列。
//
// 运行方式（默认不跑——常规 go test ./internal/agent/ 只付冒烟成本）：
//
//	REASONIX_GC_BENCH=1 go test ./internal/agent/ -run TestGCTierComparisonTL1286 -count=1 -timeout 30m
//
// 可选环境变量：
//
//	REASONIX_GC_BENCH_SESSION=<.jsonl 检查点路径>  指定真实负载（只读解码）；
//	                                               缺省在 TempDir 合成 ~64MB 拟真转录
//	REASONIX_GC_BENCH_CYCLES=<n>                   每档解码轮数（默认 6）
//
// 口径注记（报告引用时必须带上）：
//   - RSS = psapi GetProcessMemoryInfo 的 WorkingSetSize（工作集，含可共享页；
//     任务管理器「内存」列是专用工作集，量级略低于本口径），取窗口内峰值/均值；
//   - GC CPU = GOMAXPROCS × Δ(GCCPUFraction × 进程存活时长)，覆盖并发标记的
//     全部 GC 用时（PauseTotalNs 只是 STW 部分），另报 PauseTotal 差值作参考；
//   - 子进程除负载外无其他工作，窗口 ≈ 进程全程，口径残差在个位数百分点。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"reasonix/internal/provider"
)

const (
	gcBenchResultPrefix = "TL1286GCRESULT: "
	gcBenchTiers        = "50,100,150"
)

// gcBenchSample 是单档（单子进程）的一行读数。
type gcBenchSample struct {
	Tier           int     `json:"tier"`
	Cycles         int     `json:"cycles"`
	Source         string  `json:"source"`
	SourceMB       float64 `json:"sourceMB"`
	MessagesPerRun int     `json:"messagesPerRun"`
	GOMAXPROCS     int     `json:"gomaxprocs"`
	RSSPeakMB      float64 `json:"rssPeakMB"`
	RSSAvgMB       float64 `json:"rssAvgMB"`
	PrivatePeakMB  float64 `json:"privatePeakMB"`
	HeapInuseEndMB float64 `json:"heapInuseEndMB"`
	HeapSysEndMB   float64 `json:"heapSysEndMB"`
	NumGC          uint32  `json:"numGC"`
	PauseTotalMs   float64 `json:"pauseTotalMs"`
	GCCPUSec       float64 `json:"gcCPUSec"`
	TotalCPUSec    float64 `json:"totalCPUSec"`
	GCCPUFractionW float64 `json:"gcCPUFractionWindow"`
	WallSec        float64 `json:"wallSec"`
	ThroughputMBps float64 `json:"throughputMBps"`
}

// gcRSSSeries 汇总采样 goroutine 的工作集序列（psapi 采样不进 Go runtime，
// 不触发 STW，不影响被测 GC 行为）。
type gcRSSSeries struct {
	mu       sync.Mutex
	peakWS   uintptr
	sumWS    uint64
	n        uint64
	peakPriv uintptr
}

func (s *gcRSSSeries) record(ws, priv uintptr) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ws > s.peakWS {
		s.peakWS = ws
	}
	if priv > s.peakPriv {
		s.peakPriv = priv
	}
	s.sumWS += uint64(ws)
	s.n++
}

func (s *gcRSSSeries) snapshot() (peakWSMB, avgWSMB, peakPrivMB float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	const mb = 1024 * 1024
	if s.n == 0 {
		return 0, 0, 0
	}
	return float64(s.peakWS) / mb, float64(s.sumWS) / float64(s.n) / mb, float64(s.peakPriv) / mb
}

// psapi GetProcessMemoryInfo（x/sys/windows 未封装，测试内自包含声明）。
type processMemoryCountersEx struct {
	CB                      uint32
	PageFaultCount          uint32
	PeakWorkingSetSize      uintptr
	WorkingSetSize          uintptr
	QuotaPeakPagedPoolUsage uintptr
	QuotaPagedPoolUsage     uintptr
	QuotaNonPagedPoolUsage  uintptr
	PagefileUsage           uintptr
	PeakPagefileUsage       uintptr
	PrivateUsage            uintptr
}

var (
	modpsapi                 = windows.NewLazySystemDLL("psapi.dll")
	procGetProcessMemoryInfo = modpsapi.NewProc("GetProcessMemoryInfo")
)

func sampleProcessMemory() (workingSet, privateUsage uintptr) {
	var pmc processMemoryCountersEx
	pmc.CB = uint32(unsafe.Sizeof(pmc))
	r1, _, _ := procGetProcessMemoryInfo.Call(
		uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&pmc)), uintptr(pmc.CB))
	if r1 == 0 {
		return 0, 0
	}
	return pmc.WorkingSetSize, pmc.PrivateUsage
}

// processUptimeSeconds 经 GetProcessTimes 的创建时刻推算进程存活时长
// （GCCPUFraction 是「自进程启动累计」的比值，窗口 GC CPU 需要它换算）。
func processUptimeSeconds() float64 {
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &creation, &exit, &kernel, &user); err != nil {
		return 0
	}
	var now windows.Filetime
	windows.GetSystemTimeAsFileTime(&now)
	d := now.Nanoseconds() - creation.Nanoseconds()
	if d < 0 {
		return 0
	}
	return float64(d) / 1e9
}

// processTotalCPUSeconds 进程累计 CPU（内核+用户），作 GC CPU 占比的分母参考。
func processTotalCPUSeconds() float64 {
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &creation, &exit, &kernel, &user); err != nil {
		return 0
	}
	return (float64(kernel.Nanoseconds()) + float64(user.Nanoseconds())) / 1e9
}

// synthGCBenchTranscript 用真实 provider.Message 结构 marshal 出拟真转录
// （模式按构造正确），约 targetBytes 字节。
func synthGCBenchTranscript(t *testing.T, targetBytes int) string {
	t.Helper()
	paragraph := "这段转录用于校准内存回收旋钮：会话解码是内存旋钮真正作用的工作面。" +
		"段落里混排普通中文、`inline code`、与列表项：\n\n- 第一项：全量解码按行反序列化；\n" +
		"- 第二项：字符串字段占大头（literalStore 族）；\n- 第三项：回收策略按百分比缩放，无固定字节悬崖。\n\n"
	var b strings.Builder
	for i := 0; b.Len() < targetBytes; i++ {
		role := provider.RoleUser
		if i%2 == 1 {
			role = provider.RoleAssistant
		}
		content := fmt.Sprintf("（第 %d 轮）请分析下面的会话解码负载：\n\n%s", i, paragraph)
		m := provider.Message{Role: role, ID: fmt.Sprintf("bench-%06d", i), Content: content}
		if role == provider.RoleAssistant {
			m.ReasoningContent = strings.Repeat("推导：先量化分配率，再对比三档回收强度下的驻留与 GC 用时。", 4)
			m.RawContent = content + "\n（附完整原始块）" + paragraph
		}
		line, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal bench message: %v", err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	path := filepath.Join(t.TempDir(), "bench-session.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write bench transcript: %v", err)
	}
	return path
}

// TestGCBenchSmokeTL1286 仪器冒烟（常规包测试必跑）：合成 2MB 转录、真实解码
// 一轮、采样与指标装配各走一遍——构造失败一律 Fatal，不为省事 Skip。
func TestGCBenchSmokeTL1286(t *testing.T) {
	path := synthGCBenchTranscript(t, 2*1024*1024)
	hasher := newSessionTranscriptHasher()
	msgs, err := loadSessionMessagesFromJSONLContext(context.Background(), path, hasher)
	if err != nil {
		t.Fatalf("decode smoke transcript: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("smoke decode produced 0 messages")
	}
	ws, priv := sampleProcessMemory()
	if ws == 0 {
		t.Fatal("psapi GetProcessMemoryInfo returned a zero working set (sampling broken)")
	}
	_ = priv
	if processUptimeSeconds() <= 0 || processTotalCPUSeconds() <= 0 {
		t.Fatal("GetProcessTimes returned non-positive uptime/CPU (metric broken)")
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if ms.GCCPUFraction < 0 {
		t.Fatalf("negative GCCPUFraction: %v", ms.GCCPUFraction)
	}
}

// TestGCTierComparisonTL1286 三档对照入口：无 REASONIX_GC_BENCH_TIER 时做父进
// 程（fork 三档子进程并汇总），有则做子进程（跑单档解码窗口）。重测需显式
// REASONIX_GC_BENCH=1——这是仪器门控而非构造失败；默认路径的覆盖由冒烟件承担。
func TestGCTierComparisonTL1286(t *testing.T) {
	if tierStr := os.Getenv("REASONIX_GC_BENCH_TIER"); tierStr != "" {
		runGCBenchWindow(t, tierStr)
		return
	}
	if os.Getenv("REASONIX_GC_BENCH") != "1" {
		t.Skip("三档重测仪器（每档一个子进程 × 多轮全量解码，重）：设 REASONIX_GC_BENCH=1 运行；" +
			"仪器主逻辑默认覆盖见 TestGCBenchSmokeTL1286（有意门控，非构造失败）")
	}
	session := os.Getenv("REASONIX_GC_BENCH_SESSION")
	cycles := os.Getenv("REASONIX_GC_BENCH_CYCLES")
	var samples []gcBenchSample
	for _, tier := range strings.Split(gcBenchTiers, ",") {
		cmd := exec.Command(os.Args[0], "-test.run=TestGCTierComparisonTL1286", "-test.count=1", "-test.timeout=20m", "-test.v=true")
		cmd.Env = append(os.Environ(),
			"REASONIX_GC_BENCH=1",
			"REASONIX_GC_BENCH_TIER="+tier,
			"GOGC="+tier)
		if session != "" {
			cmd.Env = append(cmd.Env, "REASONIX_GC_BENCH_SESSION="+session)
		}
		if cycles != "" {
			cmd.Env = append(cmd.Env, "REASONIX_GC_BENCH_CYCLES="+cycles)
		}
		out, err := cmd.CombinedOutput()
		sample, parseErr := parseGCBenchResult(out)
		if err != nil || parseErr != nil {
			t.Fatalf("tier %s child failed: exec=%v parse=%v\nchild output:\n%s", tier, err, parseErr, out)
		}
		samples = append(samples, sample)
	}
	logGCBenchTable(t, samples)
}

// runGCBenchWindow 单档解码窗口：预热一轮（付首读页缓存与惰性初始化），随后
// 进入计量窗口连跑 cycles 轮全量解码。
func runGCBenchWindow(t *testing.T, tierStr string) {
	tier, err := strconv.Atoi(tierStr)
	if err != nil {
		t.Fatalf("bad tier %q: %v", tierStr, err)
	}
	path := os.Getenv("REASONIX_GC_BENCH_SESSION")
	source := "real-session"
	if path == "" {
		path = synthGCBenchTranscript(t, 64*1024*1024)
		source = "synthetic-64MB"
	}
	cycles := 6
	if v := os.Getenv("REASONIX_GC_BENCH_CYCLES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cycles = n
		}
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat bench source: %v", err)
	}
	sourceMB := float64(fi.Size()) / (1024 * 1024)

	// 预热一轮（不计量）：页缓存、字符串池、调度器都进稳态。
	warmHasher := newSessionTranscriptHasher()
	warmMsgs, err := loadSessionMessagesFromJSONLContext(context.Background(), path, warmHasher)
	if err != nil {
		t.Fatalf("warm-up decode: %v", err)
	}
	messagesPerRun := len(warmMsgs)
	if messagesPerRun == 0 {
		t.Fatal("bench source decoded 0 messages")
	}

	series := &gcRSSSeries{}
	stop := make(chan struct{})
	var samplerWG sync.WaitGroup
	samplerWG.Add(1)
	go func() {
		defer samplerWG.Done()
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				ws, priv := sampleProcessMemory()
				series.record(ws, priv)
			}
		}
	}()

	var ms1, ms2 runtime.MemStats
	runtime.ReadMemStats(&ms1)
	uptime1 := processUptimeSeconds()
	totalCPU1 := processTotalCPUSeconds()
	start := time.Now()

	var sink []provider.Message
	for i := 0; i < cycles; i++ {
		// 每轮新 hasher——与真实加载路径一致（每次调用各建一个）。
		msgs, err := loadSessionMessagesFromJSONLContext(context.Background(), path, newSessionTranscriptHasher())
		if err != nil {
			t.Fatalf("decode cycle %d: %v", i, err)
		}
		if len(msgs) == 0 {
			t.Fatalf("decode cycle %d produced 0 messages", i)
		}
		sink = msgs
	}
	if sink == nil {
		t.Fatal("no decode cycle produced messages")
	}
	runtime.KeepAlive(sink)

	wall := time.Since(start)
	runtime.ReadMemStats(&ms2)
	uptime2 := processUptimeSeconds()
	totalCPU2 := processTotalCPUSeconds()
	close(stop)
	samplerWG.Wait()

	procs := float64(runtime.GOMAXPROCS(0))
	gcCPUSec := procs * (ms2.GCCPUFraction*uptime2 - ms1.GCCPUFraction*uptime1)
	if gcCPUSec < 0 {
		gcCPUSec = 0
	}
	totalCPUSec := totalCPU2 - totalCPU1
	rssPeak, rssAvg, privPeak := series.snapshot()
	gcFracWindow := 0.0
	if totalCPUSec > 0 {
		gcFracWindow = gcCPUSec / totalCPUSec
	}
	sample := gcBenchSample{
		Tier:           tier,
		Cycles:         cycles,
		Source:         source,
		SourceMB:       sourceMB,
		MessagesPerRun: messagesPerRun,
		GOMAXPROCS:     runtime.GOMAXPROCS(0),
		RSSPeakMB:      rssPeak,
		RSSAvgMB:       rssAvg,
		PrivatePeakMB:  privPeak,
		HeapInuseEndMB: float64(ms2.HeapInuse) / (1024 * 1024),
		HeapSysEndMB:   float64(ms2.HeapSys) / (1024 * 1024),
		NumGC:          ms2.NumGC - ms1.NumGC,
		PauseTotalMs:   float64(ms2.PauseTotalNs-ms1.PauseTotalNs) / 1e6,
		GCCPUSec:       gcCPUSec,
		TotalCPUSec:    totalCPUSec,
		GCCPUFractionW: gcFracWindow,
		WallSec:        wall.Seconds(),
		ThroughputMBps: sourceMB * float64(cycles) / wall.Seconds(),
	}
	blob, err := json.Marshal(sample)
	if err != nil {
		t.Fatalf("marshal sample: %v", err)
	}
	t.Logf("%s%s", gcBenchResultPrefix, blob)
}

func parseGCBenchResult(out []byte) (gcBenchSample, error) {
	var sample gcBenchSample
	for _, line := range strings.Split(string(out), "\n") {
		if idx := strings.Index(line, gcBenchResultPrefix); idx >= 0 {
			blob := strings.TrimSpace(line[idx+len(gcBenchResultPrefix):])
			if err := json.Unmarshal([]byte(blob), &sample); err != nil {
				return sample, fmt.Errorf("parse result line: %w", err)
			}
			return sample, nil
		}
	}
	return sample, fmt.Errorf("no %q line in child output", gcBenchResultPrefix)
}

func logGCBenchTable(t *testing.T, samples []gcBenchSample) {
	if len(samples) == 0 {
		t.Fatal("no samples collected")
	}
	t.Logf("tl1286 GC 档位对照（负载=%s %.0fMB×%d 轮/档，GOMAXPROCS=%d；口径：RSS=WorkingSetSize，GC CPU=ΔGCCPUFraction×P×uptime）",
		samples[0].Source, samples[0].SourceMB, samples[0].Cycles, samples[0].GOMAXPROCS)
	t.Logf("%4s %10s %10s %12s %10s %9s %9s %8s %10s",
		"GOGC", "RSS峰(MB)", "RSS均(MB)", "Private峰", "GC CPU(s)", "Pause(ms)", "NumGC", "GC占比", "MB/s")
	for _, s := range samples {
		t.Logf("%4d %10.1f %10.1f %12.1f %10.2f %9.1f %9d %7.1f%% %10.1f",
			s.Tier, s.RSSPeakMB, s.RSSAvgMB, s.PrivatePeakMB, s.GCCPUSec,
			s.PauseTotalMs, s.NumGC, s.GCCPUFractionW*100, s.ThroughputMBps)
	}
}
