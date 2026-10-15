package main

// EBDD 体验评测集（阶段 1 · E 柱）——任务 414。
//
// 把已完成任务的人工验收步骤脚本化：读 desktop.log 与 sessions/*.turns.jsonl
// turn ledger，对照 tasklist 各任务验收阈值输出 P50/P95 读数与 PASS/FAIL。
// 一键重跑：
//
//	go run ./benchmarks/experience                      # 默认窗口 72h，全部 5 项
//	go run ./benchmarks/experience -bench open-topic    # boot|open-topic|tab-switch|first-response|warn-patrol
//	go run ./benchmarks/experience -window 240h -json out.json
//	go run ./benchmarks/experience -window 168h -json weekly.json -trend prev.json   # 阶段 4 周趋势
//
// 指标、阈值与 tasklist 出处的完整清单见同目录 README.md。
// 本命令只读日志与会话文件，不写任何产品状态；任一门越线以退出码 1 结束，
// 可直接挂进批次阶段门或 heartbeat（阶段 4：-json/-trend 聚合 + WARN 巡检）。

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reasonix/internal/config"
)

// 阈值默认值：全部引用 tasklist 各任务验收（详见 README.md 指标清单）。
const (
	defaultBootHotBudgetMS     = 300  // 热路径分类：prompt+mcp+agent:tools 合计预算
	defaultBootHotMaxMS        = 1000 // 363 验收①：单开新对话（热路径）boot total <1s
	defaultOpenTopicMaxMS      = 2000 // 363 追加验收⑥：打开历史对话→可发消息 <2s
	defaultRenderMaxMS         = 150  // tab_timing.go slowTabSwitchLogMs（任务 196 判定线）
	defaultFirstResponseMaxSec = 60   // 370：无数值验收，取遥测分桶 s_15_60 上沿（可配）
	defaultWindow              = 72 * time.Hour
)

type options struct {
	home                string
	logDir              string
	sessionsDir         string
	window              time.Duration
	bench               string
	bootHotBudgetMS     int64
	bootHotMaxMS        int64
	openTopicMaxMS      int64
	renderMaxMS         int64
	firstResponseMaxSec float64
	jsonOut             string
	warnPatterns        string  // 阶段 4：WARN 巡检模式（逗号分隔子串；空=关闭）
	warnMaxCount        int     // 阶段 4：每模式允许条数（超出即 FAIL）
	trend               string  // 阶段 4：上期读数 JSON（周趋势对比）
	trendWorsePct       float64 // 阶段 4：P95 恶化百分比慢化线
}

// verdict 是一项指标的判定结论。
type verdict string

const (
	verdictPass    verdict = "PASS"
	verdictFail    verdict = "FAIL"
	verdictNA      verdict = "N/A"     // 窗口内无样本：明示，不算失败也不算通过
	verdictObserve verdict = "OBSERVE" // 只报告不设门（tasklist 无数值验收）
)

// metricResult 是一项 bench 的输出（文本与 -json 共用）。
type metricResult struct {
	Bench     string  `json:"bench"`
	Metric    string  `json:"metric"`
	Source    string  `json:"source"`
	Threshold string  `json:"threshold"`
	Ref       string  `json:"ref"`
	N         int     `json:"n"`
	P50MS     float64 `json:"p50_ms,omitempty"`
	P95MS     float64 `json:"p95_ms,omitempty"`
	MaxMS     float64 `json:"max_ms,omitempty"`
	Verdict   verdict `json:"verdict"`
	Note      string  `json:"note,omitempty"`
}

