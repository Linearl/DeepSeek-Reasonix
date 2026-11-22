package main

// tl1685 ② 回归钉：boot 各相独立门（task 1223 相分析法的持续门禁化）。
// 门口径 = 热路径样本池（与 M1 同池）的相级 P95；相缺省不计样本不冒 0；
// 无热路径样本整行 N/A；某相越线该相 FAIL 且不牵连其他相。

import (
	"testing"
)

func bootPhaseFixtureCol() *logCollect {
	return &logCollect{
		Boots: []bootSample{
			// 热路径两条：prompt 相一条 40s（超 60000? 否，40s<60s 门内）、
			// agent:tools 9s（超 8000 门 → FAIL）、controller 1s（门内）。
			{Total: 41000, Hot: true, Stages: map[string]int64{"prompt": 40000, "agent:tools": 500, "controller": 800}},
			{Total: 12000, Hot: true, Stages: map[string]int64{"prompt": 11000, "agent:tools": 9000, "controller": 1000}},
			// 冷启动一条：不得进相门样本池。
			{Total: 40000, Hot: false, Stages: map[string]int64{"prompt": 38000}},
		},
	}
}

func TestBootPhaseGateRowsVerdicts(t *testing.T) {
	o := options{bootPhasePromptMax: 60000, bootPhaseToolsMax: 8000, bootPhaseCtlMax: 4000}
	rows := bootPhaseGateRows(bootPhaseFixtureCol(), o)
	if len(rows) != 3 {
		t.Fatalf("行数 = %d，want 3（prompt/agent:tools/controller 各一行）", len(rows))
	}
	if rows[0].Metric != "boot 相 prompt（热路径，独立门）" || rows[0].Verdict != verdictPass || rows[0].N != 2 || rows[0].P95MS != 40000 {
		t.Fatalf("prompt 门不符（P95=40000<60000 应 PASS，n 只计热路径 2 条）：%+v", rows[0])
	}
	if rows[1].Verdict != verdictFail || rows[1].P95MS != 9000 {
		t.Fatalf("agent:tools P95=9000>8000 应 FAIL：%+v", rows[1])
	}
	if rows[2].Verdict != verdictPass || rows[2].P95MS != 1000 {
		t.Fatalf("controller P95=1000<4000 应 PASS（相间不牵连）：%+v", rows[2])
	}
}

func TestBootPhaseGateRowsNAMissingPhase(t *testing.T) {
	// 全冷启动：相门不适用，整行 N/A（不冒 PASS）。
	coldOnly := &logCollect{Boots: []bootSample{{Total: 40000, Hot: false, Stages: map[string]int64{"prompt": 38000}}}}
	rows := bootPhaseGateRows(coldOnly, options{bootPhasePromptMax: 60000, bootPhaseToolsMax: 8000, bootPhaseCtlMax: 4000})
	for i, r := range rows {
		if r.Verdict != verdictNA || r.N != 0 {
			t.Fatalf("全冷启动时相门行 %d 应 N/A n=0：%+v", i, r)
		}
	}
	// 老版本日志缺某分相：该相不计样本（不冒 0），其他相照常。
	partial := &logCollect{Boots: []bootSample{{Total: 900, Hot: true, Stages: map[string]int64{"prompt": 120}}}}
	rows = bootPhaseGateRows(partial, options{bootPhasePromptMax: 60000, bootPhaseToolsMax: 8000, bootPhaseCtlMax: 4000})
	if rows[0].Verdict != verdictPass || rows[0].N != 1 {
		t.Fatalf("有 prompt 分相应计样本 PASS：%+v", rows[0])
	}
	if rows[1].Verdict != verdictNA || rows[2].Verdict != verdictNA {
		t.Fatalf("缺 agent:tools/controller 分相的样本不得冒 0：%+v %+v", rows[1], rows[2])
	}
}
