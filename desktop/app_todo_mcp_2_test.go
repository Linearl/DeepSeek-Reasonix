package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/mcplaunch"
	"reasonix/internal/plugin"
	"reasonix/internal/pluginpkg"
	"reasonix/internal/tool"
)

func TestCapabilitiesShowsDefaultMCPAsAutomaticIdleNotDisabled(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
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
	defer func() {
		if c := app.activeCtrl(); c != nil {
			c.Close()
		}
	}()

	view := app.Capabilities()
	for _, s := range view.Servers {
		if s.Name == "playwright" {
			if s.Status != "deferred" || s.StartIntent != "automatic" || s.RuntimeState != "idle" {
				t.Fatalf("default MCP view = %+v, want deferred automatic idle", s)
			}
			return
		}
	}
	t.Fatalf("playwright MCP missing from Capabilities: %+v", view.Servers)
}

func TestCapabilitiesIncludesInstalledPlugins(t *testing.T) {
	isolateDesktopUserDirs(t)
	reasonixHome := config.ReasonixHomeDir()
	root := filepath.Join(reasonixHome, "plugins", "superpowers")
	if err := os.MkdirAll(filepath.Join(root, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "skills", "plan"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "plan", "SKILL.md"), []byte("---\ndescription: Plan work\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".codex-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codex-plugin", "plugin.json"), []byte(`{
  "name": "superpowers",
  "version": "6.1.0",
  "description": "Planning workflows",
  "skills": "./skills/"
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := pluginpkg.Upsert(reasonixHome, pluginpkg.InstalledPlugin{
		Name:         "superpowers",
		Root:         "plugins/superpowers",
		Version:      "6.1.0",
		Description:  "Planning workflows",
		ManifestKind: "codex",
		Enabled:      true,
	}); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	plugins := app.Capabilities().Plugins
	if len(plugins) != 1 || plugins[0].Name != "superpowers" || plugins[0].Skills != 1 {
		t.Fatalf("Capabilities().Plugins = %+v", plugins)
	}
	if len(plugins[0].SkillDetails) != 1 || plugins[0].SkillDetails[0].Invocation != "/superpowers:plan" {
		t.Fatalf("Capabilities().Plugins skill details = %+v", plugins[0].SkillDetails)
	}
}

func TestDesktopSharedHostProjectMCPConnectsWithoutLaunchApproval(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping background MCP boot integration test in short mode")
	}

	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)

	srv := desktopMCPHTTPServer(t)
	defer srv.Close()
	if err := os.WriteFile(filepath.Join(dir, "reasonix.toml"), fmt.Appendf(nil, `
[[plugins]]
name = "h"
type = "http"
url = %q
`, srv.URL), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sharedHost := plugin.NewHost()
	defer sharedHost.Close()
	ctrl, err := boot.Build(ctx, boot.Options{
		WorkspaceRoot: dir,
		SessionDir:    filepath.Join(dir, "sessions"),
		SharedHost:    sharedHost,
		Stderr:        io.Discard,
	})
	if err != nil {
		t.Fatalf("boot.Build: %v", err)
	}
	defer ctrl.Close()

	// The connect itself is loopback HTTP to an in-process server; the poll
	// only has to outlast scheduler starvation under a loaded test run, not
	// any network or spawn — three seconds was measured red-flagging machine speed there, so match the boot context's own bound.
	clientDeadline := time.Now().Add(10 * time.Second)
	for !sharedHost.HasClient("h") && time.Now().Before(clientDeadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if !sharedHost.HasClient("h") {
		t.Fatalf("project MCP did not connect automatically; failures=%+v", sharedHost.Failures())
	}
	for _, failure := range sharedHost.Failures() {
		if failure.Name == "h" && failure.RequiresLaunchApproval {
			t.Fatalf("project MCP unexpectedly requested launch approval: %+v", failure)
		}
	}

	app := NewApp()
	app.tabs = map[string]*WorkspaceTab{
		"test": {
			ID:            "test",
			Scope:         "global",
			WorkspaceRoot: dir,
			Ready:         true,
			Ctrl:          ctrl,
			SharedHostKey: dir,
			disabledMCP:   map[string]ServerView{},
		},
	}
	app.activeTabID = "test"

	view := app.MCPServers()
	if len(view) != 1 || view[0].Name != "h" || view[0].Status != "connected" || view[0].RuntimeState != "ready" || view[0].RequiresLaunchApproval {
		t.Fatalf("MCPServers() = %+v, want trusted connected project h", view)
	}
}

func TestProjectMCPViewIsTrustedAndKeepsProjectSource(t *testing.T) {
	entry := config.PluginEntry{Name: "project", Source: config.MCPSourceProjectConfig}
	connected := withPluginConfig(ServerView{Name: entry.Name, Status: "connected"}, entry)
	if connected.RequiresLaunchApproval {
		t.Fatalf("connected project MCP still requires launch approval: %+v", connected)
	}
	blocked := withPluginConfig(ServerView{
		Name: entry.Name, Status: "failed", RequiresLaunchApproval: true,
	}, entry)
	if blocked.RequiresLaunchApproval {
		t.Fatalf("project MCP exposed obsolete launch approval action: %+v", blocked)
	}
	if blocked.Source != "project" || blocked.ConfigSource != "reasonix.toml" {
		t.Fatalf("blocked project MCP source = %q/%q, want project/reasonix.toml", blocked.Source, blocked.ConfigSource)
	}

	user := withPluginConfig(ServerView{Name: "user", Status: "connected"},
		config.PluginEntry{Name: "user", Source: config.MCPSourceUserConfig})
	if user.RequiresLaunchApproval {
		t.Fatalf("user-config MCP must not be launch-gate governed: %+v", user)
	}
	if user.Source != "user" || user.ConfigSource != "config.toml" {
		t.Fatalf("user MCP source = %q/%q, want user/config.toml", user.Source, user.ConfigSource)
	}
}

func TestMCPServersMatchesCapabilitiesServerProjection(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
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

	if got, want := app.MCPServers(), app.Capabilities().Servers; !reflect.DeepEqual(got, want) {
		t.Fatalf("MCPServers() = %+v, want Capabilities().Servers %+v", got, want)
	}
}

func TestConfiguredMCPWithFormerBuiltInNameIsUserServer(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "reasonix.toml"), []byte(`
[[plugins]]
name = "time"
command = "custom-time"
args = ["serve"]
tier = "lazy"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()

	view := app.Capabilities()
	found := false
	for _, s := range view.Servers {
		if s.Name != "time" {
			continue
		}
		found = true
		if s.BuiltIn || !s.Configured || s.Command != "custom-time" || !reflect.DeepEqual(s.Args, []string{"serve"}) {
			t.Fatalf("configured time view = %+v, want ordinary user MCP config", s)
		}
	}
	if !found {
		t.Fatalf("configured time server missing from Capabilities: %+v", view.Servers)
	}

	if err := app.SetMCPServerEnabled("time", false); err != nil {
		t.Fatalf("SetMCPServerEnabled(time,false): %v", err)
	}
	view = app.Capabilities()
	for _, s := range view.Servers {
		if s.Name == "time" {
			if s.Status != "disabled" || s.BuiltIn || s.Command != "custom-time" {
				t.Fatalf("disabled configured time view = %+v, want disabled external config", s)
			}
			return
		}
	}
	t.Fatalf("time missing after disable: %+v", view.Servers)
}

func TestSetMCPServerEnabledRestoresOnDemandWithoutConnecting(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("REASONIX_CACHE_HOME", t.TempDir())
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "reasonix.toml"), []byte(`
[[plugins]]
name = "offline"
type = "http"
url = "http://127.0.0.1:1/mcp"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	host := plugin.NewHost()
	defer host.Close()
	reg := tool.NewRegistry()
	ctrl := control.New(control.Options{Host: host, Registry: reg, PluginCtx: context.Background(), WorkspaceRoot: dir})
	app := NewApp()
	app.setTestCtrl(ctrl, "")
	app.tabs["test"].WorkspaceRoot = dir

	if err := app.SetMCPServerEnabled("offline", false); err != nil {
		t.Fatalf("SetMCPServerEnabled(false): %v", err)
	}
	if err := app.SetMCPServerEnabled("offline", true); err != nil {
		t.Fatalf("SetMCPServerEnabled(true) forced an unavailable connection: %v", err)
	}
	if host.HasClient("offline") {
		t.Fatal("durable enable started the disconnected MCP server")
	}
	if _, ok := reg.Get("mcp__offline__connect"); !ok {
		t.Fatalf("on-demand connect stub missing after enable; names=%v", reg.Names())
	}
}

func TestSetMCPServerEnabledSharedHostPreservesSiblingTabs(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)

	srv := desktopMCPHTTPServer(t)
	defer srv.Close()
	if err := os.WriteFile(filepath.Join(dir, "reasonix.toml"), fmt.Appendf(nil, `
[[plugins]]
name = "h"
type = "http"
url = %q
`, srv.URL), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sharedHost := plugin.NewHost()
	defer sharedHost.Close()
	tools, err := sharedHost.Add(ctx, plugin.Spec{Name: "h", Type: "http", URL: srv.URL})
	if err != nil {
		t.Fatalf("sharedHost.Add: %v", err)
	}

	activeRegistry := tool.NewRegistry()
	siblingRegistry := tool.NewRegistry()
	for _, mt := range tools {
		activeRegistry.Add(mt)
		siblingRegistry.Add(mt)
	}
	activeCtrl := control.New(control.Options{Host: sharedHost, Registry: activeRegistry, PluginCtx: context.Background()})
	siblingCtrl := control.New(control.Options{Host: sharedHost, Registry: siblingRegistry, PluginCtx: context.Background()})
	app := NewApp()
	app.tabs = map[string]*WorkspaceTab{
		"active": {
			ID:            "active",
			Scope:         "global",
			WorkspaceRoot: dir,
			Ready:         true,
			Ctrl:          activeCtrl,
			SharedHostKey: dir,
			disabledMCP:   map[string]ServerView{},
		},
		"sibling": {
			ID:            "sibling",
			Scope:         "global",
			WorkspaceRoot: dir,
			Ready:         true,
			Ctrl:          siblingCtrl,
			SharedHostKey: dir,
			disabledMCP:   map[string]ServerView{},
		},
	}
	app.activeTabID = "active"

	if err := app.SetMCPServerEnabled("h", false); err != nil {
		t.Fatalf("SetMCPServerEnabled(h,false): %v", err)
	}
	if _, found := activeRegistry.Get("mcp__h__greet"); found {
		t.Fatal("active tab still has h tools after disabling the shared server")
	}
	if _, found := siblingRegistry.Get("mcp__h__greet"); !found {
		t.Fatal("sibling tab lost h tools when active tab disabled the shared server")
	}
	if !sharedHost.HasClient("h") {
		t.Fatal("shared host client was removed by a per-tab disable")
	}
	view := app.Capabilities()
	if len(view.Servers) != 1 || view.Servers[0].Name != "h" || view.Servers[0].Status != "disabled" {
		t.Fatalf("Capabilities after disable = %+v, want h disabled for the active tab", view.Servers)
	}

	if err := app.SetMCPServerEnabled("h", true); err != nil {
		t.Fatalf("SetMCPServerEnabled(h,true): %v", err)
	}
	if _, found := activeRegistry.Get("mcp__h__greet"); !found {
		t.Fatal("active tab did not re-register h tools from the existing shared client")
	}
	view = app.Capabilities()
	if len(view.Servers) != 1 || view.Servers[0].Name != "h" || view.Servers[0].Status != "connected" {
		t.Fatalf("Capabilities after re-enable = %+v, want h connected for the active tab", view.Servers)
	}
}

func TestAuthorizeAndConnectMCPServerStartsProjectOnlyOnce(t *testing.T) {
	gateAddr, attempts := newDesktopMCPStartGate(t, func(_ int, conn net.Conn) {
		_, _ = conn.Write([]byte{1})
	})
	fixture := newGatedDesktopMCPLaunchFixture(t, gateAddr)
	waitForDesktopMCPStartAttempt(t, attempts, 1)
	oldSiblingTool, found := fixture.siblingRegistry.Get("mcp__h__greet")
	if !found {
		t.Fatal("sibling registry missing initial h tool")
	}

	if err := fixture.app.AuthorizeAndConnectMCPServer("h"); err != nil {
		t.Fatalf("AuthorizeAndConnectMCPServer(h): %v", err)
	}
	waitForDesktopMCPStartAttempt(t, attempts, 2)
	select {
	case attempt := <-attempts:
		t.Fatalf("project authorization started a temporary connection process (unexpected attempt %d)", attempt)
	case <-time.After(250 * time.Millisecond):
	}
	if !fixture.sharedHost.HasClient("h") {
		t.Fatal("project authorization did not leave h connected")
	}
	if _, found := fixture.activeRegistry.Get("mcp__h__greet"); !found {
		t.Fatal("active registry was not refreshed after project authorization")
	}
	newSiblingTool, found := fixture.siblingRegistry.Get("mcp__h__greet")
	if !found || newSiblingTool == oldSiblingTool {
		t.Fatal("sibling registry did not receive the single new project connection")
	}
	if _, found := fixture.disabledRegistry.Get("mcp__h__greet"); found {
		t.Fatal("project authorization re-enabled h in a disabled sibling tab")
	}
}

func TestReconnectMCPServerRefreshesEverySharedHostRegistry(t *testing.T) {
	fixture := newGatedDesktopMCPLaunchFixture(t, "")
	oldSiblingTool, found := fixture.siblingRegistry.Get("mcp__h__greet")
	if !found {
		t.Fatal("sibling registry missing initial h tool")
	}
	if err := fixture.app.ReconnectMCPServer("h"); err != nil {
		t.Fatalf("ReconnectMCPServer(h): %v", err)
	}
	if !fixture.sharedHost.HasClient("h") {
		t.Fatal("shared host did not reconnect h")
	}
	if _, found := fixture.activeRegistry.Get("mcp__h__greet"); !found {
		t.Fatal("active registry was not refreshed")
	}
	newSiblingTool, found := fixture.siblingRegistry.Get("mcp__h__greet")
	if !found || newSiblingTool == oldSiblingTool {
		t.Fatal("sibling registry retained the tool backed by the disconnected client")
	}
	if _, found := fixture.disabledRegistry.Get("mcp__h__greet"); found {
		t.Fatal("reconnect re-enabled a tab where the server was disabled")
	}
}

func TestReconnectMCPServerUsesEffectiveProjectConfigWhenUserNameIsShadowed(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	userServer := desktopMCPHTTPServerWithTool(t, "user-shadow", "user_tool")
	defer userServer.Close()
	projectServer := desktopMCPHTTPServerWithTool(t, "project-effective", "project_tool")
	defer projectServer.Close()

	userCfg := config.LoadForEdit(config.UserConfigPath())
	userCfg.Plugins = []config.PluginEntry{{Name: "h", Type: "http", URL: userServer.URL}}
	if err := userCfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reasonix.toml"), fmt.Appendf(nil, `
[[plugins]]
name = "h"
type = "http"
url = %q
`, projectServer.URL), 0o644); err != nil {
		t.Fatal(err)
	}

	host := plugin.NewHost()
	t.Cleanup(host.Close)
	registry := tool.NewRegistry()
	ctrl := control.New(control.Options{
		Host: host, Registry: registry, PluginCtx: context.Background(), WorkspaceRoot: dir,
		MCPConfigureSpec: func(spec *plugin.Spec) {
			// This test isolates effective-source selection from the project launch
			// approval flow, which has its own end-to-end coverage.
			spec.RequireLaunchApproval = false
			spec.Authorized = true
		},
	})
	app := NewApp()
	app.tabs = map[string]*WorkspaceTab{
		"active": {
			ID: "active", Scope: "global", WorkspaceRoot: dir, Ready: true,
			Ctrl: ctrl, disabledMCP: map[string]ServerView{},
		},
	}
	app.activeTabID = "active"

	if err := app.ReconnectMCPServer("h"); err != nil {
		t.Fatalf("ReconnectMCPServer(h): %v", err)
	}
	if _, found := registry.Get("mcp__h__project_tool"); !found {
		t.Fatal("reconnect did not use the effective project MCP configuration")
	}
	if _, found := registry.Get("mcp__h__user_tool"); found {
		t.Fatal("reconnect used the shadowed user MCP configuration")
	}
}

func TestUpdateMCPServerRefreshesEverySharedHostRegistry(t *testing.T) {
	fixture := newGatedDesktopMCPLaunchFixture(t, "")
	oldSiblingTool, found := fixture.siblingRegistry.Get("mcp__h__greet")
	if !found {
		t.Fatal("sibling registry missing initial h tool")
	}
	root := fixture.app.tabs["active"].WorkspaceRoot
	cfg, err := config.LoadForRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	entry, found := findPluginEntry(cfg.Plugins, "h")
	if !found {
		t.Fatal("fixture config missing h")
	}
	if err := fixture.app.UpdateMCPServer("h", MCPServerInput{
		Name: "h", Transport: entry.Type, Command: entry.Command, Args: entry.Args,
	}); err != nil {
		t.Fatalf("UpdateMCPServer(h): %v", err)
	}
	if !fixture.sharedHost.HasClient("h") {
		t.Fatal("shared host did not reconnect h")
	}
	if _, found := fixture.activeRegistry.Get("mcp__h__greet"); !found {
		t.Fatal("active registry was not refreshed")
	}
	newSiblingTool, found := fixture.siblingRegistry.Get("mcp__h__greet")
	if !found || newSiblingTool == oldSiblingTool {
		t.Fatal("sibling registry retained the tool backed by the disconnected client")
	}
	if _, found := fixture.disabledRegistry.Get("mcp__h__greet"); found {
		t.Fatal("update re-enabled a tab where the server was disabled")
	}
}

func TestClearMCPServerAuthenticationClearsEverySharedHostRegistry(t *testing.T) {
	fixture := newGatedDesktopMCPLaunchFixture(t, "")
	if err := fixture.app.ClearMCPServerAuthentication("h"); err != nil {
		t.Fatalf("ClearMCPServerAuthentication(h): %v", err)
	}
	if fixture.sharedHost.HasClient("h") {
		t.Fatal("shared host retained h after clearing authentication")
	}
	for label, registry := range map[string]*tool.Registry{
		"active": fixture.activeRegistry, "sibling": fixture.siblingRegistry, "disabled": fixture.disabledRegistry,
	} {
		if _, found := registry.Get("mcp__h__greet"); found {
			t.Fatalf("%s registry retained h after clearing authentication", label)
		}
	}
}

func TestRemoveMCPServerClearsEverySharedHostRegistry(t *testing.T) {
	fixture := newGatedDesktopMCPLaunchFixture(t, "")
	if err := fixture.app.RemoveMCPServer("h"); err != nil {
		t.Fatalf("RemoveMCPServer(h): %v", err)
	}
	if fixture.sharedHost.HasClient("h") {
		t.Fatal("shared host retained the removed server")
	}
	for label, registry := range map[string]*tool.Registry{
		"active": fixture.activeRegistry, "sibling": fixture.siblingRegistry, "disabled": fixture.disabledRegistry,
	} {
		if _, found := registry.Get("mcp__h__greet"); found {
			t.Fatalf("%s registry retained the removed server tool", label)
		}
	}
	for id, tab := range fixture.app.tabs {
		if _, disabled := tab.disabledMCP["h"]; disabled {
			t.Fatalf("tab %s retained removed-server disabled state", id)
		}
	}
}

type gatedDesktopMCPLaunchFixture struct {
	app              *App
	sharedHost       *plugin.Host
	activeRegistry   *tool.Registry
	siblingRegistry  *tool.Registry
	disabledRegistry *tool.Registry
}

func newGatedDesktopMCPLaunchFixture(t *testing.T, startGateAddr string) gatedDesktopMCPLaunchFixture {
	t.Helper()
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	singleInstanceAddr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	gateConfig := ""
	if startGateAddr != "" {
		gateConfig = fmt.Sprintf("DESKTOP_MCP_START_GATE_ADDR = %q\n", startGateAddr)
	}
	helperArgs := []string{"-test.run=TestDesktopMCPHelperProcess", "--"}
	if err := os.WriteFile(filepath.Join(dir, "reasonix.toml"), fmt.Appendf(nil, `
[[plugins]]
name = "h"
command = %q
args = ["-test.run=TestDesktopMCPHelperProcess", "--"]

[plugins.env]
GO_WANT_DESKTOP_MCP_HELPER = "1"
DESKTOP_MCP_SINGLE_INSTANCE_ADDR = %q
%s
[sandbox]
network = true
`, exe, singleInstanceAddr, gateConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	entry := config.PluginEntry{
		Name: "h", Command: exe, Args: helperArgs,
		Env: map[string]string{
			"GO_WANT_DESKTOP_MCP_HELPER":       "1",
			"DESKTOP_MCP_SINGLE_INSTANCE_ADDR": singleInstanceAddr,
		},
	}
	if startGateAddr != "" {
		entry.Env["DESKTOP_MCP_START_GATE_ADDR"] = startGateAddr
	}
	cfg, err := config.LoadForRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	runtimeSpecs := boot.PluginSpecsForRootWithOptions([]config.PluginEntry{entry}, dir, boot.PluginSpecOptions{
		DefaultCallTimeout: time.Duration(cfg.MCPCallTimeoutSeconds()) * time.Second,
		LaunchManager:      mcplaunch.ForWorkspace(config.ReasonixHomeDir(), dir),
		ConfigSource:       "workspace_config",
		StateHome:          config.ReasonixHomeDir(),
		WriterRoots:        cfg.WriteRootsForRoot(dir),
		ForbidReadRoots:    cfg.ForbidReadRootsForRoot(dir),
		Network:            cfg.Sandbox.Network,
	})
	if len(runtimeSpecs) != 1 {
		t.Fatalf("runtime specs = %d, want 1", len(runtimeSpecs))
	}
	runtimeSpec := runtimeSpecs[0]
	configure := func(spec *plugin.Spec) { *spec = runtimeSpec }
	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	t.Cleanup(lifeCancel)
	callCtx, callCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer callCancel()
	sharedHost := plugin.NewHost()
	t.Cleanup(sharedHost.Close)
	tools, err := sharedHost.AddWithLifecycle(lifeCtx, callCtx, runtimeSpec)
	if err != nil {
		t.Fatalf("sharedHost.Add: %v", err)
	}

	activeRegistry := tool.NewRegistry()
	siblingRegistry := tool.NewRegistry()
	disabledRegistry := tool.NewRegistry()
	for _, mt := range tools {
		activeRegistry.Add(mt)
		siblingRegistry.Add(mt)
		disabledRegistry.Add(mt)
	}
	activeCtrl := control.New(control.Options{
		Host: sharedHost, Registry: activeRegistry, PluginCtx: lifeCtx,
		MCPConfigureSpec: configure, WorkspaceRoot: dir,
	})
	siblingCtrl := control.New(control.Options{
		Host: sharedHost, Registry: siblingRegistry, PluginCtx: lifeCtx,
		MCPConfigureSpec: configure, WorkspaceRoot: dir,
	})
	disabledCtrl := control.New(control.Options{
		Host: sharedHost, Registry: disabledRegistry, PluginCtx: lifeCtx,
		MCPConfigureSpec: configure, WorkspaceRoot: dir,
	})
	disabledCtrl.UnregisterMCPServerTools("h")
	app := NewApp()
	app.tabs = map[string]*WorkspaceTab{
		"active": {
			ID: "active", Scope: "global", WorkspaceRoot: dir, Ready: true,
			Ctrl: activeCtrl, SharedHostKey: dir, disabledMCP: map[string]ServerView{},
		},
		"sibling": {
			ID: "sibling", Scope: "global", WorkspaceRoot: dir, Ready: true,
			Ctrl: siblingCtrl, SharedHostKey: dir, disabledMCP: map[string]ServerView{},
		},
		"disabled": {
			ID: "disabled", Scope: "global", WorkspaceRoot: dir, Ready: true,
			Ctrl: disabledCtrl, SharedHostKey: dir,
			disabledMCP: map[string]ServerView{"h": {Name: "h", Status: "disabled"}},
		},
	}
	app.activeTabID = "active"
	return gatedDesktopMCPLaunchFixture{
		app: app, sharedHost: sharedHost,
		activeRegistry: activeRegistry, siblingRegistry: siblingRegistry, disabledRegistry: disabledRegistry,
	}
}

func newDesktopMCPStartGate(t *testing.T, handle func(attempt int, conn net.Conn)) (string, <-chan int) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	attempts := make(chan int, 8)
	go func() {
		for attempt := 1; ; attempt++ {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			attempts <- attempt
			handle(attempt, conn)
			_ = conn.Close()
		}
	}()
	return listener.Addr().String(), attempts
}

// waitForDesktopMCPStartAttempt waits for the gated helper's n-th connection.
// The bound exists to fail a genuine "never (re)started" bug, not to measure
// the host: each attempt is a fresh spawn of the desktop test binary, and on Windows a first-execution spawn (Defender scan, cold image) has been measured spiking well past five seconds on otherwise idle machines — the serialization contracts these gates pin are ordering properties, so the deadline only has to be longer than any legitimate spawn. The elapsed log keeps the real spawn cost visible when it does regress.
func waitForDesktopMCPStartAttempt(t *testing.T, attempts <-chan int, want int) {
	t.Helper()
	start := time.Now()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case got := <-attempts:
			if got == want {
				t.Logf("[timing] MCP start attempt %d arrived after %v", want, time.Since(start))
				return
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for MCP start attempt %d (waited %v)", want, time.Since(start))
		}
	}
}
