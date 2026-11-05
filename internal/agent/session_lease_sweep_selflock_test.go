package agent

// tl1429b 回归钉:orphan sweep 的登记句柄撤除分支(正常路径 self_lock 清场)。
// 包 78 装机现场:holder_class=self_lock 出现在 NORMAL 归档路径——tl1429 只
// 豁免了 force/终局判定,正常路径的 heal(reclaim→sweep→retry reclaim)里
// sweep 的锁仲裁对自锁句柄同样失败,三连拒复现。本钉锁住修复后的两面:
//  - 登记在册句柄 + pid-less 身份(证据门内)→ sweep 一次清场,重 acquire 干净;
//  - 登记摘除(模型化真外部)→ sweep 不越界,cleared=0,锁仍持。

import (
	"os"
	"testing"
)

func TestSweepOrphanEvictsRegisteredSelfLockHandle(t *testing.T) {
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

	cleared, err := SweepOrphanInProcessSessionLease(key)
	if err != nil {
		t.Fatalf("sweep = %v, want a clean self-lock clear", err)
	}
	if cleared == 0 {
		t.Fatal("sweep cleared nothing for a booked self-lock — 正常路径 self_lock 三连拒回潮")
	}
	if SessionLeaseLockHeldInProcess(key) {
		t.Fatal("registry entry survived the sweep eviction")
	}
	if _, ok := sessionLeaseOwners.Load(key); ok {
		t.Fatal("orphan owner entry survived the sweep")
	}
	// 清场实锤:同路径立即可干净重新 acquire。
	fresh, err := TryAcquireSessionLease(userPath)
	if err != nil {
		t.Fatalf("reacquire after sweep = %v, want a clean lease state", err)
	}
	fresh.Release()
}

func TestSweepOrphanLeavesUnbookedHolderAlone(t *testing.T) {
	userPath, key := leaseTestPath(t)
	if err := os.WriteFile(userPath, []byte("turn"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	release := stagePidLessSelfLock(t, key)
	t.Cleanup(release)
	if !DropSessionLeaseLockRegistrationForTest(key) {
		t.Fatal("drop seam reported nothing dropped")
	}

	cleared, err := SweepOrphanInProcessSessionLease(key)
	if err != nil {
		t.Fatalf("sweep = %v", err)
	}
	if cleared != 0 {
		t.Fatal("sweep grabbed a lock this process cannot name — 真外部被误抢")
	}
	if !SessionLeaseHeldByOtherRuntime(key) {
		t.Fatal("unbooked holder's lock probe flipped — 保守语义被改动")
	}
}
