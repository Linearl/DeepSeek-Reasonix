package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

// 任务 1011 桌面后端：路径校验（修复永不写到会话目录之外）、需求 E 的
// v3 heads 分组列表、开关关闭时的零改动姿态。

// desktopHealFixture writes a schema-2 session through the public agent API
// whose selected head is strictly dominated: the main chain grows longer AND
// newer after a fork, then the selection lands back on the short fork (the
// ai-passport incident shape).
func desktopHealFixture(t *testing.T, dir, name string, messages int) string {
	t.Helper()
	path := filepath.Join(dir, name)
	s := agent.NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "root question"})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	forkID, err := s.ForkHead(path, "", agent.HeadKindConcurrent, "v2")
	if err != nil {
		t.Fatal(err)
	}
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "answer on the fork"})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	// 主链继续生长：更长（3 条）且活动更晚。
	if err := s.SwitchHead(path, agent.SessionMainHead); err != nil {
		t.Fatal(err)
	}
	s.Add(provider.Message{Role: provider.RoleUser, Content: "main follow-up"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "main answer two"})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	// 最后把选择留在短且旧的 fork 上——被支配形态。
	if err := s.SwitchHead(path, forkID); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func Test1011DesktopHealRefusesPathsOutsideSessionDirs(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	inside := filepath.Join(desktopSessionDir(globalWorkspaceRoot()), "ok.jsonl")
	if err := os.MkdirAll(filepath.Dir(inside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.resolveSessionPathForHeal(inside); err != nil {
		t.Fatalf("session-dir path must pass: %v", err)
	}
	if _, err := app.resolveSessionPathForHeal(filepath.Join(t.TempDir(), "elsewhere.jsonl")); err == nil {
		t.Fatal("path outside the session directories must be refused — the heal never writes outside them")
	}
	if _, err := app.resolveSessionPathForHeal(""); err == nil {
		t.Fatal("empty path must be refused")
	}
}

func Test1011DesktopVersionGroupsListMultiHeadSessions(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := desktopSessionDir(globalWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := desktopHealFixture(t, dir, "heads.jsonl", 2)
	app := NewApp()
	groups, err := app.ListSessionVersionGroups()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, g := range groups {
		if samePath(g.MainPath, path) {
			found = true
			if g.HeadCount < 2 {
				t.Fatalf("group head count = %d, want >= 2", g.HeadCount)
			}
			if g.Selected == "" {
				t.Fatal("group must carry the selected head")
			}
		}
	}
	if !found {
		t.Fatalf("multi-head session missing from the 需求 E panel list: %v", groups)
	}
}

func Test1011DesktopRepairOffPostureLeavesDataUntouched(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := globalWorkspaceRoot()
	// 显式关闭：项目 reasonix.toml 写 false（设置页翻转即时生效的同一读取面）。
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "reasonix.toml"), []byte("[agent]\nexperimental_session_selfheal = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := desktopSessionDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := desktopHealFixture(t, dir, "offposture.jsonl", 2)
	before := hashDesktopTree(t, dir)

	app := NewApp()
	activateWorkspaceRootForTest(t, app, root)
	if app.sessionSelfHealEnabled() {
		t.Fatal("explicit false must turn the heal off")
	}
	report, err := app.RepairSessionFor(path, true, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if report.Repaired {
		t.Fatalf("off posture must not repair: %+v", report)
	}
	if report.BackupDir != "" {
		t.Fatalf("off posture must not write a backup: %q", report.BackupDir)
	}
	after := hashDesktopTree(t, dir)
	for name, sum := range before {
		if after[name] != sum {
			t.Fatalf("off posture changed %s (验收硬条目: 开关关闭只提示不改数据)", name)
		}
	}
	if len(after) != len(before) {
		t.Fatalf("off posture grew files: before=%d after=%d", len(before), len(after))
	}
}

func Test1011DesktopRepairOnPostureFixesAndLogs(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := globalWorkspaceRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "reasonix.toml"), []byte("[agent]\nexperimental_session_selfheal = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := desktopSessionDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 多 head 且选中落后：fork 后不再动它，等自愈把选择切过去。
	path := desktopHealFixture(t, dir, "onposture.jsonl", 2)
	time.Sleep(10 * time.Millisecond)

	app := NewApp()
	activateWorkspaceRootForTest(t, app, root)
	if !app.sessionSelfHealEnabled() {
		t.Fatal("explicit true must turn the heal on")
	}
	check, err := app.InspectSessionHealthFor(path)
	if err != nil {
		t.Fatal(err)
	}
	if check.Healthy || check.SuggestedHeadID == "" {
		t.Fatalf("fixture must flag head_lag with a suggestion: %+v", check)
	}
	report, err := app.RepairSessionFor(path, true, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Repaired {
		t.Fatalf("expected repair: %+v", report)
	}
	if report.HeadTo == "" || report.HeadFrom == "" {
		t.Fatalf("head switch readings missing: %+v", report)
	}
	if report.BackupDir == "" {
		t.Fatal("repair must leave a backup dir")
	}
	records, err := app.ListSessionRepairRecordsFor(path)
	if err != nil || len(records) == 0 {
		t.Fatalf("repair records missing: %d err=%v", len(records), err)
	}
}

// hashDesktopTree fingerprints the regular files directly inside dir (the
// fixture layout is flat), so the zero-write assertions are byte-exact.
func hashDesktopTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		blob, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = fmt.Sprintf("%x", sha256.Sum256(blob))
	}
	return out
}

// activateWorkspaceRootForTest points the app's active tab at root so
// activeWorkspaceRoot() (the config the heal switch reads live) resolves to
// the fixture workspace instead of the process cwd.
func activateWorkspaceRootForTest(t *testing.T, app *App, root string) {
	t.Helper()
	app.mu.Lock()
	app.tabs["tab-1011"] = &WorkspaceTab{ID: "tab-1011", WorkspaceRoot: root}
	app.activeTabID = "tab-1011"
	app.mu.Unlock()
}
