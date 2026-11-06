package agent

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// tl1459 stage 4: the in-process second writer behind the 2026-11-06 early
// stop was the session-catalog listing repair. Field readings (desktop.log
// fork-decision lines + the archived session at the 16:33 fork): the live
// writer's baseline digest (base_digest, recorded as the ledger digest by its
// own save) differed from the disk replay digest at the SAME revision; the
// repair saw "ledger stale", rewrote it (revision+1, its replay digest,
// writer=this process) without a save — so peer_trigger=not_traced — and the
// writer's next save, a rewrite whose legality rested on "write authority +
// unchanged revision", conflicted as diverged with disk_writer=self_process:
// four "dead writer" takeovers and one fork in 22 minutes.
//
// The digest drift itself (in-memory vs persisted bytes at one revision) is a
// separate save-path issue reported for a ruling; these pins reproduce the
// field invariants directly and prove the repair no longer turns them into a
// conflict while a writer is live.

func liveWriterMidTurnSession(t *testing.T) (*Session, string, func()) {
	t.Helper()
	dir := schemaOneTempDir(t)
	path := filepath.Join(dir, "session.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "edit the constitution"})
	s.Add(provider.Message{
		Role: provider.RoleAssistant, Content: "editing",
		ToolCalls: []provider.ToolCall{{ID: "call_1", Name: "edit_file", Arguments: `{"path":"CONSTITUTION.md"}`}},
	})
	// The tool checkpoint: the call is persisted while the tool still runs.
	if err := s.SaveToolCheckpoint(path, false); err != nil {
		t.Fatalf("mid-turn checkpoint: %v", err)
	}
	lease, err := TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}
	auth, err := lease.IssueWriteAuthority(NextSessionWriteGeneration())
	if err != nil {
		lease.Release()
		t.Fatalf("IssueWriteAuthority: %v", err)
	}
	s.BindWriteAuthority(auth)
	return s, path, lease.Release
}

