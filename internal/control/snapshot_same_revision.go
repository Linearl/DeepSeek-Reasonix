package control

import (
	"errors"
	"log/slog"

	"reasonix/internal/agent"
	"reasonix/internal/event"
)

// persistSessionSnapshot writes path with snapshot semantics, escalating to an
// owned rewrite when mid-turn reshape or same-revision divergence is detected.
// Authority-bound rewrites stay on the canonical path; missing/stale authority
// is returned as a typed error so callers rebind instead of forking recovery.
func persistSessionSnapshot(s *agent.Session, path string, forceRewrite bool) (error, bool) {
	return persistSessionSnapshotMode(s, path, forceRewrite, false)
}

func persistSessionSnapshotMode(s *agent.Session, path string, forceRewrite, checkpoint bool) (error, bool) {
	if s == nil {
		return nil, forceRewrite
	}
	forceRewrite = forceRewrite || s.NeedsRewriteSave()
	var err error
	if checkpoint {
		err = s.SaveToolCheckpoint(path, forceRewrite)
	} else if forceRewrite {
		return s.SaveRewrite(path), true
	} else {
		err = s.SaveSnapshot(path)
	}
	if authoritySaveError(err) {
		return err, forceRewrite
	}
	if !errors.Is(err, agent.ErrSessionSnapshotConflict) {
		return err, forceRewrite
	}
	// Auto-compaction may rewrite between the decision and the write.
	if s.NeedsRewriteSave() {
		return s.SaveRewrite(path), true
	}
	// Same-revision diverged: prefer rewrite over a recovery fork when this
	// session still holds a live write authority for path.
	if err2, ok := retrySameRevisionDivergedRewrite(s, path, err); ok {
		return err2, true
	}
	return err, false
}

func retrySameRevisionDivergedRewrite(s *agent.Session, path string, err error) (error, bool) {
	kind, ok := agent.SnapshotConflictKind(err)
	if !ok || kind != agent.SessionSnapshotConflictDiverged {
		return err, false
	}
	var conflict *agent.SessionSnapshotConflictError
	if !errors.As(err, &conflict) || conflict == nil || conflict.BaseRevision != conflict.DiskRevision {
		return err, false
	}
	// SaveRewrite itself requires digest ownership or a live authority; a
	// process lease alone cannot claim the current bytes.
	return s.SaveRewrite(path), true
}

// takeoverDivergedRevLag is the tl1409 escalation inside
// recoverSnapshotConflict: it covers the rev axis the existing ladder leaves
// unadjudicated. tl1383 aligns a wrong-generation meta identity and
// retrySameRevisionDivergedRewrite owns base==disk divergence, but the shape
// "disk revision ahead of the local base + live transcript ahead of disk" —
// a second writer that advanced the ledger and then died — could only end in
// a recovery fork or a disk adoption that discards the live side. The
// takeover persists the live transcript over the dead writer's state; all
// preconditions (diverged shape, rev order, message counts, no foreign
// lease) and the under-the-locks disk-still-stale re-verification live in
// agent.SaveAdoptOverStaleDisk, and a declined attempt returns false so the
// pre-existing ladder owns the conflict unchanged.
//
// Lock contract: every recoverSnapshotConflict caller (snapshotWithDurability,
// the post-cancel flush) already holds c.snapshotMu, and that mutex is not
// reentrant — this helper must NOT take it again (the first version did and
// self-deadlocked TestRepeatedConflictFingerprintReconcilesWrongGenerationMeta
// for 27 minutes). The admission-side twin healDivergedSnapshotConflict runs
// outside that lock chain and therefore takes it itself.
func (c *Controller) takeoverDivergedRevLag(path string, saveErr error, mode string) bool {
	if c == nil || c.executor == nil {
		return false
	}
	var conflict *agent.SessionSnapshotConflictError
	if !errors.As(saveErr, &conflict) || conflict == nil || conflict.Kind != agent.SessionSnapshotConflictDiverged {
		return false
	}
	session := c.executor.Session()
	if session == nil {
		return false
	}
	if err := session.SaveAdoptOverStaleDisk(path, conflict); err != nil {
		slog.Warn("controller: snapshot conflict; rev-lag takeover declined", "err", err, "mode", mode,
			"base_revision", conflict.BaseRevision, "disk_revision", conflict.DiskRevision)
		return false
	}
	appendSnapshotConflictDiagnostic(path, mode, "diverged_takeover_committed", saveErr, "", true)
	slog.Warn("controller: snapshot conflict; live transcript took over the dead disk writer's state", "mode", mode,
		"base_revision", conflict.BaseRevision, "disk_revision", conflict.DiskRevision,
		"disk_messages", conflict.ExistingMessages, "snapshot_messages", conflict.SnapshotMessages)
	c.sink.Emit(sessionRecoveryNotice(event.NoticeCodeSessionRecoveryAdopted,
		"the session file held a stale copy from a closed writer; the live conversation took over the file and your latest turns are preserved"))
	return true
}

// persistSessionSnapshotIfPathFree is the non-blocking form of
// persistSessionSnapshot for foreground latency-sensitive paths (desktop tab
// switching). It attempts only a plain snapshot-mode save through the
// try-lock form; anything that would escalate — forced rewrite, mid-turn
// reshape, conflict retry — is declined as attempted=false so the caller's
// degraded path can run the full-semantics save later. This keeps the
// non-blocking write strictly append-shaped and never deepens a rewrite out
// of a UI path.
func persistSessionSnapshotIfPathFree(s *agent.Session, path string, forceRewrite bool) (bool, error) {
	if s == nil {
		return true, nil
	}
	if forceRewrite || s.NeedsRewriteSave() {
		return false, nil
	}
	return s.SaveSnapshotIfPathFree(path)
}
