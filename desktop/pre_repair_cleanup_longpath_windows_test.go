//go:build windows

package main

// tl1658 长路径死锁解除的后端钉（Windows 专属：MAX_PATH 是 Windows Shell 回
// 收语义的封顶，非 Windows 平台不存在该失败分支）。
//
// 硬规范（任务书）：回收站长路径行为用真实夹具实测、不得纸面推演——本文件
// 在测试内构造真实 >MAX_PATH 的深层备份树（与用户现场 285 字符同构：
// projects\<长项目名>\sessions\<长 stem>.pre-repair-<stamp>\ 内层文件），先
// 实测预检真的拒绝（SHFileOperation 调用之前的 guard 分支），再实测两段出
// 路的真实接线。验收矩阵钉：
//
//  ① L0 中转可行：真实 >260 夹具直删被拒（errTrashLongPath、目录原样）→
//     DeletePreRepairBackup 自动短路径中转 → 平台缝收到 ≤260 短路径、树内容
//     存活（桶内字节级断言）、原位消失、重扫归零、裁决 Trashed+ViaStaging；
//  ② L1 中转不可行：staging 缝强制失败 → permanentOnly 裁决（error=nil、
//     目录原样、平台缝零调用）→ DeletePreRepairBackupPermanent 明示通道真删
//     （os.RemoveAll、平台缝零调用、目录消失）；
//  ③ H1 兜底不旁路健康闸：活宿主不健康时永久桥同样拒绝（tl1116 纪律在永久
//     路径同等生效）；
//  ④ L2 批内长路径：中转成功计入 Staged；中转失败落 permanent-only 令牌。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
)

