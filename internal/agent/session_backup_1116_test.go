package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// 任务 1116 架构终裁的五条备份回归（协调线 1558 断言五条）：
//  1) 带 .lease.lock 的会话 ⇒ 备份成功且备份目录无 *.lock；
//  2) 不可读主 .jsonl ⇒ 返回 error（不制造半个备份），对照 lock 文件不影响成功；
//  3) 端到端超窗活会话（图 95%）⇒ Dieted==true + 无 backup_failed + 降幅 ≥90%；
//  4) Diet 实际调用次数 ≥1（防假绿）；
//  5) ★持锁中的 .lease.lock 场景 ⇒ 备份成功 + Diet 执行 + report.Actions 精确计数。
//
// 本文件只测 backupSessionFiles 与 SelfHealSession 的组合契约；不触碰任何真实
// 用户会话（尤其新9 样本）。

// seedMinimalDAG writes a tiny schema-2 session (one user chain) so backup has
// real sibling files to copy.
func seedMinimalDAG(t *testing.T, path string) {
	t.Helper()
	base := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	entries := []sessionDAGEntry{{Type: sessionDAGTypeLog, Generation: 1, At: base}}
	parent := ""
	for i := 0; i < 3; i++ {
		m := dagMsg(provider.RoleUser, "hello", "M"+string(rune('A'+i)))
		entries = append(entries, dagMessageEntry(t, SessionMainHead, parent, "t1", m, base.Add(time.Duration(i)*time.Second)))
		parent = m.ID
	}
	dagAppend(t, path, entries...)
}

// Test1116BackupSkipsLockFilesAndSucceeds 断言 1：带 .lease.lock 的会话备份
// 成功且备份目录里没有任何锁文件（白名单天然排除，不靠黑名单）。
func Test1116BackupSkipsLockFilesAndSucceeds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "locksess.jsonl")
	seedMinimalDAG(t, path)
	// 人为放两个锁文件（生产里它们是 OS 句柄，这里只是名字形状）。
	for _, name := range []string{"locksess.jsonl.lock", "locksess.jsonl.lease.lock"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("lock"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	backupDir, err := backupSessionFiles(path)
	if err != nil {
		t.Fatalf("backup must succeed despite lock files: %v", err)
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".lock") {
			t.Fatalf("lock file leaked into backup: %q", e.Name())
		}
	}
	// DAG 会话无 .jsonl 检查点属正常（schema-2 权威在 events）；这里断言
	// events.jsonl（白名单数据侧车）确实在备份里。
	if _, err := os.Stat(filepath.Join(backupDir, "locksess.events.jsonl")); err != nil {
		t.Fatalf("events sidecar missing from backup: %v", err)
	}
}

