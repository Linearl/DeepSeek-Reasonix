package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/instruction"
	"reasonix/internal/memory"
)

func TestMemoryViewIncludesRecallFreshnessAndOverrides(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	store := memory.Store{Dir: filepath.Join(root, "project"), GlobalDir: filepath.Join(root, "global")}
	if _, err := (memory.Store{Dir: store.GlobalDir}).Save(memory.Memory{
		Name: "deploy-target", Title: "Deploy target", Description: "legacy deployment target", Scope: memory.FactScopeGlobal, Type: memory.TypeProject, Body: "Deploy payments to the legacy cluster.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := (memory.Store{Dir: store.Dir}).Save(memory.Memory{
		Name: "deploy-target", Title: "Deploy target", Description: "current deployment target", Scope: memory.FactScopeProject, Type: memory.TypeProject, Body: "Deploy payments to the green cluster.",
	}); err != nil {
		t.Fatal(err)
	}
	ctrl := control.New(control.Options{Memory: &memory.Set{Store: store}})
	ctrl.Compose("deploy payments target cluster")
	app := NewApp()
	app.setTestCtrl(ctrl, "test-model")

	view := app.Memory()
	if len(view.Facts) != 2 || view.Facts[0].Freshness == "" || view.Facts[1].Freshness == "" {
		t.Fatalf("facts with freshness = %+v", view.Facts)
	}
	if len(view.Conflicts) != 1 || view.Conflicts[0].Resolution != "project_over_global" {
		t.Fatalf("conflicts = %+v", view.Conflicts)
	}
	if view.LastRecall.Query != "deploy payments target cluster" || len(view.LastRecall.Hits) != 1 || view.LastRecall.Hits[0].Scope != "project" {
		t.Fatalf("last recall = %+v", view.LastRecall)
	}
}

func TestMemoryRevisionAPIRestoresSelectedRevision(t *testing.T) {
	isolateDesktopUserDirs(t)
	store := memory.Store{Dir: t.TempDir()}
	first, err := store.SaveWithOptions(memory.Memory{Name: "fact", Description: "one", Body: "v1"}, memory.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveWithOptions(memory.Memory{ID: first.Memory.ID, Name: "fact", Description: "two", Body: "v2"}, memory.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Memory: &memory.Set{Store: store}}), "test-model")

	revisions := app.MemoryRevisions(first.Memory.ID)
	if len(revisions) != 1 || revisions[0].Revision != 1 {
		t.Fatalf("revisions = %+v", revisions)
	}
	restored, err := app.RestoreMemoryRevision(first.Memory.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Revision != 3 || restored.Body != "v1" {
		t.Fatalf("restored = %+v", restored)
	}
}

