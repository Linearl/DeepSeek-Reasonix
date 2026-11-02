package main

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/control"
)

// tl1352 D 验收：侧栏过滤 recovery 副本条目。
//
// 现场（20261031）：主会话停更后活跃写入落在 recovery-*.jsonl 副本上，副本
// 以主会话文件名前缀+继承 title 出现在侧栏，与 tab 标题错位，用户判为对话
// 泄露。过滤规则：非 open 且非 active 的副本条目从侧栏排除；已打开的 tab
// （正写在副本上）与 active 条目必须保留，否则用户会丢掉自己正在用的入口；
// 副本管理走既有 recovery-copy groups 面板。
func writeTranscript1352(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"tl1352d"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func listSessionPaths1352(t *testing.T, app *App, dir string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, meta := range app.ListSessions() {
		out[meta.Path] = true
	}
	return out
}

func Test1352SidebarFiltersClosedRecoveryCopies(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	main := writeTranscript1352(t, dir, "20261024-011346-main.jsonl")
	copy := writeTranscript1352(t, dir, "20261024-011346-main-recovery-0123456789abcdef.jsonl")
	other := writeTranscript1352(t, dir, "unrelated.jsonl")

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{SessionDir: dir, SessionPath: main, Label: "test"}), "")
	defer app.activeCtrl().Close()
	installSessionCatalogForTest(t, app, dir, "global", "")

	paths := listSessionPaths1352(t, app, dir)
	if !paths[main] || !paths[other] {
		t.Fatalf("main and unrelated sessions must stay listed: %v", paths)
	}
	if paths[copy] {
		t.Fatalf("closed recovery copy must be filtered from the sidebar: %v", paths)
	}
}

func Test1352SidebarKeepsOpenAndActiveRecoveryCopies(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	main := writeTranscript1352(t, dir, "20261024-011346-main.jsonl")
	// 现场（20261031）形态：活跃写入正落在副本上——副本是当前 tab 的会话。
	copy := writeTranscript1352(t, dir, "20261024-011346-main-recovery-fedcba9876543210.jsonl")

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{SessionDir: dir, SessionPath: copy, Label: "test"}), "")
	defer app.activeCtrl().Close()
	installSessionCatalogForTest(t, app, dir, "global", "")

	paths := listSessionPaths1352(t, app, dir)
	if !paths[copy] {
		t.Fatalf("the recovery copy the active tab writes to must stay listed: %v", paths)
	}
	if !paths[main] {
		t.Fatalf("the main session must stay listed: %v", paths)
	}
}
