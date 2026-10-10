package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/plugin"
	"reasonix/internal/pluginpkg"
	"reasonix/internal/tool"
)

func TestEditAndRemoveConfiguredMCPWithBuiltInName(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "reasonix.toml"), []byte(`
[[plugins]]
name = "time"
command = "custom-time"
args = ["serve"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()
	app.activeTab().disabledMCP["time"] = ServerView{Name: "time", Status: "disabled", Enabled: false}

	if err := app.UpdateMCPServer("time", MCPServerInput{
		Name:      "time",
		Transport: "stdio",
		Command:   "updated-time",
		Args:      []string{"run"},
	}); err != nil {
		t.Fatalf("UpdateMCPServer(time): %v", err)
	}
	cfg, err := config.LoadForRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := findPluginEntry(cfg.Plugins, "time")
	if !ok || updated.Command != "updated-time" || !reflect.DeepEqual(updated.Args, []string{"run"}) {
		t.Fatalf("updated time plugin = %+v, found=%v", updated, ok)
	}

	if err := app.RemoveMCPServer("time"); err != nil {
		t.Fatalf("RemoveMCPServer(time): %v", err)
	}
	cfg, err = config.LoadForRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findPluginEntry(cfg.Plugins, "time"); ok {
		t.Fatalf("time plugin still configured after remove: %+v", cfg.Plugins)
	}
}

func TestRemoveProjectMCPRevealsAndRegistersGlobalFallback(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	userCfg := config.LoadForEdit(config.UserConfigPath())
	userCfg.Plugins = []config.PluginEntry{{Name: "docs", Command: "global-docs"}}
	if err := userCfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(dir, "reasonix.toml")
	if err := os.WriteFile(projectPath, []byte(`
[[plugins]]
name = "docs"
command = "project-docs"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := tool.NewRegistry()
	var configured []plugin.Spec
	ctrl := control.New(control.Options{
		Host:          plugin.NewHost(),
		Registry:      reg,
		WorkspaceRoot: dir,
		MCPConfigureSpec: func(spec *plugin.Spec) {
			configured = append(configured, *spec)
		},
	})
	defer ctrl.Close()
	projectEntry, found, err := desktopEffectiveMCPServer(dir, "docs")
	if err != nil || !found {
		t.Fatalf("load project docs: entry=%+v found=%v err=%v", projectEntry, found, err)
	}
	if _, err := ctrl.RegisterMCPServerOnDemand(projectEntry); err != nil {
		t.Fatalf("register project docs: %v", err)
	}

	app := NewApp()
	app.setTestCtrl(ctrl, "")
	app.activeTab().WorkspaceRoot = dir
	if err := app.RemoveMCPServer("docs"); err != nil {
		t.Fatalf("RemoveMCPServer(docs): %v", err)
	}

	projectCfg := config.LoadForEdit(projectPath)
	if _, found := findPluginEntry(projectCfg.Plugins, "docs"); found {
		t.Fatalf("project docs still configured after removal: %+v", projectCfg.Plugins)
	}
	globalCfg := config.LoadForEdit(config.UserConfigPath())
	globalEntry, found := findPluginEntry(globalCfg.Plugins, "docs")
	if !found || globalEntry.Command != "global-docs" {
		t.Fatalf("global docs fallback = %+v, found=%v", globalEntry, found)
	}
	effective, found, err := desktopEffectiveMCPServer(dir, "docs")
	if err != nil || !found || effective.Source != config.MCPSourceUserConfig || effective.Command != "global-docs" {
		t.Fatalf("effective docs fallback = %+v, found=%v err=%v", effective, found, err)
	}
	if len(configured) < 2 || configured[len(configured)-1].Command != "global-docs" {
		t.Fatalf("registered specs = %+v, want global fallback registered last", configured)
	}
	if _, found := reg.Get("mcp__docs__connect"); !found {
		t.Fatalf("global fallback connect surface missing; names=%v", reg.Names())
	}
}