func main() {
	var o options
	flag.StringVar(&o.home, "home", config.ReasonixHomeDir(), "Reasonix home（默认取 REASONIX_HOME 或用户配置目录）")
	flag.StringVar(&o.logDir, "log-dir", "", "desktop.log 所在目录（默认 <home>/logs/desktop）")
	flag.StringVar(&o.sessionsDir, "sessions-dir", "", "turn ledger 目录（默认 <home>/sessions + <home>/projects/*/sessions）")
	flag.DurationVar(&o.window, "window", defaultWindow, "统计窗口（只读窗口内样本）")
	flag.StringVar(&o.bench, "bench", "all", "boot|open-topic|tab-switch|first-response|warn-patrol|all")
	flag.Int64Var(&o.bootHotBudgetMS, "boot-hot-budget-ms", defaultBootHotBudgetMS, "热路径分类的重组相合计预算")
	flag.Int64Var(&o.bootHotMaxMS, "boot-hot-max-ms", defaultBootHotMaxMS, "热路径 boot total 阈值（363①）")
	flag.Int64Var(&o.openTopicMaxMS, "open-topic-max-ms", defaultOpenTopicMaxMS, "打开历史对话端到端阈值（363⑥）")
	flag.Int64Var(&o.renderMaxMS, "render-max-ms", defaultRenderMaxMS, "切 tab 渲染阈值（196 慢线）")
	flag.Float64Var(&o.firstResponseMaxSec, "first-response-max-s", defaultFirstResponseMaxSec, "首响 P95 阈值秒数（370，可配）")
	flag.StringVar(&o.jsonOut, "json", "", "把读数写成 JSON 文件（阶段 4 聚合的入口）")
	flag.StringVar(&o.warnPatterns, "warn-patterns", defaultWarnPatterns, "WARN 巡检模式（逗号分隔，msg+detail 子串；空=关闭巡检）")
	flag.IntVar(&o.warnMaxCount, "warn-max-count", 0, "WARN 巡检每模式允许条数（超出即 FAIL，可配）")
	flag.StringVar(&o.trend, "trend", "", "上期读数 JSON（阶段 4 周趋势对比；只报告，不影响退出码）")
	flag.Float64Var(&o.trendWorsePct, "trend-worse-pct", 10, "趋势慢化线：P95 恶化百分比 ≥ 该值标 ↑（计数行增多即标，可配）")
	flag.Parse()

	if o.home == "" {
		fmt.Fprintln(os.Stderr, "无法解析 Reasonix home：请用 -home 指定")
		os.Exit(2)
	}
	if o.logDir == "" {
		o.logDir = filepath.Join(o.home, "logs", "desktop")
	}
	want := map[string]bool{}
	if o.bench != "all" {
		switch o.bench {
		case "boot", "open-topic", "tab-switch", "first-response", "warn-patrol":
			want[o.bench] = true
		default:
			fmt.Fprintf(os.Stderr, "未知 -bench %q（可选 boot|open-topic|tab-switch|first-response|warn-patrol|all）\n", o.bench)
			os.Exit(2)
		}
	}

	now := time.Now()
	fmt.Printf("EBDD 体验评测（阶段 1） · 窗口 %s · 截止 %s\n", o.window, now.Format("2006-01-02 15:04:05"))
	fmt.Printf("日志目录 %s\n会话根 %s\n\n", o.logDir, strings.Join(sessionRoots(o.home, o.sessionsDir), "; "))

	var results []metricResult

	// 日志侧共享一次采集（巡检模式在采集期过滤入池，与 -warn-patterns 同源）。
	col, err := collectLogDir(o.logDir, o.window, now, parseWarnPatterns(o.warnPatterns))
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取日志目录失败（%v）；日志侧四项不可用，仅尝试会话侧\n", err)
		col = &logCollect{Window: o.window}
	}
	fmt.Printf("已解析 %d 个日志文件 / %d 行（窗口内）\n", col.FilesParsed, col.LinesParsed)

	if len(want) == 0 || want["boot"] {
		results = append(results, benchBoot(col, o)...)
	}
	if len(want) == 0 || want["open-topic"] {
		results = append(results, benchOpenTopic(col, o)...)
	}
	if len(want) == 0 || want["tab-switch"] {
		results = append(results, benchTabSwitch(col, o)...)
	}
	if len(want) == 0 || want["first-response"] {
		results = append(results, benchFirstResponse(o, now)...)
	}
	if len(want) == 0 || want["warn-patrol"] {
		results = append(results, benchWarnPatrol(col, o)...)
	}

	printResults(results)

	// 阶段 4：周趋势对比（只报告；上期文件缺失/损坏不改退出码，stderr 提示）。
	if o.trend != "" {
		if prev, err := loadPrevResults(o.trend); err != nil {
			fmt.Fprintf(os.Stderr, "趋势对比跳过：%v\n", err)
		} else {
			printTrend(o.trend, buildTrendRows(prev, results, o.trendWorsePct), o.trendWorsePct, o.window)
		}
	}

	if o.jsonOut != "" {
		b, err := json.MarshalIndent(results, "", "  ")
		if err == nil && os.WriteFile(o.jsonOut, b, 0o644) == nil {
			fmt.Printf("\nJSON 读数已写 %s\n", o.jsonOut)
		}
	}

	failed := false
	for _, r := range results {
		if r.Verdict == verdictFail {
			failed = true
		}
	}
	if failed {
		fmt.Println("\n结论：FAIL（至少一项越线，明细见上）")
		os.Exit(1)
	}
	fmt.Println("\n结论：PASS（各项门内通过；N/A/OBSERVE 项见明细）")
}

