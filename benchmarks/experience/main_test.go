package main

// 任务 414 阶段 1 单测：解析器与判定的行为证明。夹具行全部取自
// desktop.log / sessions 事件流的真实格式（2026-10-09 实录），改的只有数值。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseSlogTextLine(t *testing.T) {
	line := `time=2026-10-09T09:35:16.089+08:00 level=INFO msg="desktop: frontend open-topic summary" feature=tab-switch detail="tab=tab_a seq=1 total=504ms history/bridge=498ms history/convert=2ms history=501ms"`
	ln, ok := parseSlogTextLine(line)
	if !ok {
		t.Fatal("实录行解析失败")
	}
	if ln.msg != "desktop: frontend open-topic summary" || ln.level != "INFO" {
		t.Fatalf("msg/level = %q/%q", ln.msg, ln.level)
	}
	if ln.ts.Format(time.RFC3339) != "2026-10-09T09:35:16+08:00" {
		t.Fatalf("ts = %v", ln.ts)
	}
	if ln.attrs["feature"] != "tab-switch" {
		t.Fatalf("feature = %q", ln.attrs["feature"])
	}
	d := parseKVList(ln.attrs["detail"])
	if d["tab"] != "tab_a" || d["total"] != "504ms" || d["history/bridge"] != "498ms" {
		t.Fatalf("detail 解析不符：%v", d)
	}

	// 带转义引号的值（slog 的 strconv.Quote 形态）。
	esc := `time=2026-10-09T09:35:16.089+08:00 level=WARN msg="desktop: frontend \"quoted\" x" k=v`
	ln2, ok := parseSlogTextLine(esc)
	if !ok || ln2.msg != `desktop: frontend "quoted" x` || ln2.attrs["k"] != "v" {
		t.Fatalf("转义引号行解析不符：ok=%v msg=%q", ok, ln2.msg)
	}

	// 杂行（无 k=v 结构 / 无时间戳）必须被安全丢弃。
	for _, junk := range []string{"", "  \n", "no attrs here", "time=not-a-time msg=x"} {
		if _, ok := parseSlogTextLine(junk); ok {
			t.Fatalf("杂行 %q 不应解析成功", junk)
		}
	}
}

func TestParseBootSummary(t *testing.T) {
	total, stages, ok := parseBootSummary(`total=4754ms config=238ms extensions=2ms session=192ms provider=0ms prompt=1669ms mcp=82ms agent:tools=2561ms agent=0ms controller=1ms finalize=6ms`)
	if !ok || total != 4754 {
		t.Fatalf("total=%d ok=%v", total, ok)
	}
	if stages["agent:tools"] != 2561 || stages["prompt"] != 1669 || stages["config"] != 238 {
		t.Fatalf("分相不符：%v", stages)
	}
	if _, _, ok := parseBootSummary("nothing here"); ok {
		t.Fatal("畸形 summary 应返回 false")
	}
}

func TestBootHotClassification(t *testing.T) {
	hot := map[string]int64{"prompt": 40, "mcp": 30, "agent:tools": 20}
	cold := map[string]int64{"prompt": 1669, "mcp": 82, "agent:tools": 2561}
	if !bootIsHotWith(hot, 300) {
		t.Fatal("重组相合计 90ms 应判热路径")
	}
	if bootIsHotWith(cold, 300) {
		t.Fatal("重组相合计 4312ms 不应判热路径")
	}
}