// induceFieldDrift puts the session into the measured field state: the
// writer's baseline digest — and the ledger digest its save recorded — name
// content the disk bytes do not replay to, at an unchanged revision.
func induceFieldDrift(t *testing.T, s *Session, path string) [32]byte {
	t.Helper()
	drifted, _, rewriteVersion := s.snapshotWithVersion()
	drifted[1].RawContent = "edit the constitution (in-memory only)"
	digest, err := digestSessionMessages(drifted)
	if err != nil {
		t.Fatal(err)
	}
	state := s.persistState(path)
	s.setPersistedBaseline(path, digest, state.version, state.revision, true, true, rewriteVersion, nil)
	if err := UpdateBranchMeta(path, false, func(meta *BranchMeta) error {
		meta.ContentDigest = digestString(digest)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, replay, _, err := loadSessionDisplayMessagesContextUnlocked(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if replay.DigestHex == digestString(digest) {
		t.Fatal("setup: drift not induced (replay digest equals the writer's digest)")
	}
	return digest
}

// continueLiveTurn is what the live writer does next in the incident: the
// edit tool reports its diff preview (a rewrite of the checkpointed assistant
// message), the result lands, and the next save runs as a rewrite.
func continueLiveTurn(t *testing.T, s *Session, path string) error {
	t.Helper()
	if !s.UpdateToolCallPreview(provider.ToolCall{ID: "call_1", Diff: "@@ -1 +1 @@\n-a\n+b\n", Added: 1, Removed: 1}) {
		t.Fatal("preview update failed")
	}
	s.Add(provider.Message{Role: provider.RoleTool, ToolCallID: "call_1", Name: "edit_file", Content: "edited"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
	if !s.NeedsRewriteSave() {
		t.Fatal("setup: the diff preview must leave a rewrite pending (incident: rewrite_pending=true)")
	}
	return s.SaveRewrite(path)
}

func ledgerOf(t *testing.T, path string) sessionLedger {
	t.Helper()
	ledger, err := sessionContentLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	return ledger
}

// TestDriftAloneDoesNotBreakLiveWriter: without the repair's bump the drifted
// writer keeps saving — the authority + same-revision ownership carries it.
func TestDriftAloneDoesNotBreakLiveWriter(t *testing.T) {
	s, path, release := liveWriterMidTurnSession(t)
	defer release()
	induceFieldDrift(t, s, path)
	if err := continueLiveTurn(t, s, path); err != nil {
		t.Fatalf("drifted live writer save without a repair: %v", err)
	}
}

// TestListingRepairLedgerBumpBreaksLiveWriter reproduces the mechanism: the
// exact ledger write commitSessionListingReplay performs when it judges the
// ledger stale, applied between two saves of a live writer, turns its next
// save into the incident's conflict.
func TestListingRepairLedgerBumpBreaksLiveWriter(t *testing.T) {
	s, path, release := liveWriterMidTurnSession(t)
	defer release()
	induceFieldDrift(t, s, path)
	_, replay, _, err := loadSessionDisplayMessagesContextUnlocked(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateBranchMeta(path, false, func(meta *BranchMeta) error {
		meta.Revision++
		meta.ContentDigest = replay.DigestHex
		meta.WriterID = SessionWriterID()
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	err = continueLiveTurn(t, s, path)
	var conflict *SessionSnapshotConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("live writer save after the repair's ledger bump = %v, want a snapshot conflict (the incident)", err)
	}
	if writer, _ := LedgerWriterOrigin(conflict.Identity.LedgerWriter); writer != LedgerWriterSelf {
		t.Fatalf("conflict disk writer = %q, want %q (incident: disk_writer=self_process)", writer, LedgerWriterSelf)
	}
	if conflict.Kind != SessionSnapshotConflictDiverged || conflict.DiskRevision != conflict.BaseRevision+1 {
		t.Fatalf("conflict = kind %q base %d disk %d, want diverged with disk=base+1 (incident: 1951/1952 ...)", conflict.Kind, conflict.BaseRevision, conflict.DiskRevision)
	}
}

// TestListingRepairYieldsToLiveInProcessWriter is the fix: with a live lease
// holder the repair leaves the ledger alone (busy → the catalog retries once
// the session is released), and the writer's next save is not a conflict —
// the same-process writer is not "taken over" as dead.
func TestListingRepairYieldsToLiveInProcessWriter(t *testing.T) {
	s, path, release := liveWriterMidTurnSession(t)
	defer release()
	induceFieldDrift(t, s, path)

	before := ledgerOf(t, path)
	result, err := RepairSessionListingProjection(context.Background(), path)
	if !errors.Is(err, ErrSessionListingRepairBusy) {
		t.Fatalf("repair on a live writer's drifted session = %+v err=%v, want ErrSessionListingRepairBusy", result, err)
	}
	if after := ledgerOf(t, path); after != before {
		t.Fatalf("repair touched the live writer's ledger: before %+v after %+v", before, after)
	}
	if err := continueLiveTurn(t, s, path); err != nil {
		t.Fatalf("live writer save after the yielded repair: %v (must not conflict)", err)
	}
	if got := recoveryJSONL(filepath.Dir(path)); len(got) != 0 {
		t.Fatalf("unexpected recovery branches: %v", got)
	}
}

// TestListingRepairStillHealsReleasedSession keeps the repair's purpose: once
// no writer holds the session (closed, or its process died and the OS lock
// freed), the stale ledger is repaired exactly as before.
func TestListingRepairStillHealsReleasedSession(t *testing.T) {
	s, path, release := liveWriterMidTurnSession(t)
	induceFieldDrift(t, s, path)
	release()

	before := ledgerOf(t, path)
	result, err := RepairSessionListingProjection(context.Background(), path)
	if err != nil {
		t.Fatalf("repair on a released session: %v", err)
	}
	after := ledgerOf(t, path)
	if !result.LedgerRepaired || after.revision != before.revision+1 || after.writer != SessionWriterID() {
		t.Fatalf("released drifted session: result %+v ledger %+v → %+v, want a ledger repair", result, before, after)
	}
}

// TestListingRepairNotBlockedByDeadForeignHolder: a crashed holder leaves its
// lease record behind but not its OS lock. That session has no live writer, so
// the repair heals it — a dead writer never pins a stale ledger forever.
func TestListingRepairNotBlockedByDeadForeignHolder(t *testing.T) {
	s, path, release := liveWriterMidTurnSession(t)
	induceFieldDrift(t, s, path)
	release()
	if err := SaveSessionLeaseInfo(path, SessionLeaseInfo{
		SessionPath: path, WriterID: "otherhost-999999-deadbeefdeadbeef", PID: 999999,
		AcquiredAt: time.Now().UTC().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	result, err := RepairSessionListingProjection(context.Background(), path)
	if err != nil || !result.LedgerRepaired {
		t.Fatalf("repair with a dead foreign lease record = %+v err=%v, want a ledger repair", result, err)
	}
}

// TestListingRepairCommitGuardHoldsBehindTheEarlyCheck pins the defense in
// depth: the entry parks a live writer's session before the replay decode,
// and the locked commit refuses the ledger write on its own too (a lease
// acquired between the two must not reopen the incident).
func TestListingRepairCommitGuardHoldsBehindTheEarlyCheck(t *testing.T) {
	s, path, release := liveWriterMidTurnSession(t)
	defer release()
	induceFieldDrift(t, s, path)
	meta, _, err := LoadBranchMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	before := ledgerOf(t, path)
	if _, err := repairSessionListingFromReplay(context.Background(), path, meta, SessionListingRepairResult{}); !errors.Is(err, ErrSessionListingRepairBusy) {
		t.Fatalf("commit-side guard err = %v, want ErrSessionListingRepairBusy", err)
	}
	if after := ledgerOf(t, path); after != before {
		t.Fatalf("commit-side guard let the ledger move: %+v → %+v", before, after)
	}
}
