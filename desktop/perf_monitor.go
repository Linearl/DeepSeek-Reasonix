package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strings"
	"sync"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
)

// Host performance monitor (task 184).
//
// Why: the 14.9 GB working-set report arrived as a single snapshot, so every
// candidate explanation had to be argued structurally rather than measured.
// This sampler turns the next occurrence into a time series — one JSON line per
// interval carrying the process counters, cumulative IO growth, the sizes of the
// session store's key files, and the session layer's resident operation
// bookkeeping (the remaining suspect for that growth).
//
// Off by default, and "off" is literal: with the switch off no ticker, goroutine
// or file handle exists.
const (
	perfMonitorDirName          = "perf"
	perfMonitorFilePrefix       = "perf-sample-"
	perfMonitorFileSuffix       = ".jsonl" // safe here: the session scanner only adopts *.jsonl inside session dirs
	perfMonitorDefaultSeconds   = 5
	perfMonitorMinSeconds       = 1
	perfMonitorMaxSeconds       = 300
	perfMonitorDefaultRetention = 168 // 7 days
	perfMonitorMaxKeyFiles      = 24
	perfMonitorMaxFileBytes     = 20 << 20
	// Heap profiles are the point of the memory investigation: one dump every
	// minute while the monitor runs, keeping only the newest few, so the profile
	// from the inflation moment is on disk without anyone having to act in time.
	// The interval is configurable (perf_monitor_heap_interval_seconds): each
	// dump is ~0.7MB of write churn, so 60s ≈ 40MB/h is the investigation tax —
	// the knob lets a long-running host pay less, not more. 0 (unset) keeps the
	// default; explicit values are clamped into 10..3600.
	perfMonitorHeapDefaultSeconds = 60
	perfMonitorHeapMinSeconds     = 10
	perfMonitorHeapMaxSeconds     = 3600
	perfMonitorHeapKept           = 3
	perfMonitorPruneInterval      = time.Hour
	// 任务 501: 两池分离。60s 定时池（heap-<ts>.pprof）滚动只留 3 份——高峰
	// 快照被后续滚动冲掉正是 499 内存膨胀调查丢掉 14.6GB 现场的直接原因，所以
	// 阈值高峰池用独立前缀 heap-high-，不进滚动、走 7 天保留期。
	perfMonitorHeapFilePrefix = "heap-"
	perfMonitorHeapHighPrefix = "heap-high-"
	// 任务 501: 防风暴门。1GB 一档，只有越过本进程见过的最高档才立即再抓；
	// 其余（同档维持、回落）走 30 分钟冷却——档边界震荡不能击穿冷却。
	perfMonitorHeapHighCooldown = 30 * time.Minute
	perfMonitorHeapHighTierMB   = 1024
)

// perfSample is one line of the time series. Field names are stable: the point
// of the file is to be read later by a person or a one-liner, not by this code.
type perfSample struct {
	TS              string  `json:"ts"`
	UptimeSeconds   float64 `json:"uptimeSeconds"`
	IntervalSeconds float64 `json:"intervalSeconds"`

	WorkingSetMB float64 `json:"workingSetMb,omitempty"`
	PrivateMB    float64 `json:"privateMb,omitempty"`
	HeapInuseMB  float64 `json:"heapInuseMb"`
	HeapSysMB    float64 `json:"heapSysMb"`
	Goroutines   int     `json:"goroutines"`
	Handles      uint32  `json:"handles,omitempty"`
	CPUPercent   float64 `json:"cpuPercent,omitempty"`
	// Task 196: the total alone cannot say whether the cores went to computation or to
	// syscalls/IO, and that is the difference between "cache the prompt" and "read less".
	KernelCPUPercent float64 `json:"kernelCpuPercent,omitempty"`
	UserCPUPercent   float64 `json:"userCpuPercent,omitempty"`
	// OSCounters is false when the platform reported nothing (see
	// perf_monitor_other.go): a zero must never be mistaken for a measurement.
	OSCounters bool `json:"osCounters"`

	// Task 182: the IO quartet dropped omitempty so the core counters appear
	// in EVERY line — a missing key reads as an absent measurement while a
	// zero (with osCounters) reads as a real one, and the series exists to
	// answer "what did the last interval cost".
	IOWriteMB      float64 `json:"ioWriteMb"`
	IOReadMB       float64 `json:"ioReadMb"`
	IOWriteDeltaMB float64 `json:"ioWriteDeltaMb"`
	IOReadDeltaMB  float64 `json:"ioReadDeltaMb"`
	// Threads (task 182) is the OS thread count beside handles: the leak
	// that is not memory (threads pinned by stuck syscalls/cgo).
	Threads uint32 `json:"threads"`

	// FilesKB is path -> KiB for the configured key-file table.
	FilesKB map[string]float64 `json:"filesKb,omitempty"`

	// Heartbeat marks a sample whose key numbers were unchanged: it keeps the
	// series alive without paying for the file table again (back-off heartbeat).
	Heartbeat bool `json:"heartbeat,omitempty"`

	// Byte totals of the two session roots, with the append-only event logs
	// broken out: those are the files a long session keeps rewriting, so their
	// growth is what a "disk is growing" report is actually about.
	StoreMB    float64 `json:"storeMb"`
	ProjectsMB float64 `json:"projectsMb"`
	EventsMB   float64 `json:"eventsMb"`

	// Open work surface: how many tabs exist and how many of them carry a v4
	// store, so a memory curve can be read against session count.
	TabsOpen         int `json:"tabsOpen"`
	ResidentSessions int `json:"residentSessions"`

	// V4 residency: the in-memory idempotency bookkeeping of the open v4 stores.
	V4SessionsOpen   int   `json:"v4SessionsOpen"`
	V4Operations     int   `json:"v4Operations"`
	V4OperationBytes int64 `json:"v4OperationBytes"`
}

