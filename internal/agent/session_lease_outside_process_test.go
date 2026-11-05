package agent

// tl1429 回归钉(判定+强放面):终局兜底「outside this process」判定输入。
//
// 2026-11-04 17:55 装机现场的根因:一个丢失绑定的本进程句柄(自锁)在
// SessionLeaseHeldByOtherRuntime 的 lock probe 里与另一进程不可区分,tl1404
// 终局兜底把它误判成外部占用,「held outside this process」提前烧断重试,
// 8s 预算没跑完就拒绝。修复后:
//  - SessionLeaseHeldByOtherRuntime 的保守语义一字不动(GC/save/rename/提示
//    等消费方都需要保守侧)——本进程句柄照旧报 true;
//  - SessionLeaseHeldOutsideProcess(终局专用)用登记表区分:登记在册=本进程
//    自己的状态(终局强放撤),登记表外=真外部(永不抢,tl1367 line 1);
//  - ForceReleaseInProcessSessionLease 对登记在册句柄直接撤——自锁形态归档
//    成功的前提;登记表外的句柄照旧不越界。

import (
	"errors"
	"os"
	"testing"

	"reasonix/internal/store"

	"golang.org/x/sys/windows"
)

// stagePidLessSelfLock 布置 tl1429 自锁形态:一个登记在册的本进程句柄持有
// lease 锁(走过 wrapper、无 lease 对象、无 owner 条目、无 tab),盘上身份
// 记录为可读的 pid=0 残留(2026-11-04 现场的 holder_class=unknown pid=0)。
func stagePidLessSelfLock(t *testing.T, key string) (release func()) {
	t.Helper()
	releaseLock, err := HoldSessionLeaseLockForTest(key)
	if err != nil {
		t.Fatalf("stage self lock: %v", err)
	}
	if !SessionLeaseLockHeldInProcess(key) {
		t.Fatal("wrapper take did not book the in-process registry entry")
	}
	writePidLessLeasePayload(t, store.SessionLeaseLock(key))
	return releaseLock
}

// writePidLessLeasePayload writes a decodable pid=0 holder record into the
// lock file while the holder's handle keeps the byte-range lock (same
// share-mask discipline as tearLeaseIdentityInPlace).
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
	// Offset 1 = sessionLeaseOwnerOffset (the lock file reserves byte 0).
	payload := []byte(`{"session_path":"staged","writer_id":"","pid":0,"acquired_at":"2026-11-04T09:55:00Z"}`)
	if _, err := f.WriteAt(payload, 1); err != nil {
		f.Close()
		t.Fatalf("write pid-less payload: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close payload handle: %v", err)
	}
}

// 判定 bug 实证(修复核心):登记在册的本进程句柄+pid=0 残留记录。
// SessionLeaseHeldByOtherRuntime 照旧报 true(保守语义钉);终局判定换用的
// SessionLeaseHeldOutsideProcess 必须报 false——修复前它走 OtherRuntime 的
// lock probe,对「本进程第二句柄」与「另一进程」同样失败,把自锁形态误判成
// 外部占用(17:55 现场的「held outside this process」提前烧断重试)。
func TestSessionLeaseHeldOutsideProcessSeparatesSelfLockFromForeign(t *testing.T) {
	userPath, key := leaseTestPath(t)
	if err := os.WriteFile(userPath, []byte("turn"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	release := stagePidLessSelfLock(t, key)
	t.Cleanup(release)

	// 盘上记录可读且 pid=0(现场形态)。
	info, err := LoadSessionLeaseInfo(key)
	if err != nil {
		t.Fatalf("load staged pid-less record: %v", err)
	}
	if info.PID != 0 {
		t.Fatalf("staged record pid = %d, want 0", info.PID)
	}

	if !SessionLeaseHeldByOtherRuntime(key) {
		t.Fatal("SessionLeaseHeldByOtherRuntime flipped for a held lock — 保守语义被改动")
	}
	if SessionLeaseHeldOutsideProcess(key) {
		t.Fatal("self-lock form misclassified as outside this process — tl1429 判定 bug 回潮")
	}

	// 句柄退场后锁空:两个判定都落回 false(free/leftover)。
	release()
	if SessionLeaseHeldByOtherRuntime(key) {
		t.Fatal("lock probe still reports held after the handle release")
	}
	if SessionLeaseHeldOutsideProcess(key) {
		t.Fatal("outside-process verdict survived the handle release")
	}
}

// 真实外部占用仍拒绝(不误抢钉):登记摘除(模型化「本进程不知情的句柄」=
// 预登记时代泄漏/另一进程)后,句柄仍持锁,终局判定回到 true,强放不越界。
func TestSessionLeaseHeldOutsideProcessTrueForUnbookedHolder(t *testing.T) {
	userPath, key := leaseTestPath(t)
	if err := os.WriteFile(userPath, []byte("turn"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	release := stagePidLessSelfLock(t, key)
	t.Cleanup(release)

	if !DropSessionLeaseLockRegistrationForTest(key) {
		t.Fatal("drop seam reported nothing dropped")
	}
	if SessionLeaseLockHeldInProcess(key) {
		t.Fatal("registry still books the dropped entry")
	}
	if !SessionLeaseHeldOutsideProcess(key) {
		t.Fatal("an unbooked holder must stay outside this process — 真实外部占用仍拒绝钉")
	}
	// 强放不越界:登记表无条目 → 原语如实报告无可释放,锁仍被持。
	if ForceReleaseInProcessSessionLease(key) {
		t.Fatal("force release grabbed a lock this process cannot name")
	}
	probe, err := tryTakeSessionLeaseLock(key)
	if !errors.Is(err, ErrSessionLeaseHeld) {
		if err == nil {
			probe.RemoveAndUnlock()
		}
		t.Fatalf("lock after refused force release: err=%v, want still held", err)
	}
}

// 强放覆盖钉(自锁形态归档成功的前提):登记在册句柄+pid=0 记录+孤儿 owner
// 条目,终局强放一次清场,同路径立即可重新 acquire。
func TestForceReleaseInProcessSessionLeaseEvictsRegisteredSelfLock(t *testing.T) {
	userPath, key := leaseTestPath(t)
	if err := os.WriteFile(userPath, []byte("turn"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	release := stagePidLessSelfLock(t, key)
	t.Cleanup(release)
	// 孤儿 owner 条目(tl1383 形态:条目活过自己的盘上记录)。
	sessionLeaseOwners.Store(key, uint64(1<<62))
	sessionLeaseActiveOwners.Store(key, uint64(1<<62))
	t.Cleanup(func() {
		sessionLeaseOwners.Delete(key)
		sessionLeaseActiveOwners.Delete(key)
	})

	if !ForceReleaseInProcessSessionLease(key) {
		t.Fatal("force release reported nothing released for a booked self-lock")
	}
	if SessionLeaseLockHeldInProcess(key) {
		t.Fatal("registry entry survived the eviction")
	}
	if _, ok := sessionLeaseOwners.Load(key); ok {
		t.Fatal("orphan owner entry survived the eviction")
	}
	if _, ok := sessionLeaseActiveOwners.Load(key); ok {
		t.Fatal("active owner entry survived the eviction")
	}
	if _, err := LoadSessionLeaseInfo(key); !os.IsNotExist(err) {
		t.Fatalf("pid-less identity record survived the eviction: %v", err)
	}
	// 清场实锤:同路径立即可干净重新 acquire。
	fresh, err := TryAcquireSessionLease(userPath)
	if err != nil {
		t.Fatalf("reacquire after eviction = %v, want a clean lease state", err)
	}
	fresh.Release()
}
