package main

// tl1711 验收钉：会话右键「移动到项目」（单会话跨分区移动）。
// 验收矩阵见 docs/reasonix/issues/reports/tl1711-单会话跨项目移动-交付报告-20261120.md §0。
//
//   M1  project→project：树可见（目标 Topics 注册）+ 转录完好（.jsonl +
//       stem 前缀侧车全家搬家 + meta 身份改写）+ 源不显示（源 Topics 摘除
//       + 源目录不命中 + 跨 scope 影子条目不出现）；
//   M2  分组成员迁移：源分组名册移除该成员（组壳留源分区）、目标落为
//       未分组、GroupsRevision 名册真变才递增；
//   M3  置顶随迁：源侧置顶 → 目标侧前置插入；
//   M4  global→project：GlobalTopics 摘除 + 目标项目注册；
//   M5  project→global：同一事务纪律的反向；
//   M6  活跃拒绝：running 会话零写入；
//   M9/M11 目标同名冲突：搬家中途失败整体回滚（含 meta scope 还原）；
//   R   往返无损：A→B→A（与 global→project→global）后注册/标题/
//       createdAt/置顶/转录 marker 全部一致；
//   墓碑：曾删除过的 id 迁移后不被 DeletedTopics 卡住（显式登记复活）；
//   迷路：meta 身份与源分区不符 → 显式拒绝（errTopicMigrateMisrouted）；
//   活口：可见 tab 先关后搬（全链跑通即回归证）+ detached 空闲副本拒绝；
//   组织性成员：无会话文件的注册条目纯注册搬家（movedSessions=0）。
//
// 禁止静默 SKIP：所有分支断言到底，构造失败一律 t.Fatal。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
)

// newTopicMoveTestApp isolates user dirs, registers two projects, and returns
// the app plus both session directories (global 源钉用 globalDir，互迁钉用
// srcDir/dstDir)。
func newTopicMoveTestApp(t *testing.T) (*App, string, string, string) {
	t.Helper()
	home := isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	srcRoot := filepath.Join(home, "project-a")
	dstRoot := filepath.Join(home, "project-b")
	for _, root := range []string{srcRoot, dstRoot} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", root, err)
		}
	}
	if err := registerTestProjects(t, app, srcRoot, dstRoot); err != nil {
		t.Fatalf("register projects: %v", err)
	}
	globalDir := desktopSessionDir(globalWorkspaceRoot())
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global dir: %v", err)
	}
	return app, srcRoot, dstRoot, globalDir
}

// seedTopicState writes the per-root display state (title + created-at) the
// tree reads — the .meta TopicTitle is only a sidecar copy.
func seedTopicState(t *testing.T, root, topicID, title string, createdAt int64) {
	t.Helper()
	if err := desktopTopicState.setTitle(root, topicID, title, topicTitleSourceManual); err != nil {
		t.Fatalf("seed title %s: %v", topicID, err)
	}
	if createdAt > 0 {
		if err := saveTopicCreatedAts(root, map[string]int64{topicID: createdAt}); err != nil {
			t.Fatalf("seed created-at %s: %v", topicID, err)
		}
	}
}

// pinTopic plants the pinned flag in the given partition's registry.
func pinTopic(t *testing.T, scope, root, topicID string) {
	t.Helper()
	err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		if scope == "global" {
			f.GlobalPinnedTopics = prependUniqueString(f.GlobalPinnedTopics, topicID)
		} else {
			i := projectIndexByRoot(f.Projects, root)
			if i < 0 {
				return false, errors.New("project missing")
			}
			f.Projects[i].PinnedTopics = prependUniqueString(f.Projects[i].PinnedTopics, topicID)
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("pin topic %s: %v", topicID, err)
	}
}

