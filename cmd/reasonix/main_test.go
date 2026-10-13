package main

import (
	"strings"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/crashreport"
)

func TestRunWithCrashCaptureRecordsAndReraises(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	previous := runCLI
	t.Cleanup(func() { runCLI = previous })
	secret := "private prompt from panic"
	runCLI = func([]string, string) int { panic(secret) }

	func() {
		defer func() {
			if recovered := recover(); recovered != secret {
				t.Fatalf("reraised panic = %#v", recovered)
			}
		}()
		runWithCrashCapture([]string{"run"}, "v1.20.0")
	}()

	reports, err := crashreport.List(config.ReasonixHomeDir())
	if err != nil || len(reports) != 1 {
		t.Fatalf("captured reports=%d err=%v", len(reports), err)
	}
	// Token-like function and file basenames may be redacted by the current
	// privacy filter. Keep asserting that the top frame is the test call site
	// without requiring its pre-redaction basename.
	if got := reports[0].Report.TopFrame; !strings.Contains(got, "_test.go:") {
		t.Fatalf("top frame = %q, want sanitized panic call site in a test file", got)
	}
	preview, err := crashreport.Preview(reports[0].Report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(preview), secret) {
		t.Fatalf("panic value leaked into report: %s", preview)
	}
}

func TestRunWithCrashCapturePassesThroughExitCode(t *testing.T) {
	previous := runCLI
	t.Cleanup(func() { runCLI = previous })
	runCLI = func(args []string, version string) int {
		if len(args) != 1 || args[0] != "version" || version != "v1.20.0" {
			t.Fatalf("runCLI args=%v version=%q", args, version)
		}
		return 17
	}
	if got := runWithCrashCapture([]string{"version"}, "v1.20.0"); got != 17 {
		t.Fatalf("exit code=%d", got)
	}
}

// 任务 849 — CLI 启动链 home 指针接线的钉：迁移互斥锁存活 ⇒ 拒启 exit 3
// （对齐 desktop/main.go 的 846 口径：home 树可能正处于 copy 中途，带病启动
// 会读写两份 home）；指针应用成功/回落 ⇒ exit 0 + stderr 一行状态；无指针
// （none）⇒ exit 0 且完全静默（缺省零行为）。依赖经包级 var 注入（runCLI
// var stub 同款惯例），测试不触碰真实 OS home。
func TestApplyHomePointerForCLI(t *testing.T) {
	origInProgress := cliMigrationInProgress
	origApply := cliApplyHomePointerEnv
	t.Cleanup(func() {
		cliMigrationInProgress = origInProgress
		cliApplyHomePointerEnv = origApply
	})

	t.Run("live migration mutex refuses to start with exit 3", func(t *testing.T) {
		cliMigrationInProgress = func() bool { return true }
		cliApplyHomePointerEnv = func() (config.HomePointerApplyStatus, string) {
			t.Fatal("ApplyHomePointerEnv must not run while a migration is in progress")
			return config.HomePointerStatusNone, ""
		}
		var stderr strings.Builder
		if got := applyHomePointerForCLI(&stderr); got != 3 {
			t.Fatalf("exit code = %d, want 3", got)
		}
		if out := stderr.String(); !strings.Contains(out, "home migration is in progress") {
			t.Fatalf("stderr = %q, want a migration refusal note", out)
		}
	})

	t.Run("applied and fallback announce one stderr line and continue", func(t *testing.T) {
		cliMigrationInProgress = func() bool { return false }
		for _, tc := range []struct {
			status config.HomePointerApplyStatus
			detail string
		}{
			{config.HomePointerStatusApplied, "/tmp/new-home"},
			{config.HomePointerStatusFallback, "pointer home is missing"},
			{config.HomePointerStatusEnvOverride, ""},
		} {
			cliApplyHomePointerEnv = func() (config.HomePointerApplyStatus, string) { return tc.status, tc.detail }
			var stderr strings.Builder
			if got := applyHomePointerForCLI(&stderr); got != 0 {
				t.Fatalf("status %s: exit code = %d, want 0", tc.status, got)
			}
			if out := stderr.String(); !strings.Contains(out, "status="+string(tc.status)) {
				t.Fatalf("status %s: stderr = %q, want an applied note", tc.status, out)
			}
		}
	})

	t.Run("no pointer stays silent and continues", func(t *testing.T) {
		cliMigrationInProgress = func() bool { return false }
		cliApplyHomePointerEnv = func() (config.HomePointerApplyStatus, string) {
			return config.HomePointerStatusNone, ""
		}
		var stderr strings.Builder
		if got := applyHomePointerForCLI(&stderr); got != 0 {
			t.Fatalf("exit code = %d, want 0", got)
		}
		if out := stderr.String(); out != "" {
			t.Fatalf("stderr = %q, want byte-for-byte silence for the zero-behaviour default", out)
		}
	})
}
