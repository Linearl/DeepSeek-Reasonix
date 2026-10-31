package main

// tl1287⑤: 清理迁移备份的后端单测。
//
// 覆盖两条硬前提里可在包内验证的部分：清单按时间倒序、保留最近一份、
// run 现算清单并以 plan 时刻无关的方式删除。「迁移未归零拒绝清理」分支的
// 输入是 ScanDagMigration 的 pending/active 统计（其 fixtures 在
// internal/agent 包内，此处不重复造第二个 schema-2 真相源），本文件只验证
// 无 schema-2 会话时的归零通路。

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
)

func writeDagbak(t *testing.T, path string, size int, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func TestDagBackupCleanupScanSortsNewestFirst(t *testing.T) {
	root := t.TempDir()
	base := time.Unix(1793000000, 0)
	old := filepath.Join(root, "proj", "sessions", "a.events.jsonl.dagbak-tl1227")
	mid := filepath.Join(root, "proj", "sessions", "b.events.jsonl.dagbak-tl1227")
	// 自愈快照树内的备份同样入列（同属迁移备份、同占体量）。
	inSnap := filepath.Join(root, "proj", "sessions", "c.pre-repair-20261030T000000", "c.events.jsonl.dagbak-tl1227")
	writeDagbak(t, old, 10, base)
	writeDagbak(t, mid, 20, base.Add(2*time.Hour))
	writeDagbak(t, inSnap, 40, base.Add(1*time.Hour))
	// 非 dagbak 文件不入列。
	if err := os.WriteFile(filepath.Join(root, "proj", "sessions", "a.events.jsonl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	backups, err := dagBackupCleanupScan(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(backups) != 3 {
		t.Fatalf("backups = %d (%v), want 3", len(backups), backups)
	}
	if backups[0].Path != mid || backups[1].Path != inSnap || backups[2].Path != old {
		t.Fatalf("order must be newest first, got %v", backups)
	}
	if backups[0].Bytes != 20 || backups[2].Bytes != 10 {
		t.Fatalf("bytes not reported per file: %+v", backups)
	}
}

// TestDagBackupCleanupPlanAndRun 保住「check 归零 + 保留最近一份 + 倒序删」
// 的端到端语义：plan 给出将删清单与体量，run 真删且最近一份幸存。
func TestDagBackupCleanupPlanAndRun(t *testing.T) {
	t.Setenv("REASONIX_STATE_HOME", t.TempDir())
	root := filepath.Join(dagMigrationProjectsRoot(), "proj", "sessions")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Unix(1793000000, 0)
	older := filepath.Join(root, "s1.events.jsonl.dagbak-tl1227")
	older2 := filepath.Join(root, "s2.events.jsonl.dagbak-tl1227")
	keep := filepath.Join(root, "s3.events.jsonl.dagbak-tl1227")
	writeDagbak(t, older, 100, base)
	writeDagbak(t, older2, 200, base.Add(time.Hour))
	writeDagbak(t, keep, 400, base.Add(2*time.Hour))

	app := &App{}
	plan := app.PlanDagBackupCleanup()
	if plan.Error != "" {
		t.Fatalf("plan error: %s", plan.Error)
	}
	if plan.RemainingMigrations != 0 {
		t.Fatalf("no schema-2 session exists, remaining = %d, want 0", plan.RemainingMigrations)
	}
	if plan.KeepPath != keep || plan.KeepBytes != 400 {
		t.Fatalf("keep = %s (%d), want the newest %s (400)", plan.KeepPath, plan.KeepBytes, keep)
	}
	if len(plan.Deletable) != 2 || plan.DeleteBytes != 300 {
		t.Fatalf("deletable = %d (%d bytes), want 2 (300)", len(plan.Deletable), plan.DeleteBytes)
	}
	// 确认弹层清单必须从新到旧展示（删除顺序与此一致）。
	if plan.Deletable[0].Path != older2 || plan.Deletable[1].Path != older {
		t.Fatalf("deletable order must be newest first: %v", plan.Deletable)
	}

	run, err := app.RunDagBackupCleanup()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if run.Deleted != 2 || run.DeletedBytes != 300 || run.Failed != 0 {
		t.Fatalf("run = %+v, want deleted 2 / 300 bytes / 0 failed", run)
	}
	if run.KeptPath != keep {
		t.Fatalf("kept %s, want %s", run.KeptPath, keep)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("the newest backup must survive: %v", err)
	}
	for _, gone := range []string{older, older2} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s should be deleted (stat err=%v)", gone, err)
		}
	}
}

// TestDagBackupCleanupRefusesWhenMigrationBusy 只验证与迁移的互斥 CAS 分支
// （迁移未归零的拒绝分支依赖 ScanDagMigration 的 schema-2 fixtures，见文件头）。
func TestDagBackupCleanupRefusesWhenMigrationBusy(t *testing.T) {
	if !dagMigrationRunning.CompareAndSwap(false, true) {
		t.Fatal("failed to take the idle dagMigrationRunning flag")
	}
	defer dagMigrationRunning.Store(false)
	app := &App{}
	if _, err := app.RunDagBackupCleanup(); err == nil {
		t.Fatal("cleanup must refuse while a migration holds the flag")
	}
}

// TestDagBackupSuffixStillAgentOwned 防止 desktop 侧另立后缀字面量——备份
// 后缀的唯一事实源在 internal/agent（回滚指引、读端 probe 都认它）。
func TestDagBackupSuffixStillAgentOwned(t *testing.T) {
	if agent.SessionDAGMigrationBackupSuffix != ".dagbak-tl1227" {
		t.Fatalf("backup suffix moved: %q", agent.SessionDAGMigrationBackupSuffix)
	}
}
