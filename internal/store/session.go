// Package store is the single authority for reasonix's on-disk persistence
// layout. Nothing else should construct a persistence path by hand.
//
// This first slice owns the session-artifact sidecars — the files and
// directories that live beside a session's .jsonl (branch metadata, goal state,
// checkpoints, background-job artifacts, the cleanup-pending marker). They were
// previously derived independently in internal/agent, internal/jobs,
// internal/control and internal/acp, each re-spelling the suffix convention; a
// layout change meant hunting across packages. Centralizing them here makes
// store the one place that knows where a session's artifacts go.
//
// store is a leaf: it imports only the standard library, so any package may
// depend on it without risking an import cycle. Root/directory resolution (the
// ~/.reasonix tree) and the desktop root unification land in later slices.
package store

import "strings"

// Suffix spellings of the session persistence layout (X6 pattern J, task 475).
// store is the only place allowed to spell these literals; every constructor
// below is written in terms of them so a constant and the layout it names
// cannot drift apart, and classification outside store must reference the
// constants instead of re-spelling the suffix (source-asserted by
// TestSessionPathSpellingsSingleSource in this package).
const (
	SessionTranscriptSuffix       = ".jsonl"
	SessionEventLogSuffix         = ".events.jsonl"
	SessionEventLogDamagedSuffix  = SessionEventLogSuffix + ".damaged"
	SessionEventLogRotatingSuffix = SessionEventLogSuffix + ".rotating"
	SessionTurnEventLogSuffix     = ".turns.jsonl"
	SessionConflictLogSuffix      = ".conflicts.jsonl"
	SessionEventIndexSuffix       = ".event-index.json"
	SessionDisplayIndexSuffix     = ".display-index.json"
	// SessionMetaFileSuffix is the full classification tail of SessionMeta
	// (which appends ".meta" to the whole transcript path, historical layout).
	SessionMetaFileSuffix  = SessionTranscriptSuffix + ".meta"
	SessionLockFileSuffix  = SessionTranscriptSuffix + ".lock"
	SessionLeaseLockSuffix = SessionTranscriptSuffix + ".lease.lock"
	SessionLeaseInfoSuffix = SessionTranscriptSuffix + ".lease.json"
)

// IsSessionTranscriptName reports whether name is a primary session transcript
// file. Append-only event logs and guardian sidecars also end in .jsonl, so
// callers that discover sessions by directory scan must use this helper instead
// of filepath.Ext.
func IsSessionTranscriptName(name string) bool {
	name = strings.TrimSpace(name)
	return strings.HasSuffix(name, SessionTranscriptSuffix) &&
		!strings.HasSuffix(name, SessionEventLogSuffix) &&
		!strings.HasSuffix(name, SessionTurnEventLogSuffix) &&
		!strings.HasSuffix(name, SessionConflictLogSuffix) &&
		!strings.HasSuffix(name, ".guardian.jsonl")
}

// SessionRecoveryState is the persisted Auto-mode recovery checkpoint state
// (<id>.recovery.json). It is a regular session-owned sidecar, not a transcript.
func SessionRecoveryState(sessionPath string) string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + ".recovery.json"
}

// SessionContext is the context-projection / compaction-state sidecar
// (<id>.context.json). It holds the model-visible projection and cache
// telemetry; transcript authority remains with the native event log once one
// exists, with the primary .jsonl retained as its compatibility checkpoint.
func SessionContext(sessionPath string) string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + ".context.json"
}

// SessionPinnedContext is the optional desktop pinned-workspace-context
// sidecar (<id>.pinned-context.json). Older versions ignore it while keeping
// the primary transcript fully readable.
func SessionPinnedContext(sessionPath string) string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + ".pinned-context.json"
}

// sessionStem strips the .jsonl suffix so a sidecar sits beside the session as
// <id>.<kind> rather than <id>.jsonl.<kind>.
func sessionStem(sessionPath string) string {
	return strings.TrimSuffix(sessionPath, ".jsonl")
}

// SessionMeta is the branch-metadata sidecar. Unlike the other sidecars it
// appends to the full session path (historical layout), so session.jsonl yields
// session.jsonl.meta.
func SessionMeta(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionPath + ".meta"
}

// SessionGoalState is the persisted active-goal sidecar (<id>.goal-state.json).
func SessionGoalState(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + ".goal-state.json"
}

// SessionEventLog is the append-only transcript event log (<id>.events.jsonl).
func SessionEventLog(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + SessionEventLogSuffix
}

// SessionEventLogDamaged is the salvage sidecar for event-log bytes that tail
// repair would otherwise discard (<id>.events.jsonl.damaged). It must NOT end
// in .jsonl: older binaries scanning a shared session directory classify any
// non-excluded .jsonl file as a primary transcript and would resurrect the
// damaged bytes as a phantom session.
func SessionEventLogDamaged(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + SessionEventLogDamagedSuffix
}

// SessionEventLogRotating marks a schema-2 log whose rotation is between
// reading the old file and publishing the new one; unlocked appenders wait
// for it to clear before trusting that their bytes reached the current log.
func SessionEventLogRotating(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + SessionEventLogRotatingSuffix
}

