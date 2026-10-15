package organization

import (
	"path/filepath"
	"strings"
	"testing"
)

// seedHome points the store at a fresh temp dir for one test.
func seedHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	SetHomeDir(func() string { return dir })
	t.Cleanup(func() { SetHomeDir(nil) })
	return dir
}

func mustCreateSessionGroup(t *testing.T, scope, root, id, title, parent string) GroupResult {
	t.Helper()
	res, err := CreateSessionGroup(scope, root, id, title, parent)
	if err != nil {
		t.Fatalf("CreateSessionGroup(%q,%q): %v", id, title, err)
	}
	if !res.Applied {
		t.Fatal("CreateSessionGroup reported Applied=false")
	}
	return res
}

// 任务911 验收①：建→改名→列表可见；sidecar 与 projects 文件都持久化，
// 重新 LoadFile 读回同一 roster（双文件 round-trip）。
func TestSessionGroupCreateRenameListRoundTrip(t *testing.T) {
	seedHome(t)

	mustCreateSessionGroup(t, "global", "", "collab-dev", "开发组", "")
	created := mustCreateSessionGroup(t, "global", "", "", "测试组", "collab-dev")
	if created.GroupID == "" || created.GroupID == "collab-dev" {
		t.Fatalf("auto id = %q, want a fresh non-colliding id", created.GroupID)
	}

	// The parent must exist: unknown parents are rejected up front.
	if _, err := CreateSessionGroup("global", "", "collab-x", "孤儿组", "no-such-parent"); err == nil {
		t.Fatal("create under unknown parent succeeded")
	}

	if _, err := RenameSessionGroup("global", "", "collab-dev", "开发一组"); err != nil {
		t.Fatalf("RenameSessionGroup: %v", err)
	}

	f := LoadFile()
	if len(f.GlobalGroups) != 2 {
		t.Fatalf("roster size = %d, want 2", len(f.GlobalGroups))
	}
	byID := map[string]Group{}
	for _, g := range f.GlobalGroups {
		byID[g.ID] = g
	}
	if byID["collab-dev"].Title != "开发一组" {
		t.Fatalf("renamed title = %q", byID["collab-dev"].Title)
	}
	if byID[created.GroupID].Parent != "collab-dev" {
		t.Fatalf("child parent = %q, want collab-dev", byID[created.GroupID].Parent)
	}

	// Round-trip: a second LoadFile (fresh read of both files) sees the same.
	again := LoadFile()
	if !EqualGroups(again.GlobalGroups, f.GlobalGroups) {
		t.Fatal("second LoadFile diverged from the first — sidecar round-trip broken")
	}
}

// 任务911 验收③（提案制）：confirm=false 返回计划且零写入；confirm=true
// 才落地。升顶默认：子组提升到被删组的父层；cascade 连子树一起删；会话
// 永远只摘组不删除。
func TestSessionGroupDeleteProposalFlow(t *testing.T) {
	seedHome(t)

	mustCreateSessionGroup(t, "global", "", "g-parent", "父组", "")
	mustCreateSessionGroup(t, "global", "", "g-child", "子组", "g-parent")
	// File two topics so the plan names them.
	if _, err := SaveSessionGroups("global", "", 0, []Group{
		{ID: "g-parent", Title: "父组", TopicIDs: []string{"topic-1"}},
		{ID: "g-child", Title: "子组", TopicIDs: []string{"topic-2"}, Parent: "g-parent"},
	}); err != nil {
		t.Fatalf("seed SaveSessionGroups: %v", err)
	}

	// Proposal only.
	plan, err := DeleteSessionGroup("global", "", "g-parent", false, false)
	if err != nil {
		t.Fatalf("DeleteSessionGroup proposal: %v", err)
	}
	if plan.Applied {
		t.Fatal("proposal reported Applied=true")
	}
	if plan.Plan == nil || len(plan.Plan.Promoted) != 1 || plan.Plan.Promoted[0] != "g-child" {
		t.Fatalf("plan = %+v, want g-child promoted", plan.Plan)
	}
	snap, _ := ReadSnapshot("global", "")
	if len(snap.Groups) != 2 {
		t.Fatalf("proposal wrote anyway: roster = %+v", snap.Groups)
	}

	// Confirm: 升顶 — g-child moves to top level, topics unfiled not deleted.
	res, err := DeleteSessionGroup("global", "", "g-parent", false, true)
	if err != nil || !res.Applied {
		t.Fatalf("confirm delete: %v applied=%v", err, res.Applied)
	}
	snap, _ = ReadSnapshot("global", "")
	if len(snap.Groups) != 1 || snap.Groups[0].ID != "g-child" {
		t.Fatalf("post-delete roster = %+v", snap.Groups)
	}
	if snap.Groups[0].Parent != "" {
		t.Fatalf("promoted child parent = %q, want top level", snap.Groups[0].Parent)
	}
	filed := 0
	for _, topicID := range snap.Groups[0].TopicIDs {
		if topicID == "topic-2" {
			filed++
		}
	}
	if filed != 0 {
		t.Fatal("topic stayed filed after its group was deleted; want unfiled")
	}
}

