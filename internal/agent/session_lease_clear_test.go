package agent

// Task 456: ClearStaleSessionLeaseInfo is the atomic "prove-free-then-remove"
// primitive shared by the desktop restore reconcile and the local-lease
// takeover adoption. The lock is the arbiter: a live holder's record is never
// touched, a free-lock leftover is retired on both identity sources.

import (
	"os"
	"testing"
	"time"

	"reasonix/internal/store"
)

// TestClearStaleSessionLeaseInfoRetiresFreeLockLeftover covers the restart
// leftover shape: a stale record with a free OS lock must be retired from the
// legacy sidecar AND the lock file, after which the session acquires cleanly.
func TestClearStaleSessionLeaseInfoRetiresFreeLockLeftover(t *testing.T) {
	userPath, key := leaseTestPath(t)
	if err := SaveSessionLeaseInfo(userPath, SessionLeaseInfo{
		SessionPath: key, WriterID: "writer-crashed", PID: 999999,
		Hostname: "restart-leftover", AcquiredAt: time.Now().Add(-2 * time.Hour).UTC(),
	}); err != nil {
		t.Fatalf("stage stale record: %v", err)
	}

	cleared, err := ClearStaleSessionLeaseInfo(userPath)
	if err != nil {
		t.Fatalf("ClearStaleSessionLeaseInfo: %v", err)
	}
	if !cleared {
		t.Fatal("free-lock leftover was not cleared")
	}
	if _, err := LoadSessionLeaseInfo(userPath); !os.IsNotExist(err) {
		t.Fatalf("record survived the clear: err=%v, want gone", err)
	}

	lease, err := TryAcquireSessionLease(userPath)
	if err != nil {
		t.Fatalf("acquire after clear: %v", err)
	}
	lease.Release()
}

// TestClearStaleSessionLeaseInfoRespectsLiveHolder pins the safety rail: a
// live lease (real OS lock held in-process) must never be cleared.
func TestClearStaleSessionLeaseInfoRespectsLiveHolder(t *testing.T) {
	userPath, _ := leaseTestPath(t)
	lease, err := TryAcquireSessionLease(userPath)
	if err != nil {
		t.Fatalf("acquire live lease: %v", err)
	}
	defer lease.Release()

	cleared, err := ClearStaleSessionLeaseInfo(userPath)
	if err != nil {
		t.Fatalf("ClearStaleSessionLeaseInfo: %v", err)
	}
	if cleared {
		t.Fatal("live holder's record was cleared")
	}
	info, err := LoadSessionLeaseInfo(userPath)
	if err != nil || info == nil || info.PID != os.Getpid() {
		t.Fatalf("live holder record damaged: info=%+v err=%v", info, err)
	}
}

// TestClearStaleSessionLeaseInfoNoRecord covers the common uncontaminated
// path: no record anywhere must report not-cleared, leave no error, and not
// create a lock file as a side effect.
func TestClearStaleSessionLeaseInfoNoRecord(t *testing.T) {
	userPath, key := leaseTestPath(t)
	cleared, err := ClearStaleSessionLeaseInfo(userPath)
	if err != nil {
		t.Fatalf("ClearStaleSessionLeaseInfo on a clean session: %v", err)
	}
	if cleared {
		t.Fatal("nothing to clear was reported as cleared")
	}
	if _, err := os.Stat(store.SessionLeaseLock(key)); !os.IsNotExist(err) {
		t.Fatalf("clean session grew a lock file side effect: %v", err)
	}
}
