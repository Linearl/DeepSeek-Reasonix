package agent

import (
	"fmt"
	"log/slog"
	"strings"

	"reasonix/internal/provider"
)

// tl1409: stale-disk takeover. The field shape behind this file — the on-disk
// transcript holds MORE messages at a HIGHER revision than this session's
// baseline, the two histories share no prefix, and the disk side has gone
// quiet — is the residue of a dead second writer: it advanced the ledger
// (disk revision > local base revision), stopped or was closed, and the live
// session kept growing past it. Ordinary save semantics treat the pair as a
// permanent mutual refusal (diverged forever, plus a recovery fork on every
// escalation), which is exactly the 2026-11-05 desktop.log shape: five
// diverged errors in 90 seconds with "persist turn admission" retrying at a
// fixed cadence. The takeover resolves it instead: persist the LIVE
// transcript over the dead writer's state inside one save-lock window (the
// tl1390 promotion-transaction principle — the intermediate state must not be
// observable), so the ledger advances past the stale revision and no second
// writer residue remains.

// SaveAdoptOverStaleDisk persists this session's live transcript over the
// on-disk state described by conflict, but only while every takeover
// precondition holds:
//
//  1. conflict is a diverged snapshot conflict (stale-prefix conflicts have
//     their own adopt-disk channel — the disk side wins there);
//  2. the disk revision ran AHEAD of this session's baseline (evidence a
//     second writer existed) and the live snapshot has MORE messages than the
//     disk transcript (evidence the live side is the one that kept growing);
//  3. this session holds a live write authority covering the path — the
//     in-register writer of that session file. A session that never bound an
//     authority (legacy CAS callers, test scaffolds) declines, and so does a
//     controller whose authority points elsewhere — this is what keeps a
//     same-process second controller (another tab) from being run over;
//  4. the branch meta carries this file's own identity — a wrong-generation
//     meta (tl1383's patient) is a different disease with its own repair and
//     must not be papered over with a byte overwrite;
//  5. no other runtime currently holds the session lease;
//  6. under the save locks, the on-disk state still matches the conflict
//     fingerprint (revision and message count unchanged since the conflict
//     was raised) — disk moved on means the "dead" writer is not dead and the
//     takeover is refused.
//
// The write itself reuses the ordinary owned-rewrite commit path (CAS ledger
// reservation, listing invalidation, event index, persist baseline), so the
// landed revision is conflict.DiskRevision+1 and every other runtime's next
// save classifies against a coherent ledger. On refusal the diverged conflict
// is returned untouched and nothing is written.
func (s *Session) SaveAdoptOverStaleDisk(path string, conflict *SessionSnapshotConflictError) error {
	if s == nil {
		return fmt.Errorf("nil session")
	}
	if conflict == nil {
		return fmt.Errorf("stale-disk takeover requires a snapshot conflict")
	}
	if conflict.Kind != SessionSnapshotConflictDiverged {
		return fmt.Errorf("stale-disk takeover requires a diverged conflict, got %q", conflict.Kind)
	}
	if conflict.BaseRevision >= conflict.DiskRevision {
		// Same-revision divergence has its own escalation
		// (retrySameRevisionDivergedRewrite); a disk revision BEHIND the local
		// baseline means the disk is simply stale and the next plain save
		// appends over it. Neither is a takeover shape.
		return fmt.Errorf("stale-disk takeover requires disk revision ahead of the local base (base=%d disk=%d)", conflict.BaseRevision, conflict.DiskRevision)
	}
	if conflict.SnapshotMessages <= conflict.ExistingMessages {
		return fmt.Errorf("stale-disk takeover requires the live snapshot ahead of disk (snapshot=%d disk=%d)", conflict.SnapshotMessages, conflict.ExistingMessages)
	}
	canonical := canonicalSessionSavePath(path)
	if canonicalSessionSavePath(conflict.Path) != canonical {
		return fmt.Errorf("stale-disk takeover conflict names a different session (%s, wanted %s)", conflict.Path, canonical)
	}
	if !s.hasValidWriteAuthority(canonical) {
		return fmt.Errorf("stale-disk takeover declined: this session holds no live write authority for the path")
	}
	if reason := foreignBranchMetaReason(canonical); reason != "" {
		return fmt.Errorf("stale-disk takeover declined: %s", reason)
	}
	if SessionLeaseHeldByOtherRuntime(canonical) {
		return fmt.Errorf("stale-disk takeover declined: another runtime holds the session lease")
	}
	if err := s.armStaleDiskTakeover(canonical, conflict); err != nil {
		return err
	}
	defer s.disarmStaleDiskTakeover()
	// The armed intent is consumed inside checkSnapshotWrite when the classifier
	// hits the diverged terminal: the ordinary owned-rewrite commit then lands
	// the live transcript at conflict.DiskRevision+1. Every precondition is
	// re-verified under the save locks before the write.
	if err := s.SaveRewrite(path); err != nil {
		return err
	}
	slog.Info("session: stale-disk takeover committed",
		"path", canonical,
		"base_revision", conflict.BaseRevision,
		"disk_revision", conflict.DiskRevision,
		"disk_messages", conflict.ExistingMessages,
		"snapshot_messages", conflict.SnapshotMessages)
	return nil
}

