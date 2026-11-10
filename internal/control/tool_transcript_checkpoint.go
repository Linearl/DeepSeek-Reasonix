package control

import (
	"context"
	"errors"

	"reasonix/internal/agent"
	"reasonix/internal/extension"
	"reasonix/internal/extension/dispatch"
)

// checkpointToolTranscript persists execution evidence before another tool may
// start. Listing projections and other UI sidecars remain on the normal autosave.
func (c *Controller) checkpointToolTranscript() error {
	c.snapshotMu.Lock()
	defer c.snapshotMu.Unlock()
	path := c.SessionPath()
	if path == "" || c.executor == nil {
		return nil
	}
	session := c.executor.Session()
	if session == nil || !session.HasContent() {
		return nil
	}
	_, err := c.extensionSessionStrategy(context.Background(), extension.PointSessionSave, dispatch.PhaseSave, path)
	if err != nil {
		return err
	}
	err, _ = persistSessionSnapshotMode(session, path, false, true)
	if err == nil {
		return nil
	}
	// tl1459 leftover B (保存主路径纵深②): a rev-lag diverged conflict here
	// used to flow straight into failTurnEventLedger and cancel the running
	// turn — the 2026-11-06 early stop's actual exit. The shape (a second
	// writer advanced the ledger and died, the live transcript kept growing)
	// is what recoverSnapshotConflict already resolves at the turn boundary
	// through agent.SaveAdoptOverStaleDisk. persistSessionSnapshotMode owns
	// the two other mid-turn-safe escalations (pending rewrite, same-revision
	// rewrite); this completes the trio. The takeover is mid-turn-safe by the
	// tl1491 rule: it keeps the same session object and path — no swap, no
	// rebind — so the running turn's transcript and recorder stay intact. The
	// conflict itself remains the last resort and stays cause-carrying
	// (goalCancelDurability renders it) whenever the takeover declines.
	if c.takeoverCheckpointRevLag(path, err) {
		return nil
	}
	return err
}

// takeoverCheckpointRevLag escalates a checkpoint-conflict rev-lag shape to
// the same takeover the turn-boundary ladder uses (trace + recovery notice
// included, so the adoption stays visible). It declines for every other
// conflict shape: same-revision divergence was already retried inside
// persistSessionSnapshotMode, stale-prefix has its disk-side-wins ladder, and
// shapes SaveAdoptOverStaleDisk's preconditions reject (foreign live writer,
// moved disk, unbound authority) belong to the turn-boundary recovery.
// Contract note: callers hold c.snapshotMu, and takeoverDivergedRevLag must
// not take it again — the same lock discipline recoverSnapshotConflict runs
// under.
func (c *Controller) takeoverCheckpointRevLag(path string, saveErr error) bool {
	var conflict *agent.SessionSnapshotConflictError
	if !errors.As(saveErr, &conflict) || conflict == nil ||
		conflict.Kind != agent.SessionSnapshotConflictDiverged ||
		conflict.DiskRevision <= conflict.BaseRevision {
		return false
	}
	trace := c.beginForkTrace(path, "checkpoint", saveErr)
	if !c.takeoverDivergedRevLag(trace, path, saveErr, "checkpoint") {
		// No decision line here: a declined takeover leaves the conflict to
		// persistSessionSnapshotMode's escalation trace (forkReasonCheckpoint),
		// which is the one decision line this conflict owns.
		return false
	}
	c.finishForkTrace(trace, conflictTookOver, nil)
	return true
}
