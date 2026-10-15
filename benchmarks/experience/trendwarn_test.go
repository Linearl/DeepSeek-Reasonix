package main

// 任务 414 阶段 4 单测：WARN 巡检（采集/匹配/判定）与周趋势对比（配对/慢化标定/上期读数加载）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// warnFixtures 是 72h 窗口内的混合日志：2 条命中默认模式、1 条同为 WARN
// 但不命中、1 条 msg 含模式文本但 level=INFO（不得计入）、1 条窗口外旧 WARN（必须被过滤）。
func warnFixtures() string {
	return strings.Join([]string{
		`time=2020-01-01T00:00:00.000+08:00 level=WARN msg="desktop: frontend slow switch stage" feature=session-monitor detail="tab=tab_old stage=stale ms=9999 seq=1"`,
		`time=2026-10-15T09:31:40.363+08:00 level=WARN msg="desktop: frontend slow switch stage" feature=session-monitor detail="tab=tab_a stage=open-topic:e2e ms=1538 seq=1"`,
		`time=2026-10-15T09:32:00.000+08:00 level=WARN msg="desktop: cold cache compact failed (will retry next tick)" err="disk full"`,
		`time=2026-10-15T09:33:00.000+08:00 level=WARN msg="desktop: open session slow" scope=project elapsedMs=3904`,
		`time=2026-10-15T09:34:00.000+08:00 level=INFO msg="desktop: frontend slow switch stage" feature=session-monitor detail="tab=tab_b stage=meta ms=10 seq=2"`,
		`time=2026-10-15T09:35:00.000+08:00 level=INFO msg="boot: stage timings" summary="total=820ms config=120ms prompt=40ms" root=D:\ws model=m/m1`,
		"",
	}, "\n")
}

func TestWarnPatrolCollectAndVerdict(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "desktop.log"), []byte(warnFixtures()), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	col, err := collectLogDir(dir, 72*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	// WARN 池 = 2 命中 + 1 不命中；INFO 行与窗口外旧 WARN 不入池。
	if len(col.Warns) != 3 {
		t.Fatalf("Warns = %d，应为 3（INFO/窗口外不入池）：%+v", len(col.Warns), col.Warns)
	}

	o := options{warnPatterns: defaultWarnPatterns, warnMaxCount: 0}
	rows := benchWarnPatrol(col, o)
	if len(rows) != 2 {
		t.Fatalf("默认两模式应出 2 行，实际 %d", len(rows))
	}
	for _, r := range rows {
		if r.N != 1 {
			t.Fatalf("%s 计数 = %d，应为 1：%+v", r.Metric, r.N, r)
		}
		if r.Verdict != verdictFail {
			t.Fatalf("%s 越线（max=0）应 FAIL：%+v", r.Metric, r)
		}
		if !strings.Contains(r.Note, "首条") {
			t.Fatalf("FAIL 行应带首条证据：%+v", r)
		}
	}

	// 门可配：max≥1 时同读数转 PASS。
	rows = benchWarnPatrol(col, options{warnPatterns: defaultWarnPatterns, warnMaxCount: 1})
	for _, r := range rows {
		if r.Verdict != verdictPass {
			t.Fatalf("max=1、n=1 应 PASS：%+v", r)
		}
	}

	// 自定义模式：不命中模式出 0 条 PASS 行；INFO 文本不得凑数。
	rows = benchWarnPatrol(col, options{warnPatterns: "open session slow, totally-absent", warnMaxCount: 0})
	if len(rows) != 2 || rows[0].N != 1 || rows[0].Verdict != verdictFail ||
		rows[1].N != 0 || rows[1].Verdict != verdictPass {
		t.Fatalf("自定义模式判定不符：%+v", rows)
	}
}

