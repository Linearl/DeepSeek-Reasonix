package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v2/pkg/options/linux"

	"reasonix/internal/sandbox"
)

// shellProbeCalls counts how many times the TestMain fallback probe hook was
// consulted. Informational only (the fallback probe spawns no subprocess); a
// count > 0 printed at teardown means some informational surface
// (ResolveShellForView) rebuilt the inventory — never an execution-path probe.
var shellProbeCalls int

func TestParseDesktopLaunchArgsStripsLegacySafeMode(t *testing.T) {
	got := parseDesktopLaunchArgs([]string{"launch", "--detach", "--safe-mode", "--other"})
	if !got.LegacySafeModeArg {
		t.Fatal("--safe-mode should still be recognized for stripping")
	}
	if parseDesktopLaunchArgs([]string{"--other"}).LegacySafeModeArg {
		t.Fatal("unrelated argument must not set legacy safe-mode flag")
	}
}

func TestParseDesktopLaunchArgsRemoteWindow(t *testing.T) {
	got := parseDesktopLaunchArgs([]string{
		"--other",
		remoteWindowTicketArgPrefix + ".remote-window-123",
		remoteWindowHostArgPrefix + "abcd1234",
		remoteWindowOwnerArgPrefix + "0123456789abcdef0123456789abcdef",
		remoteWindowParentArgPrefix + "4242",
	})
	if got.RemoteWindowTicket != ".remote-window-123" {
		t.Fatalf("RemoteWindowTicket = %q", got.RemoteWindowTicket)
	}
	if got.RemoteWindowHostKey != "abcd1234" {
		t.Fatalf("RemoteWindowHostKey = %q", got.RemoteWindowHostKey)
	}
	if got.RemoteWindowOwnerID != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("RemoteWindowOwnerID = %q", got.RemoteWindowOwnerID)
	}
	if got.RemoteWindowParentPID != 4242 {
		t.Fatalf("RemoteWindowParentPID = %d", got.RemoteWindowParentPID)
	}
	if got.LegacySafeModeArg {
		t.Fatal("remote window args unexpectedly enabled legacy safe mode")
	}
}

func TestLifecycleDiagnosticsUsePreWailsOwnershipGate(t *testing.T) {
	mainSource, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	beforeRun, _, ok := strings.Cut(string(mainSource), "err := wails.Run")
	if !ok {
		t.Fatal("main.go no longer contains the Wails run boundary")
	}
	if !strings.Contains(beforeRun, "prepareDesktopDiagnostics(app)") {
		t.Fatal("main process must claim diagnostics ownership before Wails starts")
	}

	// 任务 756: App.startup lives in app_lifecycle.go (lifecycle domain file).
	appSource, err := os.ReadFile("app_lifecycle.go")
	if err != nil {
		t.Fatal(err)
	}
	_, afterStartup, ok := strings.Cut(string(appSource), "func (a *App) startup(ctx context.Context) {")
	if !ok {
		t.Fatal("app_lifecycle.go no longer contains App.startup")
	}
	startupBody, _, ok := strings.Cut(afterStartup, "\n}")
	// 任务 756: startup delegates its process-wide hook wiring (including the
	// lifecycle consumption) to startupRegisterProcessHelpers, called
	// synchronously from startup — the ownership contract is unchanged.
	if !ok || !strings.Contains(startupBody, "a.startupRegisterProcessHelpers()") {
		t.Fatal("startup must keep the process-wide hook wiring on the Wails OnStartup path")
	}
	lifecycleSource := string(appSource) + "\n" + startupBody
	if !strings.Contains(lifecycleSource, "initializeLifecycleDiagnostics(a)") {
		t.Fatal("previous lifecycle consumption must remain owned by Wails OnStartup")
	}
}