func TestMemoryViewIncludesActiveAndArchivedFacts(t *testing.T) {
	isolateDesktopUserDirs(t)
	userDir := t.TempDir()
	cwd := t.TempDir()
	store := memory.Store{Dir: filepath.Join(userDir, "projects", "test", "memory")}
	if _, err := store.Save(memory.Memory{
		Name:        "active-fact",
		Title:       "Active fact",
		Description: "Still applies",
		Type:        memory.TypeProject,
		Body:        "Active body",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(memory.Memory{
		Name:        "archived-fact",
		Description: "No longer applies",
		Type:        memory.TypeFeedback,
		Body:        "Archived body",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Archive("archived-fact"); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Memory: &memory.Set{
		Docs: []memory.Source{{
			Path: filepath.Join(cwd, "AGENTS.md"), Scope: memory.ScopeProject, Directory: cwd,
			Body: "Project instructions", Imports: []instruction.Import{{Path: filepath.Join(cwd, "shared.md"), SourcePath: filepath.Join(cwd, "AGENTS.md")}},
		}},
		InstructionDiagnostics: []instruction.Diagnostic{{Code: "import_cycle", Path: "shared.md", SourcePath: filepath.Join(cwd, "AGENTS.md"), Line: 3, Message: "cycle"}},
		Store:                  store, CWD: cwd, UserDir: userDir,
	}}), "test-model")

	view := app.Memory()
	if !view.Available || view.StoreDir != store.Dir {
		t.Fatalf("Memory() availability/store = %v/%q, want true/%q", view.Available, view.StoreDir, store.Dir)
	}
	if len(view.Docs) != 1 || view.Docs[0].Scope != "project" || !strings.Contains(view.Docs[0].Body, "Project instructions") {
		t.Fatalf("Memory() docs = %+v", view.Docs)
	}
	if view.Docs[0].Directory != cwd || len(view.Docs[0].Imports) != 1 || len(view.InstructionDiagnostics) != 1 || view.InstructionDiagnostics[0].Code != "import_cycle" {
		t.Fatalf("Memory() instruction provenance = docs %+v diagnostics %+v", view.Docs, view.InstructionDiagnostics)
	}
	if len(view.Facts) != 1 || view.Facts[0].Name != "active-fact" || view.Facts[0].Type != "project" || view.Facts[0].Scope != "project" {
		t.Fatalf("Memory() active facts = %+v", view.Facts)
	}
	if view.Facts[0].ID == "" || view.Facts[0].Revision != 1 || view.Facts[0].CreatedAt == "" || view.Facts[0].UpdatedAt == "" {
		t.Fatalf("Memory() active fact metadata = %+v", view.Facts[0])
	}
	if len(view.Archives) != 1 || view.Archives[0].Name != "archived-fact" || view.Archives[0].Type != "feedback" || view.Archives[0].Scope != "project" ||
		view.Archives[0].Path == "" || view.Archives[0].ArchivedAt == "" {
		t.Fatalf("Memory() archived facts = %+v", view.Archives)
	}
	if view.Archives[0].ID == "" || view.Archives[0].Revision != 1 || view.Archives[0].CreatedAt == "" || view.Archives[0].UpdatedAt == "" {
		t.Fatalf("Memory() archived fact metadata = %+v", view.Archives[0])
	}
	if len(view.Scopes) != 3 {
		t.Fatalf("Memory() scopes = %+v, want user/project/local", view.Scopes)
	}
}

func TestRestoreArchivedMemoryRecoversFactForCurrentSession(t *testing.T) {
	isolateDesktopUserDirs(t)
	userDir := t.TempDir()
	cwd := t.TempDir()
	store := memory.StoreFor(userDir, cwd)
	first, err := store.SaveWithOptions(memory.Memory{
		Name: "restorable-fact", Description: "recover me", Body: "Recovered guidance.",
	}, memory.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	archivePath, err := store.Archive(first.Memory.ID)
	if err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Memory: &memory.Set{Store: store, CWD: cwd, UserDir: userDir}}), "test-model")
	if _, err := app.RestoreArchivedMemory(archivePath); err != nil {
		t.Fatal(err)
	}
	view := app.Memory()
	if len(view.Facts) != 1 || view.Facts[0].ID != first.Memory.ID || view.Facts[0].Revision != 2 {
		t.Fatalf("restored memory view = %+v", view)
	}
	if len(view.Archives) != 0 {
		t.Fatalf("restored archive remained visible: %+v", view.Archives)
	}
}

func TestBeforeCloseAllowsSystemQuitWhenBackgroundCloseEnabled(t *testing.T) {
	isolateDesktopUserDirs(t)
	consumeSystemQuitRequested()
	t.Cleanup(func() { consumeSystemQuitRequested() })

	userCfg := config.LoadForEdit(config.UserConfigPath())
	if err := userCfg.SetDesktopCloseBehavior("background"); err != nil {
		t.Fatal(err)
	}
	if err := userCfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatal(err)
	}

	markSystemQuitRequested()
	if prevent := NewApp().beforeClose(context.Background()); prevent {
		t.Fatal("system quit should bypass background close-to-tray behavior")
	}
	if consumeSystemQuitRequested() {
		t.Fatal("system quit marker should be consumed by beforeClose")
	}
}

func TestBackgroundCloseHideStrategyByPlatform(t *testing.T) {
	tests := []struct {
		goos string
		want bool
	}{
		{goos: "darwin", want: true},
		{goos: "windows", want: false},
		{goos: "linux", want: false},
		{goos: "freebsd", want: false},
	}
	for _, tt := range tests {
		if got := backgroundCloseUsesApplicationHide(tt.goos); got != tt.want {
			t.Fatalf("backgroundCloseUsesApplicationHide(%q) = %v, want %v", tt.goos, got, tt.want)
		}
	}
}

