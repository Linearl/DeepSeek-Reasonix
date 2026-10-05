package main

// 任务 501 验收：阈值触发的 heap profile 落盘（实验特性，默认关）。
// ① 开开关+顶阈值 → heap-high 文件生成且 60s 滚动不删它；
// ② 同日多份阈值快照全部保留至保留期（只删超期者）；
// ③ 关开关 → 逐字节零行为（连目录都不建）；
// ④ 同档 30 分钟冷却生效（不重复抓），新高水位档立即再抓；
// ⑤ 设置解析（默认 6GB、钳制、双面开关）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/config"
)

func heapHighFiles(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, perfMonitorHeapHighPrefix+"*.pprof"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func periodicHeapFiles(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, perfMonitorHeapFilePrefix+"*.pprof"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, path := range matches {
		if strings.HasPrefix(filepath.Base(path), perfMonitorHeapHighPrefix) {
			continue
		}
		out = append(out, path)
	}
	return out
}

// 验收 ①（前半）：开关开 + 顶阈值 → 立即落一份 heap-high-<MB>MB-<ts>.pprof，
// 文件名带触发时的 MB 读数。
func TestHeapHighCapturesWhenThresholdCrossed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "perf")
	monitor := &perfMonitor{dir: dir, heapHighEnabled: true, heapHighThresholdMB: 6144}

	monitor.maybeCaptureHeapHigh(perfSample{WorkingSetMB: 7000, HeapInuseMB: 3000},
		time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC))

	files := heapHighFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("heap-high files = %v, want exactly one", files)
	}
	if !strings.Contains(filepath.Base(files[0]), "heap-high-7000MB-20261005-120000") {
		t.Fatalf("file name %q must carry the MB reading and the timestamp", files[0])
	}
}

// 验收 ①（后半）：heap-high 快照不进 60s 定时池的 3 份滚动——多轮定时落盘
// 之后高峰快照仍在，定时池收敛到 3 份。
func TestHeapHighSnapshotSurvivesPeriodicRolling(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "perf")
	monitor := &perfMonitor{dir: dir, heapHighEnabled: true, heapHighThresholdMB: 6144}

	monitor.maybeCaptureHeapHigh(perfSample{WorkingSetMB: 7000},
		time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC))
	if got := len(heapHighFiles(t, dir)); got != 1 {
		t.Fatalf("heap-high files = %d, want 1", got)
	}
	// 模拟 5 轮 60s 定时落盘（每轮内部即执行滚动清理）。
	base := time.Date(2026, 10, 5, 12, 1, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		if path := monitor.writeHeapProfile(base.Add(time.Duration(i) * time.Minute)); path == "" {
			t.Fatalf("periodic heap dump %d failed", i)
		}
	}
	if got := len(periodicHeapFiles(t, dir)); got != perfMonitorHeapKept {
		t.Fatalf("periodic pool = %d files, want %d (rolling 3)", got, perfMonitorHeapKept)
	}
	if got := len(heapHighFiles(t, dir)); got != 1 {
		t.Fatalf("heap-high snapshot was rotated away by the periodic pool: %d left", got)
	}
}

// 验收 ②：同日多份 heap-high 快照在保留期内全部保留，只有 mtime 超期者被
// prune 删除（对齐 perf-sample 的同一保留期）。
func TestPruneKeepsFreshHeapHighAndDropsExpired(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "perf")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	fresh := []string{
		perfMonitorHeapHighPrefix + "7000MB-20261005-120000.pprof",
		perfMonitorHeapHighPrefix + "7100MB-20261005-123000.pprof",
		perfMonitorHeapHighPrefix + "7200MB-20261005-130000.pprof",
	}
	stale := perfMonitorHeapHighPrefix + "8300MB-20260927-090000.pprof"
	for _, name := range append(append([]string{}, fresh...), stale) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	past := base.Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, stale), past, past); err != nil {
		t.Fatal(err)
	}
	// 同一循环继续管 perf-sample：保留期内的新样本不能被误伤。
	sample := perfMonitorFilePrefix + "20261005" + perfMonitorFileSuffix
	if err := os.WriteFile(filepath.Join(dir, sample), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	monitor := &perfMonitor{dir: dir, retention: 7 * 24 * time.Hour}
	monitor.prune(base)

	if _, err := os.Stat(filepath.Join(dir, stale)); !os.IsNotExist(err) {
		t.Fatalf("expired heap-high survived retention: err=%v", err)
	}
	for _, name := range fresh {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("fresh heap-high %s was pruned: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, sample)); err != nil {
		t.Fatalf("fresh perf sample was pruned: %v", err)
	}
}

// 验收 ③：开关关 → 逐字节零行为。顶到天上去的采样值也不落盘、不建目录。
func TestHeapHighDisabledIsZeroBehavior(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "perf")
	monitor := &perfMonitor{dir: dir, heapHighEnabled: false, heapHighThresholdMB: 6144}

	monitor.maybeCaptureHeapHigh(perfSample{WorkingSetMB: 14900, HeapInuseMB: 9000},
		time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC))

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("perf directory created with the switch off: err=%v", err)
	}
	// 低于阈值同样零行为（开关开着时）。
	monitorOn := &perfMonitor{dir: dir, heapHighEnabled: true, heapHighThresholdMB: 6144}
	monitorOn.maybeCaptureHeapHigh(perfSample{WorkingSetMB: 6143.9}, time.Now())
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("below-threshold sample should not create anything: err=%v", err)
	}
}

