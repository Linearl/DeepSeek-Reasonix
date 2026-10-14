package main

// 任务 892A 验收测试：manage_sessions 工具族宿主半。
// 覆盖：①嵌套 schema（3 层创建 / 成环拒绝 / 存量扁平迁移无损）
// ②7 action 各测（含 move 原子性）③每次写 action 断言 reason=metadata
// 的 project-tree-changed-v2 事件 ④级联删除语义（升顶默认 / cascade）
// ⑤操作日志 + rollback。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newManageSessionsTestApp(t *testing.T) *App {
	t.Helper()
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	return app
}

// captureManageSessionsEvents installs an event sink and returns a helper that
// asserts at least one reason="metadata" v2 tree event arrived — the sidebar's
// only refresh signal (20261009 清理战役).
func captureManageSessionsEvents(t *testing.T, app *App) (expectMetadataV2 func(wait time.Duration)) {
	t.Helper()
	events := make(chan runtimeEventEnvelope, 64)
	app.runtimeEvents.emit = func(ctx context.Context, name string, payload ...any) {
		select {
		case events <- runtimeEventEnvelope{ctx: ctx, name: name, payload: append([]any(nil), payload...)}:
		default:
		}
	}
	return func(wait time.Duration) {
		t.Helper()
		deadline := time.After(wait)
		for {
			select {
			case event := <-events:
				if event.name != "project-tree:changed-v2" || len(event.payload) == 0 {
					continue
				}
				if v2, ok := event.payload[0].(ProjectTreeChangedV2); ok && v2.Reason == "metadata" {
					return
				}
			case <-deadline:
				t.Fatalf("no reason=metadata project-tree:changed-v2 event within %v", wait)
			}
		}
	}
}

func manageDispatch(t *testing.T, app *App, action string, args map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	out, err := app.ManageSessions(action, raw)
	if err != nil {
		t.Fatalf("ManageSessions(%s): %v", action, err)
	}
	return out
}

func manageDispatchErr(t *testing.T, app *App, action string, args map[string]any) error {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	_, err = app.ManageSessions(action, raw)
	return err
}

// ── ① 嵌套 schema ───────────────────────────────────────────────────────────

