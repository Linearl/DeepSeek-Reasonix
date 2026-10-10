package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/command"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/evidence"
	"reasonix/internal/history"
	"reasonix/internal/plugin"
	"reasonix/internal/skill"
	"reasonix/internal/stats"
	"reasonix/internal/store"
	"reasonix/internal/taskcatalog"
)

type todoMetaController struct {
	stubSessionAPI
	todos []evidence.TodoItem
}

func (c *todoMetaController) Todos() []evidence.TodoItem {
	return append([]evidence.TodoItem(nil), c.todos...)
}

func TestCanonicalTodosMetaWireContract(t *testing.T) {
	if got := ctrlTodos(nil); got != nil {
		t.Fatalf("nil controller todos = %+v, want unavailable", *got)
	}

	empty := Meta{CanonicalTodos: ctrlTodos(&todoMetaController{})}
	raw, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal empty canonical todos: %v", err)
	}
	if !strings.Contains(string(raw), `"canonicalTodos":[]`) {
		t.Fatalf("empty canonical todos must encode as an authoritative empty array: %s", raw)
	}

	ctrl := &todoMetaController{todos: []evidence.TodoItem{{Content: "Ship", Status: "completed"}}}
	got := ctrlTodos(ctrl)
	if got == nil || len(*got) != 1 || (*got)[0].Status != "completed" {
		t.Fatalf("canonical todos = %+v, want completed task", got)
	}

	unavailable, err := json.Marshal(Meta{CanonicalTodos: ctrlTodos(nil)})
	if err != nil {
		t.Fatalf("marshal unavailable canonical todos: %v", err)
	}
	if strings.Contains(string(unavailable), "canonicalTodos") {
		t.Fatalf("unavailable canonical todos should preserve the legacy fallback contract: %s", unavailable)
	}
}

func TestPluginToolsToViewPreservesSchemaError(t *testing.T) {
	got := pluginToolsToView([]plugin.ToolInfo{{
		Name: "generate_yso_bytes", Description: "Generate payload", ReadOnlyHint: true,
		SchemaError: "invalid input schema: bad nested type",
	}})
	if len(got) != 1 || got[0].SchemaError != "invalid input schema: bad nested type" {
		t.Fatalf("tool views = %+v", got)
	}
}

func desktopMCPHTTPServer(t *testing.T) *httptest.Server {
	return desktopMCPHTTPServerWithTool(t, "h", "greet")
}

func desktopMCPHTTPServerWithTool(t *testing.T, serverName, toolName string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2024-11-05",
				"serverInfo":      map[string]any{"name": serverName, "version": "0"},
			}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name":        toolName,
				"description": "Greet someone.",
				"inputSchema": map[string]any{"type": "object"},
			}}}
		default:
			result = map[string]any{}
		}
		resp := map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestDesktopMCPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_DESKTOP_MCP_HELPER") != "1" {
		return
	}
	if addr := os.Getenv("DESKTOP_MCP_START_GATE_ADDR"); addr != "" {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "connect MCP start gate %s: %v\n", addr, err)
			os.Exit(24)
		}
		var release [1]byte
		if _, err := io.ReadFull(conn, release[:]); err != nil {
			_ = conn.Close()
			_, _ = fmt.Fprintf(os.Stderr, "wait for MCP start gate %s: %v\n", addr, err)
			os.Exit(25)
		}
		_ = conn.Close()
	}
	var instanceListener net.Listener
	if addr := os.Getenv("DESKTOP_MCP_SINGLE_INSTANCE_ADDR"); addr != "" {
		var err error
		instanceListener, err = net.Listen("tcp", addr)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "another MCP instance is already using %s: %v\n", addr, err)
			os.Exit(23)
		}
		defer instanceListener.Close()
	}
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for {
		var req struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
		}
		if err := dec.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			t.Fatalf("decode helper request: %v", err)
		}
		if req.ID == nil {
			continue
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2024-11-05",
				"serverInfo":      map[string]any{"name": "desktop-helper", "version": "0"},
			}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name": "greet", "description": "Greet someone.",
				"inputSchema": map[string]any{"type": "object"},
			}}}
		default:
			result = map[string]any{}
		}
		if err := enc.Encode(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result}); err != nil {
			t.Fatalf("encode helper response: %v", err)
		}
	}
}

