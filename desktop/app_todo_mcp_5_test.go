package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/jobs"
	"reasonix/internal/plugin"
	"reasonix/internal/sandbox"
	"reasonix/internal/tool"
)

func TestClearMCPServerAuthenticationClearsConfigAndFailure(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "reasonix.toml"), []byte(`
[[plugins]]
name = "figma"
type = "http"
url = "https://mcp.figma.com/mcp?access_token=abc&workspace=main"
headers = { Authorization = "Bearer ${FIGMA_TOKEN}", "X-Org" = "team" }
env = { FIGMA_TOKEN = "${FIGMA_TOKEN}", DEBUG = "1" }
tier = "lazy"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	host := plugin.NewHost()
	host.RecordFailure(plugin.Spec{Name: "figma", Type: "http", URL: "https://mcp.figma.com/mcp"}, errors.New("connect: 401 unauthorized"))
	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: host}), "")
	defer app.activeCtrl().Close()

	if err := app.ClearMCPServerAuthentication("figma"); err != nil {
		t.Fatalf("ClearMCPServerAuthentication: %v", err)
	}
	if failures := host.Failures(); len(failures) != 0 {
		t.Fatalf("failure should be cleared: %+v", failures)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Plugins[0]
	if p.URL != "https://mcp.figma.com/mcp?workspace=main" {
		t.Fatalf("url = %q", p.URL)
	}
	if _, ok := p.Headers["Authorization"]; ok {
		t.Fatalf("auth header should be removed: %v", p.Headers)
	}
	if p.Headers["X-Org"] != "team" {
		t.Fatalf("ordinary header should be preserved: %v", p.Headers)
	}
	if _, ok := p.Env["FIGMA_TOKEN"]; ok {
		t.Fatalf("auth env should be removed: %v", p.Env)
	}
	if p.Env["DEBUG"] != "1" {
		t.Fatalf("ordinary env should be preserved: %v", p.Env)
	}
	view := app.Capabilities()
	for _, s := range view.Servers {
		if s.Name == "figma" {
			if s.Status != "deferred" || s.StartIntent != "automatic" || s.RuntimeState != "idle" || s.AuthStatus != "possible" {
				t.Fatalf("figma should return to background possible auth: %+v", s)
			}
			return
		}
	}
	t.Fatalf("figma MCP missing from Capabilities: %+v", view.Servers)
}

func TestUpdateMCPServerMigratesLegacyTierInProjectSource(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "reasonix.toml"), []byte(`
[[plugins]]
name = "playwright"
command = "npx"
args = ["-y", "@playwright/mcp"]
env = { TOKEN = "${PLAYWRIGHT_TOKEN}" }
tier = "lazy"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer func() {
		if c := app.activeCtrl(); c != nil {
			c.Close()
		}
	}()
	entry, ok, err := desktopEffectiveMCPServer(dir, "playwright")
	if err != nil || !ok {
		t.Fatalf("load playwright entry: found=%v err=%v", ok, err)
	}
	if err := config.DefaultMCPActivationStore().SetServerEnabled(entry, dir, false); err != nil {
		t.Fatal(err)
	}
	app.activeTab().disabledMCP["playwright"] = ServerView{Name: "playwright", Status: "disabled", Enabled: false}

	if err := app.UpdateMCPServer("playwright", MCPServerInput{
		Name:      "playwright",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{"server.js"},
	}); err != nil {
		t.Fatalf("UpdateMCPServer: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Plugins[0].Command; got != "node" {
		t.Fatalf("updated command = %q, want node", got)
	}
	if got := cfg.Plugins[0].Env["TOKEN"]; got != "${PLAYWRIGHT_TOKEN}" {
		t.Fatalf("env TOKEN = %q, want preserved env", got)
	}
	userCfg := config.LoadForEdit(config.UserConfigPath())
	if _, ok := findPluginEntry(userCfg.Plugins, "playwright"); ok {
		t.Fatalf("project plugin should not be copied to user config: %+v", userCfg.Plugins)
	}
	projectCfg := config.LoadForEdit(filepath.Join(dir, "reasonix.toml"))
	projectPlugin, ok := findPluginEntry(projectCfg.Plugins, "playwright")
	if !ok {
		t.Fatalf("playwright should remain in project config: %+v", projectCfg.Plugins)
	}
	if projectPlugin.Command != "node" || projectPlugin.Env["TOKEN"] != "${PLAYWRIGHT_TOKEN}" {
		t.Fatalf("project plugin after update = %+v", projectPlugin)
	}
	if projectPlugin.Tier != "" {
		t.Fatalf("project plugin tier = %q, want migrated empty", projectPlugin.Tier)
	}
	view := app.Capabilities()
	for _, s := range view.Servers {
		if s.Name == "playwright" {
			if s.Status != "disabled" {
				t.Fatalf("updated MCP status = %q, want disabled without a readiness probe; server = %+v", s.Status, s)
			}
			if s.Command != "node" || len(s.Args) != 1 || s.Args[0] != "server.js" {
				t.Fatalf("server command not refreshed: %+v", s)
			}
			return
		}
	}
	t.Fatalf("playwright MCP missing from Capabilities: %+v", view.Servers)
}

func TestUpdateMCPServerSplitsPastedCommandLine(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "reasonix.toml"), []byte(`
[[plugins]]
name = "playwright"
command = "npx"
args = ["-y", "@playwright/mcp"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()
	app.activeTab().disabledMCP["playwright"] = ServerView{}

	if err := app.UpdateMCPServer("playwright", MCPServerInput{
		Name:      "playwright",
		Transport: "stdio",
		Command:   "npx -y @modelcontextprotocol/server-filesystem .",
	}); err != nil {
		t.Fatalf("UpdateMCPServer: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Plugins[0]
	if p.Command != "npx" {
		t.Fatalf("command = %q, want npx", p.Command)
	}
	if got := strings.Join(p.Args, "\x00"); got != strings.Join([]string{"-y", "@modelcontextprotocol/server-filesystem", "."}, "\x00") {
		t.Fatalf("args = %v", p.Args)
	}
}

func TestUpdateMCPServerRejectsReconnectFailureWithoutPersisting(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "reasonix.toml"), []byte(`
[[plugins]]
name = "broken"
command = "reasonix-old-missing-mcp-binary"
tier = "background"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()

	if err := app.UpdateMCPServer("broken", MCPServerInput{
		Name:      "broken",
		Transport: "stdio",
		Command:   "reasonix-missing-mcp-binary",
	}); err == nil {
		t.Fatal("UpdateMCPServer should reject an unusable candidate")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Plugins[0].Command; got != "reasonix-old-missing-mcp-binary" {
		t.Fatalf("failed update command = %q, want original command", got)
	}
	if got := cfg.Plugins[0].Tier; got != "" {
		t.Fatalf("loaded legacy tier = %q, want normalized empty", got)
	}
	if !mcpFailed(app.activeCtrl(), "broken") {
		t.Fatalf("Host.Failures() = %+v, want broken failure recorded", app.activeCtrl().Host().Failures())
	}
	view := app.Capabilities()
	for _, s := range view.Servers {
		if s.Name == "broken" {
			if s.Status != "failed" {
				t.Fatalf("server status = %q, want failed; server = %+v", s.Status, s)
			}
			if s.Command != "reasonix-old-missing-mcp-binary" || s.Tier != "background" {
				t.Fatalf("failed candidate leaked into server config: %+v", s)
			}
			return
		}
	}
	t.Fatalf("broken MCP missing from Capabilities: %+v", view.Servers)
}

func TestReconnectMCPServerClearsInitializingPlaceholderAndRecordsFailure(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "reasonix.toml"), []byte(`
[[plugins]]
name = "codegraph"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := tool.NewRegistry()
	reg.Add(desktopFakeTool{name: "mcp__codegraph__connect"})
	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost(), Registry: reg}), "")
	defer app.activeCtrl().Close()

	view := app.Capabilities()
	foundIdle := false
	for _, s := range view.Servers {
		if s.Name == "codegraph" {
			foundIdle = true
			if s.Status != "deferred" || s.StartIntent != "automatic" || s.RuntimeState != "idle" {
				t.Fatalf("initial codegraph server = %+v, want automatic idle background state", s)
			}
		}
	}
	if !foundIdle {
		t.Fatalf("codegraph missing before reconnect: %+v", view.Servers)
	}
	if _, ok := reg.Get("mcp__codegraph__connect"); !ok {
		t.Fatal("test setup expected stale codegraph connect placeholder")
	}

	if err := app.ReconnectMCPServer("codegraph"); err == nil || !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("ReconnectMCPServer error = %v, want missing command", err)
	}
	if _, ok := reg.Get("mcp__codegraph__connect"); ok {
		t.Fatalf("stale codegraph placeholder still registered after reconnect failure; names=%v", reg.Names())
	}
	if !mcpFailed(app.activeCtrl(), "codegraph") {
		t.Fatalf("Host.Failures() = %+v, want codegraph failure recorded", app.activeCtrl().Host().Failures())
	}

	view = app.Capabilities()
	for _, s := range view.Servers {
		if s.Name == "codegraph" {
			if s.Status != "failed" || s.Error == "" {
				t.Fatalf("codegraph after failed reconnect = %+v, want failed with error", s)
			}
			return
		}
	}
	t.Fatalf("codegraph missing after reconnect: %+v", view.Servers)
}

func TestSetMCPServerTierPreservesProjectSourceAndRecordsConnectFailure(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "reasonix.toml"), []byte(`
[[plugins]]
name = "broken"
command = "reasonix-missing-mcp-binary"
tier = "lazy"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer func() {
		if c := app.activeCtrl(); c != nil {
			c.Close()
		}
	}()

	if err := app.SetMCPServerTier("broken", "background"); err != nil {
		t.Fatalf("SetMCPServerTier legacy binding: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Plugins[0].Tier; got != "" {
		t.Fatalf("saved tier = %q, want migrated empty", got)
	}
	userCfg := config.LoadForEdit(config.UserConfigPath())
	if _, ok := findPluginEntry(userCfg.Plugins, "broken"); ok {
		t.Fatalf("project plugin should not be copied to user config: %+v", userCfg.Plugins)
	}
	projectCfg := config.LoadForEdit(filepath.Join(dir, "reasonix.toml"))
	projectPlugin, ok := findPluginEntry(projectCfg.Plugins, "broken")
	if !ok {
		t.Fatalf("broken should remain in project config: %+v", projectCfg.Plugins)
	}
	if projectPlugin.Tier != "" {
		t.Fatalf("project plugin tier = %q, want migrated empty", projectPlugin.Tier)
	}
	if !mcpFailed(app.activeCtrl(), "broken") {
		t.Fatalf("Host.Failures() = %+v, want broken failure recorded", app.activeCtrl().Host().Failures())
	}
	view := app.Capabilities()
	for _, s := range view.Servers {
		if s.Name == "broken" {
			if s.Status != "failed" {
				t.Fatalf("server status = %q, want failed; server = %+v", s.Status, s)
			}
			if s.Tier != "background" {
				t.Fatalf("server tier = %q, want background so radio selection does not jump back", s.Tier)
			}
			return
		}
	}
	t.Fatalf("broken MCP missing from Capabilities: %+v", view.Servers)
}

func TestSetMCPServerTierRejectsBackgroundJobsBeforeSavingConfig(t *testing.T) {
	// 673L 断言修正（2026-10-09）：原断言要求夹具里的 legacy 行 tier = "lazy"
	// 原样保留，但它活不过夹具搭建——newBackgroundJobController→control.New 的
	// 引导装载会执行既定的 on-disk legacy tier 迁移（migrateLegacyMCPTiersFile 抹掉 plugins 下所有 tier 行，不限 lazy），原断言在被测调用之前就已被破坏。 被测保证本身完好（探针实证）：SetMCPServerTier 守卫先拒后存、文件零改动。 现断言改为与夹具搭建后的基线字节一致——若守卫失效先存盘， UpsertPluginInSourceForRoot 会把 updated.Tier="background" 序列化进文件， 字节比对即红；被拒调用则应零落盘。
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
[[plugins]]
name = "broken"
command = "reasonix-missing-mcp-binary"
tier = "lazy"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.setTestCtrl(newBackgroundJobController(t, "mcp-tier-job"), "")
	baseline, baselineErr := os.ReadFile(config.UserConfigPath())
	if baselineErr != nil {
		t.Fatalf("read baseline config: %v", baselineErr)
	}

	err := app.SetMCPServerTier("broken", "background")
	if err == nil || !strings.Contains(err.Error(), "stop background jobs") {
		t.Fatalf("SetMCPServerTier with background job error = %v, want active-work guard", err)
	}
	data, readErr := os.ReadFile(config.UserConfigPath())
	if readErr != nil {
		t.Fatalf("read config: %v", readErr)
	}
	if string(data) != string(baseline) {
		t.Fatalf("plugin config changed after rejected tier update:\nbaseline:\n%s\ngot:\n%s", baseline, data)
	}
}

func TestCapabilitiesMigratesFailedMCPConfiguredTierAfterRestart(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "reasonix.toml"), []byte(`
[[plugins]]
name = "broken"
command = "reasonix-missing-mcp-binary"
tier = "eager"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()
	recordMCPFailure(app.activeCtrl(), config.PluginEntry{
		Name:    "broken",
		Command: "reasonix-missing-mcp-binary",
		Tier:    "eager",
	}, errors.New("connect: missing binary"))

	view := app.Capabilities()
	for _, s := range view.Servers {
		if s.Name == "broken" {
			if s.Status != "failed" {
				t.Fatalf("server status = %q, want failed; server = %+v", s.Status, s)
			}
			if s.Tier != "background" {
				t.Fatalf("server tier = %q, want migrated background default", s.Tier)
			}
			if !s.Configured {
				t.Fatalf("server configured = false, want true; server = %+v", s)
			}
			return
		}
	}
	t.Fatalf("broken MCP missing from Capabilities: %+v", view.Servers)
}

func TestRunShellForTabRoutesToRequestedTab(t *testing.T) {
	isolateDesktopUserDirs(t)

	activeEvents := make(chan event.Event, 16)
	inactiveEvents := make(chan event.Event, 16)
	// Pin the interpreter: the contract under test is tab routing, not host
	// shell discovery. Leaving Shell zero makes RunShell resolve it inside the
	// measured window, and on Windows that discovery (probe spawns) plus the first bash spawn alone has been measured at ~3.3s — over the turn deadline on an idle machine, so the test red-flagged machine speed.
	shell := sandbox.ResolveShell("", "", nil)
	activeCtrl := control.New(control.Options{Sink: event.FuncSink(func(e event.Event) { activeEvents <- e }), Shell: shell})
	inactiveCtrl := control.New(control.Options{Sink: event.FuncSink(func(e event.Event) { inactiveEvents <- e }), Shell: shell})
	defer activeCtrl.Close()
	defer inactiveCtrl.Close()

	app := &App{
		tabs: map[string]*WorkspaceTab{
			"active":   {ID: "active", Scope: "global", Ctrl: activeCtrl, Ready: true},
			"inactive": {ID: "inactive", Scope: "global", Ctrl: inactiveCtrl, Ready: true},
		},
		tabOrder:    []string{"active", "inactive"},
		activeTabID: "active",
	}

	app.RunShellForTab("inactive", "echo route-test")

	sawDispatch := false
	// The bound catches a turn that never completes (a routing bug), not the
	// host's process-spawn speed: a bash spawn on Windows can legitimately take
	// seconds under a Defender scan, so the deadline must clear that variance while still failing a hung turn quickly.
	deadline := time.After(10 * time.Second)
	for {
		select {
		case e := <-inactiveEvents:
			if e.Kind == event.ToolDispatch && strings.Contains(e.Tool.Args, "route-test") {
				sawDispatch = true
			}
			if e.Kind == event.TurnDone {
				if !sawDispatch {
					t.Fatal("inactive tab finished without receiving shell dispatch")
				}
				select {
				case active := <-activeEvents:
					t.Fatalf("active tab received event for inactive shell: %+v", active)
				default:
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for inactive shell turn")
		}
	}
}

func TestRunShellForTabStaysBoundDuringRapidProjectTabSwitching(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping shell cancellation integration test in short mode")
	}

	isolateDesktopUserDirs(t)

	projectA := t.TempDir()
	projectB := t.TempDir()
	globalRoot := t.TempDir()
	shellEvents := make(chan event.Event, 64)
	projectEvents := make(chan event.Event, 64)
	globalEvents := make(chan event.Event, 64)
	shellCtrl := control.New(control.Options{
		Sink:          event.FuncSink(func(e event.Event) { shellEvents <- e }),
		WorkspaceRoot: projectA,
	})
	projectCtrl := control.New(control.Options{
		Sink:          event.FuncSink(func(e event.Event) { projectEvents <- e }),
		WorkspaceRoot: projectB,
	})
	globalCtrl := control.New(control.Options{
		Sink:          event.FuncSink(func(e event.Event) { globalEvents <- e }),
		WorkspaceRoot: globalRoot,
	})
	defer shellCtrl.Close()
	defer projectCtrl.Close()
	defer globalCtrl.Close()

	app := &App{
		tabs: map[string]*WorkspaceTab{
			"shell":     {ID: "shell", Scope: "project", WorkspaceRoot: projectA, Ctrl: shellCtrl, Ready: true},
			"project-b": {ID: "project-b", Scope: "project", WorkspaceRoot: projectB, Ctrl: projectCtrl, Ready: true},
			"global":    {ID: "global", Scope: "global", WorkspaceRoot: globalRoot, Ctrl: globalCtrl, Ready: true},
		},
		tabOrder:    []string{"shell", "project-b", "global"},
		activeTabID: "shell",
	}

	marker := "shell-route-marker.txt"
	if err := app.RunShellForTab("shell", longRunningMarkerCommand(marker)); err != nil {
		t.Fatalf("RunShellForTab: %v", err)
	}
	waitForShellDispatch(t, shellEvents, marker)
	waitForFile(t, filepath.Join(projectA, marker), "shell")

	for range 8 {
		if err := app.SetActiveTab("project-b"); err != nil {
			t.Fatalf("SetActiveTab(project-b): %v", err)
		}
		if err := app.SetActiveTab("global"); err != nil {
			t.Fatalf("SetActiveTab(global): %v", err)
		}
		if err := app.SetActiveTab("shell"); err != nil {
			t.Fatalf("SetActiveTab(shell): %v", err)
		}
	}
	if err := app.SetActiveTab("project-b"); err != nil {
		t.Fatalf("SetActiveTab(project-b final): %v", err)
	}
	app.CancelTab("shell")

	cancelled := false
	deadline := time.After(15 * time.Second)
	for {
		select {
		case e := <-shellEvents:
			if e.Kind == event.ToolResult && e.Tool.Name == "bash" {
				cancelled = e.Tool.Err != ""
			}
			if e.Kind == event.TurnDone {
				if !cancelled {
					t.Fatal("shell tab finished without a cancelled shell result")
				}
				if _, err := os.Stat(filepath.Join(projectB, marker)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("shell marker appeared in project-b workspace: %v", err)
				}
				if got := activeTabIDForTest(app); got != "project-b" {
					t.Fatalf("active tab = %q, want project-b after background shell cancel", got)
				}
				assertNoEvents(t, projectEvents, "project-b")
				assertNoEvents(t, globalEvents, "global")
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for shell tab cancellation")
		}
	}
}

func longRunningMarkerCommand(marker string) string {
	if sandbox.ResolveShell("", "", nil).Kind == sandbox.ShellPowerShell {
		return fmt.Sprintf("Set-Content -LiteralPath %s -Value shell; Start-Sleep -Seconds 30", marker)
	}
	return fmt.Sprintf("printf shell > %s; sleep 30", marker)
}

func waitForShellDispatch(t *testing.T, ch <-chan event.Event, marker string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e := <-ch:
			if e.Kind == event.ToolDispatch && strings.Contains(e.Tool.Args, marker) {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for shell dispatch")
		}
	}
}

func activeTabIDForTest(app *App) string {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return app.activeTabID
}

func assertNoEvents(t *testing.T, ch <-chan event.Event, name string) {
	t.Helper()
	select {
	case e := <-ch:
		t.Fatalf("%s received event while shell ran in another tab: %+v", name, e)
	default:
	}
}

type blockingRunner struct {
	started chan struct{}
	release chan struct{}
}

func (r *blockingRunner) Run(ctx context.Context, _ string) error {
	close(r.started)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.release:
		return nil
	}
}

func startNonCooperativeSessionJob(t *testing.T, jm *jobs.Manager, sessionPath string) func() {
	t.Helper()
	started := make(chan struct{})
	release := make(chan struct{})
	jm.StartForSession(agent.BranchID(sessionPath), "bash", "stuck job", func(ctx context.Context, _ io.Writer) (string, error) {
		close(started)
		<-ctx.Done()
		<-release
		return "", ctx.Err()
	})
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("background job never started")
	}
	released := false
	return func() {
		if released {
			return
		}
		released = true
		close(release)
	}
}

func waitNotRunning(t *testing.T, ctrl control.SessionAPI) {
	t.Helper()
	// Windows release runners can take more than one second to schedule the
	// controller's asynchronous completion while the full desktop suite is
	// active. Keep a bounded responsiveness check without treating scheduler delay as a leaked controller.
	deadline := time.Now().Add(5 * time.Second)
	for ctrl.Running() {
		if time.Now().After(deadline) {
			t.Fatal("controller still running")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newBackgroundJobController(t *testing.T, label string) *control.Controller {
	t.Helper()
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, label+".jsonl")
	jm := jobs.NewManager(event.Discard)
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: path, Label: "test", Jobs: jm})
	t.Cleanup(ctrl.Close)
	jm.StartForSession(agent.BranchID(path), "bash", label, func(ctx context.Context, _ io.Writer) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	return ctrl
}

func hasLevel(levels []string, want string) bool {
	return slices.Contains(levels, want)
}

func hasCommand(cmds []CommandInfo, name string) bool {
	for _, cmd := range cmds {
		if cmd.Name == name {
			return true
		}
	}
	return false
}

func hasDirEntry(entries []DirEntry, name string) bool {
	for _, entry := range entries {
		if entry.Name == name {
			return true
		}
	}
	return false
}

func TestSessionActionsWithoutControllerReturnError(t *testing.T) {
	app := &App{tabs: map[string]*WorkspaceTab{}}
	if err := app.NewSession(); err == nil {
		t.Error("NewSession with no controller must surface an error, not silently no-op")
	}
	if _, err := app.ClearSession(); err == nil {
		t.Error("ClearSession with no controller must surface an error")
	}

	app = &App{
		tabs:        map[string]*WorkspaceTab{"t1": {ID: "t1", StartupErr: "boot exploded"}},
		activeTabID: "t1",
	}
	err := app.NewSession()
	if err == nil || !strings.Contains(err.Error(), "boot exploded") {
		t.Errorf("error should carry the tab's startup failure, got %v", err)
	}
}

// Prompt history scanning tests

func identityPromptDisplay(text string) string { return text }