type perfMonitor struct {
	app          *App
	dir          string
	interval     time.Duration
	retention    time.Duration
	heapInterval time.Duration
	paths        []string

	started time.Time
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once

	mu         sync.Mutex
	lastIO     procCounters
	lastSample time.Time
	lastCPU    float64
	lastKernel float64
	lastUser   float64
	// lastKey is the previous sample's key numbers: identical keys mean nothing
	// moved, which is what turns a sample into a heartbeat instead of a full line.
	lastKey string

	// 任务 298: 超限 WARN 限频门。1.35GB events 实测下 eventsMb WARN 每 3s 刷一条
	// （整个操作窗口被日志淹没，且不说是哪个文件的锅）。warnGate 抑制平坦的超限
	// 重复告警；eventsTopFile 可注入（测试用），nil 时走真实扫描。
	warnGate      *perfWarnGate
	eventsTopFile func() (string, int64)

	// 任务 501: 阈值高峰快照（experimental_heap_high_profile）。默认关；开着
	// 时 workingSetMb 或 heapInuseMb 达阈值即落一份 heap-high profile 进 7 天
	// 保留池。四个字段仅在采样循环 goroutine 上读写（sampleAndWrite 一条线），
	// 与 warnGate 同一纪律，不加锁。
	heapHighEnabled     bool
	heapHighThresholdMB float64
	// heapHighPeakTier 是本进程见过的最高 1GB 水位档（单调不降）：升到新高
	// 峰立即再抓，回落、维持一律走 30 分钟冷却——水位在档边界震荡不能击穿
	// 冷却（否则震荡波形每采样间隔抓一份，防风暴形同虚设）。
	heapHighPeakTier int64
	lastHeapHighAt   time.Time
}

// perfWarnGate 决定同一 metric 的超限 WARN 何时再发：首次必发；之后仅当
// 数值较上次告警增长 ≥ rearm 比例（恶化中的曲线不被抑制）或距上次告警超过
// cooldown（持续超限定期重申）才再发。被抑制的样本数随下次告警一并带出，
// 「抑制了多久、多少条」不再无据可查。
type perfWarnGate struct {
	cooldown time.Duration
	rearm    float64
	memo     map[string]perfWarnMemo
}

type perfWarnMemo struct {
	lastValue  float64
	lastAt     time.Time
	suppressed int
}

const (
	perfWarnCooldown        = 15 * time.Minute
	perfWarnRearmRatio      = 1.1
	perfWarnInitialMapSlots = 8
)

func newPerfWarnGate() *perfWarnGate {
	return &perfWarnGate{
		cooldown: perfWarnCooldown,
		rearm:    perfWarnRearmRatio,
		memo:     make(map[string]perfWarnMemo, perfWarnInitialMapSlots),
	}
}

