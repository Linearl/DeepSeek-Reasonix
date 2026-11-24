package main

// tl1787 续作：后台自愈拼接 pass 的桌面侧单测。
//
// 验收钉：开关关=零动作；分叉对登记档案袋且主线转录零触碰；busy 让路；
// 归档/孤儿行跳过；单条失败不阻塞（坏候选不拖垮好候选）；每 tick 登记
// 上限；幂等闸（已登记不再重写）。

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

// seedAutosplicePair 建分叉形态（轮次粒度：宿主=新链 [C,C2]+[F,G]，备份=
// 旧链 [C,C2]+[D,E]），备份落 .pre-repair 目录（枚举同源形态）。
// 返回宿主路径与备份目录。
func seedAutosplicePair(t *testing.T, sessions, stem string) (string, string) {
	t.Helper()
	ownerPath := filepath.Join(sessions, stem+".jsonl")
	owner := agent.NewSession("sys")
	owner.Add(provider.Message{Role: provider.RoleUser, Content: "C shared"})
	owner.Add(provider.Message{Role: provider.RoleAssistant, Content: "C2 shared reply"})
	owner.Add(provider.Message{Role: provider.RoleUser, Content: stem + " F new"})
	owner.Add(provider.Message{Role: provider.RoleAssistant, Content: stem + " G reply"})
	if err := owner.Save(ownerPath); err != nil {
		t.Fatalf("save owner: %v", err)
	}
	backup := filepath.Join(sessions, stem+".pre-repair-20261110T010000")
	if err := os.MkdirAll(backup, 0o755); err != nil {
		t.Fatal(err)
	}
	backupSession := agent.NewSession("sys")
	backupSession.Add(provider.Message{Role: provider.RoleUser, Content: "C shared"})
	backupSession.Add(provider.Message{Role: provider.RoleAssistant, Content: "C2 shared reply"})
	backupSession.Add(provider.Message{Role: provider.RoleUser, Content: stem + " D lost"})
	backupSession.Add(provider.Message{Role: provider.RoleAssistant, Content: stem + " E lost"})
	if err := backupSession.Save(filepath.Join(backup, stem+".jsonl")); err != nil {
		t.Fatalf("save backup: %v", err)
	}
	return ownerPath, backup
}

func autospliceViews(t *testing.T, sessions string) []PreRepairBackupView {
	t.Helper()
	return preRepairBackupViewsIn([]string{sessions})
}

// 开关关=零动作（铁律 2：登记面与引入前逐字节一致）。
func TestPreRepairAutoSplicePassDisabledIsZeroAction(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	seedAutosplicePair(t, sessions, "topic")
	registered, skipped := runPreRepairAutoSplicePass(false, autospliceViews(t, sessions), nil, time.Now())
	if registered != 0 || skipped != 0 {
		t.Fatalf("disabled pass = %d/%d, want 0/0 (零动作)", registered, skipped)
	}
	if _, err := os.Stat(filepath.Join(sessions, "topic.splices.json")); !os.IsNotExist(err) {
		t.Fatalf("no dossier may be written while the switch is off (stat err=%v)", err)
	}
}

