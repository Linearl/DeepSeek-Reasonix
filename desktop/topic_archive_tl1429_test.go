package main

// tl1429 端到端钉:归档自锁形态的判定与强放。
//
// 2026-11-04 17:55 装机现场:两个退休会话归档拒绝,拒绝链
// removal guard refused holder_class=unknown pid=0 → force retire stale
// record(pid=0)→ still held → 8s terminal fallback 耗尽
// 「held outside this process / holder cannot be identified」。
// 根因:一个丢失绑定的本进程锁句柄(自锁)在 lock probe 里与另一进程不可区分
// ——终局兜底把它误判成外部占用提前烧断重试。
//
// 本文件钉住修复后的端到端验收:
//  a. 自锁形态(登记在册句柄+pid=0 记录)强制归档一次成功(本进程强放覆盖);
//  c. 登记表外的句柄(模型化真外部)仍拒绝,永不误抢。
// (拒绝日志的 identity 钉在 session_lease_holder_tl1429_test.go。)

import (
	"os"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/store"

	"golang.org/x/sys/windows"
)

// writePidLessLeasePayload writes a decodable pid=0 holder record into the
// lock file while the holder's handle keeps the byte-range lock (the
// 2026-11-04 scene: holder_class=unknown pid=0 — a readable record naming
// nobody).
func writePidLessLeasePayload(t *testing.T, lockPath string) {
	t.Helper()
	pathp, err := windows.UTF16PtrFromString(lockPath)
	if err != nil {
		t.Fatalf("lock path utf16: %v", err)
	}
	handle, err := windows.CreateFile(pathp,
		windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("open lease lock for payload: %v", err)
	}
	f := os.NewFile(uintptr(handle), lockPath)
	// Offset 1 = sessionLeaseOwnerOffset (same discipline as
	// corruptLeaseLockContent).
	payload := []byte(`{"session_path":"staged","writer_id":"","pid":0,"acquired_at":"2026-11-04T09:55:00Z"}`)
	if _, err := f.WriteAt(payload, 1); err != nil {
		f.Close()
		t.Fatalf("write pid-less payload: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close payload handle: %v", err)
	}
}

// stageSelfLockScene stages the tl1429 self-lock form on a staged topic:
// a registry-booked in-process handle holds the lease lock (the lost-binding
// form: no lease object, no owner entry, no tab) beside a pid=0 identity
// record. Returns the session path and the handle release.
func stageSelfLockScene(t *testing.T, name, topicID, title string) (string, string, func()) {
	t.Helper()
	sessionPath, projectRoot := stageTerminalArchiveTopic(t, name, topicID, title)
	release, err := agent.HoldSessionLeaseLockForTest(sessionPath)
	if err != nil {
		t.Fatalf("stage self lock: %v", err)
	}
	writePidLessLeasePayload(t, store.SessionLeaseLock(sessionPath))
	return sessionPath, projectRoot, release
}

// 验收 a(主钉):自锁形态强制归档一次成功——终局强放直接撤掉登记在册句柄,
// guard 正常拿下,归档走完整链;锁/身份零残留,同路径立即可重新 acquire。
func TestTrashTopicForceSurvivesRegisteredSelfLockHandle(t *testing.T) {
	isolateDesktopUserDirs(t)
	topicID := "topic_tl1429_self_lock"
	sessionPath, projectRoot, release := stageSelfLockScene(t, "tl1429-self-lock.jsonl", topicID, "TL1429 self lock")
	t.Cleanup(release)

	started := time.Now()
	if err := NewApp().TrashTopicForce(topicID); err != nil {
		t.Fatalf("TrashTopicForce against a booked self-lock = %v, want success within the budget", err)
	}
	if elapsed := time.Since(started); elapsed > archiveDeadlineBudget {
		t.Fatalf("archive took %v, want <%v even through the fallback", elapsed, archiveDeadlineBudget)
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("session transcript survived the archive: %v", err)
	}
	if _, err := os.Stat(store.SessionLeaseLock(sessionPath)); !os.IsNotExist(err) {
		t.Fatalf("self-lock handle's lock file survived the archive: %v", err)
	}
	if _, err := os.Stat(store.SessionLeaseInfo(sessionPath)); !os.IsNotExist(err) {
		t.Fatalf("pid-less identity record survived the archive: %v", err)
	}
	if got := loadTopicTitle(projectRoot, topicID); got != "" {
		t.Fatalf("archived topic title = %q, want cleared", got)
	}
	// 句柄登记条目被强放撤掉(登记面零悬挂)。
	if agent.SessionLeaseLockHeldInProcess(sessionPath) {
		t.Fatal("registry still books the evicted handle")
	}
	reattach, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("reacquire after archive = %v, want a clean lease state", err)
	}
	reattach.Release()
}

// 验收 c(不误抢钉):登记摘除(模型化「本进程不知情的句柄」=真外部占用)后,
// 强制归档必须仍拒绝——终局判定回到 outside-process,transcript 与锁原样。
func TestTrashTopicForceStillRefusesUnbookedExternalHolder(t *testing.T) {
	isolateDesktopUserDirs(t)
	topicID := "topic_tl1429_external"
	sessionPath, _, release := stageSelfLockScene(t, "tl1429-external.jsonl", topicID, "TL1429 external")
	t.Cleanup(release)
	if !agent.DropSessionLeaseLockRegistrationForTest(sessionPath) {
		t.Fatal("drop seam reported nothing dropped")
	}

	err := NewApp().TrashTopicForce(topicID)
	if err == nil {
		t.Fatal("force archive grabbed a lock this process cannot name — 真实外部占用被误抢")
	}
	if _, statErr := os.Stat(sessionPath); statErr != nil {
		t.Fatalf("refused archive removed the session: %v", statErr)
	}
	if _, statErr := os.Stat(store.SessionLeaseLock(sessionPath)); statErr != nil {
		t.Fatalf("refused archive tore the foreign holder's lock file: %v", statErr)
	}
}