func TestWarnPatrolNAAndDisabled(t *testing.T) {
	// 日志不可得（FilesParsed=0）→ N/A 明示，不得冒充 PASS。
	rows := benchWarnPatrol(&logCollect{}, options{warnPatterns: defaultWarnPatterns, warnMaxCount: 0})
	if len(rows) != 2 || rows[0].Verdict != verdictNA || rows[1].Verdict != verdictNA {
		t.Fatalf("无日志应 N/A：%+v", rows)
	}
	// 模式空 → 关闭巡检，不出行。
	if rows := benchWarnPatrol(&logCollect{FilesParsed: 1}, options{warnPatterns: " , "}); len(rows) != 0 {
		t.Fatalf("空模式应出 0 行：%+v", rows)
	}
}

func TestParseWarnPatterns(t *testing.T) {
	got := parseWarnPatterns(" slow switch stage , ,cold cache compact failed,slow switch stage ")
	want := []string{"slow switch stage", "cold cache compact failed"}
	if len(got) != len(want) {
		t.Fatalf("解析结果 = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 项 = %q，应为 %q", i, got[i], want[i])
		}
	}
}

func TestBuildTrendRows(t *testing.T) {
	prev := []metricResult{
		{Bench: "boot", Metric: "m-worse", N: 10, P95MS: 1000},
		{Bench: "boot", Metric: "m-flat", N: 10, P95MS: 1000},
		{Bench: "boot", Metric: "m-better", N: 10, P95MS: 1000},
		{Bench: "open-topic", Metric: "census", N: 7},
		{Bench: "x", Metric: "only-prev", N: 5, P95MS: 100},
	}
	cur := []metricResult{
		{Bench: "boot", Metric: "m-worse", N: 10, P95MS: 1200}, // +20% ≥10 → ↑慢化
		{Bench: "boot", Metric: "m-flat", N: 10, P95MS: 1050},  // +5% → ≈
		{Bench: "boot", Metric: "m-better", N: 10, P95MS: 850}, // -15% ≤-10 → ↓改善
		{Bench: "open-topic", Metric: "census", N: 9},          // 计数 +2 → ↑增多
		{Bench: "open-topic", Metric: "census-down", N: 0},     // 本期无样本 → 跳过
		{Bench: "y", Metric: "new", N: 3, P95MS: 50},           // 上期缺 → 跳过
	}
	rows := buildTrendRows(prev, cur, 10)
	if len(rows) != 4 {
		t.Fatalf("行数 = %d，应为 4（无样本/无上期不出行）：%+v", len(rows), rows)
	}
	byMetric := map[string]trendRow{}
	for _, r := range rows {
		byMetric[r.Metric] = r
	}
	if r := byMetric["m-worse"]; r.State != "↑慢化" || r.Delta != "+20.0%" {
		t.Fatalf("慢化行不符：%+v", r)
	}
	if r := byMetric["m-flat"]; r.State != "≈" {
		t.Fatalf("平坦行不符：%+v", r)
	}
	if r := byMetric["m-better"]; r.State != "↓改善" || r.Delta != "-15.0%" {
		t.Fatalf("改善行不符：%+v", r)
	}
	if r := byMetric["census"]; r.State != "↑增多" || r.Delta != "+2 条" || r.Prev != "n=7" || r.Cur != "n=9" {
		t.Fatalf("计数行不符：%+v", r)
	}
	// 慢化线可配：worsePct=50 时 +20% 不再标慢化。
	rows = buildTrendRows(prev, cur, 50)
	for _, r := range rows {
		if r.Metric == "m-worse" && r.State != "≈" {
			t.Fatalf("worsePct=50 时 +20%% 应 ≈：%+v", r)
		}
	}
}

func TestLoadPrevResults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prev.json")
	want := []metricResult{{Bench: "boot", Metric: "m", N: 3, P95MS: 42}}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadPrevResults(path)
	if err != nil || len(got) != 1 || got[0].P95MS != 42 {
		t.Fatalf("加载结果 = %+v, err=%v", got, err)
	}
	// 坏文件必须报错（调用方走「趋势对比跳过」，不得拿零值硬比）。
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPrevResults(path); err == nil {
		t.Fatal("坏文件应报错")
	}
	if _, err := loadPrevResults(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("缺文件应报错")
	}
}