// IsSessionEventLogName reports whether name — a base filename or a full path —
// is a session's primary event log (<id>.events.jsonl). The .damaged/.rotating
// siblings end in other tails and are classified with their own constants.
// Scan-side classification belongs here so the family shape lives in one place
// (X6 pattern J, task 475).
func IsSessionEventLogName(name string) bool {
	return strings.HasSuffix(name, SessionEventLogSuffix)
}

// ResolveSessionEventLog is the read-side half of the write/read path
// convention: a reader may be handed either accepted shape — the transcript
// path (<id>.jsonl) or an already-resolved event-log path (<id>.events.jsonl,
// e.g. the path a sessionDAGState carries) — and normalizes it here instead of
// re-spelling the suffix branch. Applying SessionEventLog to a resolved path
// would yield "x.events.events.jsonl", which stats as missing and silently
// reported size zero (task 104: the adaptive budget became a no-op for every
// caller holding a resolved path).
func ResolveSessionEventLog(sessionPathOrLog string) string {
	p := strings.TrimSpace(sessionPathOrLog)
	if p == "" || strings.HasSuffix(p, SessionEventLogSuffix) {
		return p
	}
	return SessionEventLog(p)
}

// SessionTranscriptFromEventLog is the inverse of SessionEventLog for a
// resolved log path: it recovers the owning transcript path (<id>.jsonl).
// Anything that is not an event log — a transcript itself, a .damaged salvage,
// an empty string — reports "".
func SessionTranscriptFromEventLog(logPath string) string {
	p := strings.TrimSpace(logPath)
	if !strings.HasSuffix(p, SessionEventLogSuffix) {
		return ""
	}
	return strings.TrimSuffix(p, SessionEventLogSuffix) + SessionTranscriptSuffix
}

// SessionTurnEventLog is the append-only local runtime lifecycle ledger
// (<id>.turns.jsonl). It is independent from the provider transcript so old
// readers can continue to consume the primary session unchanged.
func SessionTurnEventLog(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + SessionTurnEventLogSuffix
}

// SessionTurnEventLogDamaged preserves a corrupt/torn ledger tail before the
// valid prefix is truncated back into service.
func SessionTurnEventLogDamaged(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return SessionTurnEventLog(sessionPath) + ".damaged"
}

// SessionEventIndex is the listing/checkpoint index for the event log
// (<id>.event-index.json). It contains derived offsets and digests, not the
// transcript body.
func SessionEventIndex(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + SessionEventIndexSuffix
}

// SessionDisplayIndex is the paging sidecar for the transcript
// (<id>.display-index.json). It contains per-message byte offsets, roles, and
// turn boundaries derived from the transcript, never message bodies, so a
// reader can page a huge history without parsing whole session files.
func SessionDisplayIndex(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + SessionDisplayIndexSuffix
}

// SessionConflictLog is the append-only diagnostic log for snapshot conflict
// recoveries (<id>.conflicts.jsonl). It contains revision counters and branch
// ids, not transcript content.
func SessionConflictLog(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + SessionConflictLogSuffix
}

// SessionLockFile is the advisory save lock (<id>.jsonl.lock).
func SessionLockFile(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionPath + ".lock"
}

// SessionLeaseLock is the runtime ownership lock (<id>.jsonl.lease.lock).
func SessionLeaseLock(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionPath + ".lease.lock"
}

// SessionLeaseInfo is the runtime ownership metadata
// (<id>.jsonl.lease.json).
func SessionLeaseInfo(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionPath + ".lease.json"
}

// SessionCheckpointDir is the snapshot-checkpoint directory (<id>.ckpt).
func SessionCheckpointDir(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + ".ckpt"
}

// SessionJobsDir is the background-job artifact directory (<id>.jobs).
func SessionJobsDir(sessionPath string) string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + ".jobs"
}

// SessionInboxDir is the durable session-level instruction inbox
// (<id>.inbox/). Manifest metadata and frozen prompt blobs live here.
func SessionInboxDir(sessionPath string) string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + ".inbox"
}

// SessionCleanupPending is the delayed-cleanup marker (<id>.cleanup-pending.json).
func SessionCleanupPending(sessionPath string) string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + ".cleanup-pending.json"
}

// SessionSidecarFiles returns every regular-file sidecar owned by a session
// transcript: branch meta, goal state, event/index logs, pinned context, and
// diagnostic logs.
// Every surface that deletes a session (desktop trash, /clear, serve, ACP)
// must remove all of these — the event log is the authoritative transcript, so
// leaving it behind both leaks the "deleted" conversation and lets LoadSession
// resurrect it. Directory artifacts (checkpoints, jobs) and ephemeral
// lock/lease files have their own lifecycles and are intentionally not listed.
func SessionSidecarFiles(sessionPath string) []string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return nil
	}
	return []string{
		SessionMeta(sessionPath),
		SessionGoalState(sessionPath),
		SessionEventLog(sessionPath),
		SessionEventLogDamaged(sessionPath),
		SessionEventLogRotating(sessionPath),
		SessionTurnEventLog(sessionPath),
		SessionTurnEventLogDamaged(sessionPath),
		SessionEventIndex(sessionPath),
		SessionDisplayIndex(sessionPath),
		SessionConflictLog(sessionPath),
		SessionRecoveryState(sessionPath),
		SessionContext(sessionPath),
		SessionPinnedContext(sessionPath),
	}
}
