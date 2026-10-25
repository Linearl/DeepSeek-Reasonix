package main

// 任务 1145 机制测试：会话目录 SSOT 统一（desktop 侧收敛）。
//
// 覆盖任务书验收点：
//   AC1 树可见集合 == 通讯录集合（注册表 × 磁盘双向差集断言，逐条不等 count）；
//   AC2 树里可见的条目 delete_session 可触达（探针面 + confirm 清理）；
//   AC4 Task 550 对账覆盖第四源：shell 类（注册表有+目录无）与漏登类（目录有+注册表无）；
//   AC5 心跳轮转（删会话文件）后不留「树里有、目录查不到」条目；
//   AC6 只认 topic_id 识别空壳——同名标题的活会话不被指认；
//   铁律 2 关闭态行为等价现状（只上报、不动注册表）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// enableRegistryShellCleanupForTest writes the iron-rule-2 switch into the
// isolated user config (REASONIX_HOME/config.toml, set by
// isolateDesktopUserDirs).
func enableRegistryShellCleanupForTest(t *testing.T) {
	t.Helper()
	path := config.UserConfigPath()
	if strings.TrimSpace(path) == "" {
		t.Fatal("user config path unresolved; cannot enable the registry-shell switch")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("[agent]\nsession_collab_registry_shell_cleanup = true\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// seedShellProjectScene registers one project with:
//   - topic-live:  a real session file on disk, indexed in the registry;
//   - topic-shell: a registry entry with NO session file (the 1145 shell).
func seedShellProjectScene(t *testing.T) (root, dir, livePath string) {
	t.Helper()
	root = t.TempDir()
	if err := addProject(root, "Shell Project"); err != nil {
		t.Fatalf("add project: %v", err)
	}
	dir = desktopSessionDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	livePath = writeTopicSessionWithPrompt(t, dir, "live.jsonl", "topic-live", "Live", root, "hello shell scene", time.Now())
	if err := ensureTopicIndexedWithCreatedAt("project", root, "topic-live", "Live", topicTitleSourceManual, time.Now().UnixMilli()); err != nil {
		t.Fatalf("seed live topic: %v", err)
	}
	if err := ensureTopicIndexedWithCreatedAt("project", root, "topic-shell", "Shell", topicTitleSourceAuto, time.Now().UnixMilli()); err != nil {
		t.Fatalf("seed shell topic: %v", err)
	}
	return root, dir, livePath
}

// mismatchesByKind groups a reconcile result by mismatch kind.
func mismatchesByKind(result TopicInventoryReconcileResult) map[string][]TopicInventoryMismatch {
	out := map[string][]TopicInventoryMismatch{}
	for _, mismatch := range result.Mismatches {
		out[mismatch.Kind] = append(out[mismatch.Kind], mismatch)
	}
	return out
}

// assertTreeEqualsDirectory is the AC1 check: every registry-displayable
// topic (no tab, no tombstone) must be addressable by the directory scan,
// and vice versa — a two-sided set compare, never a count.
func assertTreeEqualsDirectory(t *testing.T, app *App) {
	t.Helper()
	tree := map[string]bool{}
	for topicID := range registryShellRefs() {
		tree[strings.ToLower(topicID)] = true
	}
	directory := directoryTopicIDs()
	for id := range tree {
		if !directory[id] {
			t.Fatalf("AC1 tree-only topic %q: the tree shows what the contact directory cannot address", id)
		}
	}
	for id := range directory {
		if !tree[id] {
			t.Fatalf("AC1 directory-only topic %q: the directory addresses what the tree does not list", id)
		}
	}
}

// ── AC4 shell 类 + AC1 集合一致 + AC5 轮转回归（开关开） ─────────────

func TestReconcileRegistryShellsRepairsAndConvergesTree(t *testing.T) {
	isolateDesktopUserDirs(t)
	enableRegistryShellCleanupForTest(t)
	root, dir, livePath := seedShellProjectScene(t)

	app := NewApp()
	result := app.ReconcileTopicInventory()

	// AC4: the shell is reported as its own mismatch kind, and the report is
	// post-repair truth (Repaired=true).
	kinds := mismatchesByKind(result)
	shellFound := false
	for _, mismatch := range kinds[topicInventoryRegistryTopicWithoutDirectory] {
		if mismatch.TopicID == "topic-shell" {
			shellFound = true
			if !mismatch.Repaired {
				t.Fatalf("shell mismatch not marked repaired: %+v", mismatch)
			}
			if strings.TrimSpace(mismatch.Handling) == "" {
				t.Fatalf("shell mismatch carries no handling path: %+v", mismatch)
			}
		}
	}
	if !shellFound {
		t.Fatalf("registry_topic_without_directory_entry mismatches = %+v, want the seeded shell", kinds[topicInventoryRegistryTopicWithoutDirectory])
	}

	// The repair converged the registry: shell gone (tombstoned), live kept.
	f := loadProjectsFile()
	projectIndex := -1
	for i := range f.Projects {
		if sameProjectRoot(f.Projects[i].Root, root) {
			projectIndex = i
		}
	}
	if projectIndex < 0 {
		t.Fatalf("project %q vanished from the registry", root)
	}
	if containsDesktopString(f.Projects[projectIndex].Topics, "topic-shell") {
		t.Fatal("shell topic still in the project registry after the repair")
	}
	if !containsDesktopString(f.DeletedTopics, "topic-shell") {
		t.Fatal("shell topic not tombstoned after the repair")
	}
	if !containsDesktopString(f.Projects[projectIndex].Topics, "topic-live") {
		t.Fatal("live topic was removed — the repair must never touch a topic with a session file")
	}
	if _, err := os.Stat(livePath); err != nil {
		t.Fatalf("live session file disturbed: %v", err)
	}

	// AC1: the tree view and the contact directory are now the same set.
	assertTreeEqualsDirectory(t, app)

	// AC5: simulate the heartbeat rotation — the session FILE is removed while
	// the registry entry stays. One reconcile later no tree-only entry remains
	// (the previous live topic is now itself a shell and gets repaired).
	if err := os.Remove(livePath); err != nil {
		t.Fatalf("remove live session (rotation simulation): %v", err)
	}
	_ = dir
	result = app.ReconcileTopicInventory()
	rotatedRepaired := false
	for _, mismatch := range mismatchesByKind(result)[topicInventoryRegistryTopicWithoutDirectory] {
		if mismatch.TopicID == "topic-live" && mismatch.Repaired {
			rotatedRepaired = true
		}
	}
	if !rotatedRepaired {
		t.Fatalf("rotation-turned shell not reported repaired: %+v", result.Mismatches)
	}
	f = loadProjectsFile()
	if containsDesktopString(f.Projects[projectIndex].Topics, "topic-live") {
		t.Fatal("rotation left a tree-visible entry with no session file behind (AC5)")
	}
	assertTreeEqualsDirectory(t, app)
}

// ── 铁律 2：开关关 = 只上报，注册表一字不动 ─────────────────────────

func TestReconcileRegistryShellsObservesWhenSwitchOff(t *testing.T) {
	isolateDesktopUserDirs(t)
	// No config written — the switch defaults to off.
	root, _, livePath := seedShellProjectScene(t)

	app := NewApp()
	result := app.ReconcileTopicInventory()

	kinds := mismatchesByKind(result)
	found := false
	for _, mismatch := range kinds[topicInventoryRegistryTopicWithoutDirectory] {
		if mismatch.TopicID == "topic-shell" {
			found = true
			if mismatch.Repaired {
				t.Fatal("shell repaired with the switch off — closed state must be byte-identical baseline")
			}
			if !strings.Contains(mismatch.Handling, "session_collab_registry_shell_cleanup") {
				t.Fatalf("off-state handling does not name the switch: %+v", mismatch)
			}
		}
	}
	if !found {
		t.Fatalf("switch off must still REPORT the shell (observation is side-effect free): %+v", kinds[topicInventoryRegistryTopicWithoutDirectory])
	}

	f := loadProjectsFile()
	for i := range f.Projects {
		if sameProjectRoot(f.Projects[i].Root, root) && !containsDesktopString(f.Projects[i].Topics, "topic-shell") {
			t.Fatal("switch off must leave the registry untouched")
		}
	}
	if _, err := os.Stat(livePath); err != nil {
		t.Fatalf("live session file disturbed: %v", err)
	}
}

// ── 保护：pinned 不自动移出；tab 引用不动；漏登类只观测 ────────────

func TestReconcileRegistryShellsProtectsPinnedTabsAndUnregistered(t *testing.T) {
	isolateDesktopUserDirs(t)
	enableRegistryShellCleanupForTest(t)
	root, dir, _ := seedShellProjectScene(t)

	// A pinned global shell: registered + pinned, no session file.
	if err := ensureTopicIndexedWithCreatedAt("global", "", "topic-pinned-shell", "Pinned Shell", topicTitleSourceAuto, time.Now().UnixMilli()); err != nil {
		t.Fatalf("seed pinned shell: %v", err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.GlobalPinnedTopics = append(f.GlobalPinnedTopics, "topic-pinned-shell")
		return true, nil
	}); err != nil {
		t.Fatalf("pin shell: %v", err)
	}

	app := NewApp()
	// A tab holds another fileless topic — a session possibly being created
	// right now; the tab reference must protect it.
	app.mu.Lock()
	app.tabs["creating"] = &WorkspaceTab{ID: "creating", Scope: "project", WorkspaceRoot: root,
		TopicID: "topic-tabshell", TopicTitle: "Creating"}
	app.mu.Unlock()
	if err := ensureTopicIndexedWithCreatedAt("project", root, "topic-tabshell", "Creating", topicTitleSourceAuto, time.Now().UnixMilli()); err != nil {
		t.Fatalf("seed tab shell: %v", err)
	}

	// An unregistered session: a live file whose topic never entered the
	// registry (the AC4 "漏登" class).
	writeTopicSessionWithPrompt(t, dir, "orphan.jsonl", "topic-orphan", "Orphan", root, "never registered", time.Now())

	result := app.ReconcileTopicInventory()
	kinds := mismatchesByKind(result)

	pinnedHandled := false
	for _, mismatch := range kinds[topicInventoryRegistryTopicWithoutDirectory] {
		if mismatch.TopicID == "topic-pinned-shell" {
			if mismatch.Repaired {
				t.Fatal("pinned shell was auto-removed — pinned entries stay for manual handling")
			}
			if !strings.Contains(mismatch.Handling, "置顶") {
				t.Fatalf("pinned shell handling does not say so: %+v", mismatch)
			}
			pinnedHandled = true
		}
		if mismatch.TopicID == "topic-tabshell" {
			t.Fatalf("tab-referenced topic reported as shell: %+v", mismatch)
		}
	}
	if !pinnedHandled {
		t.Fatalf("pinned shell missing from the shell mismatches: %+v", kinds[topicInventoryRegistryTopicWithoutDirectory])
	}

	unregisteredFound := false
	for _, mismatch := range kinds[topicInventoryDirectorySessionWithoutRegistry] {
		if mismatch.TopicID == "topic-orphan" {
			unregisteredFound = true
		}
	}
	if !unregisteredFound {
		t.Fatalf("unregistered class not reported: %+v", kinds[topicInventoryDirectorySessionWithoutRegistry])
	}

	// Observation only: the unregistered session is NOT auto-registered, and
	// the protected entries survive.
	f := loadProjectsFile()
	if !containsDesktopString(f.GlobalTopics, "topic-pinned-shell") {
		t.Fatal("pinned shell was removed from the registry")
	}
	projectTopics := ""
	for i := range f.Projects {
		if sameProjectRoot(f.Projects[i].Root, root) {
			for _, id := range f.Projects[i].Topics {
				projectTopics += id + ","
			}
		}
	}
	if !strings.Contains(projectTopics, "topic-tabshell") {
		t.Fatal("tab-referenced topic was removed despite the tab protection")
	}
	if strings.Contains(projectTopics, "topic-orphan") {
		t.Fatal("unregistered session was auto-registered — the reconcile observes, never registers")
	}
}

// ── AC2 + AC6：探针面（delete_session 的 desktop 实现） ─────────────

func TestRegistryShellProbeAndTrashConverge(t *testing.T) {
	isolateDesktopUserDirs(t)
	enableRegistryShellCleanupForTest(t)
	root, dir, _ := seedShellProjectScene(t)

	app := NewApp()

	// AC6: only exact topic ids nominate a shell — the live conversation's
	// title must never resolve to the shell, and the shell's own title must
	// not resolve either (it would collide with any same-titled live session).
	if topicID, _, ok := app.resolveRegistryShell("topic-shell"); !ok || topicID != "topic-shell" {
		t.Fatalf("resolveRegistryShell(topic-shell) = (%q, %v), want a hit", topicID, ok)
	}
	if _, _, ok := app.resolveRegistryShell("Live"); ok {
		t.Fatal("a title must never nominate a registry shell (AC6)")
	}
	if _, _, ok := app.resolveRegistryShell("Shell"); ok {
		t.Fatal("the shell's own title must not resolve — ids only (AC6)")
	}
	if _, _, ok := app.resolveRegistryShell("topic-live"); ok {
		t.Fatal("a topic with a session file is not a shell")
	}

	// Dry run converges nothing: the entry stays, no tombstone appears.
	if err := app.trashRegistryShell("topic-shell", true); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if containsDesktopString(loadProjectsFile().DeletedTopics, "topic-shell") {
		t.Fatal("a dry run must not tombstone the topic")
	}

	// TOCTOU guard: a transcript landing between probe and confirm flips the
	// verdict — the cleanup must refuse.
	writeTopicSessionWithPrompt(t, dir, "late.jsonl", "topic-shell", "Shell", root, "a first turn landed", time.Now())
	if err := app.trashRegistryShell("topic-shell", false); err == nil {
		t.Fatal("shell cleanup must refuse once a session file exists (TOCTOU)")
	}

	// Back to a true shell: the confirm converges the registry.
	if err := os.Remove(filepath.Join(dir, "late.jsonl")); err != nil {
		t.Fatalf("remove late transcript: %v", err)
	}
	if err := app.trashRegistryShell("topic-shell", false); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	f := loadProjectsFile()
	if !containsDesktopString(f.DeletedTopics, "topic-shell") {
		t.Fatal("confirmed shell cleanup did not tombstone the topic")
	}
	for i := range f.Projects {
		if sameProjectRoot(f.Projects[i].Root, root) && containsDesktopString(f.Projects[i].Topics, "topic-shell") {
			t.Fatal("confirmed shell cleanup left the registry entry in place")
		}
	}
}

// ── AC3：目录视角下四工具的存在性判定同源（list/info/status 一致） ──

func TestDirectoryExistenceViewIsSingleSourced(t *testing.T) {
	isolateDesktopUserDirs(t)
	root, dir, _ := seedShellProjectScene(t)
	sessionDir := config.SessionDir()

	collabIDs := agent.ScanAddressable(sessionDir, root)
	directory := map[string]bool{}
	for _, id := range collabIDs {
		if topic := strings.TrimSpace(id.TopicID); topic != "" {
			directory[strings.ToLower(topic)] = true
		}
	}
	if !directory["topic-live"] {
		t.Fatalf("directory view lost the live topic: %v", collabIDs)
	}
	if directory["topic-shell"] {
		t.Fatal("directory view must not see a registry shell — that is the 1145 fork")
	}

	// The desktop reconcile asks the SAME scan, so a converged registry makes
	// the tree equal the directory with no second implementation in between.
	enableRegistryShellCleanupForTest(t)
	app := NewApp()
	app.ReconcileTopicInventory()
	tree := map[string]bool{}
	for topicID := range registryShellRefs() {
		tree[strings.ToLower(topicID)] = true
	}
	if len(tree) == 0 {
		t.Fatalf("tree view empty after convergence (root %s, dir %s)", root, dir)
	}
	for id := range tree {
		if !directory[id] {
			t.Fatalf("tree-only topic %q after convergence — existence views diverged (AC3)", id)
		}
	}
	for id := range directory {
		if !tree[id] {
			t.Fatalf("directory-only topic %q after convergence — existence views diverged (AC3)", id)
		}
	}
}