// 验收 M1：project→project 未分组会话 —— 树可见/转录完好/A 不显示。
func TestMoveSessionTopicProjectToProject(t *testing.T) {
	app, srcRoot, dstRoot, _ := newTopicMoveTestApp(t)
	srcDir := desktopSessionDir(srcRoot)
	dstDir := desktopSessionDir(dstRoot)
	if err := registerTopic("project", srcRoot, topicRegisterOpts{}, "topic-1"); err != nil {
		t.Fatalf("register topic: %v", err)
	}
	seedTopicState(t, srcRoot, "topic-1", "T-topic-1", 1700000000)
	writeMigrateSessionIdentity(t, srcDir, "20261120-090000-1.jsonl", "topic-1", "project", srcRoot)

	result, err := app.MoveSessionTopic("project", srcRoot, "topic-1", dstRoot)
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if result.MovedSessions != 1 {
		t.Fatalf("receipt movedSessions = %d, want 1", result.MovedSessions)
	}

	// 树可见：目标项目 Topics 注册 + 目标目录按 topic 命中且身份为目标项目。
	f := loadProjectsFile()
	di := projectIndexByRoot(f.Projects, dstRoot)
	if di < 0 || !containsDesktopString(f.Projects[di].Topics, "topic-1") {
		t.Fatalf("target project topics = %v, want topic-1", f.Projects[di].Topics)
	}
	moved := topicSessionMatches(dstDir, "topic-1")
	if len(moved) != 1 {
		t.Fatalf("target dir matches = %d, want 1: %v", len(moved), moved)
	}
	if moved[0].scope != "project" || !sameProjectRoot(moved[0].workspaceRoot, dstRoot) {
		t.Fatalf("moved identity = %s/%s, want project/%s", moved[0].scope, moved[0].workspaceRoot, dstRoot)
	}

	// A 不显示：源 Topics 摘除 + 源目录不命中。
	si := projectIndexByRoot(f.Projects, srcRoot)
	if si < 0 || containsDesktopString(f.Projects[si].Topics, "topic-1") {
		t.Fatalf("source project still registers topic-1: %v", f.Projects[si].Topics)
	}
	if left := topicSessionMatches(srcDir, "topic-1"); len(left) != 0 {
		t.Fatalf("source dir still matches topic-1: %v", left)
	}

	// 转录完好：marker 与 stem 前缀侧车（events log）全家在目标目录。
	data, err := os.ReadFile(filepath.Join(dstDir, "20261120-090000-1.jsonl"))
	if err != nil || !strings.Contains(string(data), "topic-1") {
		t.Fatalf("transcript damaged (err=%v data=%q)", err, string(data))
	}
	if _, err := os.Stat(filepath.Join(dstDir, "20261120-090000-1.events.jsonl")); err != nil {
		t.Fatalf("events sidecar not moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(srcDir, "20261120-090000-1.events.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("events sidecar left in source dir (err=%v)", err)
	}

	// 标题分册随迁（显示名不丢）+ global 侧零影子条目。
	if title := loadTopicTitle(dstRoot, "topic-1"); title != "T-topic-1" {
		t.Fatalf("target title = %q, want T-topic-1", title)
	}
	if title := loadTopicTitle(srcRoot, "topic-1"); title != "" {
		t.Fatalf("source title = %q, want empty after move", title)
	}
	if containsDesktopString(f.GlobalTopics, "topic-1") {
		t.Fatalf("cross-scope shadow registration in GlobalTopics")
	}
}

// 验收 M2：分组成员迁移 —— 源组名册移除该成员（组壳留源分区）、目标
// 落为未分组、GroupsRevision 只在名册真变时递增。
func TestMoveSessionTopicUnfilesFromSourceGroups(t *testing.T) {
	app, srcRoot, dstRoot, _ := newTopicMoveTestApp(t)
	srcDir := desktopSessionDir(srcRoot)
	if err := registerTopic("project", srcRoot, topicRegisterOpts{}, "topic-g"); err != nil {
		t.Fatalf("register topic: %v", err)
	}
	writeMigrateSessionIdentity(t, srcDir, "20261120-091000-g.jsonl", "topic-g", "project", srcRoot)
	if err := app.SaveSessionGroups("project", srcRoot, []desktopGroup{{ID: "g-src", Title: "GSrc", TopicIDs: []string{"topic-g"}}}); err != nil {
		t.Fatalf("save source group: %v", err)
	}
	before, err := app.GetProjectGroups("project", srcRoot)
	if err != nil || before.Revision != 1 {
		t.Fatalf("fixture revision = %d err=%v", before.Revision, err)
	}

	if _, err := app.MoveSessionTopic("project", srcRoot, "topic-g", dstRoot); err != nil {
		t.Fatalf("move: %v", err)
	}

	src, err := app.GetProjectGroups("project", srcRoot)
	if err != nil || len(src.Groups) != 1 || src.Groups[0].ID != "g-src" {
		t.Fatalf("source group shell missing: %+v err=%v", src.Groups, err)
	}
	if len(src.Groups[0].TopicIDs) != 0 {
		t.Fatalf("source group still lists the moved member: %v", src.Groups[0].TopicIDs)
	}
	if src.Revision != before.Revision+1 {
		t.Fatalf("source GroupsRevision %d -> %d, want +1", before.Revision, src.Revision)
	}
	dst, err := app.GetProjectGroups("project", dstRoot)
	if err != nil || len(dst.Groups) != 0 {
		t.Fatalf("target partition unexpectedly has groups: %+v err=%v", dst.Groups, err)
	}
}

// 验收 M3：置顶随迁（源侧摘除 → 目标侧前置插入）。
func TestMoveSessionTopicMovesPin(t *testing.T) {
	app, srcRoot, dstRoot, _ := newTopicMoveTestApp(t)
	srcDir := desktopSessionDir(srcRoot)
	if err := registerTopic("project", srcRoot, topicRegisterOpts{}, "topic-pin"); err != nil {
		t.Fatalf("register topic: %v", err)
	}
	writeMigrateSessionIdentity(t, srcDir, "20261120-092000-pin.jsonl", "topic-pin", "project", srcRoot)
	pinTopic(t, "project", srcRoot, "topic-pin")

	if _, err := app.MoveSessionTopic("project", srcRoot, "topic-pin", dstRoot); err != nil {
		t.Fatalf("move: %v", err)
	}

	f := loadProjectsFile()
	di := projectIndexByRoot(f.Projects, dstRoot)
	si := projectIndexByRoot(f.Projects, srcRoot)
	if si < 0 || containsDesktopString(f.Projects[si].PinnedTopics, "topic-pin") {
		t.Fatalf("source still pins topic-pin: %v", f.Projects[si].PinnedTopics)
	}
	if di < 0 || !containsDesktopString(f.Projects[di].PinnedTopics, "topic-pin") {
		t.Fatalf("target pins = %v, want topic-pin", f.Projects[di].PinnedTopics)
	}
}

// 验收 M4：global→project（GlobalTopics 摘除 + 目标项目注册 + global 目录
// 不再命中）。
func TestMoveSessionTopicGlobalToProject(t *testing.T) {
	app, _, dstRoot, globalDir := newTopicMoveTestApp(t)
	dstDir := desktopSessionDir(dstRoot)
	if err := registerTopic("global", "", topicRegisterOpts{}, "topic-gl"); err != nil {
		t.Fatalf("register topic: %v", err)
	}
	writeMigrateSession(t, globalDir, "20261120-093000-gl.jsonl", "topic-gl")

	if _, err := app.MoveSessionTopic("global", "", "topic-gl", dstRoot); err != nil {
		t.Fatalf("move: %v", err)
	}

	f := loadProjectsFile()
	if containsDesktopString(f.GlobalTopics, "topic-gl") {
		t.Fatalf("GlobalTopics still holds topic-gl: %v", f.GlobalTopics)
	}
	di := projectIndexByRoot(f.Projects, dstRoot)
	if di < 0 || !containsDesktopString(f.Projects[di].Topics, "topic-gl") {
		t.Fatalf("target topics = %v, want topic-gl", f.Projects[di].Topics)
	}
	if left := topicSessionMatches(globalDir, "topic-gl"); len(left) != 0 {
		t.Fatalf("global dir still matches topic-gl (cross-scope shadow): %v", left)
	}
	moved := topicSessionMatches(dstDir, "topic-gl")
	if len(moved) != 1 || moved[0].scope != "project" || !sameProjectRoot(moved[0].workspaceRoot, dstRoot) {
		t.Fatalf("moved identity = %+v, want project/%s", moved, dstRoot)
	}
}

// 验收 M5：project→global（同一事务纪律的反向）。
func TestMoveSessionTopicProjectToGlobal(t *testing.T) {
	app, srcRoot, _, globalDir := newTopicMoveTestApp(t)
	srcDir := desktopSessionDir(srcRoot)
	if err := registerTopic("project", srcRoot, topicRegisterOpts{}, "topic-r"); err != nil {
		t.Fatalf("register topic: %v", err)
	}
	writeMigrateSessionIdentity(t, srcDir, "20261120-094000-r.jsonl", "topic-r", "project", srcRoot)

	if _, err := app.MoveSessionTopic("project", srcRoot, "topic-r", ""); err != nil {
		t.Fatalf("move: %v", err)
	}

	f := loadProjectsFile()
	if !containsDesktopString(f.GlobalTopics, "topic-r") {
		t.Fatalf("GlobalTopics = %v, want topic-r", f.GlobalTopics)
	}
	si := projectIndexByRoot(f.Projects, srcRoot)
	if si < 0 || containsDesktopString(f.Projects[si].Topics, "topic-r") {
		t.Fatalf("source project still registers topic-r")
	}
	if left := topicSessionMatches(srcDir, "topic-r"); len(left) != 0 {
		t.Fatalf("source dir still matches topic-r: %v", left)
	}
	moved := topicSessionMatches(globalDir, "topic-r")
	if len(moved) != 1 || moved[0].scope != "global" {
		t.Fatalf("moved identity = %+v, want global scope", moved)
	}
}

// 验收 R（往返无损）：project A→project B→project A 与 global→project→
// global 两向。终点核对：注册回到源分区、标题/createdAt/置顶/转录 marker
// 与移动前逐项一致、侧车无残留。
func TestMoveSessionTopicRoundTripLossless(t *testing.T) {
	app, srcRoot, dstRoot, globalDir := newTopicMoveTestApp(t)
	srcDir := desktopSessionDir(srcRoot)

	// 形态一：project A → project B → project A。
	if err := registerTopic("project", srcRoot, topicRegisterOpts{}, "topic-rt"); err != nil {
		t.Fatalf("register topic: %v", err)
	}
	seedTopicState(t, srcRoot, "topic-rt", "T-rt", 1700000123)
	writeMigrateSessionIdentity(t, srcDir, "20261120-095000-rt.jsonl", "topic-rt", "project", srcRoot)
	pinTopic(t, "project", srcRoot, "topic-rt")

	if _, err := app.MoveSessionTopic("project", srcRoot, "topic-rt", dstRoot); err != nil {
		t.Fatalf("move A→B: %v", err)
	}
	if _, err := app.MoveSessionTopic("project", dstRoot, "topic-rt", srcRoot); err != nil {
		t.Fatalf("move B→A: %v", err)
	}

	f := loadProjectsFile()
	si := projectIndexByRoot(f.Projects, srcRoot)
	di := projectIndexByRoot(f.Projects, dstRoot)
	if si < 0 || !containsDesktopString(f.Projects[si].Topics, "topic-rt") {
		t.Fatalf("round trip: source topics = %v, want topic-rt", f.Projects[si].Topics)
	}
	if di < 0 || containsDesktopString(f.Projects[di].Topics, "topic-rt") {
		t.Fatalf("round trip: target project still registers topic-rt")
	}
	if title := loadTopicTitle(srcRoot, "topic-rt"); title != "T-rt" {
		t.Fatalf("round trip: title = %q, want T-rt", title)
	}
	createdAts := loadTopicCreatedAts(srcRoot)
	if createdAts["topic-rt"] != 1700000123 {
		t.Fatalf("round trip: createdAt = %d, want 1700000123", createdAts["topic-rt"])
	}
	if !containsDesktopString(f.Projects[si].PinnedTopics, "topic-rt") {
		t.Fatalf("round trip: pin lost")
	}
	if containsDesktopString(f.Projects[di].PinnedTopics, "topic-rt") {
		t.Fatalf("round trip: pin leaked to target partition")
	}
	back := topicSessionMatches(srcDir, "topic-rt")
	if len(back) != 1 {
		t.Fatalf("round trip: source dir matches = %d, want 1", len(back))
	}
	if back[0].scope != "project" || !sameProjectRoot(back[0].workspaceRoot, srcRoot) {
		t.Fatalf("round trip: identity = %s/%s, want project/%s", back[0].scope, back[0].workspaceRoot, srcRoot)
	}
	data, err := os.ReadFile(filepath.Join(srcDir, "20261120-095000-rt.jsonl"))
	if err != nil || !strings.Contains(string(data), "topic-rt") {
		t.Fatalf("round trip: transcript marker lost (err=%v)", err)
	}

	// 形态二：global → project → global。
	if err := registerTopic("global", "", topicRegisterOpts{}, "topic-rt2"); err != nil {
		t.Fatalf("register topic: %v", err)
	}
	seedTopicState(t, "", "topic-rt2", "T-rt2", 1700000456)
	writeMigrateSession(t, globalDir, "20261120-095100-rt2.jsonl", "topic-rt2")

	if _, err := app.MoveSessionTopic("global", "", "topic-rt2", srcRoot); err != nil {
		t.Fatalf("move global→project: %v", err)
	}
	if _, err := app.MoveSessionTopic("project", srcRoot, "topic-rt2", ""); err != nil {
		t.Fatalf("move project→global: %v", err)
	}

	f = loadProjectsFile()
	if !containsDesktopString(f.GlobalTopics, "topic-rt2") {
		t.Fatalf("round trip 2: GlobalTopics = %v, want topic-rt2", f.GlobalTopics)
	}
	if title := loadTopicTitle("", "topic-rt2"); title != "T-rt2" {
		t.Fatalf("round trip 2: title = %q, want T-rt2", title)
	}
	createdAts = loadTopicCreatedAts("")
	if createdAts["topic-rt2"] != 1700000456 {
		t.Fatalf("round trip 2: createdAt = %d, want 1700000456", createdAts["topic-rt2"])
	}
	if back2 := topicSessionMatches(globalDir, "topic-rt2"); len(back2) != 1 || back2[0].scope != "global" {
		t.Fatalf("round trip 2: global matches = %+v, want 1 global-scope match", back2)
	}
}

// 墓钉：曾删除过的 id（DeletedTopics 墓碑）迁移后不被卡住——显式登记
// 复活语义与分组迁移同款。
func TestMoveSessionTopicClearsTombstone(t *testing.T) {
	app, srcRoot, dstRoot, _ := newTopicMoveTestApp(t)
	srcDir := desktopSessionDir(srcRoot)
	if err := registerTopic("project", srcRoot, topicRegisterOpts{}, "topic-tomb"); err != nil {
		t.Fatalf("register topic: %v", err)
	}
	writeMigrateSessionIdentity(t, srcDir, "20261120-096000-tomb.jsonl", "topic-tomb", "project", srcRoot)
	err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.DeletedTopics = append(f.DeletedTopics, "topic-tomb")
		return true, nil
	})
	if err != nil {
		t.Fatalf("plant tombstone: %v", err)
	}

	if _, err := app.MoveSessionTopic("project", srcRoot, "topic-tomb", dstRoot); err != nil {
		t.Fatalf("move: %v", err)
	}

	f := loadProjectsFile()
	if containsDesktopString(f.DeletedTopics, "topic-tomb") {
		t.Fatalf("tombstone survived the move: %v", f.DeletedTopics)
	}
	di := projectIndexByRoot(f.Projects, dstRoot)
	if di < 0 || !containsDesktopString(f.Projects[di].Topics, "topic-tomb") {
		t.Fatalf("target topics = %v, want topic-tomb", f.Projects[di].Topics)
	}
}