// foreignBranchMetaReason reports why the branch meta on disk disqualifies a
// stale-disk takeover: a wrong-generation identity is tl1383's patient and
// must be aligned by that reconciliation, not overwritten around. Empty means
// the meta is absent, digest-only, or names this very file.
func foreignBranchMetaReason(canonical string) string {
	meta, ok, err := loadBranchMetaRetry(canonical)
	if err != nil {
		return "branch meta unreadable"
	}
	if !ok {
		return ""
	}
	if id := strings.TrimSpace(meta.ID); id != "" && id != BranchID(canonical) {
		return fmt.Sprintf("branch meta carries a foreign identity %q", id)
	}
	return ""
}

// armStaleDiskTakeover records the one allowed takeover fingerprint. A second
// concurrent arm is refused: takeover is a controller-serialized escalation,
// and overlapping arms could let two saves both claim the dead writer's slot.
func (s *Session) armStaleDiskTakeover(canonical string, conflict *SessionSnapshotConflictError) error {
	s.takeoverMu.Lock()
	defer s.takeoverMu.Unlock()
	if s.takeoverArmed != nil {
		return fmt.Errorf("another stale-disk takeover is already armed")
	}
	armed := *conflict
	armed.Path = canonical
	s.takeoverArmed = &armed
	return nil
}

func (s *Session) disarmStaleDiskTakeover() {
	s.takeoverMu.Lock()
	s.takeoverArmed = nil
	s.takeoverMu.Unlock()
}

// staleDiskTakeoverDecision is the classifier hook: when the save locks are
// held, the on-disk transcript (existing) still matches the armed conflict
// fingerprint, and the live snapshot (next) is the side that kept growing,
// the diverged refusal converts into an owned-rewrite decision based at the
// current disk revision. Anything else declines so the conflict surfaces
// unchanged. Runs under the save locks; re-verifies the writer authority and
// the meta identity so a state change between arming and committing cannot
// push the write through.
func (s *Session) staleDiskTakeoverDecision(path string, existing, next []provider.Message, baseRevision, diskRevision int64) (snapshotWriteDecision, bool) {
	s.takeoverMu.Lock()
	armed := s.takeoverArmed
	s.takeoverMu.Unlock()
	if armed == nil {
		return snapshotWriteDecision{}, false
	}
	if canonicalSessionSavePath(path) != armed.Path {
		return snapshotWriteDecision{}, false
	}
	// Disk-still-stale verification: the on-disk revision and message count
	// must be exactly what the conflict reported. A moved disk means the other
	// writer is alive (or wrote again) — refuse and let the conflict ladder own
	// the save.
	if diskRevision != armed.DiskRevision || len(existing) != armed.ExistingMessages {
		return snapshotWriteDecision{}, false
	}
	// The local baseline must not have moved either: the conflict was raised
	// against this exact base, and an intervening successful save would make
	// the fingerprint stale.
	if baseRevision != armed.BaseRevision {
		return snapshotWriteDecision{}, false
	}
	if len(next) <= len(existing) {
		return snapshotWriteDecision{}, false
	}
	// Same gates as the arming preconditions, re-checked inside the lock
	// window: only the in-register writer of this path may take it over, and a
	// wrong-generation meta keeps its own repair path.
	if !s.hasValidWriteAuthority(armed.Path) {
		return snapshotWriteDecision{}, false
	}
	if reason := foreignBranchMetaReason(armed.Path); reason != "" {
		return snapshotWriteDecision{}, false
	}
	if SessionLeaseHeldByOtherRuntime(armed.Path) {
		return snapshotWriteDecision{}, false
	}
	return snapshotWriteDecision{revision: diskRevision}, true
}
