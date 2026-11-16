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

// tl1641：产物栏 dock 行菜单「在文件管理器中显示」「在内置终端打开」的
// reveal 探针。分流收敛验收的后端半边：可执行扩展名（.ps1/.bat）reveal
// 仍可用（shell 定位/cd 不执行目标），「用默认应用打开」的 open 探针照旧
// 拒绝同一目标（只裁打开一项的分流语义不变）；不存在/逃逸/相对+Global
// 依旧隐藏（死按钮防线与权限面零回退）。
func TestResolveRevealablePathForTab(t *testing.T) {
	projectRoot := robustTempDir(t)
	if err := os.MkdirAll(filepath.Join(projectRoot, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	mdFile := filepath.Join(projectRoot, "docs", "notes.md")
	if err := os.WriteFile(mdFile, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	ps1File := filepath.Join(projectRoot, "docs", "run.ps1")
	if err := os.WriteFile(ps1File, []byte("Write-Output hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	batFile := filepath.Join(projectRoot, "docs", "run.bat")
	if err := os.WriteFile(batFile, []byte("echo hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	exeFile := filepath.Join(projectRoot, "docs", "run.exe")
	if err := os.WriteFile(exeFile, []byte("MZ"), 0o644); err != nil {
		t.Fatal(err)
	}

	outsideRoot := robustTempDir(t)
	outsideFile := filepath.Join(outsideRoot, "elsewhere.md")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.tabs = map[string]*WorkspaceTab{
		"t": {ID: "t", Scope: "project", WorkspaceRoot: projectRoot},
	}
	app.activeTabID = "t"

	t.Run("ps1 reveals (tl1641 core: shell reveal/cd work for script types)", func(t *testing.T) {
		got, err := app.ResolveRevealablePathForTab("t", ps1File)
		if err != nil {
			t.Fatalf("reveal probe err = %v", err)
		}
		if got != ps1File {
			t.Fatalf("reveal probe(ps1) = %q, want %q", got, ps1File)
		}
		// 对照钉：同一目标 open 探针照旧隐藏（「用默认应用打开」被裁的语义不变）。
		if openGot, err := app.ResolveOpenablePathForTab("t", ps1File); err != nil || openGot != "" {
			t.Fatalf("open probe(ps1) = %q, %v; want \"\", nil", openGot, err)
		}
	})

	t.Run("bat and exe reveal too, open probe still refuses", func(t *testing.T) {
		for _, p := range []string{batFile, exeFile} {
			got, err := app.ResolveRevealablePathForTab("t", p)
			if err != nil || got != p {
				t.Fatalf("reveal probe(%q) = %q, %v; want %q, nil", p, got, err, p)
			}
			openGot, err := app.ResolveOpenablePathForTab("t", p)
			if err != nil || openGot != "" {
				t.Fatalf("open probe(%q) = %q, %v; want \"\", nil", p, openGot, err)
			}
		}
	})

	t.Run("md reveals and opens as before (zero regression)", func(t *testing.T) {
		got, err := app.ResolveRevealablePathForTab("t", mdFile)
		if err != nil || got != mdFile {
			t.Fatalf("reveal probe(md) = %q, %v; want %q, nil", got, err, mdFile)
		}
		openGot, err := app.ResolveOpenablePathForTab("t", mdFile)
		if err != nil || openGot != mdFile {
			t.Fatalf("open probe(md) = %q, %v; want %q, nil", openGot, err, mdFile)
		}
	})

	t.Run("missing file hides", func(t *testing.T) {
		got, err := app.ResolveRevealablePathForTab("t", filepath.Join(projectRoot, "docs", "ghost.md"))
		if err != nil || got != "" {
			t.Fatalf("reveal probe(missing) = %q, %v; want \"\", nil", got, err)
		}
	})

	t.Run("relative escaping the project root hides", func(t *testing.T) {
		got, err := app.ResolveRevealablePathForTab("t", filepath.Join("..", filepath.Base(outsideRoot), "elsewhere.md"))
		if err != nil || got != "" {
			t.Fatalf("reveal probe(escape) = %q, %v; want \"\", nil", got, err)
		}
	})

	t.Run("relative in a global tab hides", func(t *testing.T) {
		globalApp := NewApp()
		got, err := globalApp.ResolveRevealablePathForTab("t", filepath.Join("docs", "notes.md"))
		if err != nil || got != "" {
			t.Fatalf("reveal probe(global relative) = %q, %v; want \"\", nil", got, err)
		}
	})

	t.Run("absolute outside any workspace still reveals", func(t *testing.T) {
		got, err := app.ResolveRevealablePathForTab("t", outsideFile)
		if err != nil || got != outsideFile {
			t.Fatalf("reveal probe(absolute outside) = %q, %v; want %q, nil", got, err, outsideFile)
		}
	})

	t.Run("empty path hides", func(t *testing.T) {
		got, err := app.ResolveRevealablePathForTab("t", "   ")
		if err != nil || got != "" {
			t.Fatalf("reveal probe(empty) = %q, %v; want \"\", nil", got, err)
		}
	})
}