// TestMain isolates user config/state/cache dirs for the whole package. Without
// this, tests that persist desktop state, sessions, cache, or CLI-style config
// can leak into the developer's real Reasonix directories.
func TestMain(m *testing.M) {
	// Task 188: the deliberate-crash acceptance re-executes this same test
	// binary with REASONIX_CRASH_TEST=panic — arm the crash channel first, then
	// raise the unrecovered panic BEFORE any scratch dir is created, so the
	// child's death artifact lands on the pinned REASONIX_CRASH_TEST_FILE path.
	// An unknown mode falls through (never crash the suite on a typo); unset is
	// the production default.
	if mode := crashTestMode(); mode != "" {
		if mode == "panic" {
			installFatalCrashOutput()
		}
		runCrashTest(mode)
	}
	// Task 696: the suspend/resume acceptance re-executes this same test
	// binary as a child running the real watchdog (see
	// hang_suspend_experiment_test.go). Run the child branch before the
	// scratch-dir isolation below so the child inherits the parent-pinned
	// REASONIX_STATE_HOME the parent inspects afterwards.
	if scenario := hangSuspendChildScenario(); scenario != "" {
		os.Exit(runHangSuspendChildScenario(scenario))
	}
	dir, err := os.MkdirTemp("", "reasonix-desktop-test")
	if err != nil {
		os.Exit(1)
	}
	os.Setenv("HOME", dir)
	os.Setenv("REASONIX_CREDENTIALS_STORE", "file")
	os.Setenv("USERPROFILE", dir)
	os.Setenv("XDG_CONFIG_HOME", dir+"/config")
	os.Setenv("REASONIX_STATE_HOME", dir+"/state")
	os.Setenv("REASONIX_CACHE_HOME", dir+"/cache")
	// 任务 1032: config.UserConfigPath() 只认 REASONIX_HOME（其余全是 fallback），
	// 不设它，写用户 config 的测试会落到开发者真实用户目录（连带真实
	// lease/catalog/boot 快照与 ${HOME} 展开）。
	os.Setenv("REASONIX_HOME", dir+"/reasonix")
	os.Setenv("AppData", dir)
	// Tests fail closed for telemetry. Any test that expects a request must
	// replace the relevant endpoint with an httptest.Server explicitly.
	crashEndpoint = "http://127.0.0.1:0/v1/report"
	pingEndpoint = "http://127.0.0.1:0/v1/ping"
	metricsEndpoint = "http://127.0.0.1:0/v1/metrics"
	// Neutralize the Wails runtime-event bridge for the whole test binary:
	// outside a running Wails app, runtime.EventsEmit log.Fatals on the plain
	// contexts tests use, killing the process from any emitting code path.
	// Tests that assert on runtime events install their own capture through
	// the per-instance runtimeEvents.emit hook, which takes precedence.
	runtimeEventsEmitFallback = func(context.Context, string, ...any) {}
	// 任务 1862（M1 治本，挂死排查报告 20261128）：整包钉死 shell 解析，
	// 消除全包所有 probeBash 子进程探测。根因：每个走 boot 链
	// （buildTabController / boot.build）的测试都可能触发同步 shell 全量探测
	// （probeBash 逐候选起真实 bash.exe 子进程，单次 1.8-7s），而测试环境下
	// 探测缓存被结构性击穿——每个测试 fresh REASONIX_HOME 清空持久 probe 缓存、
	// 进程级 inventory 30s TTL 过期即全量重探——3925 测试 × 1.87s ≈ 122 分钟，
	// 30m 门禁超时的 victim 无差别（挂住用例每轮不同、单跑全绿）。
	// 机制：tl1735 现成的 PinResolveShellForTest 在 ResolveShell 入口短路
	// （internal/sandbox/shell_testhook.go），internal/boot golden 基线同款先例。
	// 语义核查（tl1862 前置终查）：desktop 691 个 *_test.go 无一依赖真实探测
	// 语义；唯二直接调用 sandbox.ResolveShell 的是 app_todo_mcp_5_test.go
	// （:357 / :493），两处注释明言「the contract under test is tab routing,
	// not host shell discovery」——钉 Shell{Kind: bash} 与其意图一致且更快。
	restoreShellPin := sandbox.PinResolveShellForTest(sandbox.Shell{Kind: sandbox.ShellBash, Path: "bash"})
	// 兜底：ResolveShellForView（settings/reasoning 显示面）不走 pin
	// （shell.go 的 ResolveShellForView 直接读 inventory）。把 probeBash 本体
	// 换成恒真假探针——即使个别信息面路径仍重建 inventory，也零 bash 子进程。
	// 计数仅作观测，不做断言（信息面探测不属病态）。
	restoreShellProbe := sandbox.PinShellProbeForTest(func(string) bool {
		shellProbeCalls++
		return true
	})
	code := m.Run()
	restoreShellProbe()
	restoreShellPin()
	if shellProbeCalls > 0 {
		fmt.Fprintf(os.Stderr, "desktop-test-shell-probe-fallback: %d inventory rebuild(s) served by the stub probe (no bash subprocess spawned)\n", shellProbeCalls)
	}
	os.RemoveAll(dir)
	os.Exit(code)
}

