package main

import (
	"log/slog"
	"time"
)

// 任务 298（2026-09-24 用户实测）：点击【联网调研】toast「无法打开会话」，前台
// tab 长时间不切换，desktop.log 零痕迹 —— 前端 catch 只 console.warn + toast，
// 后端错误原样返回也无人落日志，「无日志≠未发生」第 5 例。同时段 perf monitor
// 每 3s 刷一条 eventsMb=1351 超限 WARN，1.35GB 的 events 快照正落在操作窗口上。
//
// 本件给打开链路补上唯一的后端事实源：openTopicTabWithActivation 是所有打开
// 入口（project/global/topic-session，激活或不激活）的汇合点，在这里记录
//   - 失败：slog.Error "desktop: open session failed"，带 scope/root/topicId/
//     sessionPath/耗时/分相耗时/err —— 前端 toast 不再是唯一信号；
//   - 慢打开：slog.Warn "desktop: open session slow"，分相耗时能区分三嫌疑：
//     resolve/admission（路径解析与运行时准入=projection 域）、tabLock
//     （标签锁等待=196 定案的「锁内全量重放」会在这里显形）、sessionCreate/
//     profile（会话文件与档案 IO）、rest（其余收尾）。
//
// 正常快速打开不产生任何日志行（perf monitor 的采样线已覆盖常态观测）。

// openSessionSlowWarnThresholdMs 是「慢打开」的判定线。var 仅为测试可注入；
// 生产代码不得改写。
var openSessionSlowWarnThresholdMs int64 = 2000

// openSessionTrace 记录一次打开尝试的分相耗时。各相是「自上一个 mark 以来
// 的时间」，未显式 mark 的窗口并入下一个相名；finish 收尾把剩余时间记入 rest。
type openSessionTrace struct {
	scope, workspaceRoot, topicID, sessionPath string
	start                                      time.Time
	last                                       time.Time
	phases                                     map[string]int64
}

func beginOpenSessionTrace(scope, workspaceRoot, topicID, sessionPath string) *openSessionTrace {
	now := time.Now()
	return &openSessionTrace{
		scope: scope, workspaceRoot: workspaceRoot, topicID: topicID, sessionPath: sessionPath,
		start: now, last: now,
		phases: map[string]int64{},
	}
}

// mark 把自上次 mark 以来的窗口记入 phase 名下（0ms 也落键：日志里的相名
// 列表保持完整，读者能一眼看到走过了哪些相）。
func (t *openSessionTrace) mark(phase string) {
	now := time.Now()
	t.phases[phase] += now.Sub(t.last).Milliseconds()
	t.last = now
}

func (t *openSessionTrace) elapsedMs() int64 {
	return time.Since(t.start).Milliseconds()
}

// finish 在打开出口统一落日志：err 非 nil 走 Error（根因字段齐全），否则仅当
// 总耗时越过慢打开阈值才 Warn（附分相耗时）。返回 ok 表示本次是否产生了日志。
func (t *openSessionTrace) finish(err error) bool {
	t.mark("rest")
	elapsed := t.elapsedMs()
	fields := []any{
		"scope", t.scope,
		"workspaceRoot", t.workspaceRoot,
		"topicId", t.topicID,
		"sessionPath", t.sessionPath,
		"elapsedMs", elapsed,
	}
	if err != nil {
		fields = append(fields, "phasesMs", t.phases, "err", err.Error())
		slog.Error("desktop: open session failed", fields...)
		return true
	}
	if elapsed >= openSessionSlowWarnThresholdMs {
		fields = append(fields, "phasesMs", t.phases, "slowThresholdMs", openSessionSlowWarnThresholdMs)
		slog.Warn("desktop: open session slow", fields...)
		return true
	}
	return false
}
