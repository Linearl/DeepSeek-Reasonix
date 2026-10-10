package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// 任务581: 提交链分段留痕的观测契约——超阈值段必须留一行带 stage 名的 Warn
// （下次事故直接读出「卡在哪一段」），快段保持静默；stage fn 必须被执行。
func TestTimedSubmitStageWarnsAboveThreshold(t *testing.T) {
	prev := submitAdmissionSlowWarn
	submitAdmissionSlowWarn = time.Millisecond
	defer func() { submitAdmissionSlowWarn = prev }()

	var buf bytes.Buffer
	prevHandler := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prevHandler)

	ran := false
	timedSubmitStage("controller_submit", func() {
		ran = true
		time.Sleep(5 * time.Millisecond)
	})
	if !ran {
		t.Fatal("stage fn did not run")
	}
	if out := buf.String(); !strings.Contains(out, "submit admission stage slow") || !strings.Contains(out, "controller_submit") {
		t.Fatalf("slow-stage warn missing: %q", out)
	}
}

func TestTimedSubmitStageSilentBelowThreshold(t *testing.T) {
	prev := submitAdmissionSlowWarn
	submitAdmissionSlowWarn = time.Hour
	defer func() { submitAdmissionSlowWarn = prev }()

	var buf bytes.Buffer
	prevHandler := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prevHandler)

	ran := false
	timedSubmitStage("begin_tab_turn", func() { ran = true })
	if !ran {
		t.Fatal("stage fn did not run")
	}
	if out := buf.String(); strings.Contains(out, "submit admission stage slow") {
		t.Fatalf("fast stage must stay silent: %q", out)
	}
}