func TestBackgroundCloseRequiresRestorePath(t *testing.T) {
	tests := []struct {
		name        string
		goos        string
		trayStarted bool
		trayReady   bool
		want        bool
	}{
		{name: "macOS restores from Dock", goos: "darwin", trayStarted: false, trayReady: false, want: true},
		{name: "Windows tray ready", goos: "windows", trayStarted: true, trayReady: true, want: true},
		{name: "Windows tray started but not ready", goos: "windows", trayStarted: true, trayReady: false, want: false},
		{name: "Linux tray ready", goos: "linux", trayStarted: true, trayReady: true, want: true},
		{name: "Linux tray started but not ready", goos: "linux", trayStarted: true, trayReady: false, want: false},
		{name: "Linux no tray", goos: "linux", trayStarted: false, trayReady: false, want: false},
		{name: "other Unix no tray", goos: "freebsd", trayStarted: false, trayReady: false, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := backgroundCloseHasRestorePathFor(tt.goos, tt.trayStarted, tt.trayReady); got != tt.want {
				t.Fatalf("backgroundCloseHasRestorePathFor(%q, %v, %v) = %v, want %v", tt.goos, tt.trayStarted, tt.trayReady, got, tt.want)
			}
		})
	}
}

func TestBackgroundCloseReadySignalRequiresCurrentReadyState(t *testing.T) {
	app := NewApp()
	tray := newDesktopTray()
	app.mu.Lock()
	app.tray = tray
	app.mu.Unlock()

	if app.waitForTrayReady(0) {
		t.Fatal("tray should not be ready before its ready signal")
	}

	tray.markReady()
	if app.waitForTrayReady(0) {
		t.Fatal("closed ready signal should not count without the current ready state")
	}

	app.mu.Lock()
	app.trayReady = true
	app.mu.Unlock()
	if !app.waitForTrayReady(0) {
		t.Fatal("ready state should be accepted after the tray is marked ready")
	}

	app.mu.Lock()
	app.trayReady = false
	app.mu.Unlock()
	if app.waitForTrayReady(0) {
		t.Fatal("stale ready signal should not count after the tray exits")
	}
}

func TestBackgroundCloseWaitsForTrayReadySignal(t *testing.T) {
	app := NewApp()
	tray := newDesktopTray()
	app.mu.Lock()
	app.tray = tray
	app.mu.Unlock()

	go func() {
		time.Sleep(10 * time.Millisecond)
		app.mu.Lock()
		app.trayReady = true
		app.mu.Unlock()
		tray.markReady()
	}()

	if !app.waitForTrayReady(200 * time.Millisecond) {
		t.Fatal("waitForTrayReady should observe the tray becoming ready")
	}
}

func TestBackgroundRestoreMaximiseStrategy(t *testing.T) {
	tests := []struct {
		goos      string
		maximised bool
		want      bool
	}{
		{goos: "windows", maximised: true, want: true},
		{goos: "linux", maximised: true, want: true},
		{goos: "darwin", maximised: true, want: false},
		{goos: "windows", maximised: false, want: false},
	}
	for _, tt := range tests {
		if got := backgroundRestoreShouldMaximise(tt.goos, tt.maximised); got != tt.want {
			t.Fatalf("backgroundRestoreShouldMaximise(%q, %v) = %v, want %v", tt.goos, tt.maximised, got, tt.want)
		}
	}
}

func TestBackgroundRestorePlanAvoidsNormalWindowFlash(t *testing.T) {
	tests := []struct {
		name      string
		goos      string
		maximised bool
		want      backgroundRestorePlan
	}{
		{
			name:      "maximised Windows window",
			goos:      "windows",
			maximised: true,
			want:      backgroundRestorePlan{maximiseBeforeShow: true},
		},
		{
			name:      "normal Windows window",
			goos:      "windows",
			maximised: false,
			want:      backgroundRestorePlan{unminimiseAfterShow: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := backgroundRestorePlanFor(tt.goos, tt.maximised)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("backgroundRestorePlanFor(%q, %v) = %v, want %v", tt.goos, tt.maximised, got, tt.want)
			}
		})
	}
}

func TestEmitReadyInvokesReadyHook(t *testing.T) {
	app := NewApp()
	var calls atomic.Int32
	app.readyHook = func() {
		calls.Add(1)
	}

	app.emitReady(context.TODO())

	if got := calls.Load(); got != 1 {
		t.Fatalf("ready hook calls = %d, want 1", got)
	}
}