// allow 报告 metric 在此刻是否应发告警。ok=false 时返回自上次告警以来的
// 抑制计数；ok=true 时 suppressed 为刚被吞掉的条数（随告警带出），sinceMinutes
// 是距上次告警的分钟数（首次为 0）。
func (g *perfWarnGate) allow(metric string, value float64, now time.Time) (ok bool, suppressed int, sinceMinutes float64) {
	memo, seen := g.memo[metric]
	if seen {
		suppressed = memo.suppressed
		sinceMinutes = now.Sub(memo.lastAt).Minutes()
		grown := value > memo.lastValue && value >= memo.lastValue*g.rearm
		if now.Sub(memo.lastAt) < g.cooldown && !grown {
			memo.suppressed++
			g.memo[metric] = memo
			return false, memo.suppressed, sinceMinutes
		}
	}
	g.memo[metric] = perfWarnMemo{lastValue: value, lastAt: now}
	return true, suppressed, sinceMinutes
}

// perfMonitorSettings resolves the config into sampler settings, clamping the
// same way the setters do so a hand-edited config cannot create a busy loop.
func perfMonitorSettings(cfg *config.Config) (time.Duration, time.Duration, time.Duration, []string) {
	seconds := perfMonitorDefaultSeconds
	retention := perfMonitorDefaultRetention
	heapSeconds := perfMonitorHeapDefaultSeconds
	var paths []string
	if cfg != nil {
		if cfg.Agent.PerfMonitorIntervalSeconds > 0 {
			seconds = cfg.Agent.PerfMonitorIntervalSeconds
		}
		if cfg.Agent.PerfMonitorRetentionHours > 0 {
			retention = cfg.Agent.PerfMonitorRetentionHours
		}
		if cfg.Agent.PerfMonitorHeapIntervalSeconds > 0 {
			heapSeconds = cfg.Agent.PerfMonitorHeapIntervalSeconds
		}
		paths = append(paths, cfg.Agent.PerfMonitorPaths...)
	}
	if seconds < perfMonitorMinSeconds {
		seconds = perfMonitorMinSeconds
	}
	if seconds > perfMonitorMaxSeconds {
		seconds = perfMonitorMaxSeconds
	}
	if heapSeconds < perfMonitorHeapMinSeconds {
		heapSeconds = perfMonitorHeapMinSeconds
	}
	if heapSeconds > perfMonitorHeapMaxSeconds {
		heapSeconds = perfMonitorHeapMaxSeconds
	}
	return time.Duration(seconds) * time.Second,
		time.Duration(retention) * time.Hour,
		time.Duration(heapSeconds) * time.Second,
		paths
}

// perfMonitorHeapHighSettings resolves the heap-high trigger (task 501):
// enabled mirrors the experimental switch (either face counts, same as the
// monitor switch), threshold falls back to the built-in 6GB and clamps
// explicit values into 1GB..128GB so a hand-edited config can neither arm a
// trigger that fires on every sample nor set a bar nothing reaches.
func perfMonitorHeapHighSettings(cfg *config.Config) (bool, float64) {
	enabled := false
	threshold := float64(config.PerfMonitorHeapHighDefaultMB)
	if cfg != nil {
		enabled = cfg.Agent.ExperimentalHeapHighProfile || cfg.Desktop.ExperimentalHeapHighProfile
		if cfg.Agent.PerfMonitorHeapHighThresholdMB > 0 {
			threshold = float64(cfg.Agent.PerfMonitorHeapHighThresholdMB)
		}
	}
	if threshold < config.PerfMonitorHeapHighMinThresholdMB {
		threshold = config.PerfMonitorHeapHighMinThresholdMB
	}
	if threshold > config.PerfMonitorHeapHighMaxThresholdMB {
		threshold = config.PerfMonitorHeapHighMaxThresholdMB
	}
	return enabled, threshold
}

// perfMonitorDir is where samples live: under the desktop logs, never inside a
// session directory (the session scanner adopts *.jsonl, so the suffix keeps
// these files out of it, and the directory keeps them out of the way).
func perfMonitorDir() string {
	return filepath.Join(config.MemoryUserDir(), desktopLogDirName, perfMonitorDirName)
}

func newPerfMonitor(app *App, dir string, interval, retention, heapInterval time.Duration, paths []string) *perfMonitor {
	return &perfMonitor{
		app:          app,
		dir:          dir,
		interval:     interval,
		retention:    retention,
		heapInterval: heapInterval,
		paths:        paths,
		started:      time.Now(),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}
}

