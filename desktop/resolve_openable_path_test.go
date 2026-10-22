package main

import (
	"os"
	"path/filepath"
	"testing"
)

// 任务 1060：转录选中路径菜单的后端探针。四条验收里 1/2/3 的后端半边：
// 存在的绝对/相对路径解析成功；相对路径按会话项目根解析；Global（无项目根）
// 会话拒绝相对路径；不存在/逃逸/可执行目标返回空串（零误报）。
// noTabAtAll 标记「App 里没有任何 tab」的构造（配合 unknown-tab 用例）。
const noTabAtAll = "\x00no-tabs"

func TestResolveOpenablePathForTab(t *testing.T) {
	projectRoot := robustTempDir(t)
	if err := os.MkdirAll(filepath.Join(projectRoot, "docs", "task"), 0o755); err != nil {
		t.Fatal(err)
	}
	docFile := filepath.Join(projectRoot, "docs", "task", "notes.md")
	if err := os.WriteFile(docFile, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	exeFile := filepath.Join(projectRoot, "docs", "task", "run.bat")
	if err := os.WriteFile(exeFile, []byte("echo hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	outsideRoot := robustTempDir(t)
	outsideFile := filepath.Join(outsideRoot, "elsewhere.md")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}

	newApp := func(root string) *App {
		app := NewApp()
		if root != noTabAtAll {
			app.tabs = map[string]*WorkspaceTab{
				"t": {ID: "t", Scope: "project", WorkspaceRoot: root},
			}
			app.activeTabID = "t"
		}
		return app
	}

	cases := []struct {
		name    string
		tabID   string
		root    string
		path    string
		want    string
		wantErr bool
	}{
		{name: "absolute existing file resolves verbatim", tabID: "t", root: projectRoot, path: docFile, want: docFile},
		{name: "absolute existing directory resolves", tabID: "t", root: projectRoot, path: filepath.Join(projectRoot, "docs"), want: filepath.Join(projectRoot, "docs")},
		{name: "absolute missing file hides", tabID: "t", root: projectRoot, path: filepath.Join(projectRoot, "docs", "ghost.md"), want: ""},
		{name: "absolute outside any workspace still resolves", tabID: "t", root: projectRoot, path: outsideFile, want: outsideFile},
		{name: "absolute in global tab still resolves", tabID: "t", root: "", path: outsideFile, want: outsideFile},
		{name: "relative existing resolves against project root", tabID: "t", root: projectRoot, path: filepath.Join("docs", "task", "notes.md"), want: docFile},
		{name: "relative missing hides", tabID: "t", root: projectRoot, path: filepath.Join("docs", "task", "ghost.md"), want: ""},
		{name: "relative escaping the project root hides", tabID: "t", root: projectRoot, path: filepath.Join("..", filepath.Base(outsideRoot), "elsewhere.md"), want: ""},
		{name: "relative in a global tab hides (no project root)", tabID: "t", root: "", path: filepath.Join("docs", "task", "notes.md"), want: ""},
		{name: "relative with unknown tab hides", tabID: "ghost-tab", root: projectRoot, path: "docs/task/notes.md", want: ""},
		{name: "executable target hides (opener-family gate)", tabID: "t", root: projectRoot, path: filepath.Join("docs", "task", "run.bat"), want: ""},
		{name: "empty path hides", tabID: "t", root: projectRoot, path: "   ", want: ""},
		{name: "unsafe syntax hides", tabID: "t", root: projectRoot, path: "docs/task/notes.md:stream", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := tc.root
			if tc.tabID == "ghost-tab" {
				root = noTabAtAll // 探针拿到一个不存在的 tabID：App 里没有任何 tab
			}
			app := newApp(root)
			got, err := app.ResolveOpenablePathForTab(tc.tabID, tc.path)
			if tc.wantErr != (err != nil) {
				t.Fatalf("ResolveOpenablePathForTab(%q, %q) err = %v, wantErr %v", tc.tabID, tc.path, err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("ResolveOpenablePathForTab(%q, %q) = %q, want %q", tc.tabID, tc.path, got, tc.want)
			}
		})
	}
}
