package main

// tl1367-fix defect A regression: a failed lease→removal-guard conversion used
// to surface the bare errSessionBusyElsewhere sentinel, severing the
// errors.As chain — the refusal log degraded to holder_class=unknown pid=0
// and the frontend lost the holder block. The refusal must keep the lease
// error flowing (wrapped, both faces reachable through Unwrap).

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
)

func TestAcquireTopicArchiveOwnershipConvertBusyKeepsLeaseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte("work"), 0o644); err != nil {
		t.Fatalf("seed session file: %v", err)
	}
	key := sessionRuntimeKey(path)

	// A tab-bound lease that has already been released: takeTopicArchiveSessionLease
	// hands it over, TryConvertToRemovalGuard refuses it (released), and the
	// ownership acquisition must surface that refusal as an identity-carrying
	// busy error instead of the bare sentinel.
	lease, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}
	lease.Release()

	tab := &WorkspaceTab{}
	tab.adoptSessionLease(lease)
	tab.storeSessionLeaseRuntimeKey(key)
	t.Cleanup(func() { tab.releaseSessionLease() })

	removed := []removedSessionRuntime{{tab: tab, sessionPath: path}}
	targets := []topicTrashTarget{{dir: dir, sessionPath: path, key: key}}

	_, err = acquireTopicArchiveOwnership(nil, targets, removed, false)
	// tl1383: the released-lease conversion refusal carries no holder identity
	// (the lease is gone — nobody holds the session), so it classifies as the
	// unidentified-holder leftover form instead of the "another window"
	// sentinel. The defect-A contract below (the lease error flowing through
	// errors.As) is the part that must never regress.
	if !errors.Is(err, errSessionBusyUnidentifiedHolder) {
		t.Fatalf("ownership err = %v, want the unidentified-holder classification", err)
	}
	var leaseErr *agent.SessionLeaseError
	if !errors.As(err, &leaseErr) {
		t.Fatalf("errors.As(*agent.SessionLeaseError) failed on %v — the errors.As chain is severed", err)
	}
	var busy *sessionLeaseHeldBusyError
	if !errors.As(err, &busy) {
		t.Fatalf("errors.As(*sessionLeaseHeldBusyError) failed on %v", err)
	}
}
