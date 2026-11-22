package main

// tl1685 ③ 回归钉：WARN slow switch stage 按 stage 细分计数。第 6 期 83 条
//（上期 45，+38）只有总量，归因要靠「哪些 stage 在涨」——细分是观测归因
// 辅助，门仍在 M9 巡检行。关键夹具特征：stage 值可含空格
//（stage=switch-tab:ancillary effort），不能用 parseKVList 空白切分。

import (
	"strings"
	"testing"
)

func TestParseSlowSwitchDetail(t *testing.T) {
	// 实录形态（2026-11-22 desktop.log）：stage 值带空格，后面还有 ms/seq。
	stage, ms, ok := parseSlowSwitchDetail(`tab=tab_10b0ff00 stage=switch-tab:ancillary effort ms=1359 seq=56`)
	if !ok || stage != "switch-tab:ancillary effort" || ms != 1359 {
		t.Fatalf("带空格 stage 解析不符：stage=%q ms=%d ok=%v", stage, ms, ok)
	}
	// 无空格 stage + 尾部无更多字段。
	stage, ms, ok = parseSlowSwitchDetail(`tab=tab_x stage=switch-tab:total ms=1021 seq=37`)
	if !ok || stage != "switch-tab:total" || ms != 1021 {
		t.Fatalf("无空格 stage 解析不符：stage=%q ms=%d ok=%v", stage, ms, ok)
	}
	// stage 是最后一个字段（无尾随 k=v）。
	stage, _, ok = parseSlowSwitchDetail(`tab=tab_y stage=hydrate:history`)
	if !ok || stage != "hydrate:history" {
		t.Fatalf("尾部 stage 解析不符：stage=%q ok=%v", stage, ok)
	}
	// 无 stage= 字段（老版本形态）。
	if _, _, ok := parseSlowSwitchDetail(`tab=tab_z ms=99`); ok {
		t.Fatal("无 stage= 应 ok=false")
	}
}

func TestWarnSlowSwitchBreakdownByStage(t *testing.T) {
	col := &logCollect{
		FilesParsed: 1,
		Warns: []warnLine{
			{Msg: "desktop: frontend slow switch stage", Detail: "stage=switch-tab:ancillary effort ms=1800", Stage: "switch-tab:ancillary effort", MS: 1800},
			{Msg: "desktop: frontend slow switch stage", Detail: "stage=switch-tab:ancillary effort ms=400", Stage: "switch-tab:ancillary effort", MS: 400},
			{Msg: "desktop: frontend slow switch stage", Detail: "stage=switch-tab:history ms=3000", Stage: "switch-tab:history", MS: 3000},
			{Msg: "desktop: frontend slow switch stage", Detail: "tab=x ms=10", Stage: "", MS: 0}, // 解析失败 → (no-stage)
			{Msg: "desktop: cold cache compact failed", Detail: "err=disk full"},                  // 其他模式不细分
		},
	}
	row, ok := warnSlowSwitchBreakdown(col)
	if !ok {
		t.Fatal("池内有 slow switch 行应出行")
	}
	if row.Verdict != verdictObserve || row.N != 4 {
		t.Fatalf("细分行应 OBSERVE 且 N=4（不含其他模式）：%+v", row)
	}
	// 条数多者在前：ancillary effort×2 第一（P50 取就近秩：sorted[400,1800] → 400）。
	if !strings.HasPrefix(row.Note, "switch-tab:ancillary effort×2(P50=400ms,Max=1800ms)") {
		t.Fatalf("注记排序/读数不符：%q", row.Note)
	}
	if !strings.Contains(row.Note, "(no-stage)×1") {
		t.Fatalf("解析失败行应归 (no-stage) 不静默丢：%q", row.Note)
	}
	// 池内无 slow switch 行 → 不出行。
	empty, ok := warnSlowSwitchBreakdown(&logCollect{FilesParsed: 1, Warns: []warnLine{{Msg: "desktop: cold cache compact failed"}}})
	if ok || empty != (metricResult{}) {
		t.Fatalf("无 slow switch 行不应出行：%+v ok=%v", empty, ok)
	}
}

func TestWarnPatrolBreakdownRowPlacement(t *testing.T) {
	// 细分行插在模式行之后、截断行之前（截断 FAIL 行保持末位）。
	col := &logCollect{
		FilesParsed: 1,
		Warns: []warnLine{
			{Msg: "desktop: frontend slow switch stage", Detail: "stage=switch-tab:total ms=1021", Stage: "switch-tab:total", MS: 1021},
		},
		WarnTruncated: true,
	}
	rows := benchWarnPatrol(col, options{warnPatterns: defaultWarnPatterns, warnMaxCount: 0})
	if len(rows) != 4 {
		t.Fatalf("行数 = %d，want 4（2 模式 + 细分 + 截断）：%+v", len(rows), rows)
	}
	if !strings.Contains(rows[2].Metric, "按 stage 细分") {
		t.Fatalf("第 3 行应为细分行：%+v", rows[2])
	}
	last := rows[len(rows)-1]
	if last.Verdict != verdictFail || !strings.Contains(last.Metric, "截断") {
		t.Fatalf("末行应保持截断 FAIL 行：%+v", last)
	}
}
