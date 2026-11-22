package main

// 任务 414 阶段 4（生产化 · D 柱本地形态）：WARN 巡检 + 周趋势对比。
//
// 两件事都给 heartbeat 定时任务当引擎：
//   - benchWarnPatrol：把 desktop.log 的 level=WARN 按可配模式计数判定
//     （默认模式取任务书点名的 slow switch stage / cold cache compact failed）；
//   - buildTrendRows/printTrend：把本轮 -json 读数与上期 JSON 逐指标对比，
//     慢化（P95 恶化 ≥ -trend-worse-pct，或计数行增多）自动浮出。
//
// 「boot > Ns」类告警不在此处重复实现：boot 门（M1，-boot-hot-max-ms 可配）
// 就是该告警的判定面，WARN 巡检只负责 WARN 行。

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// defaultWarnPatterns 是 WARN 巡检的默认模式（任务书 414 阶段 4 点名两类）：
// slow switch stage = 任务 196 慢线面（frontend sessionMonitor 越线才落 WARN）；
// cold cache compact failed = 冷缓存压缩失败面（slog.Warn，失败必留痕）。
const defaultWarnPatterns = "slow switch stage,cold cache compact failed"

// parseWarnPatterns 把逗号分隔的模式串拆成去空白、去空项、去重的列表。
func parseWarnPatterns(s string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// warnMatches 在 msg+detail 上做子串匹配（模式可配，故不做锚定）。
func warnMatches(w *warnLine, pattern string) bool {
	return strings.Contains(w.Msg+" "+w.Detail, pattern)
}

// warnLineMatchesAny：采集期过滤用——命中任一巡检模式即入池。
func warnLineMatchesAny(w *warnLine, patterns []string) bool {
	for _, p := range patterns {
		if warnMatches(w, p) {
			return true
		}
	}
	return false
}

// slowSwitchStageNextKey 匹配 detail 里下一个「空白+key=」边界：slow switch
// stage 行的 stage 值可含空格（实录：stage=switch-tab:ancillary effort），
// 不能用 parseKVList 的空白切分——会在 "effort" 处断开并吞掉后续 ms/seq。
var slowSwitchStageNextKey = regexp.MustCompile(`\s+[A-Za-z_][\w:./-]*=`)

// slowSwitchMSRe 从 detail 里取 ms= 数值。
var slowSwitchMSRe = regexp.MustCompile(`(?:^|\s)ms=(\d+)`)

// parseSlowSwitchDetail 从 slow switch stage WARN 行的 detail
// （实录形态：tab=tab_x stage=switch-tab:ancillary effort ms=1359 seq=56）
// 取 stage 与 ms。stage= 之后到下一个「空白+key=」边界之间为值（可含空格）。
// 无 stage= 或值空白返回 ok=false。
func parseSlowSwitchDetail(detail string) (stage string, ms int64, ok bool) {
	const key = "stage="
	i := strings.Index(detail, key)
	if i < 0 {
		return "", 0, false
	}
	rest := detail[i+len(key):]
	stage = strings.TrimSpace(rest)
	if loc := slowSwitchStageNextKey.FindStringIndex(rest); loc != nil {
		stage = strings.TrimSpace(rest[:loc[0]])
	}
	if stage == "" {
		return "", 0, false
	}
	if m := slowSwitchMSRe.FindStringSubmatch(detail); m != nil {
		if v, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			ms = v
		}
	}
	return stage, ms, true
}

