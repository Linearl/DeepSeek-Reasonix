package main

import (
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// 任务 451：history 阶段分相打点。desktop.log 里 `stage=startup:history` 一类
// 数字只覆盖"前端 loadLatest 整段往返"，读盘/解析/投影哪一步占大头无法回答。
// historySliceTrace 把一次 HistorySliceForTab 调用内部的候选阶段（索引装载、
// 窗口读取、行转换、派生状态等）逐段计时，慢调用（超过阈值）合并成一行
// greppable 的 Info 日志，快调用降为 Debug，保持 desktop.log 的噪音水位不变。
//
// 约定：
//   - 零值/nil 接收者安全：所有方法在 t == nil 时照常执行 fn 但不记录，测试
//     与旧调用点（不传 trace）无需改动即保持原行为。
//   - 只计时不改行为：trace 不参与任何分页/缓存决策。
type historySliceTrace struct {
	startedAt time.Time
	parts     []string
	source    string // 命中的读路径分支：index/scan/event-log/live-index/…
}

// historySliceSlowLogMs 与前端时机线的 slowTabSwitchLogMs（150ms）对齐：低于
// 阈值的切片调用不值得一行 desktop.log，用户感知为"慢"的才会出现。
const historySliceSlowLogMs = 150

func newHistorySliceTrace() *historySliceTrace {
	return &historySliceTrace{startedAt: time.Now()}
}

// step 记录一个已完成阶段的耗时。
func (t *historySliceTrace) step(name string, d time.Duration) {
	if t == nil {
		return
	}
	t.parts = append(t.parts, fmt.Sprintf("%s=%dms", name, d.Milliseconds()))
}

// run 计时执行 fn 并把耗时记为名为 name 的阶段。fn 返回值经闭包捕获带出。
func (t *historySliceTrace) run(name string, fn func()) {
	if t == nil {
		fn()
		return
	}
	startedAt := time.Now()
	fn()
	t.step(name, time.Since(startedAt))
}

// runErr 是 run 的 error 透传形。
func (t *historySliceTrace) runErr(name string, fn func() error) error {
	if t == nil {
		return fn()
	}
	startedAt := time.Now()
	err := fn()
	t.step(name, time.Since(startedAt))
	return err
}

// markSource 记录本次切片命中的读路径分支，随 emit 一并输出。
func (t *historySliceTrace) markSource(source string) {
	if t == nil {
		return
	}
	t.source = source
}

// elapsed 返回从建 trace 起的总耗时（毫秒取整）。
func (t *historySliceTrace) elapsed() int64 {
	if t == nil {
		return 0
	}
	return time.Since(t.startedAt).Milliseconds()
}

// emit 输出本次切片的分相汇总：慢调用（>= historySliceSlowLogMs）一行 Info，
// 其余 Debug。与 reportStageSummary 一样坚持单行 key=value，便于 grep 重建。
func (t *historySliceTrace) emit(tabID string) {
	if t == nil {
		return
	}
	total := t.elapsed()
	attrs := []any{"tab", tabID, "ms", total}
	if t.source != "" {
		attrs = append(attrs, "source", t.source)
	}
	if len(t.parts) > 0 {
		attrs = append(attrs, "phases", strings.Join(t.parts, " "))
	}
	if total >= historySliceSlowLogMs {
		slog.Info("desktop: history slice timing", attrs...)
	} else {
		slog.Debug("desktop: history slice timing", attrs...)
	}
}
