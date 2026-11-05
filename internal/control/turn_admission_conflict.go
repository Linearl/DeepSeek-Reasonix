package control

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
)

// tl1409: the terminal retry channel for diverged turn admissions.
//
// Field shape (2026-11-05 desktop.log, session *-collab.jsonl): a second
// writer on disk advanced the transcript ledger then died, the live session
// kept growing past it, and every save classified as a permanent diverged
// conflict. The latched admission error then failed every submission with
// "persist turn admission: diverged on disk ..." — at a constant 15s cadence,
// with no backoff, no escalation and no end: the tl1292 cooldown both fails to
// converge on a persistent error (the takeover is meaningless against a
// conflict) and is starved whenever submissions arrive faster than 30s, since
// every failed admission refreshes lastAdmissionFailure.
//
// The channel below gives the write path what tl1404 gave the archive path:
// a terminal state. Concretely, on every admission attempt that meets a
// latched diverged conflict:
//
//   - inside the current backoff window: refuse without any work;
//   - when the window is due: attempt the stale-disk takeover self-heal
//     (agent.Session.SaveAdoptOverStaleDisk — only fires while the disk facts
//     still match the conflict and no external writer holds the lease);
//     success clears the latch and admission proceeds normally;
//   - after turnAdmissionConflictRetryLimit failed attempts: suspend — one
//     operator notice, one ERROR line, and subsequent admissions refuse
//     without retrying anything until the user reopens the session (a
//     rebind) or the takeover succeeds.
var (
	// turnAdmissionConflictRetryLimit bounds the self-heal attempts before the
	// admission suspends. Package var so tests can pin the terminal boundary.
	turnAdmissionConflictRetryLimit = 3
	// turnAdmissionConflictBackoffBase/Cap shape the exponential backoff:
	// base << (attempt-1), capped. Package vars so tests can run the whole
	// terminal ladder in milliseconds.
	turnAdmissionConflictBackoffBase = 2 * time.Second
	turnAdmissionConflictBackoffCap  = 30 * time.Second
)

// divergedSuspendWarnInterval throttles the suspended-state refusal warning:
// submissions keep arriving (the field cadence was one every 15s) and each one
// must still fail closed, but the log keeps a period-sampled trace instead of
// a per-submission storm.
const divergedSuspendWarnInterval = 10 * time.Minute

func turnAdmissionConflictBackoffFor(retries int) time.Duration {
	if retries <= 1 {
		return turnAdmissionConflictBackoffBase
	}
	d := turnAdmissionConflictBackoffBase
	for range retries - 1 {
		d *= 2
		if d >= turnAdmissionConflictBackoffCap {
			return turnAdmissionConflictBackoffCap
		}
	}
	return d
}

// gateDivergedTurnAdmission is the diverged branch of prepareTurnAdmission.
// It returns nil only when admission may proceed (the self-heal cleared the
// latch); a non-nil result is the rejection body to return to the caller.
func (c *Controller) gateDivergedTurnAdmission(conflict *agent.SessionSnapshotConflictError, admissionErr error, body func(context.Context) error) func(context.Context) error {
	c.turnEvents.mu.Lock()
	suspended := c.turnEvents.conflictSuspended
	retries := c.turnEvents.conflictRetries
	last := c.turnEvents.conflictLastAttempt
	suspendWarned := c.turnEvents.suspendWarned
	c.turnEvents.mu.Unlock()

	if suspended {
		// Terminal state: refuse without ANY retry work — no ledger clear, no
		// save, no takeover. Period-sampled WARN keeps a log trace; the error
		// still reaches the caller every time.
		c.turnEvents.mu.Lock()
		now := time.Now()
		shouldWarn := suspendWarned.IsZero() || time.Since(suspendWarned) >= divergedSuspendWarnInterval
		if shouldWarn {
			c.turnEvents.suspendWarned = now
		}
		c.turnEvents.mu.Unlock()
		if shouldWarn {
			slog.Warn("controller: turn admission suspended after diverged conflicts; reopen the session to retry",
				"err", admissionErr, "path", conflict.Path, "retries", retries)
		}
		return func(context.Context) error {
			return fmt.Errorf("persist turn admission: diverged session conflict unresolved after %d attempts; reopen this session to retry: %w", retries, admissionErr)
		}
	}

	if retries > 0 && time.Since(last) < turnAdmissionConflictBackoffFor(retries) {
		// Inside the backoff window: refuse without touching disk.
		return func(context.Context) error { return fmt.Errorf("persist turn admission: %w", admissionErr) }
	}

	// Due for an attempt: the takeover only writes when the disk facts still
	// match this exact conflict fingerprint, so a stale conflict costs one
	// declined save, not a wrong overwrite.
	if c.healDivergedSnapshotConflict(conflict) {
		c.turnEvents.mu.Lock()
		c.turnEvents.conflictRetries = 0
		c.turnEvents.conflictLastAttempt = time.Time{}
		c.turnEvents.mu.Unlock()
		c.sink.Emit(sessionRecoveryNotice(event.NoticeCodeSessionRecoveryAdopted,
			"the session file conflict was auto-repaired; the message you just sent did not run — please send it again"))
		return nil
	}

	c.turnEvents.mu.Lock()
	c.turnEvents.conflictRetries++
	c.turnEvents.conflictLastAttempt = time.Now()
	if c.turnEvents.conflictRetries >= turnAdmissionConflictRetryLimit {
		c.turnEvents.conflictSuspended = true
		c.turnEvents.suspendWarned = time.Now()
	}
	suspended = c.turnEvents.conflictSuspended
	retries = c.turnEvents.conflictRetries
	c.turnEvents.mu.Unlock()

	if suspended {
		slog.Error("controller: diverged turn admission reached terminal suspension",
			"err", admissionErr, "path", conflict.Path, "retries", retries,
			"base_revision", conflict.BaseRevision, "disk_revision", conflict.DiskRevision,
			"disk_messages", conflict.ExistingMessages, "snapshot_messages", conflict.SnapshotMessages)
		c.sink.Emit(sessionRecoveryNotice(event.NoticeCodeSessionRecoveryDepthCap,
			"the session file conflicts with another writer and could not be auto-repaired; sending is paused for this session — reopen it (history is safe) and send your message again"))
		return func(context.Context) error {
			return fmt.Errorf("persist turn admission: diverged session conflict unresolved after %d attempts; reopen this session to retry: %w", retries, admissionErr)
		}
	}
	slog.Warn("controller: persist turn admission diverged; retrying with backoff before suspension",
		"err", admissionErr, "path", conflict.Path, "attempt", retries,
		"next_backoff_ms", turnAdmissionConflictBackoffFor(retries).Milliseconds())
	return func(context.Context) error { return fmt.Errorf("persist turn admission: %w", admissionErr) }
}

