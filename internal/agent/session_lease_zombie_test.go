package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/store"
)

// tl 1201 验收①的 agent 层底座：持有者已死的残留（free lock）必须能被新
// runtime 直接接管，且接管后发布的身份必须是接管者自己的——旧死 pid 不能再
// 从身份文件里爬出来污染后续判定。
func TestTryAcquireSessionLeaseTakesOverDeadHolderLeftover(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zombie-takeover-session.jsonl")
	canonical := canonicalSessionSavePath(path)

	dead := SessionLeaseInfo{
		SessionPath: canonical,
		WriterID:    "crashed-host-deadbeef",
		PID:         999999999,
		Hostname:    "elsewhere",
		AcquiredAt:  time.Now().UTC().Add(-time.Hour),
	}

	t.Run("legacy sidecar leftover, no lock file", func(t *testing.T) {
		if err := SaveSessionLeaseInfo(path, dead); err != nil {
			t.Fatalf("stage sidecar: %v", err)
		}
		t.Cleanup(func() { _ = os.Remove(sessionLeaseInfoPath(path)) })

		lease, err := TryAcquireSessionLease(path)
		if err != nil {
			t.Fatalf("takeover from sidecar leftover: %v", err)
		}
		lease.Release()
	})

	t.Run("lock file with stale payload, lock free", func(t *testing.T) {
		payload, err := json.Marshal(dead)
		if err != nil {
			t.Fatalf("marshal stale payload: %v", err)
		}
		if err := os.WriteFile(store.SessionLeaseLock(canonical), sessionLeaseOwnerBytes(payload), 0o600); err != nil {
			t.Fatalf("stage stale lock payload: %v", err)
		}
		t.Cleanup(func() { _ = os.Remove(store.SessionLeaseLock(canonical)) })

		lease, err := TryAcquireSessionLease(path)
		if err != nil {
			t.Fatalf("takeover from stale lock payload: %v", err)
		}
		defer lease.Release()

		info, err := LoadSessionLeaseInfo(path)
		if err != nil || info == nil {
			t.Fatalf("load republished identity: %v %+v", err, info)
		}
		if info.PID != os.Getpid() || info.WriterID != SessionWriterID() {
			t.Fatalf("identity not republished to the taker: pid=%d writer=%q", info.PID, info.WriterID)
		}
	})
}

// 真实互斥回归（tl 1201 验收④的进程内形态）：一个活持有者（同进程另一
// runtime 的形态）在位时，第二个 acquire 必须继续被拒——zombie 接管只对
// free lock 生效，绝不偷活锁。
func TestTryAcquireSessionLeaseKeepsRealMutualExclusion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mutex-session.jsonl")

	holder, err := TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("holder acquire: %v", err)
	}
	defer holder.Release()

	second, err := TryAcquireSessionLease(path)
	if err == nil {
		second.Release()
		t.Fatal("second acquire succeeded against a live holder — mutual exclusion regressed")
	}
	var leaseErr *SessionLeaseError
	if !errors.As(err, &leaseErr) {
		t.Fatalf("second acquire error type = %T, want *SessionLeaseError", err)
	}
}