// benchBoot：M1 boot total——热路径 <1s（363 验收①），冷启动只报告不设门。
func benchBoot(col *logCollect, o options) []metricResult {
	var hot, cold []int64
	worst := map[string]int64{}
	for _, b := range col.Boots {
		if b.Hot {
			hot = append(hot, b.Total)
		} else {
			cold = append(cold, b.Total)
			for name, ms := range b.Stages {
				if ms > worst[name] {
					worst[name] = ms
				}
			}
		}
	}
	out := []metricResult{
		latencyResult("boot", "boot total（热路径=assembly hit 配对，兜底=重组相合计≤预算）",
			fmt.Sprintf("boot total < %dms（热路径）", o.bootHotMaxMS),
			"task 363 验收①", hot, float64(o.bootHotMaxMS),
			verdictNA, "窗口内无热路径 boot 样本（热路径=runtime assembly 复用命中；复用关闭或未开同键新对话时下属预期）"),
		{
			Bench: "boot", Metric: "boot total（冷启动，观测）",
			Source: "desktop.log「boot: stage timings」",
			Ref:    "tasklist 无冷启动数值验收；363① 的对照基线 3.4s",
			N:      len(cold), Verdict: verdictObserve,
			Note: fmt.Sprintf("冷启动 P50/P95/Max 见右列；最慢相 %s", topStages(worst, 3)),
		},
	}
	if len(cold) > 0 {
		sort.Slice(cold, func(i, j int) bool { return cold[i] < cold[j] })
		out[1].P50MS = float64(percentileInt64(cold, 0.50))
		out[1].P95MS = float64(percentileInt64(cold, 0.95))
		out[1].MaxMS = float64(cold[len(cold)-1])
	}
	if r := out[0]; r.N > 0 {
		out[0] = gated(r, r.P95MS, float64(o.bootHotMaxMS))
	}
	return out
}

// benchOpenTopic：M2 打开历史对话→可发消息 <2s（363 追加验收⑥，合成口径）；
// M6 open session failed 任何一条即 FAIL（任务 298 的「无日志≠未发生」面）。
//
// open session slow 行是**截断样本**（≥2s 才落日志）且与合成样本同源不同层
// （后端 openSession vs 前端 hydrate，同一次慢开常被两边各记一次）：并入
// 合成样本既重复计数又抬高 P95，故单列 census 行——任何一条都意味着有一次
// 打开越过 2s 线，比 P95 更早暴露劣化。
func benchOpenTopic(col *logCollect, o options) []metricResult {
	composed := pairOpenTopic(col.OpenTopicTotal, col.OpenTopicAnc)
	vals := make([]int64, 0, len(composed))
	for _, s := range composed {
		vals = append(vals, s.MS)
	}
	naNote := "窗口内无打开历史对话样本（未发生该操作，或前端未升级到带 summary 打点的版本）"
	out := []metricResult{
		latencyResult("open-topic", "打开历史对话→可发消息（summary total+ancillary effort 合成）",
			fmt.Sprintf("P95 < %dms", o.openTopicMaxMS),
			"task 363 追加验收⑥", vals, float64(o.openTopicMaxMS),
			verdictNA, naNote),
	}
	if r := out[0]; r.N > 0 {
		out[0] = gated(r, r.P95MS, float64(o.openTopicMaxMS))
	}
	fail := metricResult{
		Bench: "open-topic", Metric: "打开会话失败（census）",
		Source: "desktop.log「desktop: open session failed」",
		Ref:    "task 298（失败必须留痕）",
		N:      col.OpenSessionFail, Verdict: verdictPass,
		Threshold: "0 条", Note: "窗口内无失败记录",
	}
	if col.OpenSessionFail > 0 {
		fail.Verdict = verdictFail
		fail.Note = fmt.Sprintf("%d 条失败记录，任何一条即体验事故", col.OpenSessionFail)
	}
	out = append(out, fail)
	if len(col.OpenSessionSlow) > 0 {
		slow := samplesMS(col.OpenSessionSlow)
		sort.Slice(slow, func(i, j int) bool { return slow[i] < slow[j] })
		out = append(out, metricResult{
			Bench: "open-topic", Metric: "打开会话 ≥2s 慢开（census，截断样本单列）",
			Source:    "desktop.log「desktop: open session slow」（slowThresholdMs=2000 才落行）",
			Threshold: "0 条（越线才落日志；任何一条=一次打开超 2s）",
			Ref:       "task 363 追加验收⑥ / task 298 的慢路径面",
			N:         len(slow), Verdict: verdictPass,
			P50MS: float64(percentileInt64(slow, 0.50)),
			MaxMS: float64(slow[len(slow)-1]),
			Note:  fmt.Sprintf("%d 条慢开记录（与合成样本同源不同层，不并入 P95 门）", len(slow)),
		})
		if len(slow) > 0 {
			out[len(out)-1].Verdict = verdictFail
		}
	}
	return out
}