// withRecycleGuardSpy 安装「guard 行为镜像」的平台缝：>MAX_PATH 的树按真实
// trashRecycleGuard 同款哨兵拒绝（不触真实 Shell——预检在 Shell 之前，拒绝
// 即零副作用），短路径照 withPreRepairTrashSpy 同款语义真搬进测试桶。单换
// withPreRepairTrashSpy 不够——它整个绕过预检，长路径分支（errTrashLongPath
// →中转→裁决）根本不会被点亮。
func withRecycleGuardSpy(t *testing.T) *preRepairTrashSpy {
	t.Helper()
	spy := &preRepairTrashSpy{bin: t.TempDir()}
	previous := workspaceTrashPlatform
	workspaceTrashPlatform = func(paths []string) error {
		for _, path := range paths {
			if _, maxLen, measureErr := trashTreeMeasure(path); measureErr == nil && maxLen > maxRecyclePathLen {
				return fmt.Errorf("%w: %s contains items whose full paths exceed %d characters (%d) — Windows cannot put them in the recycle bin and would permanently delete them, refusing", errTrashLongPath, path, maxRecyclePathLen, maxLen)
			}
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

// longPreRepairStem 是长路径夹具的会话名段（183 字符）：备份目录名
// =stem+.pre-repair-+stamp（210，NTFS 单段上限 255 内），内层文件完整路径
// 在 t.TempDir() 基础上必然超过 MAX_PATH（夹具自检强制）。
func longPreRepairStem() string {
	return strings.Repeat("long-session-name-", 10) + "pad"
}

// buildLongPreRepairFixture 构造内层完整路径 >MAX_PATH 的真实备份树（含转录
// 与 meta，与 writePreRepairTree 同构），返回备份目录路径。
func buildLongPreRepairFixture(t *testing.T, sessions, stem string) string {
	t.Helper()
	backup := filepath.Join(sessions, stem+".pre-repair-20261025T212435")
	if err := os.MkdirAll(backup, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, stem+".jsonl"), []byte("pre-repair long-path payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, stem+".jsonl.meta"), []byte(`{"topic_title":"long path backup"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(backup, stem+".jsonl")
	if len(inner) <= maxRecyclePathLen {
		t.Fatalf("fixture inner path is %d chars, must exceed MAX_PATH(%d) for the pin to matter (test bug)", len(inner), maxRecyclePathLen)
	}
	return backup
}

// TestPreRepairDeleteLongPathStagesThenRecycles（验收矩阵 L0，任务书硬钉）——
// 超 MAX_PATH 备份的删除不再死锁：预检拒绝（真实分支）之后自动走短路径中转，
// 树从暂存区整体进平台回收站缝。全链路在真实文件系统上跑（真实深层夹具、
// 真实 os.Rename、真实 guard 遍历），只把「入桶」端点换成真搬运的 spy。
func TestPreRepairDeleteLongPathStagesThenRecycles(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	stem := longPreRepairStem()
	backup := buildLongPreRepairFixture(t, sessions, stem)
	spy := withRecycleGuardSpy(t)

	// 先实测预检的真实行为（真 trashPlatformPaths 的 guard 分支，拒绝发生在
	// Shell 调用之前=零真实副作用）：直删长路径被哨兵拒绝、目录原样——这是
	// 现场②那条「items whose full paths exceed 260 characters (285)」toast
	// 的来源分支，修复后它仍是第一道闸（回收语义的守门人）。
	if err := trashPlatformPaths([]string{backup}); !errors.Is(err, errTrashLongPath) {
		t.Fatalf("direct trash of a >MAX_PATH tree must be refused with the long-path sentinel, got %v", err)
	}
	if _, statErr := os.Lstat(backup); statErr != nil {
		t.Fatalf("the refused tree must stay untouched: %v", statErr)
	}

	app := &App{}
	verdict, err := app.DeletePreRepairBackup(backup, false)
	if err != nil {
		t.Fatalf("delete with staging: %v", err)
	}
	if !verdict.Trashed || !verdict.ViaStaging || verdict.PermanentOnly {
		t.Fatalf("verdict = %+v, want trashed via staging (回收语义保住)", verdict)
	}
	if len(spy.calls) != 1 {
		t.Fatalf("platform calls = %v, want exactly one (the staged short path)", spy.calls)
	}
	staged := spy.calls[0]
	if len(staged) > maxRecyclePathLen {
		t.Fatalf("staged path is %d chars, must fit the bin (≤%d)", len(staged), maxRecyclePathLen)
	}
	if _, statErr := os.Lstat(backup); !os.IsNotExist(statErr) {
		t.Fatalf("the original long path must be gone after staging (stat err=%v)", statErr)
	}
	// spy 已把树搬进测试桶（条目名带 %03d 序号前缀）：桶内树内容必须字节级
	// 存活（移动=原样搬运，不是拷贝丢失）。内层超长文件名已被截短（guard 量
	// 的是树内最长完整路径——顶层改名不够，这是夹具实测抓出的坑），按内容
	// 找文件断言。
	binned := filepath.Join(spy.bin, "000-"+filepath.Base(staged))
	sawPayload, sawMeta, sawFiles := false, false, 0
	_ = filepath.WalkDir(binned, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		sawFiles++
		kept, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		switch string(kept) {
		case "pre-repair long-path payload":
			sawPayload = true
		case `{"topic_title":"long path backup"}`:
			sawMeta = true
		}
		return nil
	})
	if sawFiles != 2 || !sawPayload || !sawMeta {
		t.Fatalf("the staged tree must arrive at the bin intact (files=%d payload=%v meta=%v)", sawFiles, sawPayload, sawMeta)
	}
	// 重扫归零：面板（枚举只扫会话目录）不再列这项，暂存区也不留痕。
	for _, view := range preRepairBackupViewsIn([]string{sessions}) {
		if view.Dir == backup {
			t.Fatalf("rescan still lists the staged-away backup: %+v", view)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(staged))
	if err == nil && len(entries) != 0 {
		t.Fatalf("staging root must be cleaned up, found %v", entries)
	}
}

// TestPreRepairDeleteLongPathStagingFailureOffersPermanentOnly（验收矩阵 L1，
// 任务书硬钉②）——中转不可行时返回 permanentOnly 裁决：不是错误（是给前端
// 第二段明示弹窗的裁决），目录原样未动；明示确认后的永久桥真删（os.RemoveAll、
// 平台回收站缝零调用——855 铁律的「彻底删除是独立显式操作」通道）。
func TestPreRepairDeleteLongPathStagingFailureOffersPermanentOnly(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	stem := longPreRepairStem()
	backup := buildLongPreRepairFixture(t, sessions, stem)
	spy := withRecycleGuardSpy(t)
	previousStaging := preRepairStagingMkdir
	preRepairStagingMkdir = func(dir, pattern string) (string, error) {
		return "", errors.New("temp unavailable")
	}
	t.Cleanup(func() { preRepairStagingMkdir = previousStaging })

	app := &App{}
	verdict, err := app.DeletePreRepairBackup(backup, false)
	if err != nil {
		t.Fatalf("permanent-only is a verdict for the FE dialog, not an error: %v", err)
	}
	if !verdict.PermanentOnly || verdict.Trashed || verdict.ViaStaging {
		t.Fatalf("verdict = %+v, want permanentOnly", verdict)
	}
	if !strings.Contains(verdict.Detail, "exceed") || !strings.Contains(verdict.Detail, "refusing") {
		t.Fatalf("detail = %q, want the long-path guard text carried through", verdict.Detail)
	}
	if len(spy.calls) != 0 {
		t.Fatalf("the recycle-bin seam must never see the over-MAX_PATH tree, got %v", spy.calls)
	}
	if _, statErr := os.Lstat(backup); statErr != nil {
		t.Fatalf("the verdict must leave the backup untouched: %v", statErr)
	}

	// 明示通道：确认后的永久删除。数据真消失、平台缝零调用（不再假装走回收站）。
	if err := app.DeletePreRepairBackupPermanent(backup, false); err != nil {
		t.Fatalf("permanent delete after explicit verdict: %v", err)
	}
	if len(spy.calls) != 0 {
		t.Fatalf("permanent delete must not touch the recycle-bin seam, got %v", spy.calls)
	}
	if _, statErr := os.Lstat(backup); !os.IsNotExist(statErr) {
		t.Fatalf("the permanently deleted backup must be gone (stat err=%v)", statErr)
	}
}

// TestPreRepairPermanentDeleteKeepsHealthGate（验收矩阵 H1）——永久桥过同一
// 健康闸：活宿主不健康时其改写前快照是唯一的回头路，明示兜底不允许旁路它
// （tl1116「无备份不减肥」在永久路径同等生效）。
func TestPreRepairPermanentDeleteKeepsHealthGate(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	writeLiveOwner(t, sessions, "sick")
	backup := writePreRepairTree(t, sessions, "sick", "20261110T010000", 120)
	previous := preRepairHealthInspect
	preRepairHealthInspect = func(path string, _ agent.HealthInspectOptions) agent.SessionHealthReport {
		return agent.SessionHealthReport{
			Path: path, Healthy: false,
			Issues: []agent.SessionHealthIssue{{Code: agent.HealthProjectionOverflow, Detail: "projection_tokens=2000000 window_tokens=1000000"}},
		}
	}
	t.Cleanup(func() { preRepairHealthInspect = previous })

	app := &App{}
	err := app.DeletePreRepairBackupPermanent(backup, false)
	if err == nil {
		t.Fatal("the permanent-delete bridge must keep the owner-health gate")
	}
	if !strings.Contains(err.Error(), "owner session is not healthy") {
		t.Fatalf("err = %v, want the unhealthy refusal", err)
	}
	if _, statErr := os.Lstat(backup); statErr != nil {
		t.Fatalf("a refused permanent delete must leave the backup in place: %v", statErr)
	}
}

// TestPreRepairBatchCleanupStagesLongPaths（验收矩阵 L2·中转成功侧）——批量里
// 的超长路径项自动中转入桶：Trashed 计满、Staged 计 1、重扫归零。
func TestPreRepairBatchCleanupStagesLongPaths(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	writeLiveOwner(t, sessions, "s1")
	short := writePreRepairTree(t, sessions, "s1", "20261108T010000", 100)
	long := buildLongPreRepairFixture(t, sessions, longPreRepairStem())
	spy := withRecycleGuardSpy(t)

	app := &App{}
	run, err := app.DeletePreRepairBackups([]string{short, long}, false)
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if run.Trashed != 2 || run.Failed != 0 {
		t.Fatalf("run = trashed %d / failed %d, want 2/0", run.Trashed, run.Failed)
	}
	if run.Staged != 1 {
		t.Fatalf("staged = %d, want 1 (the over-MAX_PATH item rides the short-path transit)", run.Staged)
	}
	shortStaged := 0
	for _, call := range spy.calls {
		if strings.Contains(call, "reasonix-prepair-") {
			shortStaged++
		}
	}
	if shortStaged != 1 {
		t.Fatalf("platform calls = %v, want exactly one staged (staging-root) path", spy.calls)
	}
	remaining := preRepairBackupViewsIn([]string{sessions})
	if len(remaining) != 0 {
		t.Fatalf("rescan = %+v, want zero (长路径项随批量清走)", remaining)
	}
}

// TestPreRepairBatchCleanupReportsPermanentOnlyWhenStagingFails（验收矩阵 L2·
// 中转失败侧）——中转不可行的项在批量回执落 permanent-only 令牌（逐条归因、
// 不静默），其余项照常清走；重扫只剩被裁决的那项。
func TestPreRepairBatchCleanupReportsPermanentOnlyWhenStagingFails(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	writeLiveOwner(t, sessions, "s1")
	short := writePreRepairTree(t, sessions, "s1", "20261108T010000", 100)
	long := buildLongPreRepairFixture(t, sessions, longPreRepairStem())
	spy := withRecycleGuardSpy(t)
	previousStaging := preRepairStagingMkdir
	preRepairStagingMkdir = func(dir, pattern string) (string, error) {
		return "", errors.New("temp unavailable")
	}
	t.Cleanup(func() { preRepairStagingMkdir = previousStaging })

	app := &App{}
	run, err := app.DeletePreRepairBackups([]string{short, long}, false)
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if run.Trashed != 1 || run.Failed != 1 {
		t.Fatalf("run = trashed %d / failed %d, want 1/1", run.Trashed, run.Failed)
	}
	for _, call := range spy.calls {
		if call == long {
			t.Fatalf("the recycle-bin seam must never see the over-MAX_PATH tree directly: %v", spy.calls)
		}
	}
	if len(run.Failures) != 1 || run.Failures[0].Reason != preRepairReasonPermanentOnly {
		t.Fatalf("failures = %+v, want exactly one permanent-only line", run.Failures)
	}
	if run.Failures[0].Dir != long || !strings.Contains(run.Failures[0].Detail, "exceed") {
		t.Fatalf("failure line = %+v, want the long dir with the guard detail", run.Failures[0])
	}
	remaining := preRepairBackupViewsIn([]string{sessions})
	if len(remaining) != 1 || remaining[0].Dir != long {
		t.Fatalf("rescan = %+v, want exactly the permanent-only verdict item", remaining)
	}
}
