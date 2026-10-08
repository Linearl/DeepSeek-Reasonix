package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/control"
)

// The session picker (#9623) enumerates in-process tabs with a live
// controller, marks the active tab, and routes grants to the picked tab.

func TestListSessionWriteDirsListsLiveTabsAndMarksActive(t *testing.T) {
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"live":    {ID: "live", TopicTitle: "Live session", Ctrl: &control.Controller{}},
			"booting": {ID: "booting", TopicTitle: "Still booting"},
			"gone":    {ID: "gone", TopicTitle: "Removed", Ctrl: &control.Controller{}, removed: true},
			"other":   {ID: "other", TopicTitle: "Other session", Ctrl: &control.Controller{}},
		},
		activeTabID: "other",
	}

	listed := app.ListSessionWriteDirs()
	if len(listed) != 2 {
		t.Fatalf("ListSessionWriteDirs = %+v, want only the two tabs with controllers", listed)
	}
	if listed[0].TabID != "other" || !listed[0].Active {
		t.Fatalf("first entry = %+v, want the active tab first and marked active", listed[0])
	}
	if listed[0].Session == nil {
		t.Fatalf("active entry session = nil, want an empty non-nil slice")
	}
	if listed[1].TabID != "live" || listed[1].Active {
		t.Fatalf("second entry = %+v, want the inactive live tab", listed[1])
	}
	for _, entry := range listed {
		if strings.TrimSpace(entry.Title) == "" {
			t.Fatalf("entry %+v has an empty title", entry)
		}
	}
}

func TestSessionWriteDirsControllerRoutesByTab(t *testing.T) {
	picked := &control.Controller{}
	other := &control.Controller{}
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"picked": {ID: "picked", Ctrl: picked},
			"other":  {ID: "other", Ctrl: other},
		},
		activeTabID: "other",
	}

	got, err := app.sessionWriteDirsController("picked")
	if err != nil || got != picked {
		t.Fatalf("sessionWriteDirsController(picked) = (%v, %v), want the picked controller", got, err)
	}
	got, err = app.sessionWriteDirsController("")
	if err != nil || got != other {
		t.Fatalf("sessionWriteDirsController(\"\") = (%v, %v), want the active controller", got, err)
	}
	if _, err := app.sessionWriteDirsController("missing"); err == nil {
		t.Fatal("sessionWriteDirsController(missing) = nil error, want unknown-tab failure")
	}
	// A tab without a live controller cannot take session-scope grants.
	app.tabs["booting"] = &WorkspaceTab{ID: "booting"}
	if _, err := app.sessionWriteDirsController("booting"); err == nil {
		t.Fatal("sessionWriteDirsController(booting) = nil error, want unavailable-session failure")
	}
}

func TestRemoveAuthorizedWriteDirForTabUnknownSessionFailsClosed(t *testing.T) {
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"booting": {ID: "booting"},
		},
		activeTabID: "booting",
	}
	if err := app.RemoveAuthorizedWriteDirForTab("booting", 1, "C:/tmp"); err == nil {
		t.Fatal("RemoveAuthorizedWriteDirForTab on a controller-less tab = nil error, want failure")
	}
	if err := app.AddAuthorizedWriteDirForTab("booting", 1, "C:/tmp"); err == nil {
		t.Fatal("AddAuthorizedWriteDirForTab on a controller-less tab = nil error, want failure")
	}
}

// 任务 634：项目写目录列移除条目时两级都要落——条目在用户级 [sandbox]
// allow_write（项目文件未定义该键，merged 视图显示的就是用户级列表）时，
// 只重写项目文件移不掉它，面板上表现为「x 点击无效」。
func TestRemoveAuthorizedWriteDirDropsUserLevelEntryWithoutController(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	keep := filepath.Join(home, "keep")
	drop := filepath.Join(home, "drop")
	for _, dir := range []string{keep, drop} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	userCfg := filepath.Join(home, "config.toml")
	body := "[sandbox]\nallow_write = " + renderStringArrayForTest([]string{keep, drop}) + "\n"
	if err := os.WriteFile(userCfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir() // 无项目 reasonix.toml 的工作区

	app := &App{tabs: map[string]*WorkspaceTab{"t": {ID: "t", WorkspaceRoot: ws}}, activeTabID: "t"}
	if err := app.RemoveAuthorizedWriteDirForTab("", 0, drop); err != nil {
		t.Fatalf("RemoveAuthorizedWriteDirForTab: %v", err)
	}

	cfg, err := config.LoadUserConfigReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.AllowWriteRoots()
	if len(got) != 1 || !sameConfigPath(got[0], keep) {
		t.Fatalf("user allow_write after removal = %v, want only %s", got, keep)
	}
	for _, p := range got {
		if sameConfigPath(p, drop) {
			t.Fatalf("dropped entry %s still present in user config", drop)
		}
	}
}

// 项目文件定义了 allow_write 时，移除改写项目文件，用户级不受牵连。
func TestRemoveAuthorizedWriteDirKeepsUserLevelWhenProjectDefines(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	userDir := filepath.Join(home, "user-level")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[sandbox]\nallow_write = "+renderStringArrayForTest([]string{userDir})+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	projectDir := filepath.Join(ws, "project-level")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projectCfg := filepath.Join(ws, "reasonix.toml")
	if err := os.WriteFile(projectCfg, []byte("[sandbox]\nallow_write = "+renderStringArrayForTest([]string{projectDir})+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := &App{tabs: map[string]*WorkspaceTab{"t": {ID: "t", WorkspaceRoot: ws}}, activeTabID: "t"}
	if err := app.RemoveAuthorizedWriteDirForTab("", 0, projectDir); err != nil {
		t.Fatalf("RemoveAuthorizedWriteDirForTab: %v", err)
	}

	cfg, err := config.LoadForRootReadOnly(ws)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range cfg.AllowWriteRoots() {
		if sameConfigPath(p, projectDir) {
			t.Fatalf("project entry %s still present after removal", projectDir)
		}
	}
	userAfter, err := config.LoadUserConfigReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	userGot := userAfter.AllowWriteRoots()
	if len(userGot) != 1 || !sameConfigPath(userGot[0], userDir) {
		t.Fatalf("user allow_write must stay untouched, got %v", userGot)
	}
}

func renderStringArrayForTest(items []string) string {
	quoted := make([]string, 0, len(items))
	for _, item := range items {
		quoted = append(quoted, strconv.Quote(item))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