// benchTabSwitch：M3 切 tab 渲染 render P95 <150ms（任务 196 慢线）；
// firstFrame / switch-tab summary 只报告（无验收数值）；后端 stage 行是
// 越线普查（tab_timing.go 只记越线样本）。
func benchTabSwitch(col *logCollect, o options) []metricResult {
	vals := samplesMS(col.SwitchRender)
	out := []metricResult{
		latencyResult("tab-switch", "切 tab 渲染 render",
			fmt.Sprintf("P95 < %dms", o.renderMaxMS),
			"tab_timing.go slowTabSwitchLogMs（任务 196 判定线）", vals, float64(o.renderMaxMS),
			verdictNA, "窗口内无切 tab 渲染样本"),
		observeRow("tab-switch", "transcript 首帧 firstFrame（观测）",
			"desktop.log「desktop: frontend transcript first frame」", samplesMS(col.FirstFrame)),
		observeRow("tab-switch", "switch-tab summary total（观测）",
			"desktop.log「desktop: frontend switch-tab summary」", samplesMS(col.SwitchTotal)),
	}
	if r := out[0]; r.N > 0 {
		out[0] = gated(r, r.P95MS, float64(o.renderMaxMS))
	}
	if len(col.BackendCensus) > 0 {
		var b strings.Builder
		for _, c := range col.BackendCensus {
			fmt.Fprintf(&b, "%s×%d(P50=%dms,Max=%dms) ", c.Stage, c.Count, c.P50MS, c.MaxMS)
		}
		out = append(out, metricResult{
			Bench: "tab-switch", Metric: "后端 stage 越线普查",
			Source:    "desktop.log「desktop: tab switch timing」",
			Threshold: "普通 stage 150ms / switch-out 50ms（越线才落日志）",
			Ref:       "tab_timing.go（任务 196）",
			Verdict:   verdictObserve, Note: strings.TrimSpace(b.String()),
		})
	}
	return out
}

// benchFirstResponse：M5 发消息→首响，P95 ≤ 阈值（370 未定数值验收，
// 默认取遥测分桶 s_15_60 上沿 60s，可配）。数据源为 sessions/*.turns.jsonl
// turn ledger（370/doctor 同款口径），不是 events 的 turn_begin 标记。
func benchFirstResponse(o options, now time.Time) []metricResult {
	out := []metricResult{}
	roots := sessionRoots(o.home, o.sessionsDir)
	scan, err := scanLedgers(roots, o.window, now)
	if err != nil {
		out = append(out, metricResult{
			Bench: "first-response", Metric: "发消息→首响",
			Source:    "sessions/*.turns.jsonl turn_started→首个模型输出",
			Threshold: fmt.Sprintf("P95 ≤ %.0fs", o.firstResponseMaxSec),
			Ref:       "task 370（阈值默认取遥测分桶 s_15_60 上沿）",
			Verdict:   verdictNA, Note: fmt.Sprintf("会话目录不可读：%v", err),
		})
		return out
	}
	vals := make([]int64, len(scan.Turns))
	for i, t := range scan.Turns {
		vals[i] = t.MS
	}
	maxSec := o.firstResponseMaxSec * 1000
	r := latencyResult("first-response", "发消息→首响（turn_started→首个模型输出）",
		fmt.Sprintf("P95 ≤ %.0fs", o.firstResponseMaxSec),
		"task 370（阈值默认取遥测分桶 s_15_60 上沿）", vals, maxSec,
		verdictNA, fmt.Sprintf("窗口内无 turn 样本（扫描 %d 个 ledger 文件）", scan.FilesScanned))
	if r.N > 0 {
		r = gated(r, r.P95MS, maxSec)
	}
	note := fmt.Sprintf("扫描 %d 个 turn ledger", scan.FilesScanned)
	if scan.NoResponse > 0 {
		note += fmt.Sprintf("；%d 个 turn 无首响（取消/失败）", scan.NoResponse)
	}
	if scan.InFlight > 0 {
		note += fmt.Sprintf("；%d 个 turn 进行中未收盘", scan.InFlight)
	}
	if scan.Torn > 0 {
		note += fmt.Sprintf("；%d 个 ledger 尾部截断（停在截断处）", scan.Torn)
	}
	r.Note = note
	out = append(out, r)
	return out
}