// cascade=true removes the whole subtree in one confirmed write.
func TestSessionGroupDeleteCascade(t *testing.T) {
	seedHome(t)

	mustCreateSessionGroup(t, "global", "", "root-g", "根", "")
	mustCreateSessionGroup(t, "global", "", "mid-g", "中", "root-g")
	mustCreateSessionGroup(t, "global", "", "leaf-g", "叶", "mid-g")

	res, err := DeleteSessionGroup("global", "", "root-g", true, true)
	if err != nil || !res.Applied {
		t.Fatalf("cascade delete: %v applied=%v", err, res.Applied)
	}
	snap, _ := ReadSnapshot("global", "")
	if len(snap.Groups) != 0 {
		t.Fatalf("cascade left %+v", snap.Groups)
	}
}

// 任务911 验收②（项目分组）：建→项目归属→删组后归属置空（unfiled 语义）。
func TestProjectGroupCatalogLifecycle(t *testing.T) {
	seedHome(t)

	if _, err := CreateProjectGroup("pg-work", "工作", ""); err != nil {
		t.Fatalf("CreateProjectGroup: %v", err)
	}
	if _, err := CreateProjectGroup("pg-deep", "深度", "pg-work"); err != nil {
		t.Fatalf("CreateProjectGroup nested: %v", err)
	}
	// Register a project pointing at the group.
	root := filepath.Join(strings.TrimSpace(t.TempDir()), "proj")
	if err := UpdateFile(func(f *File) (bool, error) {
		f.Projects = append(f.Projects, Project{Root: root, Title: "示例项目", ProjectGroup: "pg-deep"})
		return true, nil
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	if _, err := RenameProjectGroup("pg-work", "工作分组"); err != nil {
		t.Fatalf("RenameProjectGroup: %v", err)
	}

	// Unknown parent and duplicate ids are rejected.
	if _, err := CreateProjectGroup("pg-work", "重复", ""); err == nil {
		t.Fatal("duplicate project group id accepted")
	}
	if _, err := CreateProjectGroup("pg-y", "孤儿", "no-such"); err == nil {
		t.Fatal("unknown parent accepted")
	}

	// Delete the direct parent with cascade: both catalog entries go and the
	// project's pointer is cleared (unfiled, not deleted).
	res, err := DeleteProjectGroup("pg-work", true, true)
	if err != nil || !res.Applied {
		t.Fatalf("DeleteProjectGroup: %v applied=%v", err, res.Applied)
	}
	f := LoadFile()
	if len(f.ProjectGroups) != 0 {
		t.Fatalf("catalog = %+v, want empty", f.ProjectGroups)
	}
	i := ProjectIndexByRoot(f.Projects, root)
	if i < 0 {
		t.Fatal("project vanished with its group; want unfiled only")
	}
	if f.Projects[i].ProjectGroup != "" {
		t.Fatalf("project group pointer = %q, want cleared", f.Projects[i].ProjectGroup)
	}
}

// 任务911 验收④（排序）：ReorderTopicInGroup 移动已归组会话的组内位置，
// index 越界钳制，未归组会话拒绝。
func TestReorderTopicInGroup(t *testing.T) {
	seedHome(t)

	if _, err := SaveSessionGroups("global", "", 0, []Group{
		{ID: "g", Title: "组", TopicIDs: []string{"a", "b", "c"}},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := ReorderTopicInGroup("global", "", "a", "g", 2); err != nil {
		t.Fatalf("reorder a→2: %v", err)
	}
	snap, _ := ReadSnapshot("global", "")
	got := strings.Join(snap.Groups[0].TopicIDs, ",")
	if got != "b,c,a" {
		t.Fatalf("order = %q, want b,c,a", got)
	}

	// Out-of-range clamps to the tail.
	if _, err := ReorderTopicInGroup("global", "", "b", "g", 99); err != nil {
		t.Fatalf("clamped reorder: %v", err)
	}
	snap, _ = ReadSnapshot("global", "")
	got = strings.Join(snap.Groups[0].TopicIDs, ",")
	if got != "c,a,b" {
		t.Fatalf("order = %q, want c,a,b", got)
	}

	// A topic that is not a member is rejected.
	if _, err := ReorderTopicInGroup("global", "", "zzz", "g", 0); err == nil {
		t.Fatal("non-member topic accepted")
	}
}

// 并发 CAS：revision 过期时保存返回 Applied=false 与当前快照，而不是覆盖。
func TestSaveSessionGroupsCASConflict(t *testing.T) {
	seedHome(t)

	first, err := SaveSessionGroups("global", "", 0, []Group{{ID: "g1", Title: "一"}})
	if err != nil || !first.Applied {
		t.Fatalf("seed: %v", err)
	}

	// A stale writer (revision 0) must lose against the bumped revision.
	stale, err := SaveSessionGroups("global", "", 0, []Group{{ID: "g2", Title: "二"}})
	if err != nil {
		t.Fatalf("stale save: %v", err)
	}
	if stale.Applied {
		t.Fatal("stale revision overwrote a newer roster")
	}
	if len(stale.Groups) != 1 || stale.Groups[0].ID != "g1" {
		t.Fatalf("conflict snapshot = %+v, want the current g1 roster", stale.Groups)
	}
}

// FileTopic 把会话移入目标组并移出其它组（desktop task-170 语义）；目标组
// 不存在则按需创建；已在目标组内幂等零写。
func TestFileTopicMovesBetweenGroups(t *testing.T) {
	seedHome(t)

	if _, err := SaveSessionGroups("global", "", 0, []Group{
		{ID: "g-a", Title: "甲组", TopicIDs: []string{"t1", "t2"}},
		{ID: "g-b", Title: "乙组", TopicIDs: []string{"t3"}},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Move t1 from g-a into g-b.
	res, err := FileTopic("global", "", "t1", "g-b", "")
	if err != nil || !res.Applied {
		t.Fatalf("FileTopic: %v applied=%v", err, res.Applied)
	}
	snap, _ := ReadSnapshot("global", "")
	byID := map[string]Group{}
	for _, g := range snap.Groups {
		byID[g.ID] = g
	}
	for _, m := range byID["g-a"].TopicIDs {
		if m == "t1" {
			t.Fatal("t1 still in g-a after move")
		}
	}
	found := false
	for _, m := range byID["g-b"].TopicIDs {
		if m == "t1" {
			found = true
		}
	}
	if !found {
		t.Fatal("t1 not in g-b after move")
	}

	// Idempotent re-file writes nothing new (Applied stays true but roster
	// is unchanged).
	before, _ := ReadSnapshot("global", "")
	if _, err := FileTopic("global", "", "t1", "g-b", ""); err != nil {
		t.Fatalf("idempotent file: %v", err)
	}
	after, _ := ReadSnapshot("global", "")
	if !EqualGroups(before.Groups, after.Groups) {
		t.Fatal("idempotent re-file changed the roster")
	}

	// A groupTitle with no id creates the group on demand.
	if _, err := FileTopic("global", "", "t2", "", "新组"); err != nil {
		t.Fatalf("create-on-demand file: %v", err)
	}
	snap, _ = ReadSnapshot("global", "")
	created := false
	for _, g := range snap.Groups {
		if g.Title == "新组" {
			created = true
			for _, m := range g.TopicIDs {
				if m == "t2" {
					t.Log("t2 filed in 新组")
				}
			}
		}
	}
	if !created {
		t.Fatal("on-demand group not created")
	}
}
