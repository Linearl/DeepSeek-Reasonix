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