func TestSetEffortPersistsAndAutoClears(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	if err := app.SetEffort("max"); err != nil {
		t.Fatalf("SetEffort(max): %v", err)
	}
	// Task 611: assert through the unbounded direct read, not Effort().
	// EffortForTab bounds the read at effortReadTimeout (2s wall clock) and
	// serves the cached value when the cap fires — under a full-package run the first read pays a cold config load on a busy disk, the cap fires, and the stale cached level failed this test as "want auto, got max" (327 baseline red). The bound and its fallback are 421 behavior with dedicated stubbed tests; persistence here needs a deterministic oracle.
	if got := app.effortForTabDirect("").Current; got != "max" {
		t.Fatalf("Effort current = %q, want max", got)
	}
	if err := app.SetEffort("auto"); err != nil {
		t.Fatalf("SetEffort(auto): %v", err)
	}
	if got := app.effortForTabDirect("").Current; got != "auto" {
		t.Fatalf("Effort current = %q, want auto", got)
	}
	body, err := os.ReadFile(config.UserConfigPath())
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	// Whitespace-tolerant: the TOML writer pads the assignment to the widest
	// key in the table, so the literal `effort      = "max"` spelling is an
	// alignment accident, not part of the semantics under test.
	if configLineMatches(body, "effort", `"max"`) {
		t.Fatalf("auto should clear explicit max effort:\n%s", body)
	}
}

// configLineMatches reports whether body has a top-level `key = value` line,
// tolerating arbitrary padding around the assignment.
func configLineMatches(body []byte, key, value string) bool {
	re, err := regexp.Compile(`(?m)^\s*` + regexp.QuoteMeta(key) + `\s*=\s*` + regexp.QuoteMeta(value) + `\s*$`)
	if err != nil {
		return false
	}
	return re.Match(body)
}

func TestSettingsUsesUserDesktopPreferencesNotProjectConfig(t *testing.T) {
	isolateDesktopUserDirs(t)

	project := robustTempDir(t)
	if err := os.WriteFile(filepath.Join(project, "reasonix.toml"), []byte(`
[desktop]
language = "zh"
layout_style = "workbench"
theme = "light"
theme_style = "glacier"
close_behavior = "quit"
status_bar_style = "icon"
status_bar_items = ["cost", "balance"]
`), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}

	userCfg := config.LoadForEdit(config.UserConfigPath())
	if err := userCfg.SetDesktopLanguage("en"); err != nil {
		t.Fatalf("set desktop language: %v", err)
	}
	if err := userCfg.SetDesktopLayoutStyle("classic"); err != nil {
		t.Fatalf("set desktop layout style: %v", err)
	}
	if err := userCfg.SetDesktopAppearance("dark", "graphite"); err != nil {
		t.Fatalf("set desktop appearance: %v", err)
	}
	if err := userCfg.SetDesktopTerminalTheme("light"); err != nil {
		t.Fatalf("set desktop terminal theme: %v", err)
	}
	if err := userCfg.SetDesktopCloseBehavior("background"); err != nil {
		t.Fatalf("set desktop close behavior: %v", err)
	}
	if err := userCfg.SetDesktopStatusBarStyle("text"); err != nil {
		t.Fatalf("set desktop status bar style: %v", err)
	}
	if err := userCfg.SetDesktopStatusBarItems([]string{"model", "balance", "cache"}); err != nil {
		t.Fatalf("set desktop status bar items: %v", err)
	}
	if err := userCfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save user config: %v", err)
	}

	orig, _ := os.Getwd()
	defer func() { _ = os.Chdir(orig) }()
	if err := os.Chdir(project); err != nil {
		t.Fatalf("chdir project: %v", err)
	}

	got := NewApp().Settings()
	if got.DesktopLanguage != "en" || got.DesktopLayoutStyle != "classic" || got.DesktopTheme != "dark" || got.DesktopThemeStyle != "graphite" || got.DesktopTerminalTheme != "light" || got.CloseBehavior != "background" || got.StatusBarStyle != "text" {
		t.Fatalf("desktop settings = lang:%q layout:%q theme:%q style:%q close:%q status:%q, want user-level desktop prefs", got.DesktopLanguage, got.DesktopLayoutStyle, got.DesktopTheme, got.DesktopThemeStyle, got.CloseBehavior, got.StatusBarStyle)
	}
	if want := []string{"model", "balance", "cache"}; !reflect.DeepEqual(got.StatusBarItems, want) {
		t.Fatalf("desktop status bar items = %v, want user-level %v", got.StatusBarItems, want)
	}
}

