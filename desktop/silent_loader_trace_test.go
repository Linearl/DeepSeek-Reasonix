package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// 任务 475-E 验收：静默 loader 调用可观测（feature 标记日志）。逐条对应：
//   - 失败必有 callpoint 标记的日志（「静默降级」第一信号）；
//   - ≥250ms 的慢加载有 callpoint + 耗时 + 消息数；
//   - 正常快速加载零日志（不给 desktop.log 添噪，P19 教训）；
//   - REASONIX_SILENT_LOADER_TRACE=0 整体关闭（env 开关先例）。

func TestSilentLoaderFailureLogsWithCallpoint(t *testing.T) {
	buf := captureQuietChannelSlog(t)

	tr := beginSilentLoader("history_search_collect", `C:\sessions\a.jsonl`)
	tr.finish(errors.New("log replay refused"), -1)

	out := buf.String()
	for _, want := range []string{
		"desktop: silent loader failed",
		"callpoint=history_search_collect",
		"sessionPath=",
		"elapsedMs=",
		"err=\"log replay refused\"",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("failure log missing %q in: %q", want, out)
		}
	}
}

func TestSilentLoaderSlowSuccessWarnsWithThreshold(t *testing.T) {
	buf := captureQuietChannelSlog(t)
	oldThreshold := silentLoaderSlowWarnThresholdMs
	silentLoaderSlowWarnThresholdMs = 0
	t.Cleanup(func() { silentLoaderSlowWarnThresholdMs = oldThreshold })

	tr := beginSilentLoader("topic_title_from_session", `C:\sessions\big.jsonl`)
	tr.finish(nil, 3880)

	out := buf.String()
	for _, want := range []string{
		"desktop: silent loader slow",
		"callpoint=topic_title_from_session",
		"messages=3880",
		"slowThresholdMs=0",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("slow log missing %q in: %q", want, out)
		}
	}
}

func TestSilentLoaderFastSuccessIsSilent(t *testing.T) {
	buf := captureQuietChannelSlog(t)

	tr := beginSilentLoader("history_time_backfill", `C:\sessions\small.jsonl`)
	tr.finish(nil, 3)

	if out := buf.String(); out != "" {
		t.Fatalf("fast success polluted the log: %q", out)
	}
}

func TestSilentLoaderTraceEnvKillSwitch(t *testing.T) {
	t.Setenv("REASONIX_SILENT_LOADER_TRACE", "0")
	if silentLoaderTraceEnabled() {
		t.Fatal("trace enabled with REASONIX_SILENT_LOADER_TRACE=0")
	}
	buf := captureQuietChannelSlog(t)

	tr := beginSilentLoader("prompt_history_event_log", `C:\sessions\x.jsonl`)
	tr.finish(errors.New("suppressed"), -1)
	tr2 := beginSilentLoader("prompt_history_event_log", `C:\sessions\x.jsonl`)
	tr2.finish(nil, 10)

	if out := buf.String(); out != "" {
		t.Fatalf("kill switch did not suppress logging: %q", out)
	}
}

func TestSilentLoadSessionSnapshotPreservesKeepCurrentOnFailure(t *testing.T) {
	// 失败必须报 ok=false（调用方保留原值），不得把 nil 塞回去覆盖现有历史。
	_, ok := silentLoadSessionSnapshot("history_page_v4_fallback", filepath.Join(t.TempDir(), "missing.jsonl"))
	if ok {
		t.Fatal("silentLoadSessionSnapshot on a missing session reported ok=true")
	}
}
