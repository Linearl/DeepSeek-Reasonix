package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// 任务692（issue #37）：后端 SetActiveTab 的慢耗时打点契约——超
// slowTabSwitchLogMs 的切换必须留一行带 tab/path/分段毫秒的慢日志（1050ms 级
// 的后端切换过去在 desktop.log 零痕迹），快切换保持静默。
func TestLogSlowSetActiveTabEmitsAboveThreshold(t *testing.T) {
	var buf bytes.Buffer
	prevHandler := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prevHandler)

	start := time.Now().Add(-slowTabSwitchLogMs * time.Millisecond)
	logSlowSetActiveTab(start, "tab-7", "local", 120, 80)

	out := buf.String()
	for _, want := range []string{
		"SetActiveTab slow",
		"tab=tab-7",
		"path=local",
		"total_ms=",
		"snapshot_ms=120",
		"save_ms=80",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("slow log missing %q:\n%s", want, out)
		}
	}
}

func TestLogSlowSetActiveTabSilentBelowThreshold(t *testing.T) {
	var buf bytes.Buffer
	prevHandler := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prevHandler)

	logSlowSetActiveTab(time.Now(), "tab-7", "local", 0, 0)
	if out := buf.String(); strings.Contains(out, "SetActiveTab slow") {
		t.Fatalf("fast switch must stay silent:\n%s", out)
	}
}