// Start begins sampling. It samples once immediately so a short-lived run still
// produces a line, then on every interval.
func (m *perfMonitor) Start() {
	if m == nil {
		return
	}
	go m.loop()
}

func (m *perfMonitor) Stop() {
	if m == nil {
		return
	}
	m.once.Do(func() {
		close(m.stop)
		<-m.done
	})
}

func (m *perfMonitor) loop() {
	defer close(m.done)
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	prune := time.NewTicker(perfMonitorPruneInterval)
	defer prune.Stop()
	heap := time.NewTicker(m.heapInterval)
	defer heap.Stop()
	m.sampleAndWrite(time.Now())
	for {
		select {
		case <-m.stop:
			return
		case now := <-ticker.C:
			m.sampleAndWrite(now)
		case <-prune.C:
			m.prune(time.Now())
		case now := <-heap.C:
			m.writeHeapProfile(now)
		}
	}
}

func (m *perfMonitor) sampleAndWrite(now time.Time) {
	sample := m.takeSample(now)
	m.warnThresholds(sample)
	if err := m.appendSample(now, sample); err != nil {
		// The monitor must never be able to break the app it observes.
		slog.Warn("desktop: perf monitor write sample failed", "err", err)
	}
	m.maybeCaptureHeapHigh(sample, now)
}

// warnThresholds turns a sample that left the observed band into a log line, so
// the same curve is visible in desktop.log without opening the series.
// 任务 298: 同一 metric 平坦超限不再每 interval 刷屏（perfWarnGate 限频）；
// eventsMb 告警带元凶文件与处置通道（auto 旋转门状态），超限后「下一步做什么」
// 直接可读，而不是只剩一条孤立数字。
func (m *perfMonitor) warnThresholds(sample perfSample) {
	m.warnThresholdsAt(sample, time.Now())
}

func (m *perfMonitor) warnThresholdsAt(sample perfSample, now time.Time) {
	if m.warnGate == nil {
		m.warnGate = newPerfWarnGate()
	}
	warn := func(metric string, value, limit float64) {
		ok, suppressed, sinceMinutes := m.warnGate.allow(metric, value, now)
		if !ok {
			return
		}
		fields := []any{"metric", metric, "value", value, "limit", limit}
		if suppressed > 0 {
			fields = append(fields, "suppressed", suppressed, "sinceMinutes", int64(sinceMinutes))
		}
		if metric == "eventsMb" {
			fields = append(fields, m.eventsMbContext()...)
		}
		slog.Warn("desktop: perf monitor threshold", fields...)
	}
	if sample.OSCounters && sample.WorkingSetMB > perfMonitorWarnWorkingSetMB {
		warn("workingSetMb", sample.WorkingSetMB, perfMonitorWarnWorkingSetMB)
	}
	if sample.EventsMB > perfMonitorWarnEventsMB {
		warn("eventsMb", sample.EventsMB, perfMonitorWarnEventsMB)
	}
	if sample.StoreMB > perfMonitorWarnStoreMB {
		warn("storeMb", sample.StoreMB, perfMonitorWarnStoreMB)
	}
	if float64(sample.V4OperationBytes)/(1<<20) > perfMonitorWarnV4OpsMB {
		warn("v4OperationMb", float64(sample.V4OperationBytes)/(1<<20), perfMonitorWarnV4OpsMB)
	}
}

// eventsMbContext 组装 eventsMb 超限告警的追加字段：最大的 live events 文件
// （元凶）与当前处置通道状态（auto 旋转门）。扫描仅在告警真正发出时发生，
// 常态采样零额外开销。
func (m *perfMonitor) eventsMbContext() []any {
	fields := []any{}
	top := m.eventsTopFile
	if top == nil {
		top = func() (string, int64) { return topEventsFileUnder(config.MemoryUserDir()) }
	}
	if path, bytes := top(); path != "" {
		fields = append(fields, "topPath", path, "topMB", float64(bytes)/(1<<20))
	}
	mode, factor, capMB := agent.EventsAutoRotationSnapshot()
	if mode == config.EventsAutoRotationAuto {
		fields = append(fields, "disposal", fmt.Sprintf("events_auto_rotation=auto (factor %.1f, cap %d MiB) — the save-path gate compacts over-limit logs", factor, capMB))
	} else {
		fields = append(fields, "disposal", fmt.Sprintf("events_auto_rotation=%s — no automatic compaction; run the storage panel's per-session/all repair, or set events_auto_rotation=auto", mode))
	}
	return fields
}