func TestDesktopStartupSettingsUsesUserDesktopPreferencesWithoutFullSettingsPayload(t *testing.T) {
	isolateDesktopUserDirs(t)

	userCfg := config.LoadForEdit(config.UserConfigPath())
	if err := userCfg.SetDesktopLanguage("en"); err != nil {
		t.Fatalf("set desktop language: %v", err)
	}
	if err := userCfg.SetDesktopLayoutStyle("classic"); err != nil {
		t.Fatalf("set desktop layout style: %v", err)
	}
	if err := userCfg.SetDesktopAppearance("dark", "graphite"); err != nil {
		t.Fatalf("set desktop appearance: %v", err)
	}
	if err := userCfg.SetDesktopTerminalTheme("light"); err != nil {
		t.Fatalf("set desktop terminal theme: %v", err)
	}
	if err := userCfg.SetDesktopStatusBarStyle("icon"); err != nil {
		t.Fatalf("set desktop status bar style: %v", err)
	}
	if err := userCfg.SetDesktopStatusBarItems([]string{"workspace", "git_branch", "model"}); err != nil {
		t.Fatalf("set desktop status bar items: %v", err)
	}
	if err := userCfg.SetDesktopCheckUpdates(false); err != nil {
		t.Fatalf("set desktop check updates: %v", err)
	}
	if err := userCfg.SetDesktopUpdateChannel("preview"); err != nil {
		t.Fatalf("set desktop update channel: %v", err)
	}
	userCfg.Bot.Enabled = true
	userCfg.Bot.Allowlist.Enabled = true
	userCfg.Bot.Allowlist.QQUsers = []string{"alice"}
	if err := userCfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save user config: %v", err)
	}

	got := NewApp().DesktopStartupSettings()
	if got.DesktopLanguage != "en" || got.DesktopLayoutStyle != "classic" || got.DesktopTheme != "dark" || got.DesktopThemeStyle != "graphite" || got.DesktopTerminalTheme != "light" || got.DisplayMode != "standard" || got.StatusBarStyle != "icon" || got.CheckUpdates || got.UpdateChannel != "stable" {
		t.Fatalf("DesktopStartupSettings desktop prefs = %+v, want user-level startup prefs", got)
	}
	if want := []string{"workspace", "git_branch", "model"}; !reflect.DeepEqual(got.StatusBarItems, want) {
		t.Fatalf("DesktopStartupSettings status bar items = %v, want %v", got.StatusBarItems, want)
	}
	if !got.Bot.Enabled || !got.Bot.Allowlist.Enabled || !reflect.DeepEqual(got.Bot.Allowlist.QQUsers, []string{"alice"}) {
		t.Fatalf("DesktopStartupSettings bot settings = %+v, want lightweight bot snapshot", got.Bot)
	}

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal DesktopStartupSettings: %v", err)
	}
	if strings.Contains(string(raw), "providers") || strings.Contains(string(raw), "officialProviders") || strings.Contains(string(raw), "providerKinds") {
		t.Fatalf("DesktopStartupSettings must not include full Settings provider payload: %s", raw)
	}
}

// Task 514: the boot snapshot must carry the task-369 selection quick-actions
// gate. The view missing the field made every restart reset the frontend
// preference store to off while the settings switch (full Settings() view) still read on — the "选区快捷操作开不了" user report.
func TestDesktopStartupSettingsCarriesSelectionActionsGate(t *testing.T) {
	isolateDesktopUserDirs(t)

	userCfg := config.LoadForEdit(config.UserConfigPath())
	userCfg.Agent.ExperimentalSelectionActions = true
	if err := userCfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save user config: %v", err)
	}

	if got := NewApp().DesktopStartupSettings(); !got.ExperimentalSelectionActions {
		t.Fatalf("DesktopStartupSettings.ExperimentalSelectionActions = false, want true (boot snapshot must carry the task-369 gate)")
	}
}

