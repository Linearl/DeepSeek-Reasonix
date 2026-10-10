package agent

import (
	"crypto/sha256"
	"errors"
	"log/slog"

	"reasonix/internal/provider"
)

// digestIsZero reports an unset persisted baseline digest: a baseline that
// carries no digest cannot take part in the digest half of the ownership
// proof, so the caller falls back to the revision equality alone.
func digestIsZero(digest [sha256.Size]byte) bool {
	return digest == [sha256.Size]byte{}
}

// Task 339 (upstream #10970 integration): an event log already past the replay
// caps used to freeze the session on the write path too. Save classifies the
// write shape by replaying the on-disk log first, so the replay refusal
// propagated out of classification and every later save failed - the same
// "over budget means no way out" deadlock upstream fixed by giving an
// over-budget session a way out on its next save. The fork keeps the log away
// from the caps before they are reached (task 193 records-aware compact, task
// 104 size-adaptive budget, task 373 image de-referencing); this rescue is the
// second line: when the log is already over, the saving runtime holds the full
// transcript in memory, and the disk state is provably this runtime's own last
// write, fold the log from that snapshot so the save - and the session -
// continues.
//
// The proof of "disk is my own last write" is the content ledger: its revision
// still equals this runtime's persisted baseline, which only holds when no
// other writer has committed since. A log that may hold newer turns from
// another runtime keeps today's refusal - folding from memory there could
// silently discard those turns, which is exactly the honest boundary task 193
// drew for the read end.

// rescueReplayLimitedEventLog folds an over-budget event log from the
// in-memory snapshot and reports whether the log was rewritten (the caller may
// then re-classify). It declines - leaving the log and the error untouched -
// unless every condition holds:
//   - the classify error is a replay-limit refusal, not damage or a conflict;
//   - the log is a native schema-1 log (a schema-2 DAG log folds per head,
//     which a single whole-transcript replace cannot express);
//   - the ledger still records this runtime's baseline revision, and its
//     content digest still describes the baseline content when both are known.
//
// Task 715 (714 复核 B2): the proof and the overwrite run under one branch-meta
// lock, closing the check-vs-write TOCTOU window a concurrent writer could
// otherwise slip a commit into.
func (s *Session) rescueReplayLimitedEventLog(path string, msgs []provider.Message, digest [sha256.Size]byte, cause error) bool {
	var limitErr *SessionReplayLimitError
	if !errors.As(cause, &limitErr) {
		return false
	}
	// Defense in depth: today's save flow routes every non-schema-1 log to the
	// DAG writer before classification, so a limit refusal here is already a
	// schema-1 refusal. Should a DAG log ever reach this point, a whole-
	// transcript replace would still be the wrong fold - its other heads would
	// not survive it - so probe rather than trust the routing.
	if probe, err := probeSessionEventLog(path); err != nil || !probe.native || probe.dag || probe.futureSchema {
		return false
	}
	base := s.persistState(path)
	// Task 715 (714 复核 B2): the ledger check and the whole-file overwrite below
	// form one critical section under the same lock recordSessionContentRevision
	// holds, so a concurrent writer's commit either lands before this re-read
	// (the revision check then honestly declines) or after a write of proven-own
	// content — never inside the decision.
	unlock, err := LockSessionMetaPath(path)
	if err != nil {
		slog.Warn("session: replay-limited event log left in place (ledger lock unavailable)",
			"path", path, "resource", limitErr.Resource, "value", limitErr.Value, "limit", limitErr.Limit, "err", err)
		return false
	}
	defer unlock()
	diskRevision, diskDigest, err := sessionContentRevision(path)
	if err != nil || !base.ok || !base.revisionKnown || diskRevision != base.revision {
		slog.Warn("session: replay-limited event log left in place (disk may hold newer turns)",
			"path", path, "resource", limitErr.Resource, "value", limitErr.Value, "limit", limitErr.Limit)
		return false
	}
	if diskDigest != "" && !digestIsZero(base.digest) && diskDigest != digestString(base.digest) {
		slog.Warn("session: replay-limited event log left in place (ledger describes other content)",
			"path", path, "resource", limitErr.Resource, "value", limitErr.Value, "limit", limitErr.Limit)
		return false
	}
	if err := compactSessionEventLog(path, msgs, digest, diskRevision, "compact-replay-limit"); err != nil {
		slog.Warn("session: replay-limit rescue fold failed",
			"path", path, "resource", limitErr.Resource, "value", limitErr.Value, "limit", limitErr.Limit, "err", err)
		return false
	}
	slog.Warn("session: folded event log past the replay caps from the in-memory snapshot",
		"path", path, "resource", limitErr.Resource, "value", limitErr.Value, "limit", limitErr.Limit,
		"messages", len(msgs), "diskRevision", diskRevision)
	return true
}