// topEventsFileUnder walks the memory root once and reports the largest live
// events log (path, bytes) — the same live-growth rules as walkProjectsBytesMB
// (.trash skipped at any depth, *-recovery-* siblings excluded, .events.jsonl
// only). 1.35GB 超限（任务 298）下告警必须能指认是哪个会话的文件在涨。
func topEventsFileUnder(memoryRoot string) (string, int64) {
	if memoryRoot == "" {
		return "", 0
	}
	root := filepath.Join(memoryRoot, "projects")
	var topPath string
	var topBytes int64
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == ".trash" {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".events.jsonl") || strings.Contains(name, "-recovery-") {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return nil
		}
		if info.Size() > topBytes {
			topBytes = info.Size()
			topPath = path
		}
		return nil
	})
	return topPath, topBytes
}

func (m *perfMonitor) takeSample(now time.Time) perfSample {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	counters := readProcCounters()

	sample := perfSample{
		TS:              now.UTC().Format(time.RFC3339Nano),
		UptimeSeconds:   now.Sub(m.started).Seconds(),
		IntervalSeconds: m.interval.Seconds(),
		HeapInuseMB:     float64(mem.HeapInuse) / (1 << 20),
		HeapSysMB:       float64(mem.Sys) / (1 << 20),
		Goroutines:      runtime.NumGoroutine(),
		OSCounters:      counters.Available,
	}
	if counters.Available {
		sample.WorkingSetMB = float64(counters.WorkingSetBytes) / (1 << 20)
		sample.PrivateMB = float64(counters.PrivateBytes) / (1 << 20)
		sample.Handles = counters.Handles
		sample.Threads = counters.Threads
		sample.IOWriteMB = float64(counters.WriteBytes) / (1 << 20)
		sample.IOReadMB = float64(counters.ReadBytes) / (1 << 20)
	}

	m.mu.Lock()
	previous, previousAt, previousCPU := m.lastIO, m.lastSample, m.lastCPU
	m.lastIO, m.lastSample, m.lastCPU = counters, now, counters.CPUSeconds
	m.lastKernel, m.lastUser = counters.KernelSeconds, counters.UserSeconds
	m.mu.Unlock()

	if counters.Available && !previousAt.IsZero() {
		elapsed := now.Sub(previousAt).Seconds()
		sample.IOWriteDeltaMB = float64(counters.WriteBytes-previous.WriteBytes) / (1 << 20)
		sample.IOReadDeltaMB = float64(counters.ReadBytes-previous.ReadBytes) / (1 << 20)
		if elapsed > 0 {
			sample.CPUPercent = (counters.CPUSeconds - previousCPU) / elapsed * 100
			sample.KernelCPUPercent = (counters.KernelSeconds - m.lastKernel) / elapsed * 100
			sample.UserCPUPercent = (counters.UserSeconds - m.lastUser) / elapsed * 100
			if sample.KernelCPUPercent < 0 {
				sample.KernelCPUPercent = 0
			}
			if sample.UserCPUPercent < 0 {
				sample.UserCPUPercent = 0
			}
			if sample.CPUPercent < 0 {
				sample.CPUPercent = 0
			}
		}
	}

	storeMB, projectsMB, eventsMB := m.collectTotals()
	sample.StoreMB, sample.ProjectsMB, sample.EventsMB = storeMB, projectsMB, eventsMB
	tabs, resident, operations, operationBytes := m.v4Residency()
	sample.TabsOpen, sample.ResidentSessions = tabs, resident
	sample.V4SessionsOpen, sample.V4Operations, sample.V4OperationBytes = resident, operations, operationBytes

	// Back-off heartbeat: when nothing moved (byte totals, session counts, RSS to
	// the megabyte) the file table would repeat itself, so the line keeps the
	// series alive without the walk.
	key := fmt.Sprintf("%.0f|%.0f|%.0f|%d|%d|%d|%.0f",
		storeMB, projectsMB, eventsMB, tabs, resident, operationBytes, sample.WorkingSetMB)
	m.mu.Lock()
	unchanged := m.lastKey == key && m.lastKey != ""
	m.lastKey = key
	m.mu.Unlock()
	if unchanged {
		return perfSample{
			TS:               sample.TS,
			UptimeSeconds:    sample.UptimeSeconds,
			IntervalSeconds:  sample.IntervalSeconds,
			Heartbeat:        true,
			WorkingSetMB:     sample.WorkingSetMB,
			PrivateMB:        sample.PrivateMB,
			HeapInuseMB:      sample.HeapInuseMB,
			HeapSysMB:        sample.HeapSysMB,
			Goroutines:       sample.Goroutines,
			Handles:          sample.Handles,
			CPUPercent:       sample.CPUPercent,
			KernelCPUPercent: sample.KernelCPUPercent,
			UserCPUPercent:   sample.UserCPUPercent,
			OSCounters:       sample.OSCounters,
			StoreMB:          storeMB,
			ProjectsMB:       projectsMB,
			EventsMB:         eventsMB,
			TabsOpen:         tabs,
			ResidentSessions: resident,
			V4SessionsOpen:   resident,
			V4Operations:     operations,
			V4OperationBytes: operationBytes,
		}
	}
	sample.FilesKB = m.collectFiles()
	return sample
}