// Task 588: the boot snapshot must carry the quick-command snippets next to
// the gate. The view missing the field made applyDesktopPreferences refill
// the composer state with an empty list on every restart (gate on, + menu entry gone) until a settings save happened to refill it from the full Settings view — the "快捷指令入口从 + 菜单消失" user report.
func TestDesktopStartupSettingsCarriesQuickCommandSnippets(t *testing.T) {
	isolateDesktopUserDirs(t)

	userCfg := config.LoadForEdit(config.UserConfigPath())
	userCfg.Desktop.ExperimentalQuickCommands = true
	if err := userCfg.SetQuickCommands([]config.QuickCommandEntry{
		{Title: "pre-release关键动作", Text: "跑发布检查单"},
		{Title: "转线", Text: "切换到另一条线"},
	}); err != nil {
		t.Fatalf("set quick commands: %v", err)
	}
	if err := userCfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save user config: %v", err)
	}

	got := NewApp().DesktopStartupSettings()
	if !got.ExperimentalQuickCommands {
		t.Fatalf("DesktopStartupSettings.ExperimentalQuickCommands = false, want true")
	}
	want := []config.QuickCommandEntry{
		{Title: "pre-release关键动作", Text: "跑发布检查单"},
		{Title: "转线", Text: "切换到另一条线"},
	}
	if !reflect.DeepEqual(got.QuickCommands, want) {
		t.Fatalf("DesktopStartupSettings.QuickCommands = %+v, want %+v (boot snapshot must carry the task-588 snippets)", got.QuickCommands, want)
	}
}

func BenchmarkDesktopSettingsPayloads(b *testing.B) {
	home := b.TempDir()
	xdg := filepath.Join(home, ".config")
	appData := filepath.Join(home, "AppData")
	for _, dir := range []string{xdg, appData} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
	}
	b.Setenv("HOME", home)
	b.Setenv("REASONIX_CREDENTIALS_STORE", "file")
	b.Setenv("USERPROFILE", home)
	b.Setenv("XDG_CONFIG_HOME", xdg)
	b.Setenv("REASONIX_STATE_HOME", filepath.Join(home, "state"))
	b.Setenv("REASONIX_CACHE_HOME", filepath.Join(home, "cache"))
	b.Setenv("AppData", appData)
	b.Setenv("SHARED_PROVIDER_KEY", "sk-test")

	cfg := config.LoadForEdit(config.UserConfigPath())
	for i := range 40 {
		cfg.Providers = append(cfg.Providers, config.ProviderEntry{
			Name:      fmt.Sprintf("custom-%02d", i),
			Kind:      "openai",
			BaseURL:   "https://example.invalid/v1",
			APIKeyEnv: "SHARED_PROVIDER_KEY",
			Models:    []string{"model-a", "model-b"},
			Default:   "model-a",
		})
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		b.Fatalf("save config: %v", err)
	}
	app := NewApp()

	b.Run("Settings", func(b *testing.B) {
		for range b.N {
			_ = app.Settings()
		}
	})
	b.Run("DesktopStartupSettings", func(b *testing.B) {
		for range b.N {
			_ = app.DesktopStartupSettings()
		}
	})
}

func TestSettingsIgnoresActiveWorkspaceDotEnvCredentialsWithUserConfig(t *testing.T) {
	isolateDesktopUserDirs(t)

	project := robustTempDir(t)
	launch := robustTempDir(t)
	if err := os.WriteFile(filepath.Join(project, ".env"), []byte("WORKSPACE_ONLY_KEY=from-project\n"), 0o600); err != nil {
		t.Fatalf("write project env: %v", err)
	}
	userCfg := config.LoadForEdit(config.UserConfigPath())
	if err := userCfg.UpsertProvider(config.ProviderEntry{
		Name:      "workspace-provider",
		Kind:      "openai",
		BaseURL:   "https://workspace.example/v1",
		Model:     "workspace-model",
		APIKeyEnv: "WORKSPACE_ONLY_KEY",
	}); err != nil {
		t.Fatalf("upsert provider: %v", err)
	}
	userCfg.Desktop.ProviderAccess = []string{"workspace-provider"}
	if err := userCfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save user config: %v", err)
	}
	t.Setenv("WORKSPACE_ONLY_KEY", "")
	os.Unsetenv("WORKSPACE_ONLY_KEY")
	orig, _ := os.Getwd()
	defer func() { _ = os.Chdir(orig) }()
	if err := os.Chdir(launch); err != nil {
		t.Fatalf("chdir launch: %v", err)
	}

	app := NewApp()
	app.tabs = map[string]*WorkspaceTab{"project": {ID: "project", WorkspaceRoot: project}}
	app.activeTabID = "project"
	got := app.Settings()
	for _, p := range got.Providers {
		if p.Name == "workspace-provider" {
			if p.KeySet {
				t.Fatalf("workspace provider keySet = true, want false because workspace .env is ignored: %+v", p)
			}
			if p.Configured {
				t.Fatalf("workspace provider configured = true, want false because workspace .env is ignored: %+v", p)
			}
			return
		}
	}
	t.Fatalf("workspace provider missing from settings: %+v", got.Providers)
}