// warnSlowSwitchBreakdown 把巡检池里的 slow switch stage 行按 stage 细分
// （tl1685 ③，观测归因辅助）：第 6 期 83 条（上期 45，+38）只有总量，
// 归因要靠「哪些 stage 在涨」。解析失败的行（老版本无 stage= 字段）归
// "(no-stage)" 桶，不静默丢。无任何 slow switch stage 行时不产生本行
// （总量行=M9 巡检行，门在那里；本行只做细分观测）。
func warnSlowSwitchBreakdown(col *logCollect) (metricResult, bool) {
	byStage := map[string][]int64{}
	total := 0
	for i := range col.Warns {
		w := &col.Warns[i]
		if !strings.Contains(w.Msg, "slow switch stage") {
			continue
		}
		stage := w.Stage
		if stage == "" {
			stage = "(no-stage)"
		}
		byStage[stage] = append(byStage[stage], w.MS)
		total++
	}
	if total == 0 {
		return metricResult{}, false
	}
	stages := make([]string, 0, len(byStage))
	for s := range byStage {
		stages = append(stages, s)
	}
	// 条数多者在前（同名按字典序），注记只列前 8 个防止 note 无界。
	sort.Slice(stages, func(i, j int) bool {
		if len(byStage[stages[i]]) != len(byStage[stages[j]]) {
			return len(byStage[stages[i]]) > len(byStage[stages[j]])
		}
		return stages[i] < stages[j]
	})
	var b strings.Builder
	const maxStages = 8
	for i, s := range stages {
		if i == maxStages {
			fmt.Fprintf(&b, "…（共 %d 个 stage）", len(stages))
			break
		}
		vals := byStage[s]
		sort.Slice(vals, func(x, y int) bool { return vals[x] < vals[y] })
		fmt.Fprintf(&b, "%s×%d(P50=%dms,Max=%dms) ", s, len(vals),
			percentileInt64(vals, 0.50), vals[len(vals)-1])
	}
	row := metricResult{
		Bench:     "warn-patrol",
		Metric:    "WARN slow switch stage 按 stage 细分（观测）",
		Source:    "desktop.log level=WARN「desktop: frontend slow switch stage」detail 的 stage= 字段",
		Threshold: "—（观测归因；门=M9 巡检行本身，本行只回答「涨在哪些 stage」）",
		Ref:       "task 196 / tl1685 ③（第6期候选③：83 条=上期 45+38，观测归因辅助）",
		N:         total, Verdict: verdictObserve,
		Note: strings.TrimSpace(b.String()),
	}
	return row, true
}

// benchWarnPatrol：每个模式一行计数判定——条数 ≤ -warn-max-count（默认 0）
// 为 PASS，超出即 FAIL；日志目录缺失时整行 N/A（明示不算过也不算挂）；
// 池里有 slow switch stage 行时追加按 stage 细分的观测行（tl1685 ③）；
// 巡检池发生过截断（WarnTruncated）追加 FAIL 行显式化「读数不完整」。
func benchWarnPatrol(col *logCollect, o options) []metricResult {
	patterns := parseWarnPatterns(o.warnPatterns)
	out := make([]metricResult, 0, len(patterns)+1)
	for _, p := range patterns {
		r := metricResult{
			Bench:     "warn-patrol",
			Metric:    fmt.Sprintf("WARN 巡检「%s」", p),
			Source:    "desktop.log level=WARN（msg+detail 子串匹配）",
			Threshold: fmt.Sprintf("≤%d 条（-warn-max-count 可配）", o.warnMaxCount),
			Ref:       "任务 414 阶段 4 · heartbeat WARN 巡检",
			Verdict:   verdictPass,
		}
		if col.FilesParsed == 0 {
			r.Verdict = verdictNA
			r.Note = "窗口内无日志文件（巡检不可用）"
			out = append(out, r)
			continue
		}
		n, first := 0, (*warnLine)(nil)
		for i := range col.Warns {
			if warnMatches(&col.Warns[i], p) {
				if first == nil {
					first = &col.Warns[i]
				}
				n++
			}
		}
		r.N = n
		switch {
		case n > o.warnMaxCount:
			r.Verdict = verdictFail
			r.Note = fmt.Sprintf("首条 %s msg=%q（%d 条越线）", first.At.Format("01-02 15:04:05"), first.Msg, n)
		case n > 0:
			r.Note = fmt.Sprintf("%d 条（在门内）", n)
		}
		out = append(out, r)
	}
	if row, ok := warnSlowSwitchBreakdown(col); ok {
		out = append(out, row)
	}
	if col.WarnTruncated {
		out = append(out, metricResult{
			Bench:     "warn-patrol",
			Metric:    "WARN 巡检池截断（读数不完整）",
			Source:    fmt.Sprintf("采集层 warnCollectCap=%d", warnCollectCap),
			Threshold: "0 次截断（丢行即 FAIL，不许静默假 PASS）",
			Ref:       "任务 414 阶段 4",
			N:         warnCollectCap,
			Verdict:   verdictFail,
			Note:      "匹配行达采集上限，后续行未入池——本窗口各模式计数偏小，须缩窗或收窄 -warn-patterns 后重跑",
		})
	}
	return out
}

// trendRow 是一期趋势对比的一行（文本与测试共用的数据形态）。
type trendRow struct {
	Bench  string
	Metric string
	Prev   string // 上期读数（"P95=1200ms" 或 "n=7"）
	Cur    string // 本期读数
	Delta  string // "+20.0%" 或 "+2 条"
	State  string // ↑慢化 / ↓改善 / ≈ / ↑增多 / ↓减少
}

