package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// 任务 298 验收：人为复现一次打开失败 → desktop.log 有 "open session failed"
// 类记录（带根因字段），不再只有前端 toast。这里直接驱动 trace 的出口断言
// 日志措辞与字段（scope/topicId/sessionPath/耗时/分相/err）。
func TestOpenSessionTraceFailureLogsRootCauseFields(t *testing.T) {
	buf := captureQuietChannelSlog(t)

	tr := beginOpenSessionTrace("global", "", "topic-research", `C:\sessions\a.jsonl`)
	tr.mark("resolve")
	tr.mark("tabLock")
	tr.finish(errors.New("session store locked"))

	out := buf.String()
	for _, want := range []string{
		"desktop: open session failed",
		"scope=global",
		"topicId=topic-research",
		"sessionPath=",
		"elapsedMs=",
		"phasesMs=",
		"err=\"session store locked\"",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("failure log missing %q in: %q", want, out)
		}
	}
}

// 慢打开走 Warn 通道并带分相耗时——「很久才切过去」的场景里，读者要能在
// desktop.log 一次读出是锁等待（tabLock）还是会话文件 IO（sessionCreate）。
func TestOpenSessionTraceSlowSuccessWarnsWithPhases(t *testing.T) {
	buf := captureQuietChannelSlog(t)
	oldThreshold := openSessionSlowWarnThresholdMs
	openSessionSlowWarnThresholdMs = 0
	t.Cleanup(func() { openSessionSlowWarnThresholdMs = oldThreshold })

	tr := beginOpenSessionTrace("project", `C:\work`, "topic-slow", `C:\sessions\b.jsonl`)
	time.Sleep(2 * time.Millisecond)
	tr.mark("tabLock")
	if !tr.finish(nil) {
		t.Fatal("finish = false, want the slow-open warn emitted")
	}

	out := buf.String()
	if !strings.Contains(out, "desktop: open session slow") {
		t.Fatalf("slow-open warn missing in: %q", out)
	}
	if !strings.Contains(out, "tabLock:") {
		t.Fatalf("slow-open warn missing tabLock phase in: %q", out)
	}
}

// 正常快速打开必须零日志——常态观测归 perf monitor 的采样线，打开链路
// 不给 desktop.log 添噪。
func TestOpenSessionTraceFastSuccessIsSilent(t *testing.T) {
	buf := captureQuietChannelSlog(t)

	tr := beginOpenSessionTrace("global", "", "topic-fast", "")
	if tr.finish(nil) {
		t.Fatalf("fast success produced a log line: %q", buf.String())
	}
	if out := buf.String(); out != "" {
		t.Fatalf("fast success polluted the log: %q", out)
	}
}

// 分相语义：每个 mark 记的是「自上一个 mark 以来」的窗口，未 mark 的窗口
// 并入下一个相名，finish 把剩余时间收进 rest。
func TestOpenSessionTracePhaseWindowsAccumulate(t *testing.T) {
	tr := beginOpenSessionTrace("global", "", "topic-panes", "")
	time.Sleep(3 * time.Millisecond)
	tr.mark("resolve")
	time.Sleep(2 * time.Millisecond)
	tr.mark("tabLock")
	tr.finish(nil)

	if tr.phases["resolve"] < 2 {
		t.Fatalf("resolve phase = %dms, want >= 2 (first window)", tr.phases["resolve"])
	}
	if tr.phases["tabLock"] < 1 {
		t.Fatalf("tabLock phase = %dms, want >= 1 (second window)", tr.phases["tabLock"])
	}
	// resolve 与 tabLock 各自独立成窗，finish 把剩余时间收进 rest。
	if _, ok := tr.phases["rest"]; !ok {
		t.Fatalf("finish did not close the trailing rest window: %+v", tr.phases)
	}
}