// 验收 M6：活跃拒绝（running 会话零写入）。
func TestMoveSessionTopicRefusesWhileActive(t *testing.T) {
	app, srcRoot, dstRoot, _ := newTopicMoveTestApp(t)
	srcDir := desktopSessionDir(srcRoot)
	if err := registerTopic("project", srcRoot, topicRegisterOpts{}, "topic-busy"); err != nil {
		t.Fatalf("register topic: %v", err)
	}
	writeMigrateSessionIdentity(t, srcDir, "20261120-097000-busy.jsonl", "topic-busy", "project", srcRoot)
	before, err := os.ReadFile(filepath.Join(srcDir, "20261120-097000-busy.jsonl"))
	if err != nil {
		t.Fatalf("read source transcript: %v", err)
	}
	app.mu.Lock()
	app.tabs = map[string]*WorkspaceTab{
		"t1": {ID: "t1", TopicID: "topic-busy", Ctrl: &migrateBusyCtrl{status: control.RuntimeStatus{Running: true}}},
	}
	app.mu.Unlock()

	if _, err := app.MoveSessionTopic("project", srcRoot, "topic-busy", dstRoot); !errors.Is(err, errTopicMigrateActiveWork) {
		t.Fatalf("err = %v, want errTopicMigrateActiveWork", err)
	}

	// 磁盘零变化：文件原地、注册原地、目标目录零条目。
	after, err := os.ReadFile(filepath.Join(srcDir, "20261120-097000-busy.jsonl"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("source transcript changed during refused move (err=%v)", err)
	}
	f := loadProjectsFile()
	si := projectIndexByRoot(f.Projects, srcRoot)
	if si < 0 || !containsDesktopString(f.Projects[si].Topics, "topic-busy") {
		t.Fatalf("source registration disturbed by refused move")
	}
	if topicSessionMatches(desktopSessionDir(dstRoot), "topic-busy") != nil {
		t.Fatalf("session appeared in target dir despite refusal")
	}
}

// 迷路拒绝：meta 身份与源分区不符 → errTopicMigrateMisrouted、零写入
// （单会话移动不静默剔除——点名操作假成功比失败更糟）。
func TestMoveSessionTopicRefusesForeignIdentity(t *testing.T) {
	app, srcRoot, dstRoot, _ := newTopicMoveTestApp(t)
	srcDir := desktopSessionDir(srcRoot)
	foreignRoot := filepath.Join(filepath.Dir(srcRoot), "project-foreign")
	if err := os.MkdirAll(foreignRoot, 0o755); err != nil {
		t.Fatalf("mkdir foreign root: %v", err)
	}
	if err := registerTestProjects(t, app, foreignRoot); err != nil {
		t.Fatalf("register foreign project: %v", err)
	}
	if err := registerTopic("project", srcRoot, topicRegisterOpts{}, "topic-lost"); err != nil {
		t.Fatalf("register topic: %v", err)
	}
	// 注册在 project A 名下，但文件 meta 指向另一个项目（跨区影子形态）。
	writeMigrateSessionIdentity(t, srcDir, "20261120-098000-lost.jsonl", "topic-lost", "project", foreignRoot)

	if _, err := app.MoveSessionTopic("project", srcRoot, "topic-lost", dstRoot); !errors.Is(err, errTopicMigrateMisrouted) {
		t.Fatalf("err = %v, want errTopicMigrateMisrouted", err)
	}

	// 零写入：文件原地、meta 身份原地、注册原地。
	matches := topicSessionMatches(srcDir, "topic-lost")
	if len(matches) != 1 {
		t.Fatalf("file disturbed by refused move: %v", matches)
	}
	meta, ok, err := agent.LoadBranchMeta(filepath.Join(srcDir, "20261120-098000-lost.jsonl"))
	if err != nil || !ok || !sameProjectRoot(meta.WorkspaceRoot, foreignRoot) {
		t.Fatalf("meta disturbed: ok=%v err=%v", ok, err)
	}
	f := loadProjectsFile()
	si := projectIndexByRoot(f.Projects, srcRoot)
	if si < 0 || !containsDesktopString(f.Projects[si].Topics, "topic-lost") {
		t.Fatalf("source registration disturbed by refused move")
	}
}

// 验收 M9/M11：目标目录同名冲突 → 搬家中途失败整体回滚（含 meta scope
// 还原——半迁移态为红）。
func TestMoveSessionTopicRollsBackOnTargetCollision(t *testing.T) {
	app, srcRoot, dstRoot, _ := newTopicMoveTestApp(t)
	srcDir := desktopSessionDir(srcRoot)
	dstDir := desktopSessionDir(dstRoot)
	if err := registerTopic("project", srcRoot, topicRegisterOpts{}, "topic-coll"); err != nil {
		t.Fatalf("register topic: %v", err)
	}
	writeMigrateSessionIdentity(t, srcDir, "20261120-099000-coll.jsonl", "topic-coll", "project", srcRoot)
	// 目标目录已占同名转录（含同名 meta：身份改写步骤同样会撞）。
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatalf("mkdir dst: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "20261120-099000-coll.jsonl"), []byte("obstacle\n"), 0o644); err != nil {
		t.Fatalf("plant collision: %v", err)
	}

	if _, err := app.MoveSessionTopic("project", srcRoot, "topic-coll", dstRoot); err == nil {
		t.Fatalf("move succeeded despite destination name collision")
	}

	// 磁盘回到移动前：转录在源目录、meta 身份未改、注册在源分区、障碍物
	// 未被覆盖。
	data, err := os.ReadFile(filepath.Join(srcDir, "20261120-099000-coll.jsonl"))
	if err != nil || !strings.Contains(string(data), "topic-coll") {
		t.Fatalf("transcript not restored to source dir (err=%v)", err)
	}
	meta, ok, err := agent.LoadBranchMeta(filepath.Join(srcDir, "20261120-099000-coll.jsonl"))
	if err != nil || !ok || meta.DefaultScope() != "project" || !sameProjectRoot(meta.WorkspaceRoot, srcRoot) {
		t.Fatalf("meta scope not restored: ok=%v scope=%q err=%v", ok, meta.Scope, err)
	}
	f := loadProjectsFile()
	si := projectIndexByRoot(f.Projects, srcRoot)
	if si < 0 || !containsDesktopString(f.Projects[si].Topics, "topic-coll") {
		t.Fatalf("source registration lost despite rollback")
	}
	obstacle, err := os.ReadFile(filepath.Join(dstDir, "20261120-099000-coll.jsonl"))
	if err != nil || string(obstacle) != "obstacle\n" {
		t.Fatalf("pre-planted obstacle was clobbered (err=%v)", err)
	}
	if topicSessionMatches(dstDir, "topic-coll") != nil {
		t.Fatalf("target dir matches the topic despite rollback")
	}
}

// 验收 M8（后端兜底）：同分区拒绝（project→自身、global→global）。
func TestMoveSessionTopicSamePartitionRejected(t *testing.T) {
	app, srcRoot, _, _ := newTopicMoveTestApp(t)
	if _, err := app.MoveSessionTopic("project", srcRoot, "topic-x", srcRoot); !errors.Is(err, errTopicMigrateSamePartition) {
		t.Fatalf("project→self err = %v, want errTopicMigrateSamePartition", err)
	}
	if _, err := app.MoveSessionTopic("global", "", "topic-x", ""); !errors.Is(err, errTopicMigrateSamePartition) {
		t.Fatalf("global→global err = %v, want errTopicMigrateSamePartition", err)
	}
}

// 组织性成员：注册表有条目、盘面无会话文件 → 纯注册搬家（movedSessions=0），
// 与分组迁移 resolved=false 成员同语义。
func TestMoveSessionTopicMovesRegistrationWithoutSession(t *testing.T) {
	app, srcRoot, dstRoot, _ := newTopicMoveTestApp(t)
	if err := registerTopic("project", srcRoot, topicRegisterOpts{}, "topic-org"); err != nil {
		t.Fatalf("register topic: %v", err)
	}

	result, err := app.MoveSessionTopic("project", srcRoot, "topic-org", dstRoot)
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if result.MovedSessions != 0 {
		t.Fatalf("receipt movedSessions = %d, want 0", result.MovedSessions)
	}
	f := loadProjectsFile()
	si := projectIndexByRoot(f.Projects, srcRoot)
	di := projectIndexByRoot(f.Projects, dstRoot)
	if si < 0 || containsDesktopString(f.Projects[si].Topics, "topic-org") {
		t.Fatalf("source still registers topic-org")
	}
	if di < 0 || !containsDesktopString(f.Projects[di].Topics, "topic-org") {
		t.Fatalf("target topics = %v, want topic-org", f.Projects[di].Topics)
	}
}

// 活口收口：可见 tab 打开时移动先关 tab 再落盘（复用 tl1560 关 tab 编排；
// 全链进程内跑通即回归证，10s 看门狗钉互斥重入死锁形态）。
func TestMoveSessionTopicClosesOpenTabAndProceeds(t *testing.T) {
	app, srcRoot, dstRoot, _ := newTopicMoveTestApp(t)
	srcDir := desktopSessionDir(srcRoot)
	if err := registerTopic("project", srcRoot, topicRegisterOpts{}, "topic-tab"); err != nil {
		t.Fatalf("register topic: %v", err)
	}
	writeMigrateSessionIdentity(t, srcDir, "20261120-100000-tab.jsonl", "topic-tab", "project", srcRoot)
	app.mu.Lock()
	app.tabs = map[string]*WorkspaceTab{
		// Ctrl=nil：closeTabRuntime 全链对 nil ctrl 有守卫，等价「空闲且可关」。
		"t1": {ID: "t1", TopicID: "topic-tab"},
	}
	app.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, err := app.MoveSessionTopic("project", srcRoot, "topic-tab", dstRoot)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("move with an open tab: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("topic move deadlocked while closing the open tab")
	}
	app.mu.RLock()
	stillBound := 0
	for _, tab := range app.tabs {
		if tab != nil && tab.TopicID == "topic-tab" {
			stillBound++
		}
	}
	app.mu.RUnlock()
	if stillBound != 0 {
		t.Fatalf("tab survived the move: %d bound tabs", stillBound)
	}
	if topicSessionMatches(desktopSessionDir(dstRoot), "topic-tab") == nil {
		t.Fatalf("session not moved despite successful tab close")
	}
}

// detached 空闲副本持有 → 可指认拒绝（复用 tl1560 同款语义）。
func TestMoveSessionTopicRefusesDetachedHolder(t *testing.T) {
	app, srcRoot, dstRoot, _ := newTopicMoveTestApp(t)
	srcDir := desktopSessionDir(srcRoot)
	if err := registerTopic("project", srcRoot, topicRegisterOpts{}, "topic-det"); err != nil {
		t.Fatalf("register topic: %v", err)
	}
	writeMigrateSessionIdentity(t, srcDir, "20261120-101000-det.jsonl", "topic-det", "project", srcRoot)
	app.mu.Lock()
	app.detachedSessions = map[string]*WorkspaceTab{
		sessionRuntimeKey(filepath.Join(srcDir, "20261120-101000-det.jsonl")): {
			ID: "d1", TopicID: "topic-det", Ctrl: migrateIdleCtrl{},
		},
	}
	app.mu.Unlock()

	_, err := app.MoveSessionTopic("project", srcRoot, "topic-det", dstRoot)
	if !errors.Is(err, errTopicMigrateDetachedHolder) {
		t.Fatalf("err = %v, want errTopicMigrateDetachedHolder", err)
	}
	f := loadProjectsFile()
	si := projectIndexByRoot(f.Projects, srcRoot)
	if si < 0 || !containsDesktopString(f.Projects[si].Topics, "topic-det") {
		t.Fatalf("source registration disturbed by detached refusal")
	}
	if topicSessionMatches(desktopSessionDir(dstRoot), "topic-det") != nil {
		t.Fatalf("session moved despite detached refusal")
	}
}
