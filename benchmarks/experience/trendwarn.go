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

// benchWarnPatrol：每个模式一行计数判定——条数 ≤ -warn-max-count（默认 0）
// 为 PASS，超出即 FAIL；日志目录缺失时整行 N/A（明示不算过也不算挂）；
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
//   - 两侧都有 P95 的延迟行：按 P95 百分比对比，恶化 ≥ worsePct 标 ↑慢化；
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