func TestCollectLogDirAndPairing(t *testing.T) {
	dir := t.TempDir()
	log := strings.Join([]string{
		// 旧于窗口的行必须被过滤。
		`time=2020-01-01T00:00:00.000+08:00 level=INFO msg="desktop: frontend tab switch render" feature=session-monitor detail="tab=tab_old render=9999ms"`,
		`time=2026-10-09T09:35:15.628+08:00 level=INFO msg="desktop: frontend tab switch render" feature=session-monitor detail="tab=tab_a render=37ms"`,
		`time=2026-10-09T09:35:16.089+08:00 level=INFO msg="desktop: frontend open-topic summary" feature=tab-switch detail="tab=tab_a seq=1 total=504ms history/bridge=498ms"`,
		`time=2026-10-09T09:35:16.501+08:00 level=INFO msg="desktop: tab switch timing" tab=tab_a stage="open-topic:ancillary effort" ms=413`,
		`time=2026-10-09T09:35:20.770+08:00 level=INFO msg="boot: stage timings" summary="total=4754ms config=238ms prompt=1669ms mcp=82ms agent:tools=2561ms finalize=6ms" root=D:\ws model=m/m1`,
		`time=2026-10-09T09:36:00.000+08:00 level=INFO msg="boot: stage timings" summary="total=820ms config=120ms prompt=40ms mcp=30ms agent:tools=20ms finalize=5ms" root=D:\ws model=m/m1`,
		`time=2026-10-09T09:36:30.000+08:00 level=WARN msg="desktop: open session slow" scope=project topicId=t1 elapsedMs=3904 slowThresholdMs=2000`,
		`time=2026-10-09T09:37:00.000+08:00 level=ERROR msg="desktop: open session failed" scope=global topicId=t2 elapsedMs=120 err="boom"`,
		`time=2026-10-09T09:39:22.013+08:00 level=INFO msg="desktop: frontend switch-tab summary" feature=tab-switch detail="tab=tab_b seq=6 total=0ms "`,
		`time=2026-10-09T09:39:22.090+08:00 level=INFO msg="desktop: frontend tab switch render" feature=session-monitor detail="tab=tab_b render=45ms"`,
		`time=2026-10-09T09:44:26.798+08:00 level=INFO msg="desktop: frontend transcript first frame" feature=session-monitor detail="tab=tab_a firstFrame=104ms geometry=0ms"`,
		`time=2026-10-09T09:44:26.861+08:00 level=INFO msg="desktop: tab switch timing" tab=tab_b stage="switch-tab:ancillary effort" ms=180`,
		`time=2026-10-09T09:44:26.978+08:00 level=INFO msg="desktop: tab switch timing" tab=tab_b stage="switch-tab:ancillary context" ms=352`,
		"", // 结尾空行不得致错
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "desktop.log"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	col, err := collectLogDir(dir, 72*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if col.FilesParsed != 1 || col.LinesParsed != 12 {
		t.Fatalf("files=%d lines=%d（应为 1/12：旧行被窗口过滤，空行不计）", col.FilesParsed, col.LinesParsed)
	}

	// 配对：summary 504 + ancillary 413 = 917，合成口径。
	paired := pairOpenTopic(col.OpenTopicTotal, col.OpenTopicAnc)
	if len(paired) != 1 || paired[0].Kind != "composed" || paired[0].MS != 917 {
		t.Fatalf("配对结果不符：%+v", paired)
	}

	if len(col.Boots) != 2 {
		t.Fatalf("boot 样本数 = %d", len(col.Boots))
	}
	if col.Boots[0].Hot || !col.Boots[1].Hot {
		t.Fatalf("冷/热分类不符：%+v", col.Boots)
	}
	if col.OpenSessionSlow[0].MS != 3904 {
		t.Fatalf("open session slow 读数 = %d", col.OpenSessionSlow[0].MS)
	}
	if col.OpenSessionFail != 1 {
		t.Fatalf("failed 计数 = %d", col.OpenSessionFail)
	}
	if len(col.SwitchRender) != 2 || col.SwitchRender[0].MS != 37 || col.SwitchRender[1].MS != 45 {
		t.Fatalf("render 样本不符：%+v", col.SwitchRender)
	}
	if len(col.FirstFrame) != 1 || col.FirstFrame[0].MS != 104 {
		t.Fatalf("firstFrame 样本不符：%+v", col.FirstFrame)
	}
	if len(col.BackendCensus) != 3 {
		t.Fatalf("普查行数 = %d（open-topic:ancillary effort / switch-tab:ancillary effort / context）", len(col.BackendCensus))
	}
}

// TestPairReuseHits：热路径分类的主口径——boot 构建起点（At-Total）与
// assembly hit 对齐 ±2s 即复用命中；hit 一次性消费；启发式仍独立成立。
func TestPairReuseHits(t *testing.T) {
	base := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	mk := func(at time.Time, total int64) bootSample {
		return bootSample{At: at, Total: total, Stages: map[string]int64{"prompt": 4000}}
	}
	col := &logCollect{
		// hit1 对准 b1 起点（10:00:00）；hit2 对准 b2 起点（10:01:00）。
		AssemblyHits: []time.Time{base, base.Add(61 * time.Second)},
		Boots: []bootSample{
			mk(base.Add(5*time.Second), 5000),   // 起点 = base：配中 hit1
			mk(base.Add(64*time.Second), 4000),  // 起点 = base+60s：配中 hit2
			mk(base.Add(120*time.Second), 8000), // 起点 = base+112s：无 hit
		},
	}
	col.pairReuseHits()
	if !col.Boots[0].Reuse || !col.Boots[0].Hot {
		t.Fatalf("b0 应配中 hit1：%+v", col.Boots[0])
	}
	if !col.Boots[1].Reuse {
		t.Fatalf("b1 应配中 hit2：%+v", col.Boots[1])
	}
	if col.Boots[2].Reuse {
		t.Fatalf("b2 不应配中（hit 已被消费/超窗）：%+v", col.Boots[2])
	}
	// 分相启发式兜底：无 hit 但重组相合计≤预算仍判热。
	if !bootIsHotWith(map[string]int64{"prompt": 100, "mcp": 50, "agent:tools": 100}, 300) {
		t.Fatal("重组相合计 250ms 应判热（启发式兜底）")
	}
}

// TestBenchOpenTopicSlowCensus：slow 行（≥2s 截断样本）单列 census 行、
// 不并入 P95 门——并入会与合成样本重复计数并抬高 P95。
func TestBenchOpenTopicSlowCensus(t *testing.T) {
	o := options{openTopicMaxMS: 2000}
	col := &logCollect{
		OpenTopicTotal:  []sample{{MS: 800}},
		OpenSessionSlow: []sample{{MS: 3904}, {MS: 5100}},
	}
	rows := benchOpenTopic(col, o)
	if len(rows) != 3 {
		t.Fatalf("行数 = %d（合成门 + 失败 census + 慢开 census）", len(rows))
	}
	if rows[0].Verdict != verdictPass || rows[0].P95MS != 800 {
		t.Fatalf("P95 门只该看合成样本（800ms → PASS）：%+v", rows[0])
	}
	slow := rows[2]
	if slow.Verdict != verdictFail || slow.N != 2 || slow.MaxMS != 5100 || slow.P50MS != 3904 {
		t.Fatalf("慢开 census 应 2 条 FAIL（P50/Max 读数）：%+v", slow)
	}
	// 无 slow 行时不产生多余行。
	if got := benchOpenTopic(&logCollect{OpenTopicTotal: []sample{{MS: 500}}}, o); len(got) != 2 {
		t.Fatalf("无 slow 行时行数 = %d（应 2）", len(got))
	}
}

func TestPairOpenTopicWindowEdges(t *testing.T) {
	base := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
	mk := func(tab string, dt time.Duration, ms int64) sample {
		return sample{At: base.Add(dt), Tab: tab, MS: ms}
	}
	// 太早（-3s）不认配、太晚（61s）不认配、不同 tab 不认配；每个 ancillary 只用一次。
	total := []sample{mk("t1", 0, 500), mk("t1", time.Second, 600)}
	anc := []sample{
		mk("t1", -3*time.Second, 100), // 早于 -2s：不配
		mk("t1", 61*time.Second, 200), // 晚于 60s：不配
		mk("t2", 2*time.Second, 300),  // 不同 tab：不配
		mk("t1", 3*time.Second, 400),  // 配给第一条
		mk("t1", 5*time.Second, 500),  // 配给第二条
	}
	got := pairOpenTopic(total, anc)
	if len(got) != 2 {
		t.Fatalf("配对数 = %d", len(got))
	}
	if got[0].Kind != "composed" || got[0].MS != 900 {
		t.Fatalf("第一条应为 500+400=900：%+v", got[0])
	}
	if got[1].Kind != "composed" || got[1].MS != 1100 {
		t.Fatalf("第二条应为 600+500=1100：%+v", got[1])
	}
	// 无 ancillary 时回落 summary-only，仍计入样本。
	got2 := pairOpenTopic(total, nil)
	if len(got2) != 2 || got2[0].Kind != "summary-only" || got2[0].MS != 500 {
		t.Fatalf("回落不符：%+v", got2)
	}
}

// TestScanLedgersFirstResponse 覆盖 turn ledger 的首响折叠：v1（无 recordType）
// 与 v2（recordType=event）两种 schema、checkpoint 簿记跳过、无首响的
// turn_done 计入 NoResponse、进行中的 turn 计入 InFlight、窗口过滤、
// guardian 文件与 .trash 跳过、尾部截断停止。
func TestScanLedgersFirstResponse(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	base := now.Add(-time.Hour).UnixMilli()
	ledger := strings.Join([]string{
		// v2 簿记行：必须跳过、不得判截断。
		`{"schemaVersion":2,"recordType":"checkpoint","sessionId":"s","compactedThroughSeq":1}`,
		// t1：v2，turn_started → text(+31s) → turn_done。
		fmt.Sprintf(`{"recordType":"event","schemaVersion":2,"turnId":"t1","kind":"turn_started","status":"in_progress","createdAt":%d}`, base),
		fmt.Sprintf(`{"recordType":"event","schemaVersion":2,"turnId":"t1","kind":"text","status":"in_progress","createdAt":%d}`, base+31000),
		fmt.Sprintf(`{"recordType":"event","schemaVersion":2,"turnId":"t1","kind":"turn_done","status":"completed","createdAt":%d}`, base+40000),
		// t2：v1（无 recordType），turn_started → turn_done，无首响。
		fmt.Sprintf(`{"schemaVersion":1,"turnId":"t2","kind":"turn_started","status":"in_progress","createdAt":%d}`, base+50000),
		fmt.Sprintf(`{"schemaVersion":1,"turnId":"t2","kind":"turn_done","status":"failed","createdAt":%d}`, base+60000),
		// t3：进行中，扫描结束仍未收盘且无首响 → InFlight，不算失败。
		fmt.Sprintf(`{"recordType":"event","schemaVersion":2,"turnId":"t3","kind":"turn_started","status":"in_progress","createdAt":%d}`, base+70000),
		// t4：起点在窗口外（10 天前）——不计样本也不计失败。
		`{"recordType":"event","schemaVersion":2,"turnId":"t4","kind":"turn_started","status":"in_progress","createdAt":1750000000000}`,
		fmt.Sprintf(`{"recordType":"event","schemaVersion":2,"turnId":"t4","kind":"turn_done","status":"completed","createdAt":%d}`, base+80000),
		// turn_done 之后的迟到记录：忽略。
		fmt.Sprintf(`{"recordType":"event","schemaVersion":2,"turnId":"t1","kind":"text","status":"completed","createdAt":%d}`, base+90000),
		"", // 结尾空行不得致错
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "s.turns.jsonl"), []byte(ledger), 0o644); err != nil {
		t.Fatal(err)
	}
	// guardian 会话不是「用户发消息」，排除。
	if err := os.WriteFile(filepath.Join(dir, "g.guardian.turns.jsonl"), []byte(ledger), 0o644); err != nil {
		t.Fatal(err)
	}
	// 非 ledger 的 jsonl（events/transcript）不扫描。
	if err := os.WriteFile(filepath.Join(dir, "s.events.jsonl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	scan, err := scanLedgers([]string{dir}, 72*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if scan.FilesScanned != 1 {
		t.Fatalf("files = %d（guardian/events 应跳过，应为 1）", scan.FilesScanned)
	}
	if len(scan.Turns) != 1 || scan.Turns[0].MS != 31000 {
		t.Fatalf("turns = %+v（应只有 t1=31000ms）", scan.Turns)
	}
	if scan.NoResponse != 1 {
		t.Fatalf("NoResponse = %d（应为 1：t2 失败无首响；t4 窗口外不计）", scan.NoResponse)
	}
	if scan.InFlight != 1 {
		t.Fatalf("InFlight = %d（应为 1：t3 进行中）", scan.InFlight)
	}
	if scan.Torn != 0 {
		t.Fatalf("Torn = %d（不应截断）", scan.Torn)
	}
}

// TestScanLedgerTornTail：无法解析的半截行必须停在该处（不修补、不错算
// 后续行），且已折叠的样本保留。
func TestScanLedgerTornTail(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	base := now.Add(-time.Hour).UnixMilli()
	ledger := strings.Join([]string{
		fmt.Sprintf(`{"recordType":"event","schemaVersion":2,"turnId":"t1","kind":"turn_started","status":"in_progress","createdAt":%d}`, base),
		fmt.Sprintf(`{"recordType":"event","schemaVersion":2,"turnId":"t1","kind":"tool_started","status":"in_progress","createdAt":%d}`, base+10000),
		`{"recordType":"event","schemaVersion":2,"turnId":"t2","kind":"turn_started","status":"in_progress","createdAt":`, // 半截行
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "s.turns.jsonl"), []byte(ledger), 0o644); err != nil {
		t.Fatal(err)
	}
	scan, err := scanLedgers([]string{dir}, 72*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Turns) != 1 || scan.Turns[0].MS != 10000 {
		t.Fatalf("截断前样本应保留：%+v", scan.Turns)
	}
	if scan.Torn != 1 {
		t.Fatalf("Torn = %d（应为 1）", scan.Torn)
	}
}

func TestSessionRoots(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", "p1", "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	roots := sessionRoots(home, "")
	if len(roots) != 2 {
		t.Fatalf("默认根 = %v（应含 sessions 与 projects/*/sessions）", roots)
	}
	if got := sessionRoots(home, "X:\\only"); len(got) != 1 || got[0] != "X:\\only" {
		t.Fatalf("显式指定 = %v（应只含该目录）", got)
	}
}

func TestPercentileNearestRank(t *testing.T) {
	vals := []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	if got := percentileInt64(vals, 0.50); got != 50 {
		t.Fatalf("P50 = %d（就近秩第 5 位）", got)
	}
	if got := percentileInt64(vals, 0.95); got != 100 {
		t.Fatalf("P95 = %d（就近秩第 10 位）", got)
	}
	if got := percentileInt64([]int64{7}, 0.95); got != 7 {
		t.Fatalf("单样本 P95 = %d", got)
	}
	if got := percentileInt64(nil, 0.5); got != 0 {
		t.Fatalf("空样本应得 0，得 %d", got)
	}
}

func TestBenchVerdicts(t *testing.T) {
	o := options{
		bootHotBudgetMS: 300, bootHotMaxMS: 1000,
		openTopicMaxMS: 2000, renderMaxMS: 150, firstResponseMaxSec: 60,
	}
	// FAIL 面：超阈值样本必须亮红，不得静默绿。
	col := &logCollect{
		Boots:           []bootSample{{Total: 900, Hot: true}, {Total: 1500, Hot: true}},
		OpenTopicTotal:  []sample{{MS: 1800}},
		OpenTopicAnc:    []sample{{MS: 500}},
		OpenSessionFail: 1,
		SwitchRender:    []sample{{MS: 40}, {MS: 200}},
	}
	boot := benchBoot(col, o)
	if boot[0].Verdict != verdictFail || boot[0].P95MS != 1500 {
		t.Fatalf("热路径 boot 应 FAIL（P95=1500>1000）：%+v", boot[0])
	}
	ot := benchOpenTopic(col, o)
	if ot[0].Verdict != verdictFail || ot[0].P95MS != 2300 {
		t.Fatalf("合成 1800+500 应 FAIL：%+v", ot[0])
	}
	if ot[1].Verdict != verdictFail {
		t.Fatalf("failed=1 应 FAIL：%+v", ot[1])
	}
	ts := benchTabSwitch(col, o)
	if ts[0].Verdict != verdictFail || ts[0].P95MS != 200 {
		t.Fatalf("render P95=200>150 应 FAIL：%+v", ts[0])
	}
	if ts[1].Verdict != verdictObserve {
		t.Fatalf("firstFrame 无样本应 OBSERVE：%+v", ts[1])
	}

	// PASS 面 + N/A 面：空样本必须明示 N/A，不得冒充 PASS。
	empty := &logCollect{}
	boot2 := benchBoot(empty, o)
	if boot2[0].Verdict != verdictNA {
		t.Fatalf("无样本应 N/A：%+v", boot2[0])
	}
	ok := &logCollect{
		Boots:          []bootSample{{Total: 900, Hot: true}},
		OpenTopicTotal: []sample{{MS: 1200}},
		SwitchRender:   []sample{{MS: 100}, {MS: 120}},
	}
	ot2 := benchOpenTopic(ok, o)
	if ot2[0].Verdict != verdictPass {
		t.Fatalf("summary-only 1200 应 PASS：%+v", ot2[0])
	}
	ts2 := benchTabSwitch(ok, o)
	if ts2[0].Verdict != verdictPass {
		t.Fatalf("render P95=120 应 PASS：%+v", ts2[0])
	}
}