// setTestCtrl creates a minimal workspace tab (if needed) and sets its
// controller, so tests don't depend on the old App.ctrl field.
func (a *App) setTestCtrl(ctrl control.SessionAPI, model string) {
	if len(a.tabs) == 0 {
		tab := &WorkspaceTab{
			ID:          "test",
			Scope:       "global",
			Ready:       true,
			disabledMCP: map[string]ServerView{},
		}
		a.tabs = map[string]*WorkspaceTab{"test": tab}
		a.activeTabID = "test"
	}
	tab := a.tabs["test"]
	tab.Ctrl = ctrl
	a.bindControllerDisplayRecorder(ctrl)
	tab.model = model
}

func isolateDesktopUserDirs(t *testing.T) string {
	t.Helper()
	home := robustTempDir(t)
	xdg := filepath.Join(home, ".config")
	appData := filepath.Join(home, "AppData")
	for _, dir := range []string{xdg, appData} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("REASONIX_CREDENTIALS_STORE", "file")
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("REASONIX_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("REASONIX_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("AppData", appData)
	// Close process-local SQLite handles before TempDir cleanup for Windows.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		desktopTopicState.close()
		_ = history.CloseSharedCatalog(ctx)
		_ = stats.CloseUsageCatalogs(ctx)
		_ = taskcatalog.ShutdownShared(ctx)
	})
	return home
}

func primarySessionFiles(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if store.IsSessionTranscriptName(filepath.Base(path)) {
			out = append(out, path)
		}
	}
	return out
}

func readConflictLogLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read conflict log: %v", err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func setDesktopTestCredential(t *testing.T, key, value string) {
	t.Helper()
	if _, err := config.SetCredential(key, value); err != nil {
		t.Fatalf("SetCredential(%s): %v", key, err)
	}
}

func TestNeedsOnboardingIgnoresInheritedEnv(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv(onboardingKeyEnv, "inherited-key")

	app := NewApp()
	if !app.NeedsOnboarding() {
		t.Fatal("NeedsOnboarding should require a key saved in Reasonix global .env")
	}
	setDesktopTestCredential(t, onboardingKeyEnv, "saved-key")
	if app.NeedsOnboarding() {
		t.Fatal("NeedsOnboarding should be false after saving the global credential")
	}
}