// buildTrendRows 把本期读数与上期逐指标配对（键 = bench+metric）：
//   - 两侧都有 Ratio 的比值行（tl1685 ④）：按比值百分比对比，恶化 ≥
//     worsePct 标 ↑慢化（比值行不走条数对比，条数单指标会跨期误判）；
//   - 其余两侧都有 P95 的延迟行：按 P95 百分比对比，恶化 ≥ worsePct 标 ↑慢化；
//   - 其余（census / WARN 计数行）：按 n 对比，增多即 ↑（越多越坏）；
//   - 任一侧无样本（n=0）或上期缺该指标 → 不出行（新增/消失不算慢化）。
func buildTrendRows(prev, cur []metricResult, worsePct float64) []trendRow {
	idx := make(map[string]metricResult, len(prev))
	for _, p := range prev {
		if p.N > 0 {
			idx[p.Bench+"\x00"+p.Metric] = p
		}
	}
	rows := []trendRow{}
	for _, c := range cur {
		if c.N == 0 {
			continue
		}
		p, ok := idx[c.Bench+"\x00"+c.Metric]
		if !ok {
			continue
		}
		row := trendRow{Bench: c.Bench, Metric: c.Metric}
		switch {
		case c.Ratio > 0 && p.Ratio > 0:
			// 比值行（tl1685 ④）：按比值百分比对比。比值行不走条数对比——
			// 条数单指标会跨期误判（第6期慢读条数翻倍但单条变快，实为切换
			// 频次增多非劣化）。
			row.Prev = fmt.Sprintf("比值=%.2f", p.Ratio)
			row.Cur = fmt.Sprintf("比值=%.2f", c.Ratio)
			d := (c.Ratio - p.Ratio) / p.Ratio * 100
			row.Delta = fmt.Sprintf("%+.1f%%", d)
			switch {
			case d >= worsePct:
				row.State = "↑慢化"
			case d <= -worsePct:
				row.State = "↓改善"
			default:
				row.State = "≈"
			}
		case c.P95MS > 0 && p.P95MS > 0:
			// 延迟行：P95 百分比对比。
			row.Prev = fmt.Sprintf("P95=%.0fms", p.P95MS)
			row.Cur = fmt.Sprintf("P95=%.0fms", c.P95MS)
			d := (c.P95MS - p.P95MS) / p.P95MS * 100
			row.Delta = fmt.Sprintf("%+.1f%%", d)
			switch {
			case d >= worsePct:
				row.State = "↑慢化"
			case d <= -worsePct:
				row.State = "↓改善"
			default:
				row.State = "≈"
			}
		default:
			// 计数行（census / WARN 巡检）：条数对比，增多即慢化。
			row.Prev = fmt.Sprintf("n=%d", p.N)
			row.Cur = fmt.Sprintf("n=%d", c.N)
			d := c.N - p.N
			row.Delta = fmt.Sprintf("%+d 条", d)
			switch {
			case d > 0:
				row.State = "↑增多"
			case d < 0:
				row.State = "↓减少"
			default:
				row.State = "≈"
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// loadPrevResults 读上期 -json 读数（[]metricResult 数组形态）。
func loadPrevResults(path string) ([]metricResult, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rs []metricResult
	if err := json.Unmarshal(b, &rs); err != nil {
		return nil, fmt.Errorf("%s 不是上期读数 JSON（数组形态）: %w", path, err)
	}
	return rs, nil
}

// printTrend 输出趋势对比段（只报告，不影响 PASS/FAIL 退出码）。
func printTrend(prevPath string, rows []trendRow, worsePct float64, window time.Duration) {
	fmt.Printf("─ 周趋势对比 · vs %s（本次窗口 %s，慢化线 P95≥+%.0f%% 或计数增多）\n",
		prevPath, window, worsePct)
	if len(rows) == 0 {
		fmt.Println("      无可对比项（上期无样本或指标不匹配——首期报告无属正常）")
		fmt.Println("─", strings.Repeat("─", 100))
		return
	}
	for _, r := range rows {
		fmt.Printf("      [%s] %s · %s：%s → %s（%s）\n", r.State, r.Bench, r.Metric, r.Prev, r.Cur, r.Delta)
	}
	fmt.Println("─", strings.Repeat("─", 100))
}
