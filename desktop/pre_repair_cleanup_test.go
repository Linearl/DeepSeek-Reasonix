package main

// tl1603: 改写前备份清理桥的后端单测。
//
// tl1646（2026-11-16 用户裁决）语义修订：tl1603 的「仅存副本禁删」（E5 闸①）
// 被推翻——孤儿副本（宿主不在且回收站无家族）允许删除，唯一副本的警示在前端
// 确认弹窗承担；后端唯一保留的闸=活宿主健康（重放级）。验收=四态+回收语义：
//  ① 可删——活宿主健康/宿主已走删除链（回收站桶里有家族成员）的备份，删除
//     成功且**必须经平台回收站 API**（替换 workspaceTrashPlatform 钉死路径，
//     任务 855 铁律的既有测试先例）；
//  ② 孤儿副本放行（tl1646）——宿主不在、回收站无家族的备份删除成功、走平台
//     回收站、目录消失（删除决定权在用户）；
//  ③ 拒删提示——活宿主体检不过一条拒绝分支：错误带原因、平台 API 零调用、
//     目录原样；
//  ④ 批量清归零——批量后重扫归零（孤儿副本在批量中同样被清理）。
//
// 健康闸经 preRepairHealthInspect 测试缝喂假报告（拒绝分支无需构造超窗会话）；
// 「可删」态跑真缝（裸转录=healthy，真实接线全链路）。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
)

type preRepairTrashSpy struct {
	calls []string
	fail  error
	bin   string
}

// withPreRepairTrashSpy 替换平台回收站缝（workspaceTrashPlatform 的既有测试
// 先例）：钉死「删除必须经平台回收站 API」这条路径（任务 855 铁律），并**真
// 的把条目搬进测试自己的桶**（os.Rename 模拟 SHFileOperation 的搬移语义），
// 让「删后重扫归零」走的是与生产同构的端到端形态。
func withPreRepairTrashSpy(t *testing.T, fail error) *preRepairTrashSpy {
	t.Helper()
	spy := &preRepairTrashSpy{fail: fail, bin: t.TempDir()}
	previous := workspaceTrashPlatform
	workspaceTrashPlatform = func(paths []string) error {
		if spy.fail != nil {
			return spy.fail
		}
		for i, path := range paths {
			if err := os.Rename(path, filepath.Join(spy.bin, fmt.Sprintf("%03d-%s", i, filepath.Base(path)))); err != nil {
				return err
			}
		}
		spy.calls = append(spy.calls, paths...)
		return nil
	}
	t.Cleanup(func() { workspaceTrashPlatform = previous })
	return spy
}

// stagePreRepairFixture 建一个**已知会话目录形态**的树：<REASONIX_STATE_HOME>/
// projects/proj/sessions（config.AllProjectSessionDirs 只认这一引擎托管形态），
// 返回 sessions 目录。路径闸（knownSessionDirs）对其它形态一律拒绝。
func stagePreRepairFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("REASONIX_STATE_HOME", root)
	sessions := filepath.Join(root, "projects", "proj", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	return sessions
}

