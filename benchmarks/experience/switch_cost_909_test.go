package main

// 任务 909（验收自动化一期）：switch-cost 门的采集与判定测试。
// 夹具行取自 desktop.log 实录格式（model switch / effort switch /
// effort read breakdown / runtime build end），改的只有数值。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCollectSwitchCostLines：四类行各归各位——model 快路径（elapsed_ms
// 样本）、model fallback（计数不入样本）、effort 快路径（elapsed_ms 样本）、
// effort fallback（build end trigger 计数）、effort 慢读（截断样本池）。
func TestCollectSwitchCostLines(t *testing.T) {
	dir := t.TempDir()
	log := strings.Join([]string{
		`time=2026-10-16T15:10:42.834+08:00 level=INFO msg="desktop: model switch" tab=tab_a path=fast-per-request model=p/m2 elapsed_ms=12`,
		`time=2026-10-16T15:10:50.483+08:00 level=INFO msg="desktop: model switch" tab=tab_a path=fast-same-model model=p/m2 elapsed_ms=1`,
		// 老版本行（无 elapsed_ms）：必须丢弃而不是当 0 计入——0 会把 P95 拉进假 PASS。
		`time=2026-10-16T15:11:00.000+08:00 level=INFO msg="desktop: model switch" tab=tab_a path=fast-per-request model=p/m3`,
		`time=2026-10-16T15:12:00.000+08:00 level=INFO msg="desktop: model switch" tab=tab_a path=fallback-persona-boundary model=p/m4`,
		`time=2026-10-16T15:13:00.000+08:00 level=INFO msg="desktop: effort switch" tab=tab_a path=fast-per-request level=high elapsed_ms=4`,
		`time=2026-10-16T15:14:00.000+08:00 level=INFO msg="desktop: effort switch" tab=tab_a path=fast-same-level level=high elapsed_ms=2`,
		`time=2026-10-16T15:15:00.000+08:00 level=INFO msg="desktop: runtime build end" trigger=set-effort ms=4200 shared_host_reused=true path=fallback`,
		`time=2026-10-16T15:16:00.000+08:00 level=INFO msg="desktop: runtime build end" trigger=set-model ms=5100 shared_host_reused=true path=fallback`,
		`time=2026-10-16T15:17:00.000+08:00 level=INFO msg="desktop: effort read breakdown" tab=tab_a stage="switch-tab:ancillary effort" total_ms=596 reconcile_ms=594 memo_ms=1`,
		`time=2026-10-16T15:18:00.000+08:00 level=INFO msg="desktop: effort read breakdown" tab=tab_b stage="switch-tab:ancillary effort" total_ms=181 reconcile_ms=180`,
		"", // 结尾空行不得致错
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "desktop.log"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 16, 16, 0, 0, 0, time.FixedZone("CST", 8*3600))
	col, err := collectLogDir(dir, 72*time.Hour, now, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(col.ModelSwitchFast) != 2 || col.ModelSwitchFast[0].MS != 12 || col.ModelSwitchFast[1].MS != 1 {
		t.Fatalf("model 快路径样本不符（无 elapsed_ms 的老行应丢弃）：%+v", col.ModelSwitchFast)
	}
	if col.ModelSwitchFallbk != 1 {
		t.Fatalf("model fallback 计数 = %d，want 1", col.ModelSwitchFallbk)
	}
	if len(col.EffortSwitchFast) != 2 || col.EffortSwitchFast[0].MS != 4 || col.EffortSwitchFast[1].MS != 2 {
		t.Fatalf("effort 快路径样本不符：%+v", col.EffortSwitchFast)
	}
	if col.EffortSwitchFallb != 1 {
		t.Fatalf("effort fallback 计数 = %d，want 1（set-model 的 build end 不计入）", col.EffortSwitchFallb)
	}
	if len(col.EffortReadSlow) != 2 || col.EffortReadSlow[0].MS != 596 || col.EffortReadSlow[1].MS != 181 {
		t.Fatalf("effort 慢读样本不符：%+v", col.EffortReadSlow)
	}
}

// TestBenchSwitchCostVerdicts：门的判定面——P95 越线 FAIL、门内 PASS、
// 无样本 N/A（明示，不冒充 PASS）、fallback 计数行始终 OBSERVE。
func TestBenchSwitchCostVerdicts(t *testing.T) {
	o := options{switchFastMaxMS: 1500}

	// 越线：P95 取最近秩，样本 [10, 2000] → P95=2000 > 1500 → FAIL。
	failCol := &logCollect{
		ModelSwitchFast: []sample{{MS: 10}, {MS: 2000}},
		EffortSwitchFast: []sample{{MS: 8}, {MS: 30}},
	}
	rows := benchSwitchCost(failCol, o)
	if len(rows) != 4 {
		t.Fatalf("行数 = %d（model 门 + effort 门 + fallback 计数 + 慢读 census）", len(rows))
	}
	if rows[0].Verdict != verdictFail || rows[0].P95MS != 2000 {
		t.Fatalf("model P95=2000>1500 应 FAIL：%+v", rows[0])
	}
	if rows[1].Verdict != verdictPass || rows[1].P95MS != 30 {
		t.Fatalf("effort P95=30 应 PASS：%+v", rows[1])
	}
	if rows[2].Verdict != verdictObserve {
		t.Fatalf("fallback 计数行应 OBSERVE：%+v", rows[2])
	}
	if rows[3].Verdict != verdictPass || rows[3].N != 0 {
		t.Fatalf("无慢读应 PASS n=0：%+v", rows[3])
	}

	// 无样本：两个门都 N/A；有 fallback 计数时计数行照报。
	empty := benchSwitchCost(&logCollect{ModelSwitchFallbk: 2, EffortSwitchFallb: 1}, o)
	if empty[0].Verdict != verdictNA || empty[1].Verdict != verdictNA {
		t.Fatalf("无样本应 N/A：%+v %+v", empty[0], empty[1])
	}
	if empty[2].Verdict != verdictObserve || empty[2].N != 3 {
		t.Fatalf("fallback 计数 = %d，want 3：%+v", empty[2].N, empty[2])
	}

	// 慢读 census 有样本时给 P50/Max 读数且保持 OBSERVE。
	slow := benchSwitchCost(&logCollect{EffortReadSlow: []sample{{MS: 181}, {MS: 596}}}, o)
	if slow[3].Verdict != verdictObserve || slow[3].N != 2 || slow[3].P50MS != 181 || slow[3].MaxMS != 596 {
		t.Fatalf("慢读 census 读数不符：%+v", slow[3])
	}
}
