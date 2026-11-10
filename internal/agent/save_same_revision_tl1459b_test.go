package agent

import (
	"context"
	"path/filepath"
	"testing"

	"reasonix/internal/provider"
)

// tl1459 leftover A pins (保存主路径纵深①): same-revision memory/disk
// consistency. The field drift behind the 2026-11-06 early stop — the
// writer's ledger digest naming content the disk bytes do not replay to, at
// an unchanged revision — had its mechanism closed by the 1459F2 digest-input
// normalization (encoding round-trip instability on invalid UTF-8). These
// pins fence the invariant at the save-path level, one floor above the digest
// unit pins: a successful save must leave the ledger describing exactly the
// disk bytes and the writer's own view, at one revision; an unchanged re-save
// must not move that revision (write-path idempotency); and the drift form
// itself, when it exists on a live writer, reconciles through the ordinary
// checkpoint save without a conflict, a fork, or a turn kill.

// TestSaveLeavesLedgerDescribingDiskAtCommittedRevision is the idempotency
// invariant across the three save shapes a live turn uses: appends, tool
// checkpoints (deferred projections), and rewrites. After each successful
// save the ledger digest names the disk replay and the snapshot at the SAME
// committed revision; a follow-up unchanged save of the same mode keeps that
// revision instead of bumping it.
func TestSaveLeavesLedgerDescribingDiskAtCommittedRevision(t *testing.T) {
	t.Run("append snapshot", func(t *testing.T) {
		s, path := newSingleSessionAt(t, provider.Message{Role: provider.RoleUser, Content: "m1"})
		s.Add(provider.Message{Role: provider.RoleAssistant, Content: "m2"})
		if err := s.SaveSnapshot(path); err != nil {
			t.Fatalf("append save: %v", err)
		}
		assertLedgerDescribesDiskAndView(t, s, path)
		before := ledgerOf(t, path)
		if err := s.SaveSnapshot(path); err != nil {
			t.Fatalf("unchanged re-save: %v", err)
		}
		if after := ledgerOf(t, path); after.revision != before.revision {
			t.Fatalf("unchanged re-save bumped the revision %d → %d (idempotency)", before.revision, after.revision)
		}
	})
	t.Run("tool checkpoint", func(t *testing.T) {
		s, path := newSingleSessionAt(t, provider.Message{Role: provider.RoleUser, Content: "m1"})
		s.Add(provider.Message{Role: provider.RoleAssistant, Content: "working"})
		if err := s.SaveToolCheckpoint(path, false); err != nil {
			t.Fatalf("checkpoint save: %v", err)
		}
		assertLedgerDescribesDiskAndView(t, s, path)
		before := ledgerOf(t, path)
		if err := s.SaveToolCheckpoint(path, false); err != nil {
			t.Fatalf("unchanged checkpoint re-save: %v", err)
		}
		if after := ledgerOf(t, path); after.revision != before.revision {
			t.Fatalf("unchanged checkpoint re-save bumped the revision %d → %d (idempotency)", before.revision, after.revision)
		}
	})
	t.Run("rewrite", func(t *testing.T) {
		s, path := newSingleSessionAt(t, provider.Message{Role: provider.RoleUser, Content: "m1"})
		if err := s.SaveSnapshot(path); err != nil {
			t.Fatalf("initial save: %v", err)
		}
		if err := s.SaveRewrite(path); err != nil {
			t.Fatalf("rewrite save: %v", err)
		}
		assertLedgerDescribesDiskAndView(t, s, path)
		before := ledgerOf(t, path)
		if err := s.SaveRewrite(path); err != nil {
			t.Fatalf("unchanged rewrite re-save: %v", err)
		}
		if after := ledgerOf(t, path); after.revision != before.revision {
			t.Fatalf("unchanged rewrite re-save bumped the revision %d → %d (idempotency)", before.revision, after.revision)
		}
	})
	t.Run("invalid utf-8 content stays representation-stable", func(t *testing.T) {
		// The field drift's mechanism source (tl1459F2 裁决): invalid UTF-8
		// bytes in memory marshal differently than their post-round-trip
		// U+FFFD form. The digest normalization closed the fingerprint split;
		// this pin proves it at the save-path level: save → reload must agree
		// with the ledger at one revision even for such content.
		s, path := newSingleSessionAt(t, provider.Message{
			Role: provider.RoleUser, Content: "raw \xff\xfe bytes and \ufffd literal",
		})
		if err := s.SaveSnapshot(path); err != nil {
			t.Fatalf("save with invalid utf-8 content: %v", err)
		}
		assertLedgerDescribesDiskAndView(t, s, path)
	})
}