// 验收 ④：同档 30 分钟冷却生效；升到新高水位档立即再抓；回落到旧档不触发
// （档边界震荡不能击穿冷却）。
func TestHeapHighCooldownAndTierEscalation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "perf")
	monitor := &perfMonitor{dir: dir, heapHighEnabled: true, heapHighThresholdMB: 6144}
	base := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

	// 首次顶阈值（tier 6）→ 抓。
	monitor.maybeCaptureHeapHigh(perfSample{WorkingSetMB: 6200}, base)
	if got := len(heapHighFiles(t, dir)); got != 1 {
		t.Fatalf("after first crossing: %d files, want 1", got)
	}
	// 同档 10 分钟后 → 冷却，不重复抓。
	monitor.maybeCaptureHeapHigh(perfSample{WorkingSetMB: 6250}, base.Add(10*time.Minute))
	if got := len(heapHighFiles(t, dir)); got != 1 {
		t.Fatalf("same-tier within cooldown captured again: %d files, want 1", got)
	}
	// 升到 tier 8（新高水位）→ 即使在冷却窗口内也立即抓。
	monitor.maybeCaptureHeapHigh(perfSample{WorkingSetMB: 8100}, base.Add(20*time.Minute))
	if got := len(heapHighFiles(t, dir)); got != 2 {
		t.Fatalf("new peak tier did not capture immediately: %d files, want 2", got)
	}
	// 回落到旧档 tier 6（仍在冷却窗口）→ 不抓：震荡不能击穿冷却。
	monitor.maybeCaptureHeapHigh(perfSample{WorkingSetMB: 6300}, base.Add(25*time.Minute))
	if got := len(heapHighFiles(t, dir)); got != 2 {
		t.Fatalf("fall-back tier captured inside cooldown: %d files, want 2", got)
	}
	// 同档（tier 8 以下）冷却期满 → 再抓一份（持续高水位定期补照）。
	monitor.maybeCaptureHeapHigh(perfSample{WorkingSetMB: 8200}, base.Add(55*time.Minute))
	if got := len(heapHighFiles(t, dir)); got != 3 {
		t.Fatalf("cooldown expiry did not capture: %d files, want 3", got)
	}
}

// 设置解析：默认关 + 默认 6GB；双面开关（Agent/Desktop）任一为开即开；
// 阈值 0 回落默认，显式值钳进 1GB..128GB。
func TestPerfMonitorHeapHighSettings(t *testing.T) {
	if enabled, threshold := perfMonitorHeapHighSettings(nil); enabled || threshold != config.PerfMonitorHeapHighDefaultMB {
		t.Fatalf("nil config = (%v, %v), want (false, %v)", enabled, threshold, config.PerfMonitorHeapHighDefaultMB)
	}
	off := &config.Config{}
	if enabled, _ := perfMonitorHeapHighSettings(off); enabled {
		t.Fatal("default config must leave the heap-high switch off")
	}
	agentOn := &config.Config{}
	agentOn.Agent.ExperimentalHeapHighProfile = true
	if enabled, _ := perfMonitorHeapHighSettings(agentOn); !enabled {
		t.Fatal("agent-side switch must enable")
	}
	desktopOn := &config.Config{}
	desktopOn.Desktop.ExperimentalHeapHighProfile = true
	if enabled, _ := perfMonitorHeapHighSettings(desktopOn); !enabled {
		t.Fatal("desktop-side switch must enable")
	}
	thresholdCases := []struct {
		raw  int
		want float64
	}{
		{0, config.PerfMonitorHeapHighDefaultMB},
		{500, config.PerfMonitorHeapHighMinThresholdMB},
		{999999, config.PerfMonitorHeapHighMaxThresholdMB},
		{8192, 8192},
	}
	for _, tc := range thresholdCases {
		cfg := &config.Config{}
		cfg.Agent.PerfMonitorHeapHighThresholdMB = tc.raw
		if _, got := perfMonitorHeapHighSettings(cfg); got != tc.want {
			t.Fatalf("threshold %d resolved to %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// setter 钳制：与解析器同一套边界，手改配置经 setter 落盘时不可能越界。
func TestSetPerfMonitorHeapHighThresholdClamps(t *testing.T) {
	cfg := &config.Config{}
	if err := cfg.SetPerfMonitorHeapHighThresholdMB(0); err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.PerfMonitorHeapHighThresholdMB != 0 {
		t.Fatalf("0 must be stored as-is (built-in default), got %d", cfg.Agent.PerfMonitorHeapHighThresholdMB)
	}
	if err := cfg.SetPerfMonitorHeapHighThresholdMB(100); err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.PerfMonitorHeapHighThresholdMB != config.PerfMonitorHeapHighMinThresholdMB {
		t.Fatalf("below-minimum clamped to %d, got %d", config.PerfMonitorHeapHighMinThresholdMB, cfg.Agent.PerfMonitorHeapHighThresholdMB)
	}
	if err := cfg.SetPerfMonitorHeapHighThresholdMB(1 << 30); err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.PerfMonitorHeapHighThresholdMB != config.PerfMonitorHeapHighMaxThresholdMB {
		t.Fatalf("above-maximum clamped to %d, got %d", config.PerfMonitorHeapHighMaxThresholdMB, cfg.Agent.PerfMonitorHeapHighThresholdMB)
	}
	if err := cfg.SetExperimentalHeapHighProfile(true); err != nil {
		t.Fatal(err)
	}
	if !cfg.Agent.ExperimentalHeapHighProfile || !cfg.Desktop.ExperimentalHeapHighProfile {
		t.Fatal("heap-high setter must write both faces (agent + settings-view mirror)")
	}
}