// latencyResult 组装一项延迟读数（P50/P95/Max 由 vals 现算）。
// naVerdict/naNote 仅在无样本时使用。
func latencyResult(bench, metric, threshold, ref string, vals []int64, thresholdMS float64, naVerdict verdict, naNote string) metricResult {
	r := metricResult{
		Bench: bench, Metric: metric, Source: logSourceOf(bench),
		Threshold: threshold, Ref: ref, N: len(vals), Verdict: naVerdict, Note: naNote,
	}
	if len(vals) == 0 {
		return r
	}
	r.Note = "" // 有样本：无样本备注不再成立（调用方可再覆写）
	sorted := append([]int64(nil), vals...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	r.P50MS = float64(percentileInt64(sorted, 0.50))
	r.P95MS = float64(percentileInt64(sorted, 0.95))
	r.MaxMS = float64(sorted[len(sorted)-1])
	return r
}

// gated 按读数与阈值给 PASS/FAIL（P95 口径）。
func gated(r metricResult, p95MS, thresholdMS float64) metricResult {
	if p95MS <= thresholdMS {
		r.Verdict = verdictPass
	} else {
		r.Verdict = verdictFail
	}
	return r
}

func observeRow(bench, metric, source string, vals []int64) metricResult {
	r := latencyResult(bench, metric, "—", "tasklist 无数值验收（观测项）", vals, 0, verdictObserve, "窗口内无样本")
	r.Verdict = verdictObserve
	return r
}

func samplesMS(ss []sample) []int64 {
	out := make([]int64, len(ss))
	for i, s := range ss {
		out[i] = s.MS
	}
	return out
}

// topStages 取最慢的 n 个阶段名（冷启动归因线索）。
func topStages(worst map[string]int64, n int) string {
	type kv struct {
		k string
		v int64
	}
	list := make([]kv, 0, len(worst))
	for k, v := range worst {
		list = append(list, kv{k, v})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].v > list[j].v })
	parts := make([]string, 0, n)
	for i, e := range list {
		if i == n {
			break
		}
		parts = append(parts, fmt.Sprintf("%s=%dms", e.k, e.v))
	}
	if len(parts) == 0 {
		return "（无分相）"
	}
	return strings.Join(parts, " ")
}

// logSourceOf 给各 bench 的读数来源描述（latencyResult 用）。
func logSourceOf(bench string) string {
	switch bench {
	case "boot":
		return "desktop.log「boot: stage timings」"
	case "open-topic":
		return "desktop.log「open-topic summary」+「open-topic:ancillary effort」+「open session slow」"
	case "tab-switch":
		return "desktop.log「desktop: frontend tab switch render」"
	case "first-response":
		return "sessions/*.turns.jsonl turn_started→首个模型输出（370/doctor 同款口径）"
	}
	return ""
}

// printResults 文本渲染：一列一行，读数与门判定并排。
func printResults(rs []metricResult) {
	fmt.Println("─", strings.Repeat("─", 100))
	for _, r := range rs {
		readout := "—"
		switch {
		case r.Bench == "warn-patrol":
			if r.N > 0 {
				readout = "条 WARN 行"
			}
		case r.N > 0:
			readout = fmt.Sprintf("P50=%.0fms P95=%.0fms Max=%.0fms", r.P50MS, r.P95MS, r.MaxMS)
		}
		fmt.Printf("[%s] %s\n", r.Verdict, r.Metric)
		fmt.Printf("      读数 n=%d %s\n", r.N, readout)
		fmt.Printf("      阈值 %s ｜ 出处 %s\n", r.Threshold, r.Ref)
		if r.Note != "" {
			fmt.Printf("      注：%s\n", r.Note)
		}
	}
	fmt.Println("─", strings.Repeat("─", 100))
}