// healDivergedSnapshotConflict runs the stale-disk takeover for the latched
// conflict and clears the admission latch when it commits. It is the "trigger
// reconciliation" escalation of the terminal ladder: bounded by the agent-side
// preconditions (diverged shape, disk revision ahead, live snapshot ahead, no
// foreign lease) and re-verified under the save locks.
func (c *Controller) healDivergedSnapshotConflict(conflict *agent.SessionSnapshotConflictError) bool {
	if c == nil || c.executor == nil || conflict == nil {
		return false
	}
	path := conflict.Path
	if path == "" {
		return false
	}
	c.snapshotMu.Lock()
	defer c.snapshotMu.Unlock()
	session := c.executor.Session()
	if session == nil {
		return false
	}
	if err := session.SaveAdoptOverStaleDisk(path, conflict); err != nil {
		slog.Warn("controller: diverged admission self-heal declined",
			"err", err, "path", path,
			"base_revision", conflict.BaseRevision, "disk_revision", conflict.DiskRevision)
		return false
	}
	c.turnEvents.mu.Lock()
	c.turnEvents.err = nil
	c.turnEvents.mu.Unlock()
	appendSnapshotConflictDiagnostic(path, "admission", "diverged_takeover_committed", &agent.SessionSnapshotConflictError{
		Path:             path,
		Kind:             agent.SessionSnapshotConflictDiverged,
		ExistingMessages: conflict.ExistingMessages,
		SnapshotMessages: conflict.SnapshotMessages,
		BaseRevision:     conflict.BaseRevision,
		DiskRevision:     conflict.DiskRevision,
	}, "", true)
	slog.Warn("controller: diverged turn admission self-healed by takeover",
		"path", path, "base_revision", conflict.BaseRevision, "disk_revision", conflict.DiskRevision)
	return true
}

// resetDivergedAdmissionState clears the terminal ladder state. Called when a
// session rebinds (reopen / path change): the user's explicit action re-arms
// admission from a clean slate.
func (c *Controller) resetDivergedAdmissionState() {
	if c == nil {
		return
	}
	c.turnEvents.mu.Lock()
	c.turnEvents.conflictRetries = 0
	c.turnEvents.conflictLastAttempt = time.Time{}
	c.turnEvents.conflictSuspended = false
	c.turnEvents.suspendWarned = time.Time{}
	c.turnEvents.mu.Unlock()
}

// divergedAdmissionSuspended reports the terminal state for callers that need
// to branch on it (tests; runtime diagnostics).
func (c *Controller) divergedAdmissionSuspended() bool {
	if c == nil {
		return false
	}
	c.turnEvents.mu.RLock()
	defer c.turnEvents.mu.RUnlock()
	return c.turnEvents.conflictSuspended
}