func TestRemoveMCPServerClearsRecordedStartupFailure(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "reasonix.toml"), []byte(`
[[plugins]]
name = "broken"
command = "reasonix-missing-mcp-binary"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()
	recordMCPFailure(app.activeCtrl(), config.PluginEntry{
		Name:    "broken",
		Command: "reasonix-missing-mcp-binary",
	}, errors.New("connect: missing binary"))

	view := app.Capabilities()
	if len(view.Servers) != 1 || view.Servers[0].Name != "broken" || view.Servers[0].Status != "failed" {
		t.Fatalf("Capabilities before remove = %+v, want broken failed", view.Servers)
	}

	if err := app.RemoveMCPServer("broken"); err != nil {
		t.Fatalf("RemoveMCPServer(broken): %v", err)
	}
	if mcpFailed(app.activeCtrl(), "broken") {
		t.Fatalf("Host.Failures() still contains broken after remove: %+v", app.activeCtrl().Host().Failures())
	}
	view = app.Capabilities()
	for _, s := range view.Servers {
		if s.Name == "broken" {
			t.Fatalf("Capabilities after remove still contains broken: %+v", view.Servers)
		}
	}
}

func TestRemoveMCPServerDeletesProjectMCPJSONEntry(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(`{
  "mcpServers": {
    "codegraph": { "command": "codegraph", "args": ["serve", "--mcp"] },
    "keep": { "command": "keep-mcp" }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()

	if err := app.RemoveMCPServer("codegraph"); err != nil {
		t.Fatalf("RemoveMCPServer(.mcp.json codegraph): %v", err)
	}
	cfg, err := config.LoadForRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findPluginEntry(cfg.Plugins, "codegraph"); ok {
		t.Fatalf("codegraph still merged after remove: %+v", cfg.Plugins)
	}
	if _, ok := findPluginEntry(cfg.Plugins, "keep"); !ok {
		t.Fatalf("unrelated .mcp.json server should be preserved: %+v", cfg.Plugins)
	}
}

func TestRemoveMCPServerRejectsPluginManagedServerWithoutDisconnecting(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)

	srv := desktopMCPHTTPServer(t)
	defer srv.Close()
	reasonixHome := config.ReasonixHomeDir()
	root := filepath.Join(reasonixHome, "plugins", "superpowers")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, pluginpkg.NativeManifest), fmt.Appendf(nil, `{"apiVersion": "reasonix.io/plugin/v2",
  "name": "superpowers",
  "version": "1.0.0",
  "mcpServers": {
    "helper": { "type": "http", "url": %q }
  }
}`, srv.URL), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := pluginpkg.Upsert(reasonixHome, pluginpkg.InstalledPlugin{
		Name:         "superpowers",
		Root:         "plugins/superpowers",
		Version:      "1.0.0",
		ManifestKind: "reasonix",
		Enabled:      true,
	}); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadForRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := findPluginEntry(cfg.Plugins, "helper")
	if !ok {
		t.Fatalf("plugin-managed MCP missing from config: %+v", cfg.Plugins)
	}
	ctrl := control.New(control.Options{Host: plugin.NewHost()})
	defer ctrl.Close()
	if _, err := ctrl.ConnectMCPServer(entry); err != nil {
		t.Fatalf("connect plugin-managed MCP: %v", err)
	}

	app := NewApp()
	app.setTestCtrl(ctrl, "")
	app.activeTab().WorkspaceRoot = dir
	err = app.RemoveMCPServer("helper")
	if err == nil || !strings.Contains(err.Error(), "managed by plugin") || !strings.Contains(err.Error(), "superpowers") {
		t.Fatalf("RemoveMCPServer(plugin-managed) error = %v", err)
	}
	if !mcpConnected(ctrl, "helper") {
		t.Fatal("plugin-managed MCP was disconnected despite rejected removal")
	}
	for action, actionErr := range map[string]error{
		"clear auth": app.ClearMCPServerAuthentication("helper"),
		"update":     app.UpdateMCPServer("helper", MCPServerInput{Name: "helper", Transport: "http", URL: srv.URL}),
	} {
		if actionErr == nil || !strings.Contains(actionErr.Error(), "managed by plugin") {
			t.Fatalf("%s plugin-managed MCP error = %v", action, actionErr)
		}
	}
	if _, found := findPluginEntry(config.LoadForEdit(config.UserConfigPath()).Plugins, "helper"); found {
		t.Fatal("plugin-managed MCP mutation created a user-config shadow")
	}
	servers := app.MCPServers()
	if len(servers) != 1 || servers[0].Name != "helper" || servers[0].ManagedByPlugin != "superpowers" {
		t.Fatalf("MCPServers() = %+v, want helper managed by superpowers", servers)
	}
}

