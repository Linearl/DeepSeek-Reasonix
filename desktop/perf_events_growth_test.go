package main

import (
	"strings"
	"testing"
	"time"
)

// 任务 373 R4 备料：增长速率追踪器的窗口语义。稀疏采样（间隔内的读数折叠进
// 最新点）、窗口剪枝（老点出局不留衰减尾巴）、最小跨度门——斜率必须建立在
// 不共享逐样本抖动、且都落在窗口内的读数上。
func TestEventsGrowthTrackerWindowSemantics(t *testing.T) {
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	var tr eventsGrowthTracker

	// 单点：窗口未成形，不开口。
	if _, _, _, ok := tr.observe(base, 100); ok {
		t.Fatal("single point must not report a rate")
	}
	// 间隔内的读数折叠进最新点：不新增条目，时间戳前移。
	if _, _, _, ok := tr.observe(base.Add(30*time.Second), 100.5); ok {
		t.Fatal("sub-spacing reading must not report a rate")
	}
	if len(tr.points) != 1 || tr.points[0].eventsMB != 100.5 {
		t.Fatalf("sub-spacing reading must fold into the newest point: %+v", tr.points)
	}
	// 跨度不足最小窗：仍不开口，但读数已记。
	if _, _, span, ok := tr.observe(base.Add(2*time.Minute), 101); ok || span != 90*time.Second {
		t.Fatalf("short window: ok=%v span=%v, want ok=false span=90s", ok, span)
	}
	// 跨度满 5 分钟：开口，斜率按首尾两点计（104-100.5=3.5MB over 5min = 42MB/h）。
	rate, net, span, ok := tr.observe(base.Add(5*time.Minute+30*time.Second), 104)
	if !ok || span != 5*time.Minute {
		t.Fatalf("mature window: ok=%v span=%v, want ok=true span=5m", ok, span)
	}
	if diff := rate - 42.0; diff > 0.01 || diff < -0.01 {
		t.Fatalf("rate = %.2f MB/h, want ~42", rate)
	}
	if diff := net - 3.5; diff > 0.001 || diff < -0.001 {
		t.Fatalf("net = %.2f MB, want 3.5", net)
	}
	// 窗口剪枝：停顿越过整窗后，窗内已无第二读数——宁可闭嘴也不拿窗外老点
	// 稀释斜率（读数仍记，速率随新点积累自然恢复）。
	if _, _, _, ok := tr.observe(base.Add(30*time.Minute), 110); ok {
		t.Fatal("after a window-long gap with no in-window history, no rate must be reported")
	}
	if len(tr.points) != 1 {
		t.Fatalf("stale points must be pruned: %+v", tr.points)
	}
	// 剪枝后重新积累：跨度与斜率只由窗内读数构成。
	if _, _, span, ok := tr.observe(base.Add(31*time.Minute), 111); ok || span != time.Minute {
		t.Fatalf("re-accumulating window: ok=%v span=%v, want ok=false span=1m", ok, span)
	}
	rate, _, span, ok = tr.observe(base.Add(40*time.Minute), 130)
	if !ok || span != 10*time.Minute || span > eventsGrowthWindow {
		t.Fatalf("window-bounded rate: ok=%v span=%v, want ok=true span=10m (<= window)", ok, span)
	}
	if diff := rate - 120.0; diff > 0.01 || diff < -0.01 {
		t.Fatalf("rate = %.2f MB/h, want ~120 ((130-110)MB over 10min)", rate)
	}
	// 回落（瘦身）不算增长：斜率为负。
	rate, net, _, ok = tr.observe(base.Add(55*time.Minute), 20)
	if !ok || rate >= 0 || net >= 0 {
		t.Fatalf("shrinking series: rate=%.1f net=%.1f, want negative both", rate, net)
	}
}

// 速率预警的三重防误报门与限频：斜率不足、净增长不足、窗口未满一律沉默；
// 达标首警后同斜率样本在冷却窗内被 perfWarnGate 抑制；告警带元凶文件与处置
// 通道（eventsMbContext），「在涨的是谁、下一步做什么」一条日志可读。
func TestWarnEventsGrowthThresholdsAndGate(t *testing.T) {
	buf := captureQuietChannelSlog(t)
	monitor := &perfMonitor{}
	monitor.eventsTopFile = func() (string, int64) { return `C:\projects\p\sessions\whale.events.jsonl`, 100 << 20 }

	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	sample := func(eventsMB float64) perfSample { return perfSample{EventsMB: eventsMB} }

	// 1) 窗口未满（跨度 2min < 5min 最小窗）：即使在读数上涨也不开口。
	monitor.warnEventsGrowth(sample(100), base)
	monitor.warnEventsGrowth(sample(102), base.Add(2*time.Minute))
	if out := buf.String(); strings.Contains(out, "eventsGrowthMbPerH") {
		t.Fatalf("short window must stay silent: %q", out)
	}
	// 2) 净增长不足（跨度已满 6min，但窗内净差 2MB < 5MB 下限）：沉默。
	monitor.warnEventsGrowth(sample(102), base.Add(6*time.Minute))
	if out := buf.String(); strings.Contains(out, "eventsGrowthMbPerH") {
		t.Fatalf("net below floor must stay silent: %q", out)
	}
	// 3) 达标：窗内首尾 100 -> 110，净增 10MB over 15min = 40MB/h——首警必发。
	monitor.warnEventsGrowth(sample(110), base.Add(15*time.Minute))
	out := buf.String()
	for _, want := range []string{
		"metric=eventsGrowthMbPerH",
		"netMb=",
		"windowMinutes=",
		"topPath=",
		"disposal=",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("growth warn missing %q in: %q", want, out)
		}
	}
	// 4) 冷却期内同斜率（36MB/h < 40*1.1 rearm 线）：perfWarnGate 抑制。
	monitor.warnEventsGrowth(sample(112), base.Add(20*time.Minute))
	if got := strings.Count(buf.String(), "metric=eventsGrowthMbPerH"); got != 1 {
		t.Fatalf("growth warn count = %d, want exactly 1 (cooldown suppression)", got)
	}
}

// 回落不预警：瘦身后 events 总量下降，斜率为负，即使幅度巨大也保持沉默。
func TestWarnEventsGrowthSilentOnShrink(t *testing.T) {
	buf := captureQuietChannelSlog(t)
	monitor := &perfMonitor{}
	monitor.eventsTopFile = func() (string, int64) { return "", 0 }

	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	monitor.warnEventsGrowth(perfSample{EventsMB: 1100}, base)
	monitor.warnEventsGrowth(perfSample{EventsMB: 1090}, base.Add(5*time.Minute))
	monitor.warnEventsGrowth(perfSample{EventsMB: 1000}, base.Add(15*time.Minute))
	if out := buf.String(); strings.Contains(out, "eventsGrowthMbPerH") {
		t.Fatalf("shrinking series must not warn: %q", out)
	}
}
