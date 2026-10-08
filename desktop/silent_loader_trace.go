package main

import (
	"log/slog"
	"os"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

// 任务 475-E（X6 模式 E「静默降级」，2026-10-05 定性）：save 侧五类埋点齐全、
// load 侧零计时——后台 loader 的错误被 `if err != nil { return }` 吞掉、大会话
// 全量重放 20-30s 也无一行日志，排障只能靠 X6/DAG 两轮专项调研逆向定位。
// 本件给 DAG 调研报告点名的五个静默调用点（历史搜索收集、话题标题升级、
// ↑提示历史、时间戳回填、历史页 v4 未命中回退）和同族后台调用点（记忆建议、
// 会话提示续载、时间叠层全量回退）补观测，出口两种日志：
//   - 失败：slog.Warn "desktop: silent loader failed"（callpoint 标记 + err）——
//     失败低频，不设阈值，这是「静默降级」的第一信号；
//   - 慢加载：slog.Warn "desktop: silent loader slow"，默认 ≥250ms 才打
//     （X6-E 建议阈值），带 callpoint 标记与消息数。
//
// 正常快速加载零日志——P18-R1 load 缓存命中时这些调用点本就是毫秒级，
// 不给 desktop.log 添噪（P19 的 66MB/h 教训）。每行日志都带 callpoint 字段
// （feature 标记），按调用点即可归因；REASONIX_SILENT_LOADER_TRACE=0 整体
// 关闭（env 开关先例：REASONIX_DAG_LOAD_CACHE）。

// silentLoaderSlowWarnThresholdMs 是「慢加载」的判定线。var 仅为测试可注入；
// 生产代码不得改写。
var silentLoaderSlowWarnThresholdMs int64 = 250

// silentLoaderTraceEnabled reads the task-475-E gate. Default on;
// REASONIX_SILENT_LOADER_TRACE=0 restores the fully silent load path.
func silentLoaderTraceEnabled() bool {
	return os.Getenv("REASONIX_SILENT_LOADER_TRACE") != "0"
}

// silentLoaderTrace times one background loader call. begin right before the
// agent.LoadSession / agent.LoadSessionUserMessages call, finish right after;
// finish never changes the caller's control flow, it only logs.
type silentLoaderTrace struct {
	callpoint   string
	sessionPath string
	start       time.Time
}

func beginSilentLoader(callpoint, sessionPath string) *silentLoaderTrace {
	return &silentLoaderTrace{callpoint: callpoint, sessionPath: sessionPath, start: time.Now()}
}

// finish logs the outcome: err != nil always warns (silent-degradation signal,
// low frequency); otherwise only an over-threshold duration warns. messages is
// the loaded message count when known (-1 when the call failed before load).
func (t *silentLoaderTrace) finish(err error, messages int) {
	if !silentLoaderTraceEnabled() {
		return
	}
	elapsedMs := time.Since(t.start).Milliseconds()
	if err != nil {
		slog.Warn("desktop: silent loader failed",
			"callpoint", t.callpoint,
			"sessionPath", t.sessionPath,
			"elapsedMs", elapsedMs,
			"err", err.Error())
		return
	}
	if elapsedMs >= silentLoaderSlowWarnThresholdMs {
		slog.Warn("desktop: silent loader slow",
			"callpoint", t.callpoint,
			"sessionPath", t.sessionPath,
			"elapsedMs", elapsedMs,
			"messages", messages,
			"slowThresholdMs", silentLoaderSlowWarnThresholdMs)
	}
}

// silentLoadSessionSnapshot is the traced form of the recurring
// `agent.LoadSession(path)` + `Snapshot()` + swallow-error fallback triple:
// on failure it logs through the same callpoint-tagged channel and reports
// ok=false, so callers keep their previous value exactly as before.
func silentLoadSessionSnapshot(callpoint, path string) ([]provider.Message, bool) {
	tr := beginSilentLoader(callpoint, path)
	loaded, err := agent.LoadSession(path)
	if err != nil || loaded == nil {
		tr.finish(err, -1)
		return nil, false
	}
	msgs := loaded.Snapshot()
	tr.finish(nil, len(msgs))
	return msgs, true
}
