package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 任务 298 实测痛点：1.35GB events 下 eventsMb WARN 每 3s 刷屏。限频门的
// 契约：首次必发；平坦或微小增长（< rearm 比例）在冷却窗口内被抑制；
// 恶化（≥ rearm）立即再发；冷却期满定期重申。
func TestPerfWarnGateSuppressesFlatOverLimit(t *testing.T) {
	gate := newPerfWarnGate()
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	if ok, _, _ := gate.allow("eventsMb", 1351, base); !ok {
		t.Fatal("first over-limit sample must warn")
	}
	// 每 3s 一条的同值样本：全部抑制，抑制计数递增。
	for i := 1; i <= 3; i++ {
		ok, suppressed, _ := gate.allow("eventsMb", 1351, base.Add(time.Duration(3*i)*time.Second))
		if ok || suppressed != i {
			t.Fatalf("flat sample %d: ok=%v suppressed=%d, want suppressed warn with count=%d", i, ok, suppressed, i)
		}
	}
	// 抑制期间的小幅抖动（<1.1x）不算恶化。
	if ok, suppressed, _ := gate.allow("eventsMb", 1400, base.Add(15*time.Second)); ok {
		t.Fatal("sub-rearm jitter (1400 < 1351*1.1) must stay suppressed")
	} else if suppressed != 4 {
		t.Fatalf("jitter suppression count = %d, want 4", suppressed)
	}
}

func TestPerfWarnGateRearmsOnGrowthAndCooldown(t *testing.T) {
	gate := newPerfWarnGate()
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	if ok, _, _ := gate.allow("eventsMb", 1000, base); !ok {
		t.Fatal("first sample must warn")
	}
	if ok, _, _ := gate.allow("eventsMb", 1005, base.Add(3*time.Second)); ok {
		t.Fatal("flat sample must be suppressed")
	}
	// 恶化 ≥1.1x：冷却期内也必须立即再发，抑制数随行带出。
	ok, suppressed, _ := gate.allow("eventsMb", 1351, base.Add(6*time.Second))
	if !ok {
		t.Fatal("growth past rearm ratio must re-warn inside the cooldown window")
	}
	if suppressed != 1 {
		t.Fatalf("re-warn suppressed count = %d, want 1 (the one flat sample)", suppressed)
	}
	// 冷却期满：无恶化也重申。
	if ok, _, _ = gate.allow("eventsMb", 1351, base.Add(6*time.Second).Add(perfWarnCooldown)); !ok {
		t.Fatal("cooldown expiry must re-emit the warn")
	}
	// 不同 metric 互不串门。
	if ok, _, _ := gate.allow("storeMb", 9999, base); !ok {
		t.Fatal("a second metric's first sample must warn independently")
	}
}

// 阈值边界：恰好压线（512）不算超限（> 判定），越过 1MB 才告警；同值第二条
// 在限频下不再出现。eventsMb 的 warn 需要注入 top-file 探针以免测试扫真实
// 用户目录。
func TestPerfWarnThresholdEventsBoundaryAndSuppression(t *testing.T) {
	buf := captureQuietChannelSlog(t)
	monitor := &perfMonitor{}
	monitor.eventsTopFile = func() (string, int64) { return `C:\projects\p\sessions\whale.events.jsonl`, 1351 << 20 }

	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	monitor.warnThresholdsAt(perfSample{EventsMB: perfMonitorWarnEventsMB}, at)
	if out := buf.String(); strings.Contains(out, "eventsMb") {
		t.Fatalf("sample exactly at the limit must not warn: %q", out)
	}

	monitor.warnThresholdsAt(perfSample{EventsMB: perfMonitorWarnEventsMB + 1}, at)
	out := buf.String()
	for _, want := range []string{
		"metric=eventsMb",
		"topPath=",
		"topMB=1351",
		"disposal=",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("eventsMb warn missing %q in: %q", want, out)
		}
	}

	// 3s 后同值：抑制，不产生第二条。
	monitor.warnThresholdsAt(perfSample{EventsMB: perfMonitorWarnEventsMB + 1}, at.Add(3*time.Second))
	if got := strings.Count(buf.String(), "metric=eventsMb"); got != 1 {
		t.Fatalf("eventsMb warn count = %d, want exactly 1 (repeat suppressed)", got)
	}
}

// 元凶指认：多个 live events 文件取最大；.trash（任意深度）与 *-recovery-*
// 副本沿用 walkProjectsBytesMB 的 live-growth 规则，不得顶替元凶。
func TestTopEventsFileUnderPicksLargestLiveEventsLog(t *testing.T) {
	root := t.TempDir()
	write := func(rel string, size int) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("projects/p1/sessions/small.events.jsonl", 3<<20)
	write("projects/p2/sessions/whale.events.jsonl", 5<<20)
	write("projects/p2/sessions/note.jsonl", 9<<20) // 非 .events.jsonl：不计入
	write("projects/p2/sessions/.trash/dead.events.jsonl", 99<<20)
	write("projects/p2/sessions/whale-recovery-abcdef12.events.jsonl", 99<<20)

	topPath, topBytes := topEventsFileUnder(root)
	if topBytes != 5<<20 {
		t.Fatalf("topBytes = %d, want %d (the 5MiB live whale)", topBytes, int64(5<<20))
	}
	if filepath.Base(topPath) != "whale.events.jsonl" {
		t.Fatalf("topPath = %q, want the live whale (trash/recovery leaked in)", topPath)
	}
}

func TestTopEventsFileUnderEmptyRoot(t *testing.T) {
	if path, bytes := topEventsFileUnder(""); path != "" || bytes != 0 {
		t.Fatalf("empty root = (%q, %d), want empty", path, bytes)
	}
	if path, bytes := topEventsFileUnder(t.TempDir()); path != "" || bytes != 0 {
		t.Fatalf("root without events logs = (%q, %d), want empty", path, bytes)
	}
}