func TestNeedsOnboardingTreatsBlankSavedKeyAsMissing(t *testing.T) {
	isolateDesktopUserDirs(t)
	if err := os.MkdirAll(filepath.Dir(config.UserCredentialsPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.UserCredentialsPath(), []byte(onboardingKeyEnv+"=\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	if !app.NeedsOnboarding() {
		t.Fatal("NeedsOnboarding should require a non-empty saved credential")
	}
}

func TestNeedsOnboardingAcceptsConfiguredCustomProvider(t *testing.T) {
	isolateDesktopUserDirs(t)
	cfg := config.Default()
	cfg.DefaultModel = "custom/custom-model"
	cfg.Desktop.ProviderAccess = []string{"custom"}
	cfg.Providers = []config.ProviderEntry{{
		Name: "custom", Kind: "openai", BaseURL: "https://models.example.invalid/v1",
		Model: "custom-model", APIKeyEnv: "CUSTOM_API_KEY",
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save custom provider config: %v", err)
	}
	setDesktopTestCredential(t, "CUSTOM_API_KEY", "saved-custom-key")

	if NewApp().NeedsOnboarding() {
		t.Fatal("NeedsOnboarding should be false when a custom provider is configured")
	}
}

func TestNeedsOnboardingAcceptsNoAuthLocalProvider(t *testing.T) {
	isolateDesktopUserDirs(t)
	cfg := config.Default()
	cfg.DefaultModel = "local/local-model"
	cfg.Desktop.ProviderAccess = []string{"local"}
	cfg.Providers = []config.ProviderEntry{{
		Name: "local", Kind: "openai", BaseURL: "http://127.0.0.1:11434/v1",
		Model: "local-model",
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save local provider config: %v", err)
	}

	if NewApp().NeedsOnboarding() {
		t.Fatal("NeedsOnboarding should be false for a no-auth local provider")
	}
}

func providerNamesFromView(providers []ProviderView) []string {
	out := make([]string, 0, len(providers))
	for _, p := range providers {
		out = append(out, p.Name)
	}
	return out
}

func modelRefsFromView(models []ModelInfo) map[string]bool {
	out := map[string]bool{}
	for _, m := range models {
		out[m.Ref] = true
	}
	return out
}

type desktopFakeTool struct {
	name string
}

func (t desktopFakeTool) Name() string { return t.name }

func (desktopFakeTool) Description() string { return "fake desktop tool" }

func (desktopFakeTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (desktopFakeTool) Execute(context.Context, json.RawMessage) (string, error) { return "", nil }

func (desktopFakeTool) ReadOnly() bool { return true }

type desktopAskRuntimeRunner struct {
	ask func(context.Context) error
}

func (r *desktopAskRuntimeRunner) Run(ctx context.Context, _ string) error {
	if r.ask == nil {
		return nil
	}
	return r.ask(ctx)
}

func TestCommandsIncludesDocsAndEffortNotThinking(t *testing.T) {
	app := NewApp()
	cmds := app.Commands()
	if !hasCommand(cmds, "docs") {
		t.Fatalf("Commands() should include docs: %+v", cmds)
	}
	if !hasCommand(cmds, "effort") {
		t.Fatalf("Commands() should include effort: %+v", cmds)
	}
	if !hasCommand(cmds, "reload") {
		t.Fatalf("Commands() should include reload: %+v", cmds)
	}
	if hasCommand(cmds, "thinking") {
		t.Fatalf("Commands() should not include thinking: %+v", cmds)
	}
}

func TestCommandsDocsShowsOnlyRuntimeWinner(t *testing.T) {
	tests := []struct {
		name     string
		commands []command.Command
		skills   []skill.Skill
		wantKind string
	}{
		{
			name:     "custom command shadows builtin",
			commands: []command.Command{{Name: "docs", Description: "custom docs"}},
			wantKind: "custom",
		},
		{
			name:     "skill shadows builtin",
			skills:   []skill.Skill{{Name: "docs", Description: "docs skill"}},
			wantKind: "skill",
		},
		{
			name:     "custom command shadows skill and builtin",
			commands: []command.Command{{Name: "docs", Description: "custom docs"}},
			skills:   []skill.Skill{{Name: "docs", Description: "docs skill"}},
			wantKind: "custom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := control.New(control.Options{Commands: tt.commands, Skills: tt.skills})
			defer ctrl.Close()
			app := NewApp()
			app.setTestCtrl(ctrl, "")

			var docs []CommandInfo
			for _, cmd := range app.Commands() {
				if cmd.Name == "docs" {
					docs = append(docs, cmd)
				}
			}
			if len(docs) != 1 || docs[0].Kind != tt.wantKind {
				t.Fatalf("docs commands = %+v, want one %s entry", docs, tt.wantKind)
			}
			if fallback, ok := commandInfoByName(app.Commands(), control.ReasonixDocsSlashName); !ok || fallback.Kind != "builtin" {
				t.Fatalf("qualified docs fallback = %+v, %v; want built-in", fallback, ok)
			}
		})
	}
}

func commandInfoByName(commands []CommandInfo, name string) (CommandInfo, bool) {
	for _, command := range commands {
		if command.Name == name {
			return command, true
		}
	}
	return CommandInfo{}, false
}

func TestCommandsDocsAccountsForHiddenCompatibilityAliases(t *testing.T) {
	tests := []struct {
		name          string
		commands      []command.Command
		skills        []skill.Skill
		wantCanonical string
	}{
		{
			name: "hidden plugin command alias",
			commands: []command.Command{
				{Name: "docs", Plugin: "manuals", Hidden: true},
				{Name: "manuals:docs", Plugin: "manuals"},
			},
			wantCanonical: "manuals:docs",
		},
		{
			name:          "compatible plugin skill alias",
			skills:        []skill.Skill{{Name: "docs", Plugin: "manuals"}},
			wantCanonical: "manuals:docs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := control.New(control.Options{Commands: tt.commands, Skills: tt.skills})
			defer ctrl.Close()
			app := NewApp()
			app.setTestCtrl(ctrl, "")
			commands := app.Commands()
			if _, ok := commandInfoByName(commands, "docs"); ok {
				t.Fatalf("hidden runtime owner left a misleading docs entry: %+v", commands)
			}
			for _, want := range []string{control.ReasonixDocsSlashName, tt.wantCanonical} {
				if _, ok := commandInfoByName(commands, want); !ok {
					t.Fatalf("commands missing %q: %+v", want, commands)
				}
			}
		})
	}
}

func TestCommandsDocsDoesNotDisplaceQualifiedCustomCommands(t *testing.T) {
	ctrl := control.New(control.Options{Commands: []command.Command{
		{Name: "docs", Description: "custom docs"},
		{Name: "reasonix:docs", Description: "qualified custom docs"},
		{Name: "reasonix:builtin:docs", Description: "second qualified custom docs"},
	}})
	defer ctrl.Close()
	app := NewApp()
	app.setTestCtrl(ctrl, "")
	commands := app.Commands()
	for _, want := range []struct {
		name string
		kind string
	}{
		{name: "docs", kind: "custom"},
		{name: "reasonix:docs", kind: "custom"},
		{name: "reasonix:builtin:docs", kind: "custom"},
		{name: "reasonix:builtin:docs:2", kind: "builtin"},
	} {
		if command, ok := commandInfoByName(commands, want.name); !ok || command.Kind != want.kind {
			t.Fatalf("command %q = %+v, %v; want kind %q", want.name, command, ok, want.kind)
		}
	}
}

func TestCommandsClassifiesSubagentSkills(t *testing.T) {
	ctrl := control.New(control.Options{Skills: []skill.Skill{
		{Name: "init", Description: "inline skill", RunAs: skill.RunInline},
		{Name: "explore", Description: "isolated skill", RunAs: skill.RunSubagent, Color: "amber"},
	}})
	defer ctrl.Close()
	app := NewApp()
	app.setTestCtrl(ctrl, "")

	kinds := map[string]string{}
	groups := map[string]string{}
	colors := map[string]string{}
	for _, cmd := range app.Commands() {
		kinds[cmd.Name] = cmd.Kind
		groups[cmd.Name] = cmd.Group
		colors[cmd.Name] = cmd.Color
	}
	if kinds["init"] != "skill" {
		t.Fatalf("inline skill kind = %q, want skill", kinds["init"])
	}
	if kinds["explore"] != "subagent" {
		t.Fatalf("subagent skill kind = %q, want subagent", kinds["explore"])
	}
	if colors["explore"] != "amber" {
		t.Fatalf("subagent skill color = %q, want amber", colors["explore"])
	}
	if groups["new"] != "actions" {
		t.Fatalf("new command group = %q, want actions", groups["new"])
	}
	if groups["mcp"] != "integrations" || groups["plugins"] != "integrations" {
		t.Fatalf("integration command groups = mcp:%q plugins:%q", groups["mcp"], groups["plugins"])
	}
	if groups["skill"] != "skills" {
		t.Fatalf("skill command group = %q, want skills", groups["skill"])
	}
}

func TestMetaForTabIncludesWorkspaceContext(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	isolateDesktopUserDirs(t)
	resetWorkspaceGitBranchMetaCacheForTest(t)

	repo := t.TempDir()
	configuredSandboxRoot := filepath.Join(t.TempDir(), "sandbox")
	cfg := config.LoadForEdit(config.UserConfigPath())
	cfg.Sandbox.WorkspaceRoot = configuredSandboxRoot
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatal(err)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatal(err)
		}
	}()
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	runGit(t, "init")
	runGit(t, "checkout", "-b", "feature/meta")

	app := NewApp()
	app.tabs = map[string]*WorkspaceTab{"tab-1": {
		ID:            "tab-1",
		Scope:         "project",
		WorkspaceRoot: repo,
		Ready:         true,
		disabledMCP:   map[string]ServerView{},
	}}
	app.activeTabID = "tab-1"

	got := app.MetaForTab("tab-1")
	if got.Cwd != repo || got.WorkspaceRoot != repo || got.WorkspacePath != repo {
		t.Fatalf("workspace fields = cwd:%q root:%q path:%q, want %q", got.Cwd, got.WorkspaceRoot, got.WorkspacePath, repo)
	}
	if got.WorkspaceName != filepath.Base(repo) {
		t.Fatalf("workspaceName = %q, want %q", got.WorkspaceName, filepath.Base(repo))
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	if strings.Contains(string(raw), "sandboxPath") || strings.Contains(string(raw), configuredSandboxRoot) {
		t.Fatalf("meta should not expose configured sandbox root as sandboxPath: %s", raw)
	}
	// The first git process launch can be noticeably slower on Windows runners
	// while MetaForTab intentionally keeps the caller path non-blocking.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got = app.MetaForTab("tab-1"); got.GitBranch == "feature/meta" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("gitBranch = %q, want feature/meta after async refresh", got.GitBranch)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestListTabsDoesNotExposeConfiguredSandboxPath(t *testing.T) {
	isolateDesktopUserDirs(t)
	workspace := t.TempDir()
	configuredSandboxRoot := filepath.Join(t.TempDir(), "sandbox")
	cfg := config.LoadForEdit(config.UserConfigPath())
	cfg.Sandbox.WorkspaceRoot = configuredSandboxRoot
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.tabs = map[string]*WorkspaceTab{"tab-1": {
		ID:            "tab-1",
		Scope:         "project",
		WorkspaceRoot: workspace,
		Ready:         true,
		disabledMCP:   map[string]ServerView{},
	}}
	app.activeTabID = "tab-1"
	app.tabOrder = []string{"tab-1"}

	raw, err := json.Marshal(app.ListTabs())
	if err != nil {
		t.Fatalf("marshal tabs: %v", err)
	}
	if strings.Contains(string(raw), "sandboxPath") || strings.Contains(string(raw), configuredSandboxRoot) {
		t.Fatalf("tab metadata should not expose configured sandbox root as sandboxPath: %s", raw)
	}
}

func TestListTabsExposesStructuredRuntimeStatus(t *testing.T) {
	asks := make(chan event.Ask, 1)
	done := make(chan event.Event, 1)
	runner := &desktopAskRuntimeRunner{}
	ctrl := control.New(control.Options{
		Runner: runner,
		Sink: event.FuncSink(func(e event.Event) {
			switch e.Kind {
			case event.AskRequest:
				asks <- e.Ask
			case event.TurnDone:
				done <- e
			}
		}),
	})
	runner.ask = func(ctx context.Context) error {
		_, err := ctrl.Ask(ctx, []event.AskQuestion{{
			ID:      "choice",
			Prompt:  "Pick one",
			Options: []event.AskOption{{Label: "A"}, {Label: "B"}},
		}})
		return err
	}

	app := NewApp()
	app.setTestCtrl(ctrl, "prov/model")
	app.tabOrder = []string{"test"}
	ctrl.Send("ask user")
	select {
	case <-asks:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ask request")
	}

	tabs := app.ListTabs()
	if len(tabs) != 1 {
		t.Fatalf("tabs = %d, want 1", len(tabs))
	}
	if !tabs[0].Running || !tabs[0].PendingPrompt || !tabs[0].Cancellable || tabs[0].CancelRequested {
		t.Fatalf("tab runtime = running:%v pending:%v cancellable:%v cancel:%v", tabs[0].Running, tabs[0].PendingPrompt, tabs[0].Cancellable, tabs[0].CancelRequested)
	}

	app.CancelTab("test")
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for turn_done")
	}
}

func TestMetaForTabLeavesGitBranchEmptyOutsideGit(t *testing.T) {
	isolateDesktopUserDirs(t)
	workspace := t.TempDir()
	app := NewApp()
	app.tabs = map[string]*WorkspaceTab{"tab-1": {
		ID:            "tab-1",
		Scope:         "project",
		WorkspaceRoot: workspace,
		Ready:         true,
		disabledMCP:   map[string]ServerView{},
	}}
	app.activeTabID = "tab-1"

	if got := app.MetaForTab("tab-1"); got.GitBranch != "" {
		t.Fatalf("gitBranch = %q, want empty", got.GitBranch)
	}
}

func TestEffortDefaultsBeforeStartup(t *testing.T) {
	isolateDesktopUserDirs(t)

	// Task 611: value assertions go through the deterministic direct read —
	// Effort()'s 2s cap fired under a loaded -count=3 round and its fallback
	// blanked the default high.
	got := NewApp().effortForTabDirect("")
	if !got.Supported || got.Current != "auto" || got.Default != "high" || !hasLevel(got.Levels, "auto") {
		t.Fatalf("pre-startup Effort() = %+v, want auto with DeepSeek default high", got)
	}
}

func TestMemoryViewReturnsNonNilArraysBeforeStartup(t *testing.T) {
	isolateDesktopUserDirs(t)

	view := NewApp().Memory()
	if view.Docs == nil || view.Facts == nil || view.Archives == nil || view.Scopes == nil || view.InstructionDiagnostics == nil || view.Conflicts == nil || view.LastRecall.Hits == nil {
		t.Fatalf("Memory() arrays must be non-nil before startup: %+v", view)
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal Memory(): %v", err)
	}
	for _, bad := range []string{`"docs":null`, `"facts":null`, `"archives":null`, `"scopes":null`, `"instructionDiagnostics":null`, `"conflicts":null`, `"hits":null`} {
		if strings.Contains(string(raw), bad) {
			t.Fatalf("Memory() JSON contains %s; frontend expects []: %s", bad, raw)
		}
	}
	if revisions := NewApp().MemoryRevisions("missing"); revisions == nil {
		t.Fatal("MemoryRevisions must return [] before startup, not nil")
	}
}