func TestRemoveMCPServerRejectsRuntimeOnlyServerWithoutDisconnecting(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)

	srv := desktopMCPHTTPServer(t)
	defer srv.Close()
	ctrl := control.New(control.Options{Host: plugin.NewHost()})
	defer ctrl.Close()
	if _, err := ctrl.ConnectMCPServer(config.PluginEntry{Name: "runtime-only", Type: "http", URL: srv.URL}); err != nil {
		t.Fatalf("connect runtime-only MCP: %v", err)
	}

	app := NewApp()
	app.setTestCtrl(ctrl, "")
	app.activeTab().WorkspaceRoot = dir
	err := app.RemoveMCPServer("runtime-only")
	if err == nil || !strings.Contains(err.Error(), "no removable MCP server") {
		t.Fatalf("RemoveMCPServer(runtime-only) error = %v", err)
	}
	if !mcpConnected(ctrl, "runtime-only") {
		t.Fatal("runtime-only MCP was disconnected despite failed persistence removal")
	}
}

func TestUpdateMCPServerEditsProjectMCPJSONEntry(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(`{
  "mcpServers": {
    "codegraph": { "command": "codegraph", "args": ["serve", "--mcp"] }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()
	entry, ok, err := desktopEffectiveMCPServer(dir, "codegraph")
	if err != nil || !ok {
		t.Fatalf("load codegraph entry: found=%v err=%v", ok, err)
	}
	if err := config.DefaultMCPActivationStore().SetServerEnabled(entry, dir, false); err != nil {
		t.Fatal(err)
	}
	app.activeTab().disabledMCP["codegraph"] = ServerView{}

	if err := app.UpdateMCPServer("codegraph", MCPServerInput{
		Name:      "codegraph",
		Transport: "stdio",
		Command:   "reasonix-missing-mcp-binary",
		Args:      []string{"serve", "--mcp"},
		Env:       map[string]string{"CODEGRAPH_LOG": "debug"},
	}); err != nil {
		t.Fatalf("UpdateMCPServer(.mcp.json codegraph): %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	got := doc.MCPServers["codegraph"]
	if got.Command != "reasonix-missing-mcp-binary" || !reflect.DeepEqual(got.Args, []string{"serve", "--mcp"}) || got.Env["CODEGRAPH_LOG"] != "debug" {
		t.Fatalf(".mcp.json codegraph = %+v, want updated command/args/env", got)
	}
	if _, ok := findPluginEntry(config.LoadForEdit(config.UserConfigPath()).Plugins, "codegraph"); ok {
		t.Fatalf(".mcp.json update should not create a user config shadow entry")
	}
}

func TestUpdateMCPServerPreservesProjectTOMLSourceAndGlobalShadow(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	userCfg := config.LoadForEdit(config.UserConfigPath())
	userCfg.Plugins = []config.PluginEntry{{Name: "docs", Command: "global-docs"}}
	if err := userCfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(dir, "reasonix.toml")
	if err := os.WriteFile(projectPath, []byte(`
[[plugins]]
name = "docs"
command = "project-docs"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost(), WorkspaceRoot: dir}), "")
	defer app.activeCtrl().Close()
	app.activeTab().WorkspaceRoot = dir
	entry, ok, err := desktopEffectiveMCPServer(dir, "docs")
	if err != nil || !ok || entry.Source != config.MCPSourceProjectConfig {
		t.Fatalf("load project docs entry: entry=%+v found=%v err=%v", entry, ok, err)
	}
	if err := config.DefaultMCPActivationStore().SetServerEnabled(entry, dir, false); err != nil {
		t.Fatal(err)
	}
	app.activeTab().disabledMCP["docs"] = ServerView{}

	if err := app.UpdateMCPServer("docs", MCPServerInput{
		Name: "docs", Transport: "stdio", Command: "project-docs-updated",
	}); err != nil {
		t.Fatalf("UpdateMCPServer(project reasonix.toml docs): %v", err)
	}

	projectCfg := config.LoadForEdit(projectPath)
	projectEntry, found := findPluginEntry(projectCfg.Plugins, "docs")
	if !found || projectEntry.Command != "project-docs-updated" {
		t.Fatalf("project docs entry = %+v, found=%v", projectEntry, found)
	}
	globalCfg := config.LoadForEdit(config.UserConfigPath())
	globalEntry, found := findPluginEntry(globalCfg.Plugins, "docs")
	if !found || globalEntry.Command != "global-docs" {
		t.Fatalf("global shadow changed while editing project entry: %+v, found=%v", globalEntry, found)
	}
	effective, found, err := desktopEffectiveMCPServer(dir, "docs")
	if err != nil || !found || effective.Source != config.MCPSourceProjectConfig || effective.Command != "project-docs-updated" {
		t.Fatalf("effective docs after edit = %+v, found=%v err=%v", effective, found, err)
	}
}

