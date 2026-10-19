package agent

import (
	"crypto/sha256"
	"log/slog"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// SaveToolCheckpoint commits the canonical transcript, CAS revision and event
// index before execution continues. Listing and display indexes are derived;
// rebuilding them for every tool receipt belongs to the normal snapshot path.
func (s *Session) SaveToolCheckpoint(path string, rewrite bool) error {
	mode := sessionSaveToolCheckpoint
	if rewrite {
		// Rewrites invalidate old indexed prefixes and must publish that change.
		mode = sessionSaveRewrite
	}
	return s.saveObserved(path, mode)
}

func refreshCheckpointDisplayIndex(path string, msgs []provider.Message, digest [sha256.Size]byte, revision int64, appendFrom int, deferred bool) error {
	if deferred {
		return nil
	}
	return refreshSessionDisplayIndex(path, msgs, digest, revision, appendFrom)
}

func (s *Session) markCheckpointPersisted(path string, digest [sha256.Size]byte, version uint64, revision int64, rewriteVersion int, msgs []provider.Message, deferred bool) {
	if !deferred {
		s.markPersistedWithListing(path, digest, version, revision, rewriteVersion, msgs)
		return
	}
	s.setPersistedBaseline(path, digest, version, revision, true, true, rewriteVersion, msgs)
	s.mu.Lock()
	s.persisted.projectionPending = true
	s.mu.Unlock()
}

// Checkpoint modes share ordinary CAS and rewrite rules, but defer projections.
func (mode sessionSaveMode) defersProjection() bool {
	return mode == sessionSaveToolCheckpoint
}

func (mode sessionSaveMode) allowsOwnedRewrite() bool {
	return mode == sessionSaveRewrite || mode == sessionSaveRewriteCompact
}

func (s *Session) refreshPendingCheckpointProjection(path string, msgs []provider.Message, digest [sha256.Size]byte, revision int64, deferred bool) {
	state := s.persistState(path)
	if deferred || (!state.projectionPending && state.saveVerified) {
		return
	}
	if err := refreshSessionDisplayIndex(path, msgs, digest, revision, -1); err != nil {
		// Match normal saves: a derived index cannot invalidate a durable receipt.
		slog.Warn("session: keeping save after display index write failure", "path", path, "err", err)
	}
}

// flushDeferredDerivedFiles republishes the derived files a tool checkpoint or
// an unlocked shutdown append deliberately deferred, using the digest and
// revision that save already committed. It runs on the snapshot no-op path,
// where the transcript is provably current against disk, so all that is left is
// rebuilding the listing sidecar and the display cache from the in-memory view —
// no serialize or digest pass of its own. Gating the no-op on projectionPending
// instead would push every defensive switch/close snapshot on a large session
// through that full path just to republish a derived index.
//
// It reports whether the no-op may stand. A schema-2 (DAG) log also derives its
// head index and meta mirror from the DAG save context, so that case declines
// and lets the full path publish what it owns. A failed derived write declines
// too: projectionPending stays set so a later save retries, matching the full
// save path, because a derived file must never invalidate the transcript
// receipt.
func (s *Session) flushDeferredDerivedFiles(path string) bool {
	state := s.persistState(path)
	if !state.ok {
		return true
	}
	if !state.projectionPending {
		// Cross-process deferred projection (restart shape): a tool checkpoint
		// or shutdown append in ANOTHER process deferred the derived files and
		// exited, so this freshly loaded session never arms projectionPending —
		// yet the committed transcript runs ahead of the on-disk display index
		// and nothing else would republish it ("restart left stale projection").
		// Compare the index's own revision/digest against the save baseline:
		// an exact match keeps the no-op fast path at zero rebuild cost
		// (#10056 switch/close snapshots); a lag — or a missing/undecodable
		// index — declines so the full save republishes.
		if indexPath := store.SessionDisplayIndex(path); indexPath != "" {
			idx, err := LoadSessionDisplayIndex(indexPath)
			if err != nil || !idx.RevisionKnown || idx.Revision != state.revision || idx.ContentDigest != digestString(state.digest) {
				return false
			}
		}
		return true
	}
	probe, err := probeLogForSave(path)
	if err != nil || s.dagSaveRoute(path, probe) != dagRouteSchemaOne {
		return false
	}
	msgs, version, rewriteVersion := s.snapshotWithVersion()
	if version != state.version {
		// The transcript moved between the no-op decision and here: the
		// ordinary save path owns this generation and will republish.
		return false
	}
	if err := refreshSessionDisplayIndex(path, msgs, state.digest, state.revision, -1); err != nil {
		slog.Warn("session: keeping save after display index write failure", "path", path, "err", err)
		return false
	}
	// Publishes the listing sidecar and clears projectionPending against the
	// digest this baseline already describes.
	s.markPersistedWithListing(path, state.digest, version, state.revision, rewriteVersion, msgs)
	return true
}

func (mode sessionSaveMode) eventReason() string {
	switch mode {
	case sessionSaveSnapshot, sessionSaveToolCheckpoint:
		return "snapshot"
	case sessionSaveRewrite:
		return "rewrite"
	case sessionSaveRewriteCompact:
		return "rewrite-compact"
	default:
		return "save"
	}
}
