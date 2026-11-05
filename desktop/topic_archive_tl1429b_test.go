package main

// tl1429b 回归钉:包 78 装机现场四缺陷的修复面。
// ① 正常归档遇 self_lock(登记在册句柄+pid=0 记录)不再三连拒——heal 链的
//    sweep 撤掉本进程句柄后 retry reclaim 自愈;登记摘除(真外部)仍拒绝。
// ② meta 缺失的 unique 回收桶:不再「outside known session dirs」卡死——
//    列表可见、purge 可清。
// ③ cleanup-pending reconcile 对 transcript 已消失的 target 不再铸造第二
//    个空桶(双记录源),只撕残留。

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/store"
)

// 验收①(主钉):正常路径归档遇 self_lock 一次成功——不再要求用户升级 force。
func TestTrashTopicNormalPathSurvivesRegisteredSelfLock(t *testing.T) {
	isolateDesktopUserDirs(t)
	topicID := "topic_tl1429b_normal_selflock"
	sessionPath, projectRoot, release := stageSelfLockScene(t, "tl1429b-normal.jsonl", topicID, "TL1429b normal")
	t.Cleanup(release)

	if err := NewApp().TrashTopic(topicID); err != nil {
		t.Fatalf("normal TrashTopic against a booked self-lock = %v, want success (heal 自愈)", err)
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("session transcript survived the archive: %v", err)
	}
	if got := loadTopicTitle(projectRoot, topicID); got != "" {
		t.Fatalf("archived topic title = %q, want cleared", got)
	}
	reattach, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("reacquire after archive = %v, want a clean lease state", err)
	}
	reattach.Release()
}

// 验收②对照(真外部仍拒绝):登记摘除后正常归档仍拒绝,transcript 原样。
func TestTrashTopicNormalPathStillRefusesUnbookedHolder(t *testing.T) {
	isolateDesktopUserDirs(t)
	topicID := "topic_tl1429b_normal_external"
	sessionPath, _, release := stageSelfLockScene(t, "tl1429b-external.jsonl", topicID, "TL1429b external")
	t.Cleanup(release)
	if !agent.DropSessionLeaseLockRegistrationForTest(sessionPath) {
		t.Fatal("drop seam reported nothing dropped")
	}

	if err := NewApp().TrashTopic(topicID); err == nil {
		t.Fatal("normal archive grabbed a lock this process cannot name — 真外部被误抢")
	}
	if _, statErr := os.Stat(sessionPath); statErr != nil {
		t.Fatalf("refused archive removed the session: %v", statErr)
	}
}

// 验收②(meta 缺失桶):无 .trash-meta.json 的 unique 桶保持可见且可 purge,
// 不再报「trashed session path outside known session dirs」。
func TestMetaLessTrashBucketStaysListableAndPurgeable(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	// unique 桶(带 -deleted- 时间戳名)+桶内 transcript,故意不写 meta——
	// 「move 成功、meta 写入前崩溃」的桶形态。
	stem := "tl1429b-metaless.jsonl"
	itemName := stem + "-deleted-" + time.Now().Format("20060102T150405.000000000") + "-0"
	itemDir := filepath.Join(dir, sessionTrashDir, itemName)
	if err := os.MkdirAll(itemDir, 0o755); err != nil {
		t.Fatalf("mkdir trash bucket: %v", err)
	}
	transcriptPath := filepath.Join(itemDir, stem)
	if err := os.WriteFile(transcriptPath, []byte("turn"), 0o600); err != nil {
		t.Fatalf("write bucketed transcript: %v", err)
	}

	app := NewApp()
	listed := app.ListTrashedSessions()
	if len(listed) != 1 {
		t.Fatalf("ListTrashedSessions = %d entries, want the meta-less bucket listed", len(listed))
	}
	if err := app.PurgeTrashedSession(listed[0].Path); err != nil {
		t.Fatalf("PurgeTrashedSession on a meta-less bucket = %v, want success (not \"outside known session dirs\")", err)
	}
	if _, err := os.Stat(transcriptPath); !os.IsNotExist(err) {
		t.Fatalf("meta-less bucket transcript survived the purge: %v", err)
	}
}

// 验收③(双记录源):cleanup-pending reconcile 对 transcript 已消失的 target
// 只撕残留,不再铸造第二个空桶。
func TestCleanupPendingReconcileDoesNotMintSecondBucket(t *testing.T) {
	isolateDesktopUserDirs(t)
	sessionPath, projectRoot := stageTerminalArchiveTopic(t, "tl1429b-double.jsonl", "topic_tl1429b_double", "TL1429b double")
	key := filepath.Base(sessionPath)
	dir := filepath.Dir(sessionPath)

	// 布景:第一次归档已把 transcript 移走(unique 桶+meta 完整),但
	// cleanup-pending 残留;reconcile 对该 target 重跑。
	itemDir := filepath.Join(dir, sessionTrashDir, key)
	if err := os.MkdirAll(itemDir, 0o755); err != nil {
		t.Fatalf("mkdir bucket: %v", err)
	}
	if err := os.Rename(sessionPath, filepath.Join(itemDir, key)); err != nil {
		t.Fatalf("stage first archive move: %v", err)
	}
	if err := agent.MarkCleanupPending(sessionPath, "delete"); err != nil {
		t.Fatalf("stage cleanup pending: %v", err)
	}

	if err := reconcileDesktopTrashSessionArtifacts(dir, sessionPath, key); err != nil {
		t.Fatalf("reconcile = %v, want a residue-only pass", err)
	}
	// 第二桶(guard 桶形态)不得出现:.trash 下只有第一桶。
	entries, _ := os.ReadDir(filepath.Join(dir, sessionTrashDir))
	if len(entries) != 1 {
		t.Fatalf("trash holds %d buckets, want exactly the first one — 双记录回潮", len(entries))
	}
	if agent.IsCleanupPending(sessionPath) {
		t.Fatal("cleanup-pending survived the reconcile")
	}
	// 残留侧车被撕:transcript 原路径的 meta/锁文件不复活。
	if _, err := os.Stat(store.SessionLeaseLock(sessionPath)); !os.IsNotExist(err) {
		t.Fatalf("lease lock residue survived: %v", err)
	}
	_ = projectRoot
}