func TestAddMCPServerPersistsRemoteHeaders(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	t.Setenv("STRIPE_TOKEN", "stripe-test-token")
	srv := desktopMCPHTTPServer(t)
	defer srv.Close()

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()

	tools, err := app.AddMCPServer(MCPServerInput{
		Name:      "stripe",
		Transport: "http",
		URL:       srv.URL,
		Headers: map[string]string{
			"Authorization": "Bearer ${STRIPE_TOKEN}",
			"X-Org":         "team",
		},
	})
	if err != nil {
		t.Fatalf("AddMCPServer(stripe): %v", err)
	}
	if tools != 1 {
		t.Fatalf("tools = %d, want 1", tools)
	}

	cfg, err := config.LoadForRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := findPluginEntry(cfg.Plugins, "stripe")
	if !ok {
		t.Fatalf("stripe plugin missing from config: %+v", cfg.Plugins)
	}
	if p.Type != "http" || p.URL != srv.URL {
		t.Fatalf("stripe plugin transport = %q url = %q", p.Type, p.URL)
	}
	if p.Headers["Authorization"] != "Bearer ${STRIPE_TOKEN}" || p.Headers["X-Org"] != "team" {
		t.Fatalf("stripe headers = %+v", p.Headers)
	}

	view := app.MCPServers()
	for _, s := range view {
		if s.Name == "stripe" {
			if !reflect.DeepEqual(s.HeaderKeys, []string{"Authorization", "X-Org"}) {
				t.Fatalf("stripe header keys = %+v", s.HeaderKeys)
			}
			return
		}
	}
	t.Fatalf("stripe MCP missing from view: %+v", view)
}

func TestInstallMCPServerHandshakeFailureDoesNotPersist(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()

	result, err := app.InstallMCPServer(MCPServerInput{
		Name: "broken", Transport: "stdio", Command: "reasonix-missing-mcp-binary",
	})
	if err != nil {
		t.Fatalf("InstallMCPServer returned transport error instead of structured issue: %v", err)
	}
	if result.State != "issue" || result.Action != "retry" {
		t.Fatalf("install result = %+v, want retryable issue", result)
	}
	cfg, err := config.LoadForRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findPluginEntry(cfg.Plugins, "broken"); ok {
		t.Fatalf("failed candidate was persisted: %+v", cfg.Plugins)
	}
	for _, server := range app.MCPServers() {
		if server.Name == "broken" {
			t.Fatalf("failed candidate leaked into the installed server list: %+v", server)
		}
	}
}

func TestInstallMCPServerAuthenticationRequiredPersistsForResume(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()

	result, err := app.InstallMCPServer(MCPServerInput{Name: "oauth", Transport: "http", URL: srv.URL})
	if err != nil {
		t.Fatalf("InstallMCPServer auth result: %v", err)
	}
	if result.State != "action_required" || result.Action != "authenticate" {
		t.Fatalf("install result = %+v, want authentication action", result)
	}
	cfg, err := config.LoadForRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findPluginEntry(cfg.Plugins, "oauth"); !ok {
		t.Fatalf("auth-pending candidate must persist for resume: %+v", cfg.Plugins)
	}
}

