package collabinbox

import (
	"context"
	"strings"
	"testing"
	"time"

	"reasonix/internal/filelock"
	"reasonix/internal/sessioncollab"
)

// 任务461 P11 验收：①无持锁 → 面板秒开（共享锁短预算）；②锁被楔住 → 降级
// 无锁直读，面板有数据（Degraded=true）而不是空/报错，且降级读绝不改动状态
// （保留期跳过）；③写路径超时错误指认最后持锁者。真断言，无 SKIP。

func TestDegradedReadReturnsDataWhenLockWedged(t *testing.T) {
	store, mail := fixtureStore(t)
	deliver(t, mail, sessioncollab.MailMessage{From: "sc_peer", To: "sc_me", Body: "panel data"})
	ctx := context.Background()

	// 楔住 .collab-inbox.lock（独占，等价于一个卡死的长持锁者）。
	wedge, err := filelock.TryAcquire(store.lockFilePath())
	if err != nil {
		t.Fatalf("wedge the inbox lock: %v", err)
	}
	defer wedge()

	started := time.Now()
	snap, err := store.List(ctx, Query{}, true) // applyRetention=true：降级读必须跳过保留期而非报错
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("degraded read must succeed while the lock is wedged, got err=%v", err)
	}
	if !snap.Degraded {
		t.Fatal("snapshot must be marked Degraded on the unlocked read")
	}
	if snap.Total != 1 {
		t.Fatalf("degraded read must still carry data, got total=%d", snap.Total)
	}
	if elapsed >= lockWaitTimeout {
		t.Fatalf("degraded read waited %v, want well under the write budget %v", elapsed, lockWaitTimeout)
	}
	// 降级读绝不改动状态：保留期未被执行（状态文件未被动过）。
	if holder := store.LockHolderInfo(); strings.Contains(holder, "pid=") {
		t.Fatalf("degraded read must not (re)stamp the holder info, got %q", holder)
	}
}

func TestWritePathTimeoutNamesLastHolder(t *testing.T) {
	store, _ := fixtureStore(t)
	ctx := context.Background()

	// 先做一次成功写操作：持锁者信息被写入锁文件。
	if _, err := store.Dismiss(ctx, []string{"msg_x"}); err != nil {
		t.Fatal(err)
	}
	if holder := store.LockHolderInfo(); !strings.HasPrefix(holder, "pid=") {
		t.Fatalf("holder info must be stamped on exclusive acquire, got %q", holder)
	}

	// 楔住锁 → 写路径超时错误必须指认最后持锁者。
	wedge, err := filelock.TryAcquire(store.lockFilePath())
	if err != nil {
		t.Fatal(err)
	}
	defer wedge()

	_, err = store.Dismiss(ctx, []string{"msg_y"})
	if err == nil {
		t.Fatal("contended write must fail with the bounded budget")
	}
	if !strings.Contains(err.Error(), "last holder: pid=") {
		t.Fatalf("write timeout error must name the last holder, got: %v", err)
	}
}
