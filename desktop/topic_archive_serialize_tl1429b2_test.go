package main

// tl1429b-2 回归钉:归档破坏性流的互斥序列化。包 79 装机现场(2026-11-06
// 08:26)的完整机制链:
//
//  1. cleanup-pending reconcile(每次 controller boot 都跑)不持
//     sessionRemovalMu,与 in-flight removal guard(归档 trash 相、延迟
//     trash、另一并发 reconcile)交错;
//  2. 后到者 acquire 被活句柄拒绝(holder_class=self_lock kind=removal_guard),
//     heal 把「登记在册 + 盘上身份缺失」的活句柄当孤儿撤掉(cleared=1);
//  3. 两条 trash 流并发移动同一会话 artifacts → 回收站双记录、meta 缺失桶、
//     「outside known session dirs」楔死清空、侧栏残留。
//
// 修复:reconcile / delayed trash / 启动 metadata reconcile 全部持
// sessionRemovalMu(有界等待,忙则跳过本遍,marker 留给下个收敛点);agent
// 层 sweep/ForceRelease 对 in-flight removal guard 不撤(见 agent 包钉)。
// 本文件钉 desktop 侧三面:并发 reconcile 单记录、忙跳过不误伤、延迟 trash
// 等锁不抢跑。

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
)

func writePendingTrashFixture(t *testing.T) (dir, sessionPath, key string) {
	t.Helper()
	dir = t.TempDir()
	sessionPath = filepath.Join(dir, "race.jsonl")
	if err := os.WriteFile(sessionPath, []byte(`{"role":"user","content":"tl1429b-2"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := agent.MarkCleanupPending(sessionPath, "delete"); err != nil {
		t.Fatal(err)
	}
	return dir, sessionPath, filepath.Base(sessionPath)
}

func trashRecordCount(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(sessionTrashPath(dir))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("read trash root: %v", err)
	}
	return len(entries)
}

// 并发 reconcile 全部持锁串行:先到者 trash,后到者看到 marker 已清即空转。
// 修复前这里会双桶(后到者的 heal 撤掉先到者的活句柄后并发 trash)。
func TestConcurrentCleanupPendingReconcilesProduceSingleTrashRecord(t *testing.T) {
	dir, sessionPath, key := writePendingTrashFixture(t)
	app := NewApp()

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := app.reconcileDesktopCleanupPending(dir); err != nil {
				t.Errorf("reconcile: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := trashRecordCount(t, dir); got != 1 {
		t.Fatalf("trash buckets = %d, want exactly 1 — 双记录回潮", got)
	}
	if _, err := os.Stat(filepath.Join(sessionTrashPath(dir), key, key)); err != nil {
		t.Fatalf("trashed transcript missing: %v", err)
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("live session survived reconciliation (err=%v)", err)
	}
	if agent.IsCleanupPending(sessionPath) {
		t.Fatal("cleanup-pending marker survived reconciliation")
	}
}

// 锁被占(在途破坏性流)时:等待超预算 → 本遍跳过(marker 原样保留),绝不
// 抢跑;锁释放后的下一遍正常收敛。修复前后到者会 heal 抢锁。
func TestReconcileSkipsPassWhenRemovalMutexBusy(t *testing.T) {
	dir, sessionPath, _ := writePendingTrashFixture(t)
	prevBudget := sessionRemovalMuWaitBudget
	sessionRemovalMuWaitBudget = 80 * time.Millisecond
	t.Cleanup(func() { sessionRemovalMuWaitBudget = prevBudget })

	app := NewApp()
	app.sessionRemovalMu.Lock()

	done := make(chan error, 1)
	go func() { done <- app.reconcileDesktopCleanupPending(dir) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("busy pass must skip cleanly, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reconcile did not give up within the shortened budget")
	}
	app.sessionRemovalMu.Unlock()

	if agent.IsCleanupPending(sessionPath) != true {
		t.Fatal("busy-skip pass consumed the cleanup-pending marker — 抢跑了")
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("busy-skip pass trashed the session: %v", err)
	}

	// 锁空闲:同一入口立即收敛。
	if err := app.reconcileDesktopCleanupPending(dir); err != nil {
		t.Fatalf("post-busy reconcile: %v", err)
	}
	if agent.IsCleanupPending(sessionPath) {
		t.Fatal("marker survived the post-busy reconcile")
	}
	if got := trashRecordCount(t, dir); got != 1 {
		t.Fatalf("trash buckets = %d after convergence, want 1", got)
	}
}

// 延迟 trash(destroy 超时形态)先等 teardown 再持 sessionRemovalMu:
// 持锁期间别人 TryLock 失败(诚实忙),它自己串行完成 trash 并消费 guard。
func TestDelayedTopicTrashWaitsForRemovalMutex(t *testing.T) {
	dir, sessionPath, key := writePendingTrashFixture(t)
	guard, err := agent.TryAcquireSessionRemovalGuard(sessionPath)
	if err != nil {
		t.Fatalf("guard acquire: %v", err)
	}
	app := NewApp()
	app.sessionRemovalMu.Lock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		app.delayedDesktopTopicTrash(dir, sessionPath, key, guard, nil)
	}()

	// guard 仍活、锁被占,延迟 trash 不得抢跑建桶。
	time.Sleep(120 * time.Millisecond)
	if got := trashRecordCount(t, dir); got != 0 {
		t.Fatalf("delayed trash built buckets while the mutex was busy — 抢跑")
	}
	if !agent.SessionLeaseLockHeldInProcess(sessionPath) {
		t.Fatal("guard lost its locks before the mutex freed up")
	}

	app.sessionRemovalMu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("delayed trash did not finish after the mutex freed")
	}
	if got := trashRecordCount(t, dir); got != 1 {
		t.Fatalf("trash buckets = %d after delayed trash, want 1", got)
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("live session survived the delayed trash (err=%v)", err)
	}
}
