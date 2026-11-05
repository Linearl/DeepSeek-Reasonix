package agent

// tl1429 回归钉(拒绝身份面):18:39 口径——日志必须给 identity,禁止恒 pid=0。
// pid=0 记录+登记在册句柄的 removal-guard 拒绝必须携带进程内证据,错误文本
// 可直接读出持有者 kind 与时刻;登记表外(模型化真外部)的拒绝不带证据,
// 保守文本不变。

import (
	"errors"
	"os"
	"strings"
	"testing"

	"reasonix/internal/store"
)

// 拒绝身份主钉:登记在册自锁+pid=0 记录 → 拒绝带 HeldInProcess 证据,
// Error() 文本含 kind/since(桌面拒绝日志经同一错误对象 dump)。
func TestTryAcquireSessionRemovalGuardCarriesInProcessEvidence(t *testing.T) {
	userPath, key := leaseTestPath(t)
	if err := os.WriteFile(userPath, []byte("turn"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	release := stagePidLessSelfLock(t, key)
	t.Cleanup(release)

	_, err := TryAcquireSessionRemovalGuard(key)
	var leaseErr *SessionLeaseError
	if !errors.As(err, &leaseErr) {
		t.Fatalf("guard refusal = %v, want a SessionLeaseError", err)
	}
	if len(leaseErr.HeldInProcess) == 0 {
		t.Fatal("refusal carries no in-process evidence — 恒 pid=0 零身份回潮")
	}
	if got := leaseErr.HeldInProcess[0].Kind; got != "test_self_lock" {
		t.Fatalf("evidence kind = %q, want test_self_lock", got)
	}
	if leaseErr.HeldInProcess[0].AcquiredAt.IsZero() {
		t.Fatal("evidence acquired_at is zero")
	}
	text := leaseErr.Error()
	for _, want := range []string{"lock held inside this process", "kind=test_self_lock", "since="} {
		if !strings.Contains(text, want) {
			t.Fatalf("refusal text %q missing %q", text, want)
		}
	}
}

// save 锁长持形态(Holder unknown + save 锁被本进程登记句柄持)同样带证据:
// save 锁拒绝点走登记面的 save-lock 查询。
func TestTryAcquireSessionRemovalGuardCarriesSaveLockEvidence(t *testing.T) {
	userPath, key := leaseTestPath(t)
	if err := os.WriteFile(userPath, []byte("turn"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	lock, err := tryTakeSessionLockFile(store.SessionLockFile(key))
	if err != nil {
		t.Fatalf("hold save lock: %v", err)
	}
	t.Cleanup(lock.Unlock)
	lock.annotateSessionLock("test_save_lock_holder")
	if !SessionSaveLockHeldInProcess(key) {
		t.Fatal("save-lock registry entry missing for the held handle")
	}

	_, guardErr := TryAcquireSessionRemovalGuard(key)
	var leaseErr *SessionLeaseError
	if !errors.As(guardErr, &leaseErr) {
		t.Fatalf("guard refusal = %v, want a SessionLeaseError", guardErr)
	}
	if len(leaseErr.HeldInProcess) == 0 {
		t.Fatal("save-lock refusal carries no in-process evidence")
	}
	if got := leaseErr.HeldInProcess[0].Kind; got != "test_save_lock_holder" {
		t.Fatalf("save-lock evidence kind = %q, want test_save_lock_holder", got)
	}
}