// Thresholds come from the 2026-09-19 memory investigation: a 14.9 GB working set
// with a 256 MiB bolt cache and a 5.4x events/jsonl ratio. They are deliberately
// generous — the point is to catch a curve leaving the observed band, not to nag.
const (
	perfMonitorWarnWorkingSetMB = 4096
	perfMonitorWarnEventsMB     = 512
	perfMonitorWarnStoreMB      = 2048
	perfMonitorWarnV4OpsMB      = 256
)

// collectTotals walks the two session roots once per sample. The event logs are
// counted separately because they are the append-only files that grow with every
// turn.
func (m *perfMonitor) collectTotals() (storeMB, projectsMB, eventsMB float64) {
	storeMB = walkBytesMB(config.SessionStoreDir())
	projectsMB, eventsMB = walkProjectsBytesMB(config.MemoryUserDir())
	return storeMB, projectsMB, eventsMB
}

// walkBytesMB sums every regular file under root. Errors are ignored on purpose:
// a stat race must not silence a sample.
func walkBytesMB(root string) float64 {
	if root == "" {
		return 0
	}
	var total int64
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() {
			return nil
		}
		if info, statErr := entry.Info(); statErr == nil {
			total += info.Size()
		}
		return nil
	})
	return float64(total) / (1 << 20)
}

// walkProjectsBytesMB sums the per-project session roots and, separately, the
// append-only event logs inside them. Directories named .trash are skipped at
// any depth: they are storage the user chose to keep, not live growth, and
// counting them made the events alert fire on dead weight (measured: two
// deleted sessions under sessions/.trash pushed it past 2 GB).
// Task 239: recovery copies are sibling files named *-recovery-*.events.jsonl
// (see recovery_isolated.go stableRecoverySessionPath) — safety snapshots, not
// live growth. They must be filtered by filename, not by a "recovery" directory
// name (there is no such directory).
func walkProjectsBytesMB(memoryRoot string) (totalMB float64, eventsMB float64) {
	if memoryRoot == "" {
		return 0, 0
	}
	root := filepath.Join(memoryRoot, "projects")
	var total, events int64
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == ".trash" {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		// Recovery copies live next to their originals as *-recovery-*.jsonl;
		// skip them so eventsMb reflects live growth only.
		if strings.Contains(name, "-recovery-") {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return nil
		}
		total += info.Size()
		if strings.HasSuffix(name, ".events.jsonl") {
			events += info.Size()
		}
		return nil
	})
	return float64(total) / (1 << 20), float64(events) / (1 << 20)
}

// collectFiles expands the configured globs and keeps the biggest/most recently
// written files, so a long session directory cannot turn one sample into a full
// walk of the state root.
func (m *perfMonitor) collectFiles() map[string]float64 {
	patterns := m.paths
	if len(patterns) == 0 {
		patterns = defaultPerfMonitorPaths()
	}
	type entry struct {
		path string
		size int64
		mod  time.Time
	}
	var entries []entry
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, path := range matches {
			info, statErr := os.Stat(path)
			if statErr != nil || info.IsDir() {
				continue
			}
			entries = append(entries, entry{path: path, size: info.Size(), mod: info.ModTime()})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].mod.After(entries[j].mod) })
	if len(entries) > perfMonitorMaxKeyFiles {
		entries = entries[:perfMonitorMaxKeyFiles]
	}
	out := make(map[string]float64, len(entries))
	for _, e := range entries {
		out[e.path] = float64(e.size) / 1024
	}
	return out
}