func TestAddMCPServerPersistsConnectionConfiguration(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	srv := desktopMCPHTTPServer(t)
	defer srv.Close()

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()
	autoStart := false
	callTimeout := 45
	_, err := app.AddMCPServer(MCPServerInput{
		Name:               "admin",
		Transport:          "streamable-http",
		URL:                srv.URL,
		AutoStart:          &autoStart,
		CallTimeoutSeconds: &callTimeout,
		ToolTimeoutSeconds: map[string]int{
			"wipe": 120,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadForRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := findPluginEntry(cfg.Plugins, "admin")
	if !ok || entry.Type != "http" || entry.AutoStart == nil || *entry.AutoStart ||
		entry.CallTimeoutSeconds != 45 || entry.ToolTimeoutSeconds["wipe"] != 120 {
		t.Fatalf("persisted advanced MCP entry = %+v, found=%v", entry, ok)
	}

	views := app.MCPServers()
	if len(views) != 1 || views[0].Transport != "http" || views[0].AutoStart ||
		views[0].CallTimeoutSeconds != 45 || views[0].ToolTimeoutSeconds["wipe"] != 120 {
		t.Fatalf("advanced MCP ServerView = %+v", views)
	}
}

func TestUpdateMCPServerPreservesAbsentFieldsAndClearsExplicitOnes(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	srv := desktopMCPHTTPServer(t)
	defer srv.Close()

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()
	callTimeout := 45
	if _, err := app.AddMCPServer(MCPServerInput{
		Name:               "admin",
		Transport:          "http",
		URL:                srv.URL,
		CallTimeoutSeconds: &callTimeout,
		ToolTimeoutSeconds: map[string]int{"wipe": 120},
	}); err != nil {
		t.Fatal(err)
	}

	// An old frontend (or a partial payload) omits optional timeout fields.
	if err := app.UpdateMCPServer("admin", MCPServerInput{Name: "admin", Transport: "http", URL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadForRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := findPluginEntry(cfg.Plugins, "admin")
	if !ok || entry.CallTimeoutSeconds != 45 || entry.ToolTimeoutSeconds["wipe"] != 120 {
		t.Fatalf("absent input fields must preserve persisted values, entry = %+v, found=%v", entry, ok)
	}

	// Explicit zero values are the editor's clear semantics.
	cleared := 0
	if err := app.UpdateMCPServer("admin", MCPServerInput{
		Name:               "admin",
		Transport:          "http",
		URL:                srv.URL,
		CallTimeoutSeconds: &cleared,
		ToolTimeoutSeconds: map[string]int{},
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.LoadForRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok = findPluginEntry(cfg.Plugins, "admin")
	if !ok || entry.CallTimeoutSeconds != 0 || len(entry.ToolTimeoutSeconds) != 0 {
		t.Fatalf("explicit empty fields must clear persisted values, entry = %+v, found=%v", entry, ok)
	}
}

func TestUpdateMCPServerFailedCandidateRollsBackConfigAndConnection(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	srv := desktopMCPHTTPServer(t)
	defer srv.Close()

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()
	if _, err := app.AddMCPServer(MCPServerInput{Name: "stable", Transport: "http", URL: srv.URL}); err != nil {
		t.Fatal(err)
	}

	err := app.UpdateMCPServer("stable", MCPServerInput{
		Name: "stable", Transport: "stdio", Command: "reasonix-missing-mcp-binary",
	})
	if err == nil {
		t.Fatal("broken update candidate should fail")
	}
	cfg, err := config.LoadForRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := findPluginEntry(cfg.Plugins, "stable")
	if !ok || entry.Type != "http" || entry.URL != srv.URL {
		t.Fatalf("failed update changed durable config: %+v, found=%v", entry, ok)
	}
	if !app.activeCtrl().Host().HasClient("stable") {
		t.Fatal("previous MCP connection was not restored after failed update")
	}
}

func TestCapabilitiesMarksBackgroundRemoteMCPAuthPossible(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "reasonix.toml"), []byte(`
[[plugins]]
name = "dida"
type = "http"
url = "https://mcp.dida365.com"
tier = "lazy"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()

	view := app.Capabilities()
	for _, s := range view.Servers {
		if s.Name == "dida" {
			if s.Status != "deferred" || s.StartIntent != "automatic" || s.RuntimeState != "idle" || s.AuthStatus != "possible" || s.AuthURL != "https://mcp.dida365.com" {
				t.Fatalf("dida auth diagnosis = %+v", s)
			}
			return
		}
	}
	t.Fatalf("dida MCP missing from Capabilities: %+v", view.Servers)
}

func TestCapabilitiesDoesNotMarkRemoteMCPWithAuthHeaderPossible(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "reasonix.toml"), []byte(`
[[plugins]]
name = "stripe"
type = "http"
url = "https://mcp.stripe.com"
headers = { Authorization = "Bearer ${STRIPE_TOKEN}" }
tier = "lazy"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()

	view := app.Capabilities()
	for _, s := range view.Servers {
		if s.Name == "stripe" {
			if s.AuthStatus != "none" {
				t.Fatalf("stripe auth status = %q, want none; server = %+v", s.AuthStatus, s)
			}
			return
		}
	}
	t.Fatalf("stripe MCP missing from Capabilities: %+v", view.Servers)
}

func TestCapabilitiesMarksAuthFailureRequired(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "reasonix.toml"), []byte(`
[[plugins]]
name = "figma"
type = "http"
url = "https://mcp.figma.com/mcp"
tier = "lazy"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	host := plugin.NewHost()
	host.RecordFailure(plugin.Spec{Name: "figma", Type: "http", URL: "https://mcp.figma.com/mcp"}, errors.New("connect: 401 unauthorized"))
	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: host}), "")
	defer app.activeCtrl().Close()

	view := app.Capabilities()
	for _, s := range view.Servers {
		if s.Name == "figma" {
			if s.Status != "failed" || s.AuthStatus != "required" || s.AuthURL != "https://mcp.figma.com/mcp" {
				t.Fatalf("figma auth diagnosis = %+v", s)
			}
			return
		}
	}
	t.Fatalf("figma MCP missing from Capabilities: %+v", view.Servers)
}