func writeLiveOwner(t *testing.T, sessions, stem string) string {
	t.Helper()
	owner := filepath.Join(sessions, stem+".jsonl")
	// 空转录=健康的最小形态（probe/load 均通过、零消息零 token）——健康闸的
	// 「可删」分支跑的是真缝（不用假报告）。
	if err := os.WriteFile(owner, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return owner
}

// writeTrashBucket 模拟删除/归档链的回收站桶：<sessions>/.trash/<bucket>/ 内
// 放一份 stem 家族成员（memberBase 用 <stem>.jsonl 或 <stem>.pre-repair-<stamp>）。
func writeTrashBucket(t *testing.T, sessions, bucket, memberBase string) {
	t.Helper()
	dir := filepath.Join(sessions, ".trash", bucket)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, memberBase), []byte("archived"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPreRepairDeleteTrashesLiveOwnerBackup（三态①：可删）——活宿主+健康，
// 删除成功、走平台回收站、目录消失；重扫不再含该目录。
func TestPreRepairDeleteTrashesLiveOwnerBackup(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	writeLiveOwner(t, sessions, "s1")
	backup := writePreRepairTree(t, sessions, "s1", "20261110T010000", 120)
	spy := withPreRepairTrashSpy(t, nil)

	app := &App{}
	if err := app.DeletePreRepairBackup(backup); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(spy.calls) != 1 || spy.calls[0] != backup {
		t.Fatalf("trash calls = %v, want exactly [%s]", spy.calls, backup)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("backup must be gone from the session dir (stat err=%v)", err)
	}
	for _, view := range preRepairBackupViewsIn([]string{sessions}) {
		if view.Dir == backup {
			t.Fatalf("rescan still lists the deleted backup: %+v", view)
		}
	}
}

// TestPreRepairDeleteAllowsSoleCopyByUserVerdict（tl1646 态②：孤儿副本放行）
// ——宿主不在且回收站无家族成员（tl1603 时代会拒绝的「仅存副本」）：用户裁决
// 删除决定权在用户，删除成功、走平台回收站、目录消失；重扫不再含该目录。
func TestPreRepairDeleteAllowsSoleCopyByUserVerdict(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	backup := writePreRepairTree(t, sessions, "lonely", "20261110T010000", 120)
	spy := withPreRepairTrashSpy(t, nil)

	app := &App{}
	if err := app.DeletePreRepairBackup(backup); err != nil {
		t.Fatalf("the sole surviving copy must be deletable by user verdict (tl1646): %v", err)
	}
	if len(spy.calls) != 1 || spy.calls[0] != backup {
		t.Fatalf("trash calls = %v, want exactly [%s]", spy.calls, backup)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("the deleted sole copy must be gone from the session dir (stat err=%v)", err)
	}
	for _, view := range preRepairBackupViewsIn([]string{sessions}) {
		if view.Dir == backup {
			t.Fatalf("rescan still lists the deleted sole copy: %+v", view)
		}
	}
}

// TestPreRepairDeleteRefusesUnhealthyOwner（三态②第二分支）——活宿主体检
// 不过：拒绝、平台 API 零调用（改写前快照是病会话唯一的回头路）。
func TestPreRepairDeleteRefusesUnhealthyOwner(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	writeLiveOwner(t, sessions, "sick")
	backup := writePreRepairTree(t, sessions, "sick", "20261110T010000", 120)
	spy := withPreRepairTrashSpy(t, nil)
	previous := preRepairHealthInspect
	preRepairHealthInspect = func(path string, _ agent.HealthInspectOptions) agent.SessionHealthReport {
		return agent.SessionHealthReport{
			Path: path, Healthy: false,
			Issues: []agent.SessionHealthIssue{{Code: agent.HealthProjectionOverflow, Detail: "projection_tokens=2000000 window_tokens=1000000"}},
		}
	}
	t.Cleanup(func() { preRepairHealthInspect = previous })

	app := &App{}
	err := app.DeletePreRepairBackup(backup)
	if err == nil {
		t.Fatal("delete must refuse when the owner session is unhealthy")
	}
	if !strings.Contains(err.Error(), "owner session is not healthy") || !strings.Contains(err.Error(), "projection_overflow") {
		t.Fatalf("err = %v, want the unhealthy reason with the criterion codes", err)
	}
	if len(spy.calls) != 0 {
		t.Fatalf("trash must not be called on refusal, got %v", spy.calls)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("refused backup must stay untouched: %v", err)
	}
}

// TestPreRepairDeleteAllowsArchivedRemains——宿主不在但其转录躺在回收站桶里
// （删除/归档链的现场）：非唯一退路成立，删除放行。
func TestPreRepairDeleteAllowsArchivedRemains(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	backup := writePreRepairTree(t, sessions, "archived", "20261110T010000", 120)
	writeTrashBucket(t, sessions, "archived.jsonl", "archived.jsonl")
	spy := withPreRepairTrashSpy(t, nil)

	app := &App{}
	if err := app.DeletePreRepairBackup(backup); err != nil {
		t.Fatalf("delete of archived remains must pass the gate: %v", err)
	}
	if len(spy.calls) != 1 || spy.calls[0] != backup {
		t.Fatalf("trash calls = %v, want [%s]", spy.calls, backup)
	}
}

// TestPreRepairDeleteRefusesOutsideSessionDirs——已知会话目录之外的一切路径
// 拒绝（路径闸；任意文件系统路径在此全挡）。
func TestPreRepairDeleteRefusesOutsideSessionDirs(t *testing.T) {
	stagePreRepairFixture(t)
	outside := writePreRepairTree(t, t.TempDir(), " rogue", "20261110T010000", 10)
	spy := withPreRepairTrashSpy(t, nil)

	app := &App{}
	if err := app.DeletePreRepairBackup(outside); err == nil {
		t.Fatal("delete must refuse paths outside known session dirs")
	}
	if len(spy.calls) != 0 {
		t.Fatalf("trash must not be called, got %v", spy.calls)
	}
}

// TestPreRepairDeleteTrashFailureIsHonest——平台回收站调用失败时错误上抛
// （面板拿得到原因），目录原样。
func TestPreRepairDeleteTrashFailureIsHonest(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	writeLiveOwner(t, sessions, "s1")
	backup := writePreRepairTree(t, sessions, "s1", "20261110T010000", 120)
	withPreRepairTrashSpy(t, errors.New("bin refused"))

	app := &App{}
	if err := app.DeletePreRepairBackup(backup); err == nil {
		t.Fatal("a recycle-bin failure must surface")
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("failed trash must leave the backup in place: %v", err)
	}
}

// TestPreRepairBatchCleanupSweepsToZero（三态③：批量清归零）——三条可删备份
// 批量后重扫归零；回执逐条计数与体量齐平。
func TestPreRepairBatchCleanupSweepsToZero(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	writeLiveOwner(t, sessions, "s1")
	b1 := writePreRepairTree(t, sessions, "s1", "20261108T010000", 100)
	writeLiveOwner(t, sessions, "s2")
	b2 := writePreRepairTree(t, sessions, "s2", "20261109T010000", 200)
	// 宿主已归档形态（桶里有转录）也属可删集合。
	b3 := writePreRepairTree(t, sessions, "s3", "20261110T010000", 300)
	writeTrashBucket(t, sessions, "s3.jsonl", "s3.jsonl")
	spy := withPreRepairTrashSpy(t, nil)

	app := &App{}
	run, err := app.DeletePreRepairBackups([]string{b1, b2, b3})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if run.Trashed != 3 || run.Failed != 0 {
		t.Fatalf("run = trashed %d / failed %d, want 3/0", run.Trashed, run.Failed)
	}
	if run.TrashedBytes <= 0 {
		t.Fatalf("trashed bytes = %d, must carry the swept volume", run.TrashedBytes)
	}
	if run.Failures == nil {
		t.Fatal("failures must be an empty slice, never nil (JSON null crashes the frontend)")
	}
	if len(spy.calls) != 3 {
		t.Fatalf("trash calls = %v, want each gated dir exactly once", spy.calls)
	}
	remaining := preRepairBackupViewsIn([]string{sessions})
	if len(remaining) != 0 {
		t.Fatalf("rescan after batch = %+v, want zero (归零)", remaining)
	}
}

// TestPreRepairBatchCleanupIncludesOrphans——批量里的孤儿副本（tl1603 会被
// 拒的仅存副本）随 tl1646 放行一并清理：活宿主与孤儿各删各的，回执零失败，
// 重扫归零。
func TestPreRepairBatchCleanupIncludesOrphans(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	writeLiveOwner(t, sessions, "s1")
	b1 := writePreRepairTree(t, sessions, "s1", "20261108T010000", 100)
	sole := writePreRepairTree(t, sessions, "lonely", "20261109T010000", 200)
	spy := withPreRepairTrashSpy(t, nil)

	app := &App{}
	run, err := app.DeletePreRepairBackups([]string{b1, sole})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if run.Trashed != 2 || run.Failed != 0 {
		t.Fatalf("run = trashed %d / failed %d, want 2/0 (orphans ride the batch, tl1646)", run.Trashed, run.Failed)
	}
	if len(run.Failures) != 0 {
		t.Fatalf("failures = %+v, want none", run.Failures)
	}
	if len(spy.calls) != 2 {
		t.Fatalf("trash calls = %v, want both dirs", spy.calls)
	}
	remaining := preRepairBackupViewsIn([]string{sessions})
	if len(remaining) != 0 {
		t.Fatalf("rescan = %+v, want zero (归零)", remaining)
	}
}

// TestPreRepairBatchHealthReadCachedPerOwner——同一宿主的多份备份在批内只体检
// 一次（重放成本封顶），被拒宿主的第二份备份不再重放。
func TestPreRepairBatchHealthReadCachedPerOwner(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	writeLiveOwner(t, sessions, "sick")
	b1 := writePreRepairTree(t, sessions, "sick", "20261108T010000", 100)
	b2 := writePreRepairTree(t, sessions, "sick", "20261109T010000", 200)
	withPreRepairTrashSpy(t, nil)
	inspected := 0
	previous := preRepairHealthInspect
	preRepairHealthInspect = func(path string, _ agent.HealthInspectOptions) agent.SessionHealthReport {
		inspected++
		return agent.SessionHealthReport{Path: path, Healthy: false}
	}
	t.Cleanup(func() { preRepairHealthInspect = previous })

	app := &App{}
	run, err := app.DeletePreRepairBackups([]string{b1, b2})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if run.Failed != 2 || run.Trashed != 0 {
		t.Fatalf("run = trashed %d / failed %d, want 0/2", run.Trashed, run.Failed)
	}
	if inspected != 1 {
		t.Fatalf("health inspect ran %d times, want 1 (one replay per owner per batch)", inspected)
	}
	if run.Failures[0].Reason != preRepairReasonOwnerUnhealthy || run.Failures[1].Reason != preRepairReasonOwnerUnhealthy {
		t.Fatalf("failure tokens = %+v, want owner-unhealthy on both", run.Failures)
	}
}

// TestPreRepairEnumerationListsOrphansPlainly——列表三形态同列（tl1646：不再
// 有可删初判）：活宿主、桶里宿主、孤儿副本都以同一形态列出；孤儿行的
// OwnerPath 为空（前端据此在确认弹窗里给唯一副本警示），label/体量读数齐全。
func TestPreRepairEnumerationListsOrphansPlainly(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	writeLiveOwner(t, sessions, "s1")
	writePreRepairTree(t, sessions, "s1", "20261108T010000", 100)
	writePreRepairTree(t, sessions, "archived", "20261109T010000", 100)
	writeTrashBucket(t, sessions, "archived.jsonl", "archived.jsonl")
	writePreRepairTree(t, sessions, "lonely", "20261110T010000", 100)

	views := preRepairBackupViewsIn([]string{sessions})
	if len(views) != 3 {
		t.Fatalf("views = %d, want 3", len(views))
	}
	byStem := map[string]PreRepairBackupView{}
	for _, view := range views {
		byStem[strings.Split(filepath.Base(view.Dir), ".pre-repair-")[0]] = view
	}
	if byStem["s1"].OwnerPath == "" {
		t.Fatalf("live-owner backup = %+v, want the owner path filled", byStem["s1"])
	}
	if byStem["archived"].OwnerPath != "" {
		t.Fatalf("archived-remains backup = %+v, want an empty owner path (owner lives in the trash)", byStem["archived"])
	}
	if byStem["lonely"].OwnerPath != "" {
		t.Fatalf("orphan backup = %+v, want an empty owner path (the confirm-dialog sole-copy warning keys off this, tl1646)", byStem["lonely"])
	}
	if byStem["lonely"].Bytes <= 0 || byStem["lonely"].Label == "" {
		t.Fatalf("orphan backup = %+v, want complete readings (bytes/label)", byStem["lonely"])
	}
}