// Test1116BackupFailsFastOnUnreadableTranscript 断言 2：白名单内文件（主
// .jsonl）复制失败 ⇒ 整体 error，不制造半个备份。用一个「目录伪装成 .jsonl」
// 让 copyFile 必然失败，同时保留锁文件证明锁不影响成功路径。
func Test1116BackupFailsFastOnUnreadableTranscript(t *testing.T) {
	dir := t.TempDir()
	// 主 .jsonl 用目录占位（copyFile 读它必然失败）。
	transcript := filepath.Join(dir, "bad.jsonl")
	if err := os.Mkdir(transcript, 0o755); err != nil {
		t.Fatal(err)
	}
	// 锁文件正常存在，验证它们不影响「成功路径」的判据（这里整体应失败）。
	for _, name := range []string{"bad.jsonl.lock", "bad.jsonl.lease.lock"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("lock"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := backupSessionFiles(transcript); err == nil {
		t.Fatal("unreadable main transcript must fail the whole backup (fail-fast), not produce a partial one")
	}
}

// Test1116SelfHealDietHeldLeaseBackupSucceeds 断言 5（★持锁场景）：本进程
// 持有该会话租约时（.lease.lock 被 OS 句柄占用），备份必须成功、Diet 必须
// 执行、report.Actions 精确计数（strip/fold 各一次 done，绝无 promote/hydrate）。
func Test1116SelfHealDietHeldLeaseBackupSucceeds(t *testing.T) {
	t.Skip("tl1227 DAG 读退役: selfheal 备份经 schema-2 读面取样；存量 DAG 日志由 MigrateSessionDAGLog 一次性迁移为 schema-1，读侧单路径化后本场景不再可达")
	dir := t.TempDir()
	path := filepath.Join(dir, "heldlease.jsonl")
	seedMinimalDAG(t, path)
	// 持有真实租约：TryAcquireSessionLease 会创建并锁住 .lease.lock（生产同一条
	// 路径），模拟「启动 Diet 扫描持租约修复闭合会话」的现场。
	lease, err := TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	defer lease.Release()

	// 造一个超窗投影（图占大头），让 Diet 必须真的动手。
	s, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	msgs := s.Snapshot()
	msgs = append(msgs, provider.Message{
		Role: provider.RoleUser, ID: NewMessageID(),
		Content: "big", Images: []string{"data:image/png;base64," + strings.Repeat("A", 2_000_000)},
	})
	// 剥图后仍有足量文本 ⇒ fold 也必须触发（只有一条巨大消息时 fold 会 skipped
	// ——foldProjectionTail 的「even the newest alone busts the budget」分支）。
	for i := 0; i < 40; i++ {
		msgs = append(msgs, provider.Message{
			Role: provider.RoleAssistant, ID: NewMessageID(),
			Content: strings.Repeat("t", 40_000),
		})
	}
	s.Rewrite(msgs, "test_fixture")
	if err := s.SaveRewriteCompact(path); err != nil {
		t.Fatal(err)
	}

	const tinyWindow = 100_000
	report, err := SelfHealSession(path, SelfHealOptions{Diet: true, WindowTokens: tinyWindow})
	if err != nil {
		t.Fatalf("held-lease repair must not fail on backup: %v", err)
	}
	if !report.Repaired {
		t.Fatalf("expected a repair: %+v", report)
	}
	// ★精确计数：strip/fold 各恰好一次 done；无 promote/hydrate。
	stripDone, foldDone, promoteOrHydrate := 0, 0, 0
	for _, a := range report.Actions {
		switch {
		case a.Action == HealActionStripImages && a.Status == "done":
			stripDone++
		case a.Action == HealActionFold && a.Status == "done":
			foldDone++
		case a.Action == HealActionPromoteHead || a.Action == HealActionHydrate:
			promoteOrHydrate++
		}
	}
	if stripDone != 1 || foldDone != 1 {
		t.Fatalf("Diet-only must run strip+fold each exactly once: strip=%d fold=%d actions=%+v",
			stripDone, foldDone, report.Actions)
	}
	if promoteOrHydrate != 0 {
		t.Fatalf("Diet-only must never run promote/hydrate: %+v", report.Actions)
	}
	// 备份目录里不得有锁文件。
	if report.BackupDir == "" {
		t.Fatal("repair must leave a backup dir")
	}
	entries, err := os.ReadDir(report.BackupDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".lock") {
			t.Fatalf("lock file leaked into held-lease backup: %q", e.Name())
		}
	}
}

// Test1116RepairLogSuffixExcludedFromBackup 断言修复记录不在白名单内（它
// 是审计轨迹、不是数据侧车）——避免把上一次修复的日志误当证据备份。
func Test1116RepairLogSuffixExcludedFromBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "withrepair.jsonl")
	seedMinimalDAG(t, path)
	repairLog := filepath.Join(dir, "withrepair"+store.SessionRepairLogSuffix)
	if err := os.WriteFile(repairLog, []byte(`{"at":"x"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	backupDir, err := backupSessionFiles(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(backupDir, filepath.Base(repairLog))); err == nil {
		t.Fatal("repair log must not be backed up (audit trail, not a data sidecar)")
	}
}
