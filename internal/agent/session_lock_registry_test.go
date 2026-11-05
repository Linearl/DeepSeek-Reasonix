package agent

// tl1429 回归钉(登记面):进程内每把 wrapper 句柄在锁登记面留痕——
// kind(哪个 runtime/op 在持)+ 时刻。泄漏句柄(丢 binding 的自锁形态)由此
// 可指认;判定/强放两侧的消费钉见 session_lease_outside_process_test.go
// 与 session_removal_evidence_test.go。

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionLockRegistryBooksAnnotatesAndRetires(t *testing.T) {
	userPath, key := leaseTestPath(t)
	if err := os.WriteFile(userPath, []byte("turn"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	release, err := HoldSessionLeaseLockForTest(key)
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if !SessionLeaseLockHeldInProcess(key) {
		t.Fatal("registry lost the entry of a live wrapper handle")
	}
	ev := SessionLeaseLockInProcessHolders(key)
	if len(ev) != 1 {
		t.Fatalf("evidence entries = %d, want 1", len(ev))
	}
	if ev[0].Kind != "test_self_lock" {
		t.Fatalf("evidence kind = %q, want test_self_lock (annotate 打点)", ev[0].Kind)
	}
	if ev[0].AcquiredAt.IsZero() {
		t.Fatal("evidence acquired_at is zero — 登记面必须给时刻")
	}
	if time.Since(ev[0].AcquiredAt) > time.Minute {
		t.Fatalf("evidence acquired_at = %v, want recent", ev[0].AcquiredAt)
	}
	// 泄漏句柄的脱敏证据:kind+时刻可 dump,路径不出(日志纪律)。
	if got := ev[0].String(); got == "" || filepath.IsAbs(got) {
		t.Fatalf("evidence String() = %q, want a redacted kind=… since=… form", got)
	}

	release()
	if SessionLeaseLockHeldInProcess(key) {
		t.Fatal("registry entry survived the handle release")
	}
	if ev := SessionLeaseLockInProcessHolders(key); len(ev) != 0 {
		t.Fatalf("evidence after release = %v, want empty", ev)
	}
}
