package main

// tl1834 件4：pre-repair 强制删除链路的后端钉。
//
// 语义边界（pre_repair_cleanup.go 闸注释同源）：force=true 只旁路健康闸
// （活宿主体检）——「用户明示要清理」的豁免通道；路径闸与回收站语义全保留，
// 物理失败（gone/trash-failed）force 也照实拒绝（force 不命令文件系统）。
//
// 验收四钉：
//  ① 单删：不健康宿主 → force=false 拒（平台 API 零调用、目录原样）；
//     force=true 走平台回收站成功、目录消失；
//  ② 批量：健康+不健康混合 → force=false 只清健康的（不健康逐条带令牌拒绝）；
//     force=true 全清；
//  ③ 强制的诚实边界：force=true 对已消失目录仍拒绝（gone，不静默吞）；
//  ④ 永久桥：不健康宿主 → force=false 拒；force=true 真删（与回收站通道
//     同一豁免口径，都只在用户明示确认后到达）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
)

// stageUnhealthyOwner 是健康闸拒绝分支的夹具：活宿主+假体检报告（与
// TestPreRepairDeleteRefusesUnhealthyOwner 同一缝）。**按路径定向**：只有
// 本 stem 的宿主报病（批量钉里同场的健康宿主走真缝——空转录=healthy），
// 全局假报告会把同批健康宿主一起拖下水。
func stageUnhealthyOwner(t *testing.T, sessions, stem, stamp string) (string, func()) {
	t.Helper()
	writeLiveOwner(t, sessions, stem)
	backup := writePreRepairTree(t, sessions, stem, stamp, 120)
	sickOwner := filepath.Join(sessions, stem+".jsonl")
	previous := preRepairHealthInspect
	preRepairHealthInspect = func(path string, _ agent.HealthInspectOptions) agent.SessionHealthReport {
		if path != sickOwner {
			return agent.SessionHealthReport{Path: path, Healthy: true}
		}
		return agent.SessionHealthReport{
			Path: path, Healthy: false,
			Issues: []agent.SessionHealthIssue{{Code: agent.HealthProjectionOverflow, Detail: "projection_tokens=2000000 window_tokens=1000000"}},
		}
	}
	return backup, func() { preRepairHealthInspect = previous }
}

// TestPreRepairForceDeleteSingle（钉①）——单删强制链：拒绝在先、强制走通。
func TestPreRepairForceDeleteSingle(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	backup, restore := stageUnhealthyOwner(t, sessions, "sick", "20261110T010000")
	defer restore()
	spy := withPreRepairTrashSpy(t, nil)
	app := &App{}

	// force=false：健康闸拒绝，平台 API 零调用，目录原样。
	if _, err := app.DeletePreRepairBackup(backup, false); err == nil {
		t.Fatal("plain delete must refuse an unhealthy owner")
	}
	if len(spy.calls) != 0 {
		t.Fatalf("plain refusal must not touch the trash platform, got %v", spy.calls)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("refused backup must stay untouched: %v", err)
	}

	// force=true：闸旁路，走平台回收站，目录消失。
	verdict, err := app.DeletePreRepairBackup(backup, true)
	if err != nil {
		t.Fatalf("force delete must pass the skipped health gate: %v", err)
	}
	if !verdict.Trashed || verdict.PermanentOnly {
		t.Fatalf("verdict = %+v, want trashed via the recycle bin", verdict)
	}
	if len(spy.calls) != 1 || spy.calls[0] != backup {
		t.Fatalf("trash calls = %v, want [%s]", spy.calls, backup)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("forced backup must be gone: %v", err)
	}
}

// TestPreRepairForceDeleteBatch（钉②）——批量强制链：force=false 只清健康的、
// 不健康的逐条 owner-unhealthy；force=true 对同一清单全清。
func TestPreRepairForceDeleteBatch(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	sick, restore := stageUnhealthyOwner(t, sessions, "sick", "20261110T010000")
	defer restore()
	healthyStem := "well"
	writeLiveOwner(t, sessions, healthyStem)
	healthy := writePreRepairTree(t, sessions, healthyStem, "20261109T010000", 120)
	spy := withPreRepairTrashSpy(t, nil)
	app := &App{}

	// force=false：健康的清掉，不健康的带令牌拒绝。
	plain, err := app.DeletePreRepairBackups([]string{sick, healthy}, false)
	if err != nil {
		t.Fatalf("plain batch must not fail wholesale: %v", err)
	}
	if plain.Trashed != 1 || plain.Failed != 1 {
		t.Fatalf("plain batch = %+v, want 1 trashed + 1 refused", plain)
	}
	if len(plain.Failures) != 1 || plain.Failures[0].Reason != preRepairReasonOwnerUnhealthy || plain.Failures[0].Dir != sick {
		t.Fatalf("failures = %+v, want the sick dir with owner-unhealthy", plain.Failures)
	}

	// force=true：同一清单全清。
	forced, err := app.DeletePreRepairBackups([]string{sick}, true)
	if err != nil {
		t.Fatalf("forced batch must not fail wholesale: %v", err)
	}
	if forced.Trashed != 1 || forced.Failed != 0 {
		t.Fatalf("forced batch = %+v, want the sick backup trashed", forced)
	}
	if _, err := os.Stat(sick); !os.IsNotExist(err) {
		t.Fatalf("forced backup must be gone: %v", err)
	}
	if len(spy.calls) != 2 {
		t.Fatalf("trash calls = %v, want the healthy then the forced sick backup", spy.calls)
	}
}

// TestPreRepairForceDeleteStillReportsPhysicalFailures（钉③）——强制的诚实
// 边界：目录已消失时 force=true 仍拒绝（gone），绝不静默吞。
func TestPreRepairForceDeleteStillReportsPhysicalFailures(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	backup, restore := stageUnhealthyOwner(t, sessions, "sick", "20261110T010000")
	defer restore()
	spy := withPreRepairTrashSpy(t, nil)
	app := &App{}

	// 目录先消失（枚举后被别的链搬走的形态）。
	if err := os.RemoveAll(backup); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DeletePreRepairBackup(backup, true); err == nil {
		t.Fatal("force delete must still refuse a vanished backup (gone)")
	}
	if len(spy.calls) != 0 {
		t.Fatalf("trash must not be called for a vanished backup, got %v", spy.calls)
	}

	// 路径闸对 force 同样有效：会话目录之外的路径强制不了。
	outside := filepath.Join(t.TempDir(), "elsewhere.pre-repair-20261110T010000")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DeletePreRepairBackup(outside, true); err == nil || !strings.Contains(err.Error(), "outside known session directories") {
		t.Fatalf("force delete must keep the path gate, err = %v", err)
	}
}

// TestPreRepairForceDeletePermanent（钉④）——永久桥同一豁免口径：force=false
// 拒（tl1116 在永久路径上默认同等生效），force=true 真删。
func TestPreRepairForceDeletePermanent(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	backup, restore := stageUnhealthyOwner(t, sessions, "sick", "20261110T010000")
	defer restore()
	app := &App{}

	if err := app.DeletePreRepairBackupPermanent(backup, false); err == nil {
		t.Fatal("plain permanent delete must refuse an unhealthy owner")
	}
	if err := app.DeletePreRepairBackupPermanent(backup, true); err != nil {
		t.Fatalf("forced permanent delete must pass: %v", err)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("forced permanent backup must be gone: %v", err)
	}
}