// defaultPerfMonitorPaths covers the four families this monitor exists for: the
// v4 recovery caches (one bolt per bridge session), the desktop log, and the
// transcripts a live session keeps rewriting.
func defaultPerfMonitorPaths() []string {
	memoryRoot := config.MemoryUserDir()
	return []string{
		filepath.Join(config.SessionStoreDir(), ".recovery-cache", "*", "recovery-v1.bolt"),
		filepath.Join(memoryRoot, desktopLogDirName, desktopLogSubDir, "*.log"),
		filepath.Join(memoryRoot, "projects", "*", "sessions", "*.jsonl"),
		filepath.Join(memoryRoot, "projects", "*", "sessions", "*.events.jsonl"),
		filepath.Join(memoryRoot, "projects", "*", "sessions", "*.turns.jsonl"),
	}
}

// v4Residency sums the resident operation bookkeeping of every open v4 store.
// It rides the existing controller accessors, so it adds no new lifetime rules.
func (m *perfMonitor) v4Residency() (tabs int, sessions int, operations int, bytes int64) {
	if m == nil || m.app == nil {
		return 0, 0, 0, 0
	}
	m.app.mu.Lock()
	tabList := make([]*WorkspaceTab, 0, len(m.app.tabs))
	for _, tab := range m.app.tabs {
		tabList = append(tabList, tab)
	}
	for _, tab := range m.app.detachedSessions {
		tabList = append(tabList, tab)
	}
	m.app.mu.Unlock()

	openTabs := len(tabList)
	seen := map[string]bool{}
	for _, tab := range tabList {
		if tab == nil {
			continue
		}
		ctrl, ok := m.app.controllerForTab(tab).(*control.Controller)
		if !ok || ctrl == nil {
			continue
		}
		bridge := ctrl.SessionV4()
		if bridge == nil {
			continue
		}
		service := bridge.Service()
		if service == nil {
			continue
		}
		for _, entry := range service.OperationResidency() {
			if entry.SessionID != "" {
				if seen[entry.SessionID] {
					continue
				}
				seen[entry.SessionID] = true
			}
			sessions++
			operations += entry.Operations
			bytes += entry.ApproxBytes
		}
	}
	return openTabs, sessions, operations, bytes
}

func (m *perfMonitor) dayPath(now time.Time) string {
	return filepath.Join(m.dir, perfMonitorFilePrefix+now.Format("20060102")+perfMonitorFileSuffix)
}

func (m *perfMonitor) appendSample(now time.Time, sample perfSample) error {
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return err
	}
	payload, err := json.Marshal(sample)
	if err != nil {
		return err
	}
	path := m.dayPath(now)
	if info, statErr := os.Stat(path); statErr == nil && info.Size() > perfMonitorMaxFileBytes {
		// One backup keeps the ceiling honest: samples near the limit stay
		// readable if the app then idles for hours.
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(payload, '\n'))
	return err
}

// dumpHeapProfile writes a Go heap profile to path (creating the directory as
// needed). Errors go to the caller: an observer must never take the app down
// with it, but the two pools log differently so a failure says which pool lost
// a dump.
func dumpHeapProfile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := pprof.WriteHeapProfile(f); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// writeHeapProfile dumps a Go heap profile next to the samples and keeps only the
// newest perfMonitorHeapKept of them: the goal is the profile taken while memory
// was inflated, not a history of profiles. 任务 501: 本函数只管 60s 定时池，滚动
// 不触碰 heap-high- 高峰池（见 pruneHeapProfiles）。
func (m *perfMonitor) writeHeapProfile(now time.Time) string {
	if m == nil {
		return ""
	}
	path := filepath.Join(m.dir, perfMonitorHeapFilePrefix+now.Format("20060102-150405")+".pprof")
	if err := dumpHeapProfile(path); err != nil {
		slog.Warn("desktop: perf monitor heap profile", "err", err)
		return ""
	}
	m.pruneHeapProfiles()
	return path
}