func TestSettingsShowsGlobalCredentialWithoutMutatingWorkspaceEnv(t *testing.T) {
	// 673L 定性（2026-10-09，基线 d9b7e006f 干净复现稳态红，desktop 对基线零差异）：
	// 预存红为产品缺陷，非测试过期。根因链：Settings()（settings_app.go）为任务 634
	// 的写目录面板调 mergedSandboxWriteRoots → config.LoadForRootReadOnly，该装载 loadCredentials:true → loadCredentialStoreForRoot → loadDotEnvFileAs 以 CredentialSourceCredentials 来源读全局凭据 .env；dotenv.go 对该来源跳过 「env 已存在即跳过」守卫，os.Setenv 把凭据值强制重钉进进程环境，覆盖测试在 SetCredential 之后 t.Setenv 预置的项目值（"from-project" → "from-credentials"）。 这违反两处成文契约：loadDesktopUserConfigForView「Credentials are not loaded」 与 loadDotEnvForRoot「workspace .env 不写进程环境」（多工作区凭据互不泄漏）。 Settings 展示路径本应用内存态 resolver（ResolveGlobalFirst 只读文件不钉 env）。 引入点：任务 634（c7968b57f）为 merged 沙箱读选择了带凭据的装载变体。 修复（任务 693，wt-693）：Settings() 展示路径两处 merged 装载 （mergedSandboxWriteRoots 与 populateWebSearchSettings）改用不带凭据的 LoadForRootWithoutCredentialsReadOnly，凭据装载仅保留给真正喂 runtime 的 路径；展示层 provider 凭据状态继续走内存态 ResolveGlobalFirst。本测试 去 skip 后断言零改动原样转绿（-count=3）。 证据链见 docs/report/zcode交付/zcode交付-680-Sidecars修复-673L-20261009.md 与 docs/report/zcode交付/zcode交付-693-凭据重钉修复-20261009.md。
	isolateDesktopUserDirs(t)

	project := robustTempDir(t)
	launch := robustTempDir(t)
	if err := os.WriteFile(filepath.Join(project, ".env"), []byte("SHARED_SETTINGS_KEY=from-project\n"), 0o600); err != nil {
		t.Fatalf("write project env: %v", err)
	}
	if _, err := config.SetCredential("SHARED_SETTINGS_KEY", "from-credentials"); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}
	userCfg := config.LoadForEditWithoutCredentials(config.UserConfigPath())
	if err := userCfg.UpsertProvider(config.ProviderEntry{
		Name:      "settings-provider",
		Kind:      "openai",
		BaseURL:   "https://settings.example/v1",
		Model:     "settings-model",
		APIKeyEnv: "SHARED_SETTINGS_KEY",
	}); err != nil {
		t.Fatalf("upsert provider: %v", err)
	}
	userCfg.Desktop.ProviderAccess = []string{"settings-provider"}
	if err := userCfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save user config: %v", err)
	}
	t.Setenv("SHARED_SETTINGS_KEY", "from-project")
	orig, _ := os.Getwd()
	defer func() { _ = os.Chdir(orig) }()
	if err := os.Chdir(launch); err != nil {
		t.Fatalf("chdir launch: %v", err)
	}

	app := NewApp()
	app.tabs = map[string]*WorkspaceTab{"project": {ID: "project", WorkspaceRoot: project}}
	app.activeTabID = "project"
	got := app.Settings()
	for _, p := range got.Providers {
		if p.Name != "settings-provider" {
			continue
		}
		if !p.KeySet || !strings.Contains(p.KeySource, "Reasonix credentials") {
			t.Fatalf("settings-provider key = set:%v source:%q, want Reasonix credentials: %+v", p.KeySet, p.KeySource, p)
		}
		if env := os.Getenv("SHARED_SETTINGS_KEY"); env != "from-project" {
			t.Fatalf("Settings mutated SHARED_SETTINGS_KEY = %q, want existing project env", env)
		}
		return
	}
	t.Fatalf("settings provider missing from settings: %+v", got.Providers)
}
