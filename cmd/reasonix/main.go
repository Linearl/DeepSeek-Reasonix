// Command reasonix is a config- and plugin-driven coding agent CLI.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"reasonix/internal/cli"
	"reasonix/internal/config"
	"reasonix/internal/crashreport"
	"reasonix/internal/homemigrate"
	"reasonix/internal/plugin"

	// Blank imports wire compile-time built-ins into their registries.
	_ "reasonix/internal/provider/anthropic"
	_ "reasonix/internal/provider/openai"
	_ "reasonix/internal/provider/responses"
	_ "reasonix/internal/tool/builtin"
)

// Build identity injected via -ldflags (see Makefile). version remains the
// single-line contract for `reasonix --version`; gitCommit/buildTimeUTC feed
// `reasonix version --verbose` / `--json` without embedding config paths.
var (
	version      = "dev"
	gitCommit    = ""
	buildTimeUTC = ""
)

// runCLI is the CLI entry; tests may stub it. Production routes through
// RunWithBuildInfo so ldflags metadata is available to version --verbose/--json.
var runCLI = func(args []string, buildVersion string) int {
	return cli.RunWithBuildInfo(args, cli.BuildInfo{
		Version:      buildVersion,
		GitCommit:    gitCommit,
		BuildTimeUTC: buildTimeUTC,
	})
}

// 任务 849（846 遗留第 4 条「CLI 跟随」）：CLI 进程启动时应用 home 指针
// 文件，与 desktop/main.go 的启动链接线同构——否则迁移向导把 home 指到新
// 目录后，CLI 进程仍解析到 OS 默认 home，正是 846 风险 1 展示过的双 home
// 割裂。两处依赖提成包级 var 供测试注入（main_test.go 的 runCLI var stub
// 同款惯例）；生产路径零注入直用真函数。
var (
	cliMigrationInProgress = homemigrate.MigrationInProgress
	cliApplyHomePointerEnv = config.ApplyHomePointerEnv
)

// applyHomePointerForCLI mirrors the desktop startup wiring (task 846): refuse
// to start while a migration mutex is live (the home tree may be mid-copy),
// then apply the pointer file. Returns the process exit code; 0 = continue.
// Zero behaviour by default: no pointer file (and no live migration) means
// byte-for-byte the same startup as before task 846/849, and an explicit
// REASONIX_HOME still wins (test/CI contract).
func applyHomePointerForCLI(stderr io.Writer) int {
	if cliMigrationInProgress() {
		fmt.Fprintln(stderr, "reasonix: a home migration is in progress; refusing to start (task 846)")
		return 3
	}
	if status, detail := cliApplyHomePointerEnv(); status != config.HomePointerStatusNone {
		// Applied/fallback/env_override 各值一行 stderr 提示；none 静默。
		// stderr 是 CLI/serve 的日志通道，不进 stdout 协议流。
		fmt.Fprintf(stderr, "reasonix: home pointer applied (status=%s detail=%s)\n", string(status), detail)
	}
	return 0
}

func main() {
	plugin.SetMCPClientVersion(version)
	if code := applyHomePointerForCLI(os.Stderr); code != 0 {
		os.Exit(code)
	}
	os.Exit(runWithCrashCapture(os.Args[1:], version))
}

func runWithCrashCapture(args []string, buildVersion string) (exitCode int) {
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = crashreport.CapturePanic(config.ReasonixHomeDir(), buildVersion, recovered, debug.Stack())
			panic(recovered)
		}
	}()
	return runCLI(args, buildVersion)
}
