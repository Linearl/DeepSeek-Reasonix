package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// tl 1163（启动链分阶段打点）：boot:summary 的一次启动段级 breakdown 落盘。
//
// 任务 824 的 boot:summary 只进 desktop.log——4MB 滚动文件里捞一次启动的
// 段级读数既不便查也不便做 p50/p90 统计。本件把同一次 emit 的汇总渲染成
// 一行式 breakdown，追加写 cwd 的 startup-metrics.log（对齐竞品惯例：
// ZCode `[startup]` 日志点、DSH 每行带 +ms；TriliumNext 同名
// startup-metrics.log）。多次启动逐行累积，正好是「连续 N 次冷启动
// p50/p90」的原始数据源。
//
// 纪律（与 824 一致）：
//   - 纯观测零行为：只在 bootTimelineEmitSummary 的既有触发点多一次文件
//     append；写失败静默（debug 行），绝不反噬启动路径；
//   - 不引入新实验开关（tl 1163 正文硬约束①）：每次启动恰一次 write 调用
//     （~几百字节），开销可忽略；
//   - cwd 落盘，不写用户数据目录（正文硬约束；dev/wails dev 的 cwd 分别是
//     仓库根与 desktop/，均已 gitignore；装机场景 cwd 不可写时读数缺失是
//     已知取舍——诊断不能反过来要求启动路径具备写权限重试逻辑）。

// startupMetricsFileName is the per-boot breakdown sink (cwd, gitignored).
const startupMetricsFileName = "startup-metrics.log"

// startupMetricsPath returns the sink path relative to the process cwd.
func startupMetricsPath() string {
	return filepath.Join(".", startupMetricsFileName)
}

// renderStartupMetricsLineLocked renders the one-line breakdown for this
// boot's summary. Single source of truth: it renders bootSummaryAttrsLocked's
// attrs (wall_ms / marks / p1 / legs percentiles / frontend arrivals), so the
// slog line and the file line can never drift apart. Caller holds obs.mu.
//
// Format (one line, key=value, space-separated; values containing spaces are
// double-quoted):
//
//	[startup] ts=2026-10-26T12:34:56+08:00 trigger=frontend-ready+10s version=… channel=… wall_ms=941 tabs=2 marks="main_enter=2ms …" p1="…" legs=2 …
func renderStartupMetricsLineLocked(obs *bootTimeline, trigger string) string {
	attrs := bootSummaryAttrsLocked(obs)
	var b strings.Builder
	b.WriteString("[startup] ts=")
	b.WriteString(time.Now().Format(time.RFC3339))
	b.WriteString(" trigger=" + trigger)
	b.WriteString(" version=" + version)
	b.WriteString(" channel=" + channel)
	for i := 0; i+1 < len(attrs); i += 2 {
		key, _ := attrs[i].(string)
		b.WriteString(" " + key + "=" + renderStartupMetricsValue(attrs[i+1]))
	}
	return b.String()
}

// renderStartupMetricsValue renders one attr value; strings containing spaces
// (the marks/p1 stage lists) are quoted so the line stays field-split safe.
func renderStartupMetricsValue(v any) string {
	switch value := v.(type) {
	case string:
		if strings.ContainsAny(value, " \"") {
			return strconv.Quote(value)
		}
		return value
	case int64:
		return strconv.FormatInt(value, 10)
	case int:
		return strconv.Itoa(value)
	default:
		return fmt.Sprint(value)
	}
}

// appendStartupMetricsFile appends one breakdown line, best effort. Every
// failure path is silent at Info level (Debug only): a diagnostic sink must
// never turn a finished boot summary into an error.
func appendStartupMetricsFile(path, line string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		slog.Debug("startup metrics: append unavailable", "path", path, "err", err)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		slog.Debug("startup metrics: append failed", "path", path, "err", err)
	}
}