// writeHeapHighProfile 落阈值高峰池快照（任务 501）：文件名带触发时的 MB 读数，
// 量级不用打开文件就能辨认；只写文件，不做任何滚动——本池走 prune 的 7 天
// 保留期，当天多份快照全部留到超期。
func (m *perfMonitor) writeHeapHighProfile(now time.Time, levelMB float64) string {
	if m == nil {
		return ""
	}
	path := filepath.Join(m.dir, fmt.Sprintf("%s%dMB-%s.pprof",
		perfMonitorHeapHighPrefix, int64(levelMB), now.Format("20060102-150405")))
	if err := dumpHeapProfile(path); err != nil {
		slog.Warn("desktop: perf monitor heap-high profile", "err", err)
		return ""
	}
	return path
}

// maybeCaptureHeapHigh 是采样循环里的高峰快照分支（任务 501）。开关关着时第
// 一行返回，采样循环逐字节保持原行为；开着时 workingSetMb / heapInuseMb 取
// 大者达阈值即落盘（OS 计数不可用时 WorkingSetMB 为 0，此时只看堆）。防风暴：
// 1GB 一档，只有越过本进程见过的最高水位档才立即再抓（新高峰 = 新现场）；
// 同档维持、回落到旧档一律 30 分钟冷却——档边界震荡不能把冷却击穿。仅在
// 采样循环 goroutine 上调用（档位状态不加锁，与 warnGate 同一纪律）。
func (m *perfMonitor) maybeCaptureHeapHigh(sample perfSample, now time.Time) {
	if m == nil || !m.heapHighEnabled {
		return
	}
	level := sample.WorkingSetMB
	if sample.HeapInuseMB > level {
		level = sample.HeapInuseMB
	}
	if level < m.heapHighThresholdMB {
		return
	}
	tier := int64(level) / perfMonitorHeapHighTierMB
	if tier <= m.heapHighPeakTier && now.Sub(m.lastHeapHighAt) < perfMonitorHeapHighCooldown {
		return
	}
	if tier > m.heapHighPeakTier {
		m.heapHighPeakTier = tier
	}
	m.lastHeapHighAt = now
	path := m.writeHeapHighProfile(now, level)
	if path == "" {
		return
	}
	slog.Warn("desktop: perf monitor heap-high captured",
		"path", path,
		"workingSetMb", sample.WorkingSetMB,
		"heapInuseMb", sample.HeapInuseMB,
		"thresholdMb", int64(m.heapHighThresholdMB),
		"tierMb", tier*perfMonitorHeapHighTierMB)
}

// pruneHeapProfiles 只滚动 60s 定时池（heap-<ts>.pprof，保留最新 3 份）。
// heap-high- 前缀的高峰池不在此列——高峰现场被后续滚动冲掉正是本池存在的
// 动机；它的生命周期归 prune 的保留期管（任务 501 两池分离）。
func (m *perfMonitor) pruneHeapProfiles() {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, perfMonitorHeapHighPrefix) {
			continue
		}
		if strings.HasPrefix(name, perfMonitorHeapFilePrefix) && strings.HasSuffix(name, ".pprof") {
			names = append(names, name)
		}
	}
	// The timestamp is in the name, so lexical order is chronological order.
	sort.Strings(names)
	for len(names) > perfMonitorHeapKept {
		_ = os.Remove(filepath.Join(m.dir, names[0]))
		names = names[1:]
	}
}

// SaveHeapProfile writes a heap profile on demand and returns its path. It works
// whether or not the sampler is running: capturing the heap of a process that has
// already inflated must never require the monitor to be switched on first.
func (a *App) SaveHeapProfile() (string, error) {
	helper := &perfMonitor{dir: perfMonitorDir()}
	path := helper.writeHeapProfile(time.Now())
	if path == "" {
		return "", fmt.Errorf("heap profile could not be written under %s", perfMonitorDir())
	}
	return path, nil
}

// prune drops sample files older than the retention window; the monitor must not
// become the growth it was built to find. 任务 501: heap-high- 高峰池同走本
// 循环，按 mtime 对齐 perf-sample 的保留期（同一 retention 配置，默认 7 天）
// ——只删超期者，同日多份快照全部保留。
func (m *perfMonitor) prune(now time.Time) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return
	}
	cutoff := now.Add(-m.retention)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			continue
		}
		isSample := strings.HasPrefix(name, perfMonitorFilePrefix)
		isHeapHigh := strings.HasPrefix(name, perfMonitorHeapHighPrefix) && strings.HasSuffix(name, ".pprof")
		if !isSample && !isHeapHigh {
			continue
		}
		if info, statErr := entry.Info(); statErr == nil && info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(m.dir, name))
	}
}
