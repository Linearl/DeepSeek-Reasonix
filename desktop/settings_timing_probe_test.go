package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// 任务 1098（阶段 1 补打点）验收：Settings() 加载链路的「分项耗时」打点必须
// 真实发出——块名恒定（config_load / write_roots_load / active_ctrl_lock /
// view_build / web_search / credentials_revision / providers /
// sandbox_shell_scan），可被 desktop.log 端 grep「desktop: settings step」
// 检索到。不关心具体毫秒数值（那是被测机器的属性），只关心「打点确实存在、
// 块名齐全、锁等待与执行时长两个字段都在」——否则又回到「查不出原因」的原点。
func TestSettingsEmitsPerBlockTimingLogs(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	_ = NewApp().Settings()

	out := buf.String()
	if !strings.Contains(out, "desktop: settings view loaded") {
		t.Fatalf("missing the task-739 total-time line; got:\n%s", out)
	}
	if !strings.Contains(out, "desktop: settings step") {
		t.Fatalf("missing task-1098 per-block timing lines; got:\n%s", out)
	}
	for _, block := range []string{
		"config_load",
		"write_roots_load",
		"active_ctrl_lock",
		"view_build",
		"web_search",
		"credentials_revision",
		"providers",
		"sandbox_shell_scan",
	} {
		if !strings.Contains(out, "block="+block) {
			t.Errorf("missing timing block %q; got:\n%s", block, out)
		}
	}
	// 锁等待与执行时长两类时间必须分开记：只有把 lock_wait_ms 和 ms 两个字段
	// 都写进日志，下一例才能区分「等锁被拖慢」与「纯计算慢」。
	if !strings.Contains(out, "lock_wait_ms=") {
		t.Errorf("missing lock_wait_ms field (needed to separate lock wait from pure compute); got:\n%s", out)
	}
	if !strings.Contains(out, " ms=") {
		t.Errorf("missing ms field (per-block execution time); got:\n%s", out)
	}
}
