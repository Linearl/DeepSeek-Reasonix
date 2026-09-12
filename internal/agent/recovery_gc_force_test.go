package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTrashRecoveryBranchForcedWithoutParentMeta locks in the forced-sweep
// contract: a copy whose meta cannot satisfy the parent guard (no parent id,
// no recovered flag - the damaged/exotic case the preview can still show) is
// archived by a forced sweep anyway, because the user named the winner.
// Background GC paths keep requiring the full guard; only force skips it.
func TestTrashRecoveryBranchForcedWithoutParentMeta(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "20260912-000000.000000000-model-main.jsonl")
	copy := filepath.Join(dir, "20260912-000000.000000000-model-main-recovery-fedcba9876543210.jsonl")

	body := strings.Repeat("{\"kind\":\"x\"}\n", 5)
	for _, p := range []string{main, copy} {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Deliberately NO meta sidecar: the parent guard's preconditions
	// (recovered flag, digest, resolvable parent id) cannot be satisfied.

	if err := TrashRecoveryBranchForced(copy, dir); err != nil {
		t.Fatalf("forced sweep should archive a copy without parent meta, got: %v", err)
	}

	if _, err := os.Stat(copy); !os.IsNotExist(err) {
		t.Fatalf("copy still sits in the session directory: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, ".trash"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("forced sweep did not land in the recoverable trash: %v", err)
	}
}

// The background-GC variant must keep refusing the same copy: without meta and
// without coverage there is no proof the copy is redundant, and no user has
// named a winner.
func TestTrashReclaimableRecoveryBranchStillRequiresMeta(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "20260912-000000.000000000-model-main.jsonl")
	copy := filepath.Join(dir, "20260912-000000.000000000-model-main-recovery-0123456789abcdef.jsonl")
	body := strings.Repeat("{\"kind\":\"x\"}\n", 5)
	for _, p := range []string{main, copy} {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := TrashReclaimableRecoveryBranch(copy, dir); err == nil {
		t.Fatal("background GC must still refuse a copy without parent meta")
	}
}