// 分叉对登记档案袋；主线转录零触碰（纯元信息）。
func TestPreRepairAutoSplicePassRegistersDivergedFork(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	ownerPath, backupDir := seedAutosplicePair(t, sessions, "topic")
	transcriptBefore, err := os.ReadFile(ownerPath)
	if err != nil {
		t.Fatal(err)
	}

	registered, skipped := runPreRepairAutoSplicePass(true, autospliceViews(t, sessions), nil, time.Now())
	if registered != 1 || skipped != 0 {
		t.Fatalf("pass = %d registered / %d skipped, want 1/0", registered, skipped)
	}
	if !agent.HasPreRepairSplice(ownerPath, backupDir) {
		t.Fatal("the fork must read back registered from the owner's ledger")
	}
	transcriptAfter, err := os.ReadFile(ownerPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(transcriptBefore) != string(transcriptAfter) {
		t.Fatal("the transcript changed during a metadata-only autosplice (投影零改动 violated)")
	}
}

// busy 让路 + 幂等闸：已登记的备份不重写。
func TestPreRepairAutoSplicePassBusyAndIdempotent(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	ownerPath, backupDir := seedAutosplicePair(t, sessions, "topic")
	views := autospliceViews(t, sessions)

	busyOwners := map[string]bool{ownerPath: true}
	registered, _ := runPreRepairAutoSplicePass(true, views, func(owner string) bool { return busyOwners[owner] }, time.Now())
	if registered != 0 {
		t.Fatalf("busy owner registered %d, want 0 (让路)", registered)
	}

	// 预登记后：pass 幂等跳过（不重写）。
	plan, err := agent.CheckPreRepairSplice(ownerPath, filepath.Join(backupDir, "topic.jsonl"), 0)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if _, err := agent.RegisterPreRepairSplice(ownerPath, agent.PreRepairSpliceRequest{
		BackupDir: backupDir, BackupTranscript: filepath.Join(backupDir, "topic.jsonl"),
		Stamp: "20261110T010000", Plan: plan,
	}); err != nil {
		t.Fatalf("pre-register: %v", err)
	}
	registered, skipped := runPreRepairAutoSplicePass(true, views, nil, time.Now())
	if registered != 0 {
		t.Fatalf("re-registration happened (%d), want 0 (幂等闸)", registered)
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1 (已登记行进跳过桶)", skipped)
	}
}

// 归档/孤儿行跳过：无可拼的活宿主。
func TestPreRepairAutoSplicePassSkipsOrphanAndArchived(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	orphanOwner, _ := seedAutosplicePair(t, sessions, "orphan")
	if err := os.Remove(orphanOwner); err != nil {
		t.Fatal(err)
	}
	archOwner, archBackup := seedAutosplicePair(t, sessions, "arch")
	bucket := filepath.Join(sessions, ".trash", "arch.jsonl-deleted-1")
	if err := os.MkdirAll(bucket, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(archOwner, filepath.Join(bucket, "arch.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bucket, sessionTrashMetaFile), []byte(`{"key":"arch.jsonl","deletedAt":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = archBackup

	registered, _ := runPreRepairAutoSplicePass(true, autospliceViews(t, sessions), nil, time.Now())
	if registered != 0 {
		t.Fatalf("registered = %d, want 0 (孤儿/归档行没有可拼的活宿主)", registered)
	}
}

// 失败不阻塞：坏候选（转录缺失→判据失败）不拖垮好候选；登记上限每 tick
// 生效（5 候选 → 4 登记 + 1 上限跳过）。
func TestPreRepairAutoSplicePassFailureIsolationAndCap(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	// 坏候选：备份目录在、转录文件缺失（判据 fail-closed）。
	broken := filepath.Join(sessions, "broken.pre-repair-20261110T010000")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessions, "broken.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// 好候选：能登记。
	goodOwner, _ := seedAutosplicePair(t, sessions, "good")
	_ = goodOwner
	registered, skipped := runPreRepairAutoSplicePass(true, autospliceViews(t, sessions), nil, time.Now())
	if registered != 1 {
		t.Fatalf("registered = %d, want 1 (坏候选不拖垮好候选——失败不阻塞)", registered)
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1 (坏候选进跳过桶)", skipped)
	}

	// 上限：再铺 5 个好候选，本 pass 登记数到 preRepairAutospliceMaxPerPass
	// 封顶（坏候选不占预算——它在判据前置闸就跳过了）。
	for _, stem := range []string{"c1", "c2", "c3", "c4", "c5"} {
		seedAutosplicePair(t, sessions, stem)
	}
	views := autospliceViews(t, sessions)
	registered, _ = runPreRepairAutoSplicePass(true, views, nil, time.Now())
	if registered != preRepairAutospliceMaxPerPass {
		t.Fatalf("registered = %d, want %d (每 tick 登记上限)", registered, preRepairAutospliceMaxPerPass)
	}
}