// TestTestMainShellPinIsActive asserts the TestMain shell pin is in effect
// (任务 1862 验收②): ResolveShell must return the pinned value exactly, and
// must do so without ever consulting probeBash — the probe hook is temporarily
// replaced by a sentinel that flags any consultation. This is the in-process
// evidence that "probeBash subprocesses == 0" during the whole suite: the pin
// short-circuits at the ResolveShell entry, before any inventory/probe work.
// Kept non-Parallel: it runs exclusively in the sequential test phase, so no
// Parallel test can observe the sentinel probe.
func TestTestMainShellPinIsActive(t *testing.T) {
	restore := sandbox.PinShellProbeForTest(func(string) bool {
		t.Error("probeBash was consulted: the TestMain shell pin must short-circuit every ResolveShell call")
		return true
	})
	t.Cleanup(restore)

	got := sandbox.ResolveShell("", "", nil)
	want := sandbox.Shell{Kind: sandbox.ShellBash, Path: "bash"}
	if got != want {
		t.Fatalf("ResolveShell(\"\", \"\", nil) = %+v, want pinned %+v — TestMain pin not in effect", got, want)
	}
}

func TestDesktopTestTelemetryEndpointsAreFailClosed(t *testing.T) {
	for name, endpoint := range map[string]string{
		"crash":   crashEndpoint,
		"ping":    pingEndpoint,
		"metrics": metricsEndpoint,
	} {
		if strings.Contains(endpoint, "crash.reasonix.io") {
			t.Fatalf("%s test endpoint targets production: %s", name, endpoint)
		}
	}
}

func TestWindowsWebview2GPUDisabled(t *testing.T) {
	oldChannel := channel
	t.Cleanup(func() {
		channel = oldChannel
		os.Unsetenv(disableWebview2GPUEnv)
		os.Unsetenv(legacyDisableWebview2GPUEnv)
	})

	tests := []struct {
		name    string
		channel string
		env     string
		want    bool
	}{
		{name: "stable default keeps gpu", channel: "stable", want: false},
		{name: "preview default disables gpu", channel: "preview", want: true},
		{name: "legacy canary default disables gpu", channel: "canary", want: true},
		{name: "env enables fallback", channel: "stable", env: "1", want: true},
		{name: "env disables canary fallback", channel: "canary", env: "0", want: false},
		{name: "truthy env", channel: "stable", env: "yes", want: true},
		{name: "falsey env", channel: "canary", env: "off", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel = tt.channel
			if tt.env == "" {
				os.Unsetenv(disableWebview2GPUEnv)
			} else {
				os.Setenv(disableWebview2GPUEnv, tt.env)
			}
			if got := windowsWebview2GPUDisabled(); got != tt.want {
				t.Fatalf("windowsWebview2GPUDisabled() = %v, want %v", got, tt.want)
			}
		})
	}
	os.Unsetenv(disableWebview2GPUEnv)
	os.Setenv(legacyDisableWebview2GPUEnv, "1")
	if !windowsWebview2GPUDisabled() {
		t.Fatal("legacy WebView2 GPU override was not accepted")
	}
}

func TestLinuxWebviewGpuPolicyDisablesGpuWithoutAccessibleRenderNode(t *testing.T) {
	glob := filepath.Join(t.TempDir(), "renderD*")

	if got := linuxWebviewGpuPolicy(glob); got != linux.WebviewGpuPolicyNever {
		t.Fatalf("linuxWebviewGpuPolicy() = %v, want %v", got, linux.WebviewGpuPolicyNever)
	}
}

func TestLinuxWebviewGpuPolicyDisablesGpuForInaccessibleRenderNode(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "renderD128"), 0o700); err != nil {
		t.Fatal(err)
	}

	if got := linuxWebviewGpuPolicy(filepath.Join(dir, "renderD*")); got != linux.WebviewGpuPolicyNever {
		t.Fatalf("linuxWebviewGpuPolicy() = %v, want %v", got, linux.WebviewGpuPolicyNever)
	}
}

func TestLinuxWebviewGpuPolicyKeepsOnDemandWithAccessibleRenderNode(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "renderD128"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if got := linuxWebviewGpuPolicy(filepath.Join(dir, "renderD*")); got != linux.WebviewGpuPolicyOnDemand {
		t.Fatalf("linuxWebviewGpuPolicy() = %v, want %v", got, linux.WebviewGpuPolicyOnDemand)
	}
}
