package agent

// tl1429b-2 回归钉:in-flight removal guard 的登记纪律。包 79 装机现场
// (2026-11-06 08:26):heal→refused→heal(cleared=1)循环 5 秒一轮——根因是
// cleanup-pending reconcile / delayed trash 与归档流在同一会话上交错,后到者
// 的 heal 把先到者**仍然活着的** removal guard 句柄当孤儿撤掉(cleared=1),
// 两条破坏性流并发操作同一会话(双记录/无 meta 桶/锁残留的全家桶)。
// 修复两面:
//  - 登记面区分「in-flight removal guard」(活主,goroutine 在身后,会自己
//    释放)与「丢绑定句柄」(孤儿,可撤)。sweep 与终局强放对前者不越界。
//  - (desktop 侧)reconcile / delayed trash 持 sessionRemovalMu,让后续流
//    序列化或诚实拒绝——见 desktop 包测试。
// 本文件钉 agent 层:in-flight guard 存续期 sweep/ForceRelease 不撤;guard
// 正常退场后清场语义照旧(既有 TestSweepOrphanEvictsRegisteredSelfLockHandle
// 继续钉 test_self_lock 孤儿形态的撤除)。

import (
	"os"
	"testing"
)

func stageInFlightRemovalGuard(t *testing.T, userPath string) *SessionRemovalGuard {
	t.Helper()
	guard, err := TryAcquireSessionRemovalGuard(userPath)
	if err != nil {
		t.Fatalf("removal guard acquire: %v", err)
	}
	if !SessionLeaseLockHeldInProcess(userPath) {
		t.Fatal("removal guard did not book its lease lock handle")
	}
	return guard
}

func TestSweepLeavesInFlightRemovalGuardInPlace(t *testing.T) {
	userPath, key := leaseTestPath(t)
	if err := os.WriteFile(userPath, []byte("turn"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	guard := stageInFlightRemovalGuard(t, userPath)

	// Guard 存续期:盘上无身份记录(removal guard 不发布身份)——正是包 79
	// 现场的证据门形态。sweep 必须认出这是 in-flight 活主,不撤句柄。
	cleared, err := SweepOrphanInProcessSessionLease(key)
	if err != nil {
		t.Fatalf("sweep = %v, want a clean leave-alone pass", err)
	}
	if cleared != 0 {
		t.Fatalf("sweep cleared %d during an in-flight removal guard — 活主被当孤儿撤,互斥被撕", cleared)
	}
	if !SessionLeaseLockHeldInProcess(key) {
		t.Fatal("in-flight removal guard's registration was evicted by the sweep")
	}

	// Guard 正常退场后:登记随句柄注销,锁文件已删,路径回到干净态。
	if err := guard.RemoveSidecarsAndRelease(); err != nil {
		t.Fatalf("guard release: %v", err)
	}
	if SessionLeaseLockHeldInProcess(key) {
		t.Fatal("guard release left its registration behind")
	}
	// 同路径立即可重新拿全(互斥恢复)。
	fresh := stageInFlightRemovalGuard(t, userPath)
	fresh.RemoveSidecarsAndRelease()
}

func TestForceReleaseLeavesInFlightRemovalGuardInPlace(t *testing.T) {
	userPath, key := leaseTestPath(t)
	if err := os.WriteFile(userPath, []byte("turn"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	guard := stageInFlightRemovalGuard(t, userPath)

	if ForceReleaseInProcessSessionLease(key) {
		t.Fatal("terminal force-release tore down an in-flight removal guard — 两条破坏性流被同时放行")
	}
	if !SessionLeaseLockHeldInProcess(key) {
		t.Fatal("in-flight removal guard's registration was evicted by the force release")
	}
	if err := guard.RemoveSidecarsAndRelease(); err != nil {
		t.Fatalf("guard release: %v", err)
	}
	if SessionLeaseLockHeldInProcess(key) {
		t.Fatal("guard release left its registration behind")
	}
}