// TestSameRevisionDriftReconcilesThroughToolCheckpoint reproduces the
// incident's same-revision drift on a live writer (the baseline and the
// ledger digest name content the disk bytes do not replay to, revision
// unchanged) and pins the reconciliation semantics through the exact call the
// turn's tool checkpoint makes: the save succeeds via the authority-bound
// same-revision reshape, and afterwards one revision names all three views —
// no conflict for failTurnEventLedger to feed on, no recovery fork.
func TestSameRevisionDriftReconcilesThroughToolCheckpoint(t *testing.T) {
	s, path, release := liveWriterMidTurnSession(t)
	defer release()
	induceFieldDrift(t, s, path)
	before := ledgerOf(t, path)

	if err := s.SaveToolCheckpoint(path, false); err != nil {
		t.Fatalf("tool checkpoint over the drifted same-revision baseline = %v, want the reconciliation save", err)
	}

	// 归零断言: one revision, three views. The committed revision advanced by
	// exactly one; the ledger digest, the disk replay digest, and the writer's
	// own snapshot digest agree; the next checkpoint is an idempotent no-op.
	msgs, _, _ := s.snapshotWithVersion()
	digest, err := digestSessionMessages(msgs)
	if err != nil {
		t.Fatal(err)
	}
	after := ledgerOf(t, path)
	if after.revision != before.revision+1 {
		t.Fatalf("reconciliation save revision = %d, want %d", after.revision, before.revision+1)
	}
	if after.digest != digestString(digest) {
		t.Fatalf("ledger digest after reconciliation does not describe the writer's view (%q vs recomputed)", after.digest)
	}
	_, replay, _, err := loadSessionDisplayMessagesContextUnlocked(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if replay.DigestHex != after.digest {
		t.Fatalf("disk replay digest %q != ledger digest %q after reconciliation", replay.DigestHex, after.digest)
	}
	if got := recoveryJSONL(filepath.Dir(path)); len(got) != 0 {
		t.Fatalf("unexpected recovery branches: %v", got)
	}
	if err := s.SaveToolCheckpoint(path, false); err != nil {
		t.Fatalf("follow-up checkpoint after reconciliation: %v", err)
	}
	if final := ledgerOf(t, path); final.revision != after.revision {
		t.Fatalf("follow-up checkpoint bumped the revision %d → %d (idempotency)", after.revision, final.revision)
	}
}

// newSingleSessionAt seeds a session holding (system + msg), saved once, so
// the caller starts from a save-verified baseline.
func newSingleSessionAt(t *testing.T, first provider.Message) (*Session, string) {
	t.Helper()
	dir := schemaOneTempDir(t)
	path := filepath.Join(dir, "same-revision.jsonl")
	s := NewSession("sys")
	s.Add(first)
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	return s, path
}

// assertLedgerDescribesDiskAndView is the invariant one-liner: the ledger
// digest describes the disk replay AND the writer's snapshot at the same
// revision.
func assertLedgerDescribesDiskAndView(t *testing.T, s *Session, path string) {
	t.Helper()
	ledger := ledgerOf(t, path)
	_, replay, _, err := loadSessionDisplayMessagesContextUnlocked(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if replay.DigestHex != ledger.digest {
		t.Fatalf("ledger digest %q does not describe the disk replay %q at revision %d", ledger.digest, replay.DigestHex, ledger.revision)
	}
	digest, err := digestSessionMessages(s.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if ledger.digest != digestString(digest) {
		t.Fatalf("ledger digest %q does not describe the writer's view at revision %d", ledger.digest, ledger.revision)
	}
}
