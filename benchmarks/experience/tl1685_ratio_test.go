package main

// tl1685 ④ 回归钉：effort 慢读条数÷切 tab 次数比值化。第 6 期实测教训：
// 慢读 125→237 条升但 P95 2503→1855ms 反降——条数升/单条快=切换次数增多
// 非单读劣化，条数单指标跨期对比会误判；比值化后跨期对比走比值。

import (
	"strings"
	"testing"
)

func TestEffortSlowReadRatioRow(t *testing.T) {
	// 常态：4 条慢读 ÷ 2 次切 tab = 2.00 条/次，OBSERVE。
	col := &logCollect{
		EffortReadSlow: []sample{{MS: 200}, {MS: 300}, {MS: 400}, {MS: 500}},
		SwitchTotal:    []sample{{MS: 800}, {MS: 900}},
	}
	row := effortSlowReadRatioRow(col)
	if row.Verdict != verdictObserve || row.Ratio != 2.0 || row.N != 4 {
		t.Fatalf("比值行不符：%+v", row)
	}
	if !strings.Contains(row.Note, "4 条 ÷ 切 tab 2 次 = 2.00 条/次") {
		t.Fatalf("注记应携带比值算式：%q", row.Note)
	}
	if !strings.Contains(row.Note, "非劣化") {
		t.Fatalf("注记应携带第6期教训的判读口径：%q", row.Note)
	}
	// 切 tab 样本 0：比值不可算 → N/A 明示（有慢读时不冒 PASS）。
	na := effortSlowReadRatioRow(&logCollect{EffortReadSlow: []sample{{MS: 200}}})
	if na.Verdict != verdictNA || na.Ratio != 0 {
		t.Fatalf("无切 tab 样本应 N/A：%+v", na)
	}
	// 双零：PASS（无事发生）。
	quiet := effortSlowReadRatioRow(&logCollect{})
	if quiet.Verdict != verdictPass {
		t.Fatalf("双零应 PASS：%+v", quiet)
	}
}

func TestBuildTrendRowsRatio(t *testing.T) {
	prev := []metricResult{
		{Bench: "switch-cost", Metric: "effort 慢读条数÷切 tab 次数（比值观测）", N: 125, Ratio: 2.5},
	}
	cur := []metricResult{
		// 慢读条数翻倍但切换次数也翻倍 → 比值持平 → ≈（第6期式场景不再误判 ↑增多）。
		{Bench: "switch-cost", Metric: "effort 慢读条数÷切 tab 次数（比值观测）", N: 250, Ratio: 2.5},
	}
	rows := buildTrendRows(prev, cur, 10)
	if len(rows) != 1 || rows[0].State != "≈" || rows[0].Delta != "+0.0%" {
		t.Fatalf("比值持平应 ≈（条数翻倍不再标 ↑增多）：%+v", rows)
	}
	if rows[0].Prev != "比值=2.50" || rows[0].Cur != "比值=2.50" {
		t.Fatalf("比值行 Prev/Cur 形态不符：%+v", rows[0])
	}

	// 比值 +20% → ↑慢化；-25% → ↓改善。
	worse := buildTrendRows(prev, []metricResult{{Bench: "switch-cost", Metric: "effort 慢读条数÷切 tab 次数（比值观测）", N: 300, Ratio: 3.0}}, 10)
	if len(worse) != 1 || worse[0].State != "↑慢化" || worse[0].Delta != "+20.0%" {
		t.Fatalf("比值 +20%% 应 ↑慢化：%+v", worse)
	}
	better := buildTrendRows(prev, []metricResult{{Bench: "switch-cost", Metric: "effort 慢读条数÷切 tab 次数（比值观测）", N: 100, Ratio: 1.875}}, 10)
	if len(better) != 1 || better[0].State != "↓改善" || better[0].Delta != "-25.0%" {
		t.Fatalf("比值 -25%% 应 ↓改善：%+v", better)
	}

	// 上期 JSON 无 Ratio 字段（旧读数）→ 落计数行形态对比（增多即标），
	// 不崩、不冒 P95 对比。
	oldPrev := []metricResult{{Bench: "switch-cost", Metric: "effort 慢读条数÷切 tab 次数（比值观测）", N: 100}}
	mixed := buildTrendRows(oldPrev, []metricResult{{Bench: "switch-cost", Metric: "effort 慢读条数÷切 tab 次数（比值观测）", N: 120, Ratio: 1.2}}, 10)
	if len(mixed) != 1 || mixed[0].State != "↑增多" || mixed[0].Prev != "n=100" {
		t.Fatalf("上期无 Ratio 应退计数对比：%+v", mixed)
	}
}

func TestBenchSwitchCostRatioRowAppended(t *testing.T) {
	// benchSwitchCost 末尾追加比值行：原有 4 行语义不变，第 5 行=比值。
	o := options{switchFastMaxMS: 1500}
	col := &logCollect{
		EffortSwitchFast: []sample{{MS: 4}},
		EffortReadSlow:   []sample{{MS: 600}},
		SwitchTotal:      []sample{{MS: 800}, {MS: 900}},
	}
	rows := benchSwitchCost(col, o)
	if len(rows) != 5 {
		t.Fatalf("行数 = %d，want 5（2 门 + fallback + 慢读 census + 比值）：%+v", len(rows), rows)
	}
	last := rows[len(rows)-1]
	if last.Metric != "effort 慢读条数÷切 tab 次数（比值观测）" || last.Ratio != 0.5 {
		t.Fatalf("比值行不符：%+v", last)
	}
}
