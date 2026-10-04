package collabinbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/filelock"
)

// task 461 P1 acceptance（461 卡）：锁被外部进程持有时，收件箱操作 ≤5s 返回
// 含锁占用语义的明确错误而非无限挂起；锁等待期间取消请求 ctx（用户点停止）
// ≤1s 生效。断言全部硬判定，无 SKIP。

// newLockedStore builds a store over a temp mail dir whose inbox lock is held
// by an OS-level file lock — equivalent to another Reasonix window/process
// owning it (same-process different-handle conflicts at the OS layer exactly
// like a second process).
func newLockedStore(t *testing.T) (*Store, func()) {
	t.Helper()
	s := New(t.TempDir(), nil)
	if err := os.MkdirAll(s.mailDir, 0o755); err != nil {
		t.Fatal(err)
	}
	held, err := filelock.TryAcquire(filepath.Join(s.mailDir, lockName))
	if err != nil {
		t.Fatalf("hold inbox lock: %v", err)
	}
	return s, held
}

// 任务461 P11 升级了读路径语义：锁被楔住时 List 不再报错，而是降级无锁直读
// （Degraded=true，数据不空）——该行为由 degraded_read_test.go 钉死。本文件
// 保留写路径（ApplyRetention）的有界语义：锁被占 → 预算内返错。
func TestApplyRetentionErrorsWithinBudgetWhenLockHeldElsewhere(t *testing.T) {
	s, held := newLockedStore(t)
	defer held()

	started := time.Now()
	_, err := s.ApplyRetention(context.Background())
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("ApplyRetention must fail while the inbox lock is held elsewhere, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ApplyRetention error = %v, want deadline exceeded", err)
	}
	if !strings.Contains(err.Error(), "lock busy") {
		t.Fatalf("error lacks lock-busy semantics (锁被占用): %v", err)
	}
	if elapsed > lockWaitTimeout+2*time.Second {
		t.Fatalf("ApplyRetention waited %v, want ≤ lockWaitTimeout(%v)+slack", elapsed, lockWaitTimeout)
	}
}

func TestWritePathReturnsWhenRequestCancelledWhileLockHeld(t *testing.T) {
	s, held := newLockedStore(t)
	defer held()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(150*time.Millisecond, cancel)

	started := time.Now()
	_, err := s.ApplyRetention(ctx)
	elapsed := time.Since(started)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ApplyRetention error = %v, want context.Canceled", err)
	}
	if elapsed >= time.Second {
		t.Fatalf("returned after %v, want <1s after request cancel (task 461 P1 停止 SLA)", elapsed)
	}
}