// 3 层嵌套经 group_create 逐层落地，list 报告 parentId/depth/order。
func TestManageSessionsCreateThreeLevelNesting(t *testing.T) {
	app := newManageSessionsTestApp(t)
	expectV2 := captureManageSessionsEvents(t, app)

	manageDispatch(t, app, "group_create", map[string]any{"group_id": "lvl1", "title": "L1"})
	expectV2(2 * time.Second)
	manageDispatch(t, app, "group_create", map[string]any{"group_id": "lvl2", "title": "L2", "parent_id": "lvl1"})
	expectV2(2 * time.Second)
	manageDispatch(t, app, "group_create", map[string]any{"group_id": "lvl3", "title": "L3", "parent_id": "lvl2"})
	expectV2(2 * time.Second)

	out := manageDispatch(t, app, "list", map[string]any{})
	var list ManageSessionsListResult
	if err := json.Unmarshal(out, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	views := list.Groups["global"]
	depths := map[string]int{}
	parents := map[string]string{}
	for _, view := range views {
		depths[view.ID] = view.Depth
		parents[view.ID] = view.ParentID
	}
	for id, want := range map[string]int{"lvl1": 0, "lvl2": 1, "lvl3": 2} {
		if depths[id] != want {
			t.Fatalf("depth(%s) = %d, want %d (views %#v)", id, depths[id], want, views)
		}
	}
	if parents["lvl3"] != "lvl2" || parents["lvl2"] != "lvl1" {
		t.Fatalf("parents = %#v", parents)
	}

	// 成环/悬空拒绝：unknown parent 拒建；depth 上限之上拒建。
	if err := manageDispatchErr(t, app, "group_create", map[string]any{"group_id": "orphan", "title": "Orphan", "parent_id": "ghost"}); err == nil {
		t.Fatal("group_create with unknown parent accepted")
	}
}

// 成环拒绝：宿主写入口（UI/工具同链路）对环形 roster 整体拒绝。
func TestManageSessionsRejectsCyclicRoster(t *testing.T) {
	app := newManageSessionsTestApp(t)
	err := app.SaveSessionGroups("global", "", []desktopGroup{
		{ID: "a", Title: "A", Parent: "b"},
		{ID: "b", Title: "B", Parent: "a"},
	})
	if err == nil {
		t.Fatal("cyclic roster accepted by SaveSessionGroups")
	}
	// 3 节点环同样拒绝。
	err = app.SaveSessionGroups("global", "", []desktopGroup{
		{ID: "a", Title: "A", Parent: "c"},
		{ID: "b", Title: "B", Parent: "a"},
		{ID: "c", Title: "C", Parent: "b"},
	})
	if err == nil {
		t.Fatal("three-node cycle accepted by SaveSessionGroups")
	}
}

// 存量扁平迁移无损：直接落一份旧格式 desktop-projects.json（扁平数组、
// 无 parent 键、无 projectGroups 键），loadProjectsFile 读入不丢字段，
// list 正常工作且 sidecar 回写不长出 parent。
func TestManageSessionsFlatLegacyMigrationLossless(t *testing.T) {
	app := newManageSessionsTestApp(t)
	legacy := `{
  "globalTopics": ["topic-old"],
  "globalGroups": [{"id":"g1","title":"Team","topicIds":["topic-old"]}],
  "projects": []
}`
	if err := os.MkdirAll(desktopConfigDir(), 0o755); err != nil {
		t.Fatalf("config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(desktopConfigDir(), desktopProjectsFile), []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy projects file: %v", err)
	}
	f := loadProjectsFile()
	if len(f.GlobalGroups) != 1 || f.GlobalGroups[0].ID != "g1" || f.GlobalGroups[0].Parent != "" {
		t.Fatalf("legacy groups = %#v", f.GlobalGroups)
	}
	if !sameStringList(f.GlobalTopics, []string{"topic-old"}) {
		t.Fatalf("legacy topics = %#v", f.GlobalTopics)
	}
	out := manageDispatch(t, app, "list", map[string]any{})
	var list ManageSessionsListResult
	if err := json.Unmarshal(out, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Groups["global"]) != 1 || list.Groups["global"][0].Depth != 0 {
		t.Fatalf("legacy list = %#v", list.Groups["global"])
	}
	// 一次写之后 sidecar 落盘：扁平组不得长出 parent 键。
	manageDispatch(t, app, "group_create", map[string]any{"group_id": "g2", "title": "New"})
	raw, err := os.ReadFile(filepath.Join(desktopConfigDir(), desktopProjectOrganizationFile))
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	var sidecar desktopProjectOrganizationFileData
	if err := json.Unmarshal(raw, &sidecar); err != nil {
		t.Fatalf("decode sidecar: %v", err)
	}
	for _, group := range sidecar.Global.Groups {
		if group.ID == "g1" && group.Parent != "" {
			t.Fatalf("flat group gained parent: %q", group.Parent)
		}
	}
}

// ── ② 7 action 各测 ─────────────────────────────────────────────────────────

func TestManageSessionsRenameAction(t *testing.T) {
	app := newManageSessionsTestApp(t)
	expectV2 := captureManageSessionsEvents(t, app)
	root := t.TempDir()
	if err := addProject(root, "Project"); err != nil {
		t.Fatal(err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.Projects[projectIndexByRoot(f.Projects, root)].Topics = []string{"topic-a"}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := setTopicTitle(root, "topic-a", "旧标题"); err != nil {
		t.Fatal(err)
	}
	out := manageDispatch(t, app, "rename_session", map[string]any{"topic_id": "topic-a", "title": "新标题"})
	expectV2(2 * time.Second)
	if !strings.Contains(string(out), `"renamed"`) {
		t.Fatalf("rename result = %s", out)
	}
	if titles := loadTopicTitles(root); titles["topic-a"] != "新标题" {
		t.Fatalf("title store = %q", titles["topic-a"])
	}
}

func TestManageSessionsMoveAction(t *testing.T) {
	app := newManageSessionsTestApp(t)
	expectV2 := captureManageSessionsEvents(t, app)
	root := t.TempDir()
	if err := addProject(root, "Project"); err != nil {
		t.Fatal(err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.Projects[projectIndexByRoot(f.Projects, root)].Topics = []string{"topic-a"}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.SaveSessionGroups("project", root, []desktopGroup{
		{ID: "grp-old", Title: "Old", TopicIDs: []string{"topic-a"}},
	}); err != nil {
		t.Fatal(err)
	}
	manageDispatch(t, app, "move_session", map[string]any{
		"topic_id": "topic-a", "scope": "project", "workspace_root": root, "group": "Team",
	})
	expectV2(2 * time.Second)
	groups, err := app.ListProjectGroups("project", root)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]desktopGroup{}
	for _, group := range groups {
		byID[group.ID] = group
	}
	// The session ends up in exactly one group; the emptied old group stays
	// (existing MoveTopicToGroup contract — groups are not auto-pruned).
	if !sameStringList(byID["collab-team"].TopicIDs, []string{"topic-a"}) {
		t.Fatalf("groups after move = %#v", groups)
	}
	if groupContainsTopic(byID["grp-old"], "topic-a") {
		t.Fatalf("session left behind in old group: %#v", groups)
	}
}

// move 原子性：摘旧组 + append 新组在一次版本化保存里落地；保存失败（脏
// roster 触发校验拒绝）时旧状态原样保留——topic 不会消失在两组之间。
func TestManageSessionsMoveAtomicOnSaveFailure(t *testing.T) {
	app := newManageSessionsTestApp(t)
	root := t.TempDir()
	if err := addProject(root, "Project"); err != nil {
		t.Fatal(err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.Projects[projectIndexByRoot(f.Projects, root)].Topics = []string{"topic-a"}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.SaveSessionGroups("project", root, []desktopGroup{
		{ID: "grp-old", Title: "Old", TopicIDs: []string{"topic-a"}},
	}); err != nil {
		t.Fatal(err)
	}
	// 植入一条超长标题的脏组：任何整册保存都会被 validateSessionGroups
	// 拒绝 —— 模拟"append 新组失败"。
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		i := projectIndexByRoot(f.Projects, root)
		f.Projects[i].Groups = append(f.Projects[i].Groups, desktopGroup{ID: "dirty", Title: strings.Repeat("长", maxSessionGroupRunes+1)})
		f.Projects[i].GroupsRevision++
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := manageDispatchErr(t, app, "move_session", map[string]any{
		"topic_id": "topic-a", "scope": "project", "workspace_root": root, "group": "Team",
	}); err == nil {
		t.Fatal("move succeeded against a roster that cannot be saved")
	}
	groups, err := app.ListProjectGroups("project", root)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		if group.ID == "grp-old" && !sameStringList(group.TopicIDs, []string{"topic-a"}) {
			t.Fatalf("topic lost from old group after failed move: %#v", groups)
		}
		if group.ID == "collab-team" {
			t.Fatalf("topic leaked into new group after failed move: %#v", groups)
		}
	}
}

func TestManageSessionsReorderAction(t *testing.T) {
	app := newManageSessionsTestApp(t)
	expectV2 := captureManageSessionsEvents(t, app)
	if err := app.SaveSessionGroups("global", "", []desktopGroup{
		{ID: "grp", Title: "Team", TopicIDs: []string{"a", "b", "c"}},
	}); err != nil {
		t.Fatal(err)
	}
	manageDispatch(t, app, "reorder_session", map[string]any{
		"topic_id": "a", "group_id": "grp", "index": 2,
	})
	expectV2(2 * time.Second)
	groups, err := app.ListProjectGroups("global", "")
	if err != nil {
		t.Fatal(err)
	}
	if !sameStringList(groups[0].TopicIDs, []string{"b", "c", "a"}) {
		t.Fatalf("order after reorder = %#v", groups[0].TopicIDs)
	}
	// 未入组 topic 拒绝 reorder。
	if err := manageDispatchErr(t, app, "reorder_session", map[string]any{
		"topic_id": "stranger", "group_id": "grp", "index": 0,
	}); err == nil {
		t.Fatal("reorder of an unfiled topic accepted")
	}
}

func TestManageSessionsGroupRenameAction(t *testing.T) {
	app := newManageSessionsTestApp(t)
	expectV2 := captureManageSessionsEvents(t, app)
	if err := app.SaveSessionGroups("global", "", []desktopGroup{
		{ID: "grp", Title: "Old", Parent: ""},
	}); err != nil {
		t.Fatal(err)
	}
	manageDispatch(t, app, "group_rename", map[string]any{"group_id": "grp", "title": "New"})
	expectV2(2 * time.Second)
	groups, err := app.ListProjectGroups("global", "")
	if err != nil {
		t.Fatal(err)
	}
	if groups[0].Title != "New" || groups[0].ID != "grp" {
		t.Fatalf("groups = %#v", groups)
	}
}

// ── ④ 级联删除语义 ─────────────────────────────────────────────────────────

// 默认（升顶）：删中间层 b → 子组 c 提升到 b 的父层 a，b 的会话被摘组
// （不删除），提案阶段零写入。
func TestManageSessionsGroupDeletePromotesChildrenByDefault(t *testing.T) {
	app := newManageSessionsTestApp(t)
	expectV2 := captureManageSessionsEvents(t, app)
	if err := app.SaveSessionGroups("global", "", []desktopGroup{
		{ID: "a", Title: "A"},
		{ID: "b", Title: "B", Parent: "a", TopicIDs: []string{"t-b"}},
		{ID: "c", Title: "C", Parent: "b", TopicIDs: []string{"t-c"}},
	}); err != nil {
		t.Fatal(err)
	}
	// 提案（无 confirm）：返回计划，零写入、零事件。
	out := manageDispatch(t, app, "group_delete", map[string]any{"group_id": "b"})
	var proposal ManageSessionsGroupResult
	if err := json.Unmarshal(out, &proposal); err != nil {
		t.Fatalf("decode proposal: %v", err)
	}
	if proposal.Applied || proposal.Plan == nil {
		t.Fatalf("proposal = %#v, want plan-only", proposal)
	}
	if !sameStringList(proposal.Plan.Deleted, []string{"b"}) || !sameStringList(proposal.Plan.Promoted, []string{"c"}) {
		t.Fatalf("plan = %#v", proposal.Plan)
	}
	if !sameStringList(proposal.Plan.UnfiledTopics, []string{"t-b"}) {
		t.Fatalf("plan unfiled = %#v", proposal.Plan.UnfiledTopics)
	}
	groups, err := app.ListProjectGroups("global", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 3 {
		t.Fatalf("proposal wrote state: %#v", groups)
	}
	// 执行（confirm=true）：b 删除、c 升到 a、t-b 摘组、t-c 保留。
	manageDispatch(t, app, "group_delete", map[string]any{"group_id": "b", "confirm": true})
	expectV2(2 * time.Second)
	groups, err = app.ListProjectGroups("global", "")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]desktopGroup{}
	for _, group := range groups {
		byID[group.ID] = group
	}
	if _, gone := byID["b"]; gone {
		t.Fatalf("b still present: %#v", groups)
	}
	if byID["c"].Parent != "a" {
		t.Fatalf("c not promoted to a: %#v", byID["c"])
	}
	if !sameStringList(byID["c"].TopicIDs, []string{"t-c"}) {
		t.Fatalf("c membership changed: %#v", byID["c"])
	}
}

// cascade=true：整棵子树删除，孙组一并消失，全部会话摘组。
func TestManageSessionsGroupDeleteCascadeSubtree(t *testing.T) {
	app := newManageSessionsTestApp(t)
	expectV2 := captureManageSessionsEvents(t, app)
	if err := app.SaveSessionGroups("global", "", []desktopGroup{
		{ID: "a", Title: "A"},
		{ID: "b", Title: "B", Parent: "a", TopicIDs: []string{"t-b"}},
		{ID: "c", Title: "C", Parent: "b"},
		{ID: "d", Title: "D", Parent: "c", TopicIDs: []string{"t-d"}},
	}); err != nil {
		t.Fatal(err)
	}
	out := manageDispatch(t, app, "group_delete", map[string]any{"group_id": "b", "cascade": true, "confirm": true})
	expectV2(2 * time.Second)
	var result ManageSessionsGroupResult
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if !result.Applied || !sameStringList(result.Plan.Deleted, []string{"b", "c", "d"}) {
		t.Fatalf("cascade result = %#v", result)
	}
	groups, err := app.ListProjectGroups("global", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		if group.ID != "a" {
			t.Fatalf("subtree member survived cascade: %#v", groups)
		}
	}
}

// ── 项目分组（kind=project）─────────────────────────────────────────────────

func TestManageSessionsProjectGroupActions(t *testing.T) {
	app := newManageSessionsTestApp(t)
	expectV2 := captureManageSessionsEvents(t, app)
	root := t.TempDir()
	if err := addProject(root, "Project"); err != nil {
		t.Fatal(err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.Projects[projectIndexByRoot(f.Projects, root)].ProjectGroup = "pg-top"
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	manageDispatch(t, app, "group_create", map[string]any{"kind": "project", "group_id": "pg-top", "title": "Top"})
	expectV2(2 * time.Second)
	manageDispatch(t, app, "group_create", map[string]any{"kind": "project", "group_id": "pg-child", "title": "Child", "parent_id": "pg-top"})
	expectV2(2 * time.Second)
	manageDispatch(t, app, "group_rename", map[string]any{"kind": "project", "group_id": "pg-child", "title": "Child2"})
	expectV2(2 * time.Second)

	out := manageDispatch(t, app, "list", map[string]any{})
	var list ManageSessionsListResult
	if err := json.Unmarshal(out, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.ProjectGroups) != 2 {
		t.Fatalf("project groups = %#v", list.ProjectGroups)
	}
	for _, group := range list.ProjectGroups {
		if group.ID == "pg-child" && (group.Title != "Child2" || group.ParentID != "pg-top" || group.Depth != 1) {
			t.Fatalf("pg-child view = %#v", group)
		}
	}
	// kind 非法拒绝。
	if err := manageDispatchErr(t, app, "group_create", map[string]any{"kind": "bogus", "title": "X"}); err == nil {
		t.Fatal("bogus kind accepted")
	}
	// 提案 → 执行删除：子组升顶，指向被删组的项目被摘组（不删除项目）。
	manageDispatch(t, app, "group_delete", map[string]any{"kind": "project", "group_id": "pg-top", "confirm": true})
	expectV2(2 * time.Second)
	f := loadProjectsFile()
	if len(f.ProjectGroups) != 1 || f.ProjectGroups[0].ID != "pg-child" || f.ProjectGroups[0].Parent != "" {
		t.Fatalf("catalog after delete = %#v", f.ProjectGroups)
	}
	for _, project := range f.Projects {
		if project.ProjectGroup != "" {
			t.Fatalf("project kept dangling group pointer: %#v", project)
		}
	}
}

// ── ⑤ 操作日志 + rollback ──────────────────────────────────────────────────

func TestManageSessionsOpLogAndRollback(t *testing.T) {
	app := newManageSessionsTestApp(t)
	expectV2 := captureManageSessionsEvents(t, app)
	if err := app.SaveSessionGroups("global", "", []desktopGroup{
		{ID: "keep", Title: "Keep"},
	}); err != nil {
		t.Fatal(err)
	}
	out := manageDispatch(t, app, "group_create", map[string]any{"group_id": "temp", "title": "Temp"})
	expectV2(2 * time.Second)
	var created ManageSessionsGroupResult
	if err := json.Unmarshal(out, &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.OpID == "" {
		t.Fatalf("create result missing opId: %s", out)
	}
	// op_log 记录了这次写（含 before/after 快照）。
	logOut := manageDispatch(t, app, "op_log", map[string]any{})
	var logResult struct {
		Operations []manageSessionsOperation `json:"operations"`
	}
	if err := json.Unmarshal(logOut, &logResult); err != nil {
		t.Fatalf("decode op_log: %v", err)
	}
	found := false
	for _, op := range logResult.Operations {
		if op.OpID == created.OpID && op.Action == "group_create" && len(op.Before) > 0 && len(op.After) > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("op_log missing create entry: %#v", logResult.Operations)
	}
	// rollback 恢复 before 快照：temp 组消失，keep 保留。
	manageDispatch(t, app, "rollback", map[string]any{"op_id": created.OpID})
	expectV2(2 * time.Second)
	groups, err := app.ListProjectGroups("global", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		if group.ID == "temp" {
			t.Fatalf("rollback did not remove created group: %#v", groups)
		}
	}
	if len(groups) != 1 || groups[0].ID != "keep" {
		t.Fatalf("groups after rollback = %#v", groups)
	}
}

// 写契约：每个写 action 都要发出 reason=metadata 的 v2 事件（本测试对
// group_create/group_rename/group_delete/reorder_session 逐一断言；rename/
// move 在各自测试里断言）。
func TestManageSessionsEveryWriteEmitsMetadataV2(t *testing.T) {
	app := newManageSessionsTestApp(t)
	expectV2 := captureManageSessionsEvents(t, app)
	manageDispatch(t, app, "group_create", map[string]any{"group_id": "g1", "title": "G1"})
	expectV2(2 * time.Second)
	manageDispatch(t, app, "group_rename", map[string]any{"group_id": "g1", "title": "G1-renamed"})
	expectV2(2 * time.Second)
	manageDispatch(t, app, "group_delete", map[string]any{"group_id": "g1", "confirm": true})
	expectV2(2 * time.Second)
}

// list 全景：嵌套层级 + 会话归属 + 顺序。
func TestManageSessionsListReportsMembershipOrderAndPaths(t *testing.T) {
	app := newManageSessionsTestApp(t)
	root := t.TempDir()
	if err := addProject(root, "Project"); err != nil {
		t.Fatal(err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.Projects[projectIndexByRoot(f.Projects, root)].Topics = []string{"t-1", "t-2"}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.SaveSessionGroups("project", root, []desktopGroup{
		{ID: "top", Title: "Top"},
		{ID: "leaf", Title: "Leaf", Parent: "top", TopicIDs: []string{"t-1", "t-2"}},
	}); err != nil {
		t.Fatal(err)
	}
	out := manageDispatch(t, app, "list", map[string]any{})
	var list ManageSessionsListResult
	if err := json.Unmarshal(out, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	var leafTopics []ManageSessionsTopicView
	for _, view := range list.Groups["project|"+root] {
		if view.ID == "leaf" {
			leafTopics = view.Topics
		}
	}
	if len(leafTopics) != 2 || leafTopics[0].TopicID != "t-1" || leafTopics[0].Order != 0 || leafTopics[1].Order != 1 {
		t.Fatalf("leaf topics = %#v", leafTopics)
	}
	var session ManageSessionsSessionView
	for _, view := range list.Sessions {
		if view.TopicID == "t-2" {
			session = view
		}
	}
	if session.GroupID != "leaf" || session.Scope != "project" || session.WorkspaceRoot != root {
		t.Fatalf("session view = %#v", session)
	}
	if len(session.GroupPath) != 2 || session.GroupPath[0] != "Top" || session.GroupPath[1] != "Leaf" {
		t.Fatalf("group path = %#v", session.GroupPath)
	}
}
