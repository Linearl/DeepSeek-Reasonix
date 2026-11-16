package agent

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

const (
	maxRecoveryParentStemBytes = 80

)

var (
	ErrSessionSnapshotConflict = errors.New("session snapshot conflicts with newer transcript")

	// ErrSessionExternallyRemoved means a live Session still has a verified
	// baseline for path, but every authoritative transcript artifact disappeared.
	// Treating it as a first save would silently recreate a file the user or an
	// external cleanup tool deliberately removed.
	ErrSessionExternallyRemoved = errors.New("session was removed while still open")

	ErrSessionRecoveryNotNeeded = errors.New("session recovery not needed")

	// ErrSessionRecoveryDepthExceeded is retained for older callers. New
	// recovery writes update one stable branch and no longer return it.
	ErrSessionRecoveryDepthExceeded = errors.New("session recovery chain depth exceeded")

)

// SessionRecoveryMaxDepth is the historical nested-fork cap. New writes stamp
// RecoveryDepth=1 and update one stable path instead of deepening a chain.
const SessionRecoveryMaxDepth = 3


type sessionPersistState struct {
	projectionPending bool
	path              string
	digest            [sha256.Size]byte
	version           uint64
	revision          int64
	// revisionKnown marks revision as a real ledger value. It is false when
	// the baseline was established while the meta sidecar was unreadable
	// (torn or corrupt): the session must still open, but revision 0 must not
	// pose as a baseline or every honest on-disk revision would read as a
	// stale-runtime conflict. CAS checks fall back to digest+version until a
	// successful save re-learns the revision.
	revisionKnown bool
	// saveVerified marks a baseline established by a completed save in this
	// process, whose write path verified transcript and ledger agree. A
	// baseline adopted at load time pairs the disk transcript with whatever
	// the meta sidecar said — which can lag the transcript after an
	// interrupted save — so only save-verified baselines may arm the
	// snapshot no-op fast path; the first save after a load must run in full
	// and heal a stale ledger.
	//
	// 任务 831②: the load-time exception is narrowed. A load that came back
	// clean — no normalization repair, no damaged log, no tail truncation, and
	// a decode-time digest the ledger sidecar agrees with — has already proven
	// that the in-memory transcript is byte-identical to what the ledger
	// describes. That baseline is recorded with cleanLoad set and arms the
	// same no-op: the defensive switch/close snapshot a large session pays
	// right after a resume (SetActiveTab slow snapshot_ms=1000) is re-proving
	// an identity the load just established, and the full marshal+digest pass
	// it triggers is pure rework. A load whose ledger disagrees (the aftermath
	// of an interrupted save) or that needed repairs keeps the old
	// first-save-must-run-full behavior — that first save is still the one
	// that heals a stale ledger.
	cleanLoad bool
	// logTail is the event-log size observed when the baseline was recorded.
	// The no-op re-checks it (plus the ledger identity) before skipping: a
	// concurrent writer's append grows the log past this tail, and a save that
	// is genuinely behind must surface the stale-prefix conflict, not skip.
	logTail int64
	// checkpointBytes is the .jsonl checkpoint size observed when the baseline
	// was recorded (tl1281). The tail-window snapshot skip re-checks it the
	// same way: a first-paint window decoded from the checkpoint is only a
	// pure projection of the disk transcript while the checkpoint itself is
	// byte-stable.
	checkpointBytes int64
	saveVerified    bool
	ok              bool
}


type sessionSaveMode int


const (
	sessionSaveSnapshot sessionSaveMode = iota
	sessionSaveRewrite
	sessionSaveRewriteCompact
	sessionSaveToolCheckpoint
)


type snapshotWriteDecision struct {
	revision         int64
	reservedRevision int64
	upToDate         bool
	appendFrom       int
	appendOnly       bool
	// repairLog is set when the on-disk event log was damaged (torn tail with
	// a lost suffix, or nothing decodable): the safe write shape is a full
	// rewrite that also compacts the log back to a healthy single event.
	repairLog bool
	// ledgerStale is set when the on-disk transcript already matches the
	// snapshot but the meta ledger still describes older content — the
	// aftermath of a save whose bytes landed and whose revision record then
	// failed. The up-to-date path must heal the ledger instead of skipping it.
	ledgerStale bool
}


type SessionSnapshotConflictKind string


const (
	SessionSnapshotConflictStalePrefix SessionSnapshotConflictKind = "stale_prefix"
	SessionSnapshotConflictDiverged    SessionSnapshotConflictKind = "diverged"
)


type SessionSnapshotConflictError struct {
	Path             string
	Kind             SessionSnapshotConflictKind
	ExistingMessages int
	SnapshotMessages int
	BaseRevision     int64
	DiskRevision     int64
	Identity         SnapshotConflictIdentity // tl1443 trace evidence; never part of Error()
}


func (e *SessionSnapshotConflictError) Error() string {
	if e == nil {
		return ErrSessionSnapshotConflict.Error()
	}
	switch e.Kind {
	case SessionSnapshotConflictStalePrefix:
		return fmt.Sprintf("%s: %s has %d messages at revision %d; stale snapshot has %d messages from revision %d",
			ErrSessionSnapshotConflict, e.Path, e.ExistingMessages, e.DiskRevision, e.SnapshotMessages, e.BaseRevision)
	default:
		return fmt.Sprintf("%s: %s diverged on disk (%d messages, revision %d) from snapshot (%d messages, revision %d)",
			ErrSessionSnapshotConflict, e.Path, e.ExistingMessages, e.DiskRevision, e.SnapshotMessages, e.BaseRevision)
	}
}


func (e *SessionSnapshotConflictError) Unwrap() error {
	return ErrSessionSnapshotConflict
}


func SnapshotConflictKind(err error) (SessionSnapshotConflictKind, bool) {
	var conflict *SessionSnapshotConflictError
	if errors.As(err, &conflict) && conflict != nil {
		return conflict.Kind, true
	}
	return "", false
}


const RecoveryBranchDefaultName = "Recovered unsaved changes from stale runtime"


type RecoveryBranchOptions struct {
	OriginalPath string
	Name         string
	Reason       string
	BranchMeta   BranchMeta
	BaseRevision int64
	DiskRevision int64
}


type RecoveryBranchInfo struct {
	Path     string
	Digest   string
	Existing bool
	Meta     BranchMeta
	Preview  string
	Turns    int
}


// Save persists the session using the normal CAS-protected snapshot protocol.
// It is kept as the convenient default for callers that do not need to spell
// out rewrite intent; it must never provide a force-overwrite escape hatch.
// The .jsonl file remains as a compatibility checkpoint and discovery anchor;
// the append-only event log is authoritative once present, while the .jsonl
// checkpoint also serves as the random-read model for history paging.
func (s *Session) Save(path string) error {
	return s.saveObserved(path, sessionSaveSnapshot)
}


// SaveSnapshot writes a normal autosave/snapshot only when doing so cannot hide
// a newer transcript already on disk. Explicit history rewrites such as rewind,
// compaction, and cancel recovery should call SaveRewrite instead.
func (s *Session) SaveSnapshot(path string) error {
	return s.saveObserved(path, sessionSaveSnapshot)
}


// SaveSnapshotIfPathFree is the non-blocking form of SaveSnapshot for
// latency-sensitive foreground paths (desktop tab switching): it performs the
// full snapshot only when the save path is uncontended, and reports
// attempted=false — nothing written, session untouched — when another
// goroutine holds the save-path mutex, so the caller can degrade to a
// background save instead of queueing behind a long reader or writer
// (measured p50 28s / max 69s on large-session loads, DAG research
// 2026-10-04). attempted=false always pairs with err=nil; an attempted save
// reports its failure exactly like SaveSnapshot would.
func (s *Session) SaveSnapshotIfPathFree(path string) (attempted bool, err error) {
	if path == "" {
		return false, fmt.Errorf("empty session path")
	}
	// tl1281: same skip as saveObserved — an unchanged tail-window session's
	// snapshot has nothing to write, so the upgrade's full reload is pure
	// decode cost on the tab-switch path.
	if s.snapshotTailWindowClean(path) {
		return false, nil
	}
	// Advisory probe before the potentially expensive truncated-transcript
	// upgrade: a busy path means the file is owned by someone else right now,
	// so no speculative work should start. The authoritative gate is the
	// try-lock in tryWithSessionSaveLocks, which holds the mutex through the
	// locked save.
	if !savePathLockFree(path) {
		return false, nil
	}
	if err := s.upgradeTruncatedTranscriptForWrite(path); err != nil {
		return true, err
	}
	return s.tryWithSessionSaveLocks(path, func() error {
		return s.saveObservedAfterUpgrade(path, sessionSaveSnapshot, func() error {
			return s.saveLocked(path, sessionSaveSnapshot)
		})
	})
}


// SaveRewrite writes an intentional non-append history rewrite only while this
// Session still owns the current on-disk transcript baseline. It prevents a
// stale controller from force-rewinding a newer transcript written elsewhere.
func (s *Session) SaveRewrite(path string) error {
	return s.saveObserved(path, sessionSaveRewrite)
}


// SaveRewriteCompact performs a CAS-protected rewrite and folds the event log
// to one replace record. It is for destructive maintenance such as redaction:
// retaining old WAL records would keep the removed bytes recoverable on disk.
func (s *Session) SaveRewriteCompact(path string) error {
	return s.saveObserved(path, sessionSaveRewriteCompact)
}


// RewriteAndSaveCompact installs an in-memory history rewrite and persists it
// as one CAS-protected compact rewrite inside a single save-path lock window
// (tl1352 C+). The separate Rewrite → SaveRewriteCompact pair leaves a window
// where the in-memory transcript is already folded but the disk still holds
// the old bytes and the CAS ledger the old baseline: a foreground checkpoint
// or autosave firing inside that window races the diet on the same session
// file, and the losing side classifies the mismatch as a foreign divergence
// (ErrSessionSnapshotConflict kind=diverged) and forks a recovery copy — the
// 20261031 recovery-copy storm. Holding the save-path mutex across both steps
// makes that intermediate state unobservable to every other saver of the
// path: they queue behind the same mutex the foreground paths already use.
//
// fold runs inside the lock window against the complete in-memory transcript
// at save time, so the rewrite product never derives from a stale snapshot.
// It must be pure and must not acquire session save locks or call back into
// Session write methods (the path mutex is non-reentrant — re-entry from here
// deadlocks). The transcript upgrade (task 187) still runs outside the locks,
// so first-paint tail-window sessions are completed before fold sees them.
//
// On save failure the in-memory transcript keeps the folded state while the
// disk keeps the old bytes — the same observable outcome as the previous
// Rewrite-then-SaveRewriteCompact pair, so callers' failure handling is
// unchanged.
func (s *Session) RewriteAndSaveCompact(path string, reason string, fold func(complete []provider.Message) []provider.Message) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("empty session path")
	}
	if fold == nil {
		return fmt.Errorf("rewrite compact %s: nil fold", path)
	}
	if err := s.upgradeTruncatedTranscriptForWrite(path); err != nil {
		return err
	}
	// Task 885 discipline: directory create is disk IO, not a critical-section
	// step — it runs before the mutex so a slow volume cannot stretch the hold.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}
	lockWaitStart := time.Now()
	unlock, lockErr := lockSessionSavePathWithin(path, sessionSavePathLockWait)
	if lockErr != nil {
		return lockErr
	}
	defer unlock()
	unlockFile, err := lockSessionFile(path)
	if err != nil {
		return fmt.Errorf("lock session file: %w", err)
	}
	defer unlockFile()
	// Task 357: the wait just paid is a first-class segment of the save total.
	s.lastSaveLockWaitMs.Store(time.Since(lockWaitStart).Milliseconds())
	return s.saveObservedAfterUpgrade(path, sessionSaveRewriteCompact, func() error {
		s.Rewrite(fold(s.Snapshot()), reason)
		return s.saveLocked(path, sessionSaveRewriteCompact)
	})
}


func (s *Session) save(path string, mode sessionSaveMode) error {
	if path == "" {
		return fmt.Errorf("empty session path")
	}
	return s.withSessionSaveLocks(path, func() error {
		return s.saveLocked(path, mode)
	})
}


func (s *Session) withSessionSaveLocks(path string, fn func() error) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("empty session path")
	}
	// Task 885: the directory create is disk IO, not a critical-section step —
	// it runs before the mutex so a slow volume cannot stretch the lock hold.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}
	lockWaitStart := time.Now()
	unlock, lockErr := lockSessionSavePathWithin(path, sessionSavePathLockWait)
	if lockErr != nil {
		return lockErr
	}
	defer unlock()
	unlockFile, err := lockSessionFile(path)
	if err != nil {
		return fmt.Errorf("lock session file: %w", err)
	}
	defer unlockFile()
	// Task 357: both save locks are held from here; the wait just paid is a
	// first-class segment of the save total (196-family attribution).
	s.lastSaveLockWaitMs.Store(time.Since(lockWaitStart).Milliseconds())
	return fn()
}


// lockSessionSavePathWithin acquires the in-process save-path mutex for path,
// polling every sessionSavePathLockPoll, and gives up after budget with
// ErrSessionFileLockHeld instead of queueing forever behind a wedged holder
// (任务 885, extended to every lockSessionSavePath caller by 任务 946). The
// budget-out error carries the canonical key, the wait actually paid and the
// budget itself, so a timed-out caller can name the wedged session in its
// user-facing error and the log line points at the same facts.
func lockSessionSavePathWithin(path string, budget time.Duration) (func(), error) {
	key := canonicalSessionSavePath(path)
	v, _ := sessionSaveLocks.LoadOrStore(key, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	waitStart := time.Now()
	deadline := waitStart.Add(budget)
	for {
		if mu.TryLock() {
			waited := time.Since(waitStart)
			if waited >= sessionSaveLockWarnWait {
				slog.Warn("session: save-path lock waited", "path", key, "wait_ms", waited.Milliseconds())
			}
			return mu.Unlock, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			waited := time.Since(waitStart)
			slog.Warn("session: save-path lock wait exceeded budget; caller declines", "path", key, "wait_ms", waited.Milliseconds(), "budget_ms", budget.Milliseconds())
			return nil, fmt.Errorf("save-path lock %s held %s (budget %s): %w", key, waited.Round(time.Millisecond), budget, ErrSessionFileLockHeld)
		}
		sleep := sessionSavePathLockPoll
		if sleep > remaining {
			sleep = remaining
		}
		time.Sleep(sleep)
	}
}


// tryWithSessionSaveLocks is the non-blocking form of withSessionSaveLocks for
// latency-sensitive foreground paths (desktop tab switching): the in-process
// save-path mutex is taken only when immediately free, and attempted=false is
// reported otherwise, so the caller can degrade to a later save instead of
// queueing behind a long reader or writer. The cross-process compatibility
// file lock keeps its normal bounded wait — only same-process queueing is
// declined. attempted=false always pairs with err=nil and leaves the session
// and the file untouched; once the attempt commits (path mutex held), a
// failure surfaces as attempted=true plus the error, like the blocking form.
func (s *Session) tryWithSessionSaveLocks(path string, fn func() error) (attempted bool, err error) {
	if strings.TrimSpace(path) == "" {
		return false, fmt.Errorf("empty session path")
	}
	unlockPath, ok := tryLockSessionSavePath(path)
	if !ok {
		return false, nil
	}
	defer unlockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return true, fmt.Errorf("create session dir: %w", err)
	}
	unlockFile, err := lockSessionFile(path)
	if err != nil {
		return true, fmt.Errorf("lock session file: %w", err)
	}
	defer unlockFile()
	// Same attribution contract as withSessionSaveLocks (Task 357): a
	// try-lock that succeeded waited zero.
	s.lastSaveLockWaitMs.Store(0)
	return true, fn()
}


func sessionArtifactExists(path string) bool {
	if _, err := os.Lstat(path); err == nil {
		return true
	}
	for _, artifact := range store.SessionSidecarFiles(path) {
		// A legacy v0 event transcript can share the native
		// `<id>.events.jsonl` name with the destination being imported. It is
		// intentionally left in place, and must not make SaveIfAbsent believe
		// that the v1 `<id>.jsonl` destination already exists. Native event logs
		// remain owned artifacts and still protect the destination from a second
		// writer.
		if artifact == store.SessionEventLog(path) {
			probe, probeErr := probeSessionEventLog(path)
			if probeErr != nil {
				return true
			}
			if !probe.native {
				continue
			}
		}
		if _, err := os.Lstat(artifact); err == nil {
			return true
		}
	}
	return false
}


// saveLocked runs the save body and, on success, the tl1285 fold-floor
// fallback: a log the probe ladder has MEASURED at its fold floor and that is
// still over the configured cap is archived losslessly here, inside the same
// file lock, instead of being left in place forever. Inactive saves pay two
// atomic loads, one stat and one map read.
func (s *Session) saveLocked(path string, mode sessionSaveMode) error {
	err := s.saveLockedInner(path, mode)
	if err == nil {
		s.maybeFloorArchiveEventLog(path)
	}
	return err
}


func (s *Session) saveLockedInner(path string, mode sessionSaveMode) error {
	baseRevision := int64(0)
	// Task 357: millisecond segments for the locked section. The dag path
	// carries its own line (dag state for save); this one accounts for the
	// rest so "where do the seconds go" answers from one grep.
	phaseStart := time.Now()
	var authMs, healMs, snapMs, probeMs int64
	var noopReason string
	defer func() {
		slog.Info("session: save phases",
			"path", canonicalSessionSavePath(path), "mode", int(mode),
			"lock_wait_ms", s.lastSaveLockWaitMs.Load(),
			"auth_ms", authMs, "heal_ms", healMs, "snap_ms", snapMs, "probe_ms", probeMs,
			"noop", noopReason,
			"locked_total_ms", time.Since(phaseStart).Milliseconds())
	}()
	releaseAuth, err := s.requireWriteAuthorityForSave(path)
	if err != nil {
		return err
	}
	authMs = time.Since(phaseStart).Milliseconds()
	defer releaseAuth()
	healStart := time.Now()
	observeUnleasedSessionWrite(path, mode)
	// tl1313: stamp a baseline-known file that vanished out of band BEFORE the
	// WAL heal below can silently recreate it — the 20261031 ghost-save
	// incident ran for days with saves reporting success while a periodic
	// external remover deleted transcript and event log between writes, and
	// the heal+no-op pair absorbed every disappearance without a trace (the
	// only signal was canonical-probe `outcome=missing` lines). One WARN per
	// disappearance episode turns that into a first-class desktop.log event
	// carrying the baseline facts (size/revision) for later forensics.
	s.warnVanishedPersistedFiles(path)
	// Heal an empty/missing checkpoint from a valid WAL before classification
	// so a 0-byte .jsonl never forces a false diverged recovery.
	if err := healEmptyCheckpointFromWAL(path); err != nil {
		return err
	}
	healMs = time.Since(healStart).Milliseconds()
	if mode == sessionSaveSnapshot && s.snapshotUpToDate(path) {
		// Nothing changed since the last successful save to this exact path:
		// skip the rest of the save — including the full transcript serialize
		// + digest + disk probe the up-to-date decision below would still
		// pay. Desktop switch/close/prune paths snapshot defensively on every
		// navigation, and on large sessions that per-save cost is the
		// user-visible seconds of UI freeze in #6607. Version bookkeeping
		// makes this exact: any Add/Replace/preview update bumps version, any
		// rewrite bumps rewriteVersion, and load-time repairs or log damage
		// disarm the fast path until a real save persists them. The check
		// runs under the save locks, not before them, so a saver that waited
		// on a concurrent writer still re-evaluates against the state it must
		// persist when it finally enters the critical section.
		//
		// An up-to-date transcript does not imply up-to-date derived files: a
		// tool checkpoint commits the transcript but defers the listing and
		// display indexes. Republish them here instead of paying for the full
		// save above just to rebuild a derived index. A log whose derived files
		// need more than that (schema 2 also derives its head index and meta
		// mirror) declines and falls through unchanged.
		//
		// 任务 831②: noop names the baseline source that armed the skip
		// (save-verified / clean-load) so the fast path's coverage is provable
		// from desktop.log instead of inferred from absent slow lines.
		noopReason = s.snapshotNoOpReason(path)
		if s.flushDeferredDerivedFiles(path) {
			return nil
		}
		noopReason = ""
	}
	// Capture the snapshot only while holding the save locks. Concurrent
	// in-process savers (turn-end snapshot, periodic autosave, shutdown
	// snapshot) that captured before locking could land out of order: the
	// stalest capture written last would then read the newer transcript it
	// lost the race to as a bogus stale-prefix conflict.
	snapStart := time.Now()
	msgs, version, rewriteVersion := s.snapshotWithVersion()
	snapMs = time.Since(snapStart).Milliseconds()
	probeStart := time.Now()
	probe, err := probeLogForSave(path)
	if err != nil {
		// Task 339: the tail repair replays the log, so a log already past the
		// replay caps refuses here, before classification ever runs. When the
		// disk state is provably this runtime's own last write, fold the log
		// from the in-memory snapshot and re-probe; the folded log repairs and
		// classifies as a healthy single replace.
		var limitErr *SessionReplayLimitError
		if errors.As(err, &limitErr) {
			if rescueDigest, _, digestErr := digestAndSizeSessionMessages(msgs); digestErr == nil &&
				s.rescueReplayLimitedEventLog(path, msgs, rescueDigest, err) {
				probe, err = probeLogForSave(path)
			}
		}
		if err != nil {
			return err
		}
	}
	probeMs = time.Since(probeStart).Milliseconds()
	// tl1280: the schema-2 slow lane (dagSaveRoute/saveDAGLocked) is gone. A
	// sealed schema-2 log is refused by probeLogForSave above, so every save
	// that reaches here writes the flat schema-1 form.
	digest, contentBytes, err := s.transcriptDigestAndSizeForSave(msgs, version, rewriteVersion)
	if err != nil {
		return err
	}
	repairLog := false
	deferProjection := mode.defersProjection()
	ownedRewrite := mode.allowsOwnedRewrite()
	decision, err := s.classifySnapshotWriteForCommit(path, msgs, digest, version, ownedRewrite, mode)
	if err != nil {
		return err
	}
	if decision.upToDate && mode != sessionSaveRewriteCompact {
		// Disk already holds exactly this transcript. Rewriting it would only
		// bump the revision, invalidating the persistence baseline of every
		// other runtime resumed on this file and turning their next
		// legitimate save into a stale-runtime conflict. Skip the write and
		// adopt the current on-disk revision as this session's baseline.
		if decision.ledgerStale {
			// ...unless the ledger never learned about this transcript: a
			// prior save landed its bytes and then failed to record the
			// revision. Same-content retries are exactly the "later save"
			// that failure deferred to, and skipping here would strand the
			// ledger on the old digest forever. Record now, reproducing
			// the state the interrupted save would have left.
			revision, err := recordSessionContentRevision(path, digest, decision.revision, decision.reservedRevision)
			if err != nil {
				return err
			}
			displayModelCurrent := true
			if err := writeSessionMessages(path, msgs); err != nil {
				displayModelCurrent = false
				slog.Warn("session: keeping save after display read-model repair failure", "path", path, "err", err)
			}
			if probe.native {
				if err := writeSessionEventIndex(path, msgs, digest, revision); err != nil {
					// See the append path below: index loss must not fail a
					// save whose transcript and revision already landed.
					slog.Warn("session: keeping save after event index write failure", "path", path, "err", err)
				}
			}
			if displayModelCurrent {
				if err := refreshCheckpointDisplayIndex(path, msgs, digest, revision, -1, deferProjection); err != nil {
					// The display index is a derived sidecar; transcript durability
					// must not depend on rebuilding it successfully.
					slog.Warn("session: keeping save after display index write failure", "path", path, "err", err)
				}
			}
			s.markCheckpointPersisted(path, digest, version, revision, rewriteVersion, msgs, deferProjection)
			return nil
		}
		s.refreshPendingCheckpointProjection(path, msgs, digest, decision.revision, deferProjection)
		s.markCheckpointPersisted(path, digest, version, decision.revision, rewriteVersion, msgs, deferProjection)
		return nil
	}
	if decision.appendOnly && probe.native && mode != sessionSaveRewriteCompact {
		logSize := sessionEventLogSize(path)
		displayModelCurrent := false
		switch {
		case logSize == 0:
			if err := appendSessionReplaceEvent(path, msgs, digest, decision.revision, "snapshot"); err != nil {
				return err
			}
			displayModelCurrent, err = appendSessionDisplayReadModel(path, msgs, decision.appendFrom, decision.revision)
		case sessionEventIndexNearCap(path):
			// Task 193 long fix: the event index shows the replay record caps are
			// close; fold history now (msgs are already in memory) so a live turn
			// never pushes the log past the replay gate and freezes the session.
			if err := compactSessionEventLog(path, msgs, digest, decision.revision, "compact-records"); err != nil {
				return err
			}
			// Fold history into one replace event and refresh the random-read
			// model atomically. Normal appends keep it current below too.
			if err := compactSessionEventLog(path, msgs, digest, decision.revision, "compact"); err != nil {
				return err
			}
			if err := writeSessionMessages(path, msgs); err != nil {
				return err
			}
			displayModelCurrent = true
		default:
			if err := appendSessionAppendEvent(path, decision.appendFrom, msgs[decision.appendFrom:], digest, decision.revision); err != nil {
				return err
			}
			displayModelCurrent, err = appendSessionDisplayReadModel(path, msgs, decision.appendFrom, decision.revision)
		}
		if err != nil {
			slog.Warn("session: keeping save after display read-model append failure", "path", path, "err", err)
		}
		revision, err := recordSessionContentRevision(path, digest, decision.revision, decision.reservedRevision)
		if err != nil {
			return err
		}
		if err := writeSessionEventIndex(path, msgs, digest, revision); err != nil {
			// The event index is only a listing accelerator; the transcript
			// and its revision are already durable above. Failing the save
			// here would skip markPersisted and leave the in-memory baseline
			// behind the disk state it just wrote, misreading the next save
			// as a stale-runtime conflict.
			slog.Warn("session: keeping save after event index write failure", "path", path, "err", err)
		}
		if displayModelCurrent {
			if err := refreshCheckpointDisplayIndex(path, msgs, digest, revision, decision.appendFrom, deferProjection); err != nil {
				// The append boundary lets the refresh extend the previous index
				// instead of re-encoding the whole transcript.
				slog.Warn("session: keeping save after display index write failure", "path", path, "err", err)
			}
		}
		s.markCheckpointPersisted(path, digest, version, revision, rewriteVersion, msgs, deferProjection)
		return nil
	}
	baseRevision = decision.revision
	repairLog = decision.repairLog
	// Full-rewrite path: new snapshots, intentional history rewrites, and
	// damage repairs. The event log mutates first so a crash between the two
	// writes leaves the newer transcript authoritative; the anchor rewrite
	// keeps the compatibility .jsonl fresh for direct readers.
	reason := mode.eventReason()
	if repairLog {
		reason = "repair"
	}
	logSize := sessionEventLogSize(path)
	switch {
	case !probe.native:
		// A foreign file (legacy import leftover) squats the native log path.
		// Never write into or over it — the session stays checkpoint-only.
	case mode == sessionSaveRewriteCompact:
		// Maintenance rewrites compact even a short log so redacted or otherwise
		// removed bytes do not remain recoverable in historical WAL records.
		if err := compactSessionEventLog(path, msgs, digest, baseRevision, reason); err != nil {
			return err
		}
	case repairLog, sessionEventLogOversized(path, logSize, contentBytes):
		if err := compactSessionEventLog(path, msgs, digest, baseRevision, reason); err != nil {
			return err
		}
		// tl 1285: the gate's probe ladder trusts measurements, not estimates —
		// report what the fold actually freed so a probe that really shrank the
		// log clears the strike count and a no-gain probe counts one.
		noteSessionFoldOutcome(path, logSize, sessionEventLogSize(path))
	default:
		if err := appendSessionReplaceEvent(path, msgs, digest, baseRevision, reason); err != nil {
			return err
		}
	}
	if err := writeSessionMessages(path, msgs); err != nil {
		return err
	}
	revision, err := recordSessionContentRevision(path, digest, baseRevision, decision.reservedRevision)
	if err != nil {
		return err
	}
	if probe.native {
		if err := writeSessionEventIndex(path, msgs, digest, revision); err != nil {
			// See the append path above: index loss must not fail a save whose
			// transcript and revision already landed.
			slog.Warn("session: keeping save after event index write failure", "path", path, "err", err)
		}
	}
	if err := refreshCheckpointDisplayIndex(path, msgs, digest, revision, -1, deferProjection); err != nil {
		// Warn-only like the event index above: the display index is a pure
		// derived sidecar and must never fail a save.
		slog.Warn("session: keeping save after display index write failure", "path", path, "err", err)
	}
	s.markCheckpointPersisted(path, digest, version, revision, rewriteVersion, msgs, deferProjection)
	return nil
}


// checkSnapshotWrite decides whether this session may write msgs over path, and
// whether the safe write shape is a no-op, append-only suffix, or full rewrite.
func (s *Session) checkSnapshotWrite(path string, next []provider.Message, nextDigest [sha256.Size]byte, nextVersion uint64, allowOwnedRewrite bool) (snapshotWriteDecision, error) {
	baseState := s.persistState(path)
	current, err := loadSessionUnlocked(path)
	if err != nil {
		if os.IsNotExist(err) {
			if baseState.ok {
				return snapshotWriteDecision{}, ErrSessionExternallyRemoved
			}
			return snapshotWriteDecision{}, nil
		}
		return snapshotWriteDecision{}, err
	}
	ledger, err := sessionContentLedger(path)
	if err != nil {
		return snapshotWriteDecision{}, err
	}
	currentRevision, currentLedgerDigest := ledger.revision, ledger.digest
	existing := current.Snapshot()
	existingDigest, err := digestSessionMessages(existing)
	if err != nil {
		return snapshotWriteDecision{}, err
	}
	// raw is the transcript as stored, before load-time normalization repaired
	// it; it equals existing when no repair ran. The prefix checks below must
	// be able to fall back to it: a mid-turn snapshot legitimately cuts an
	// assistant tool call from its still-running result, normalization then
	// fabricates a placeholder answer on load, and the live session's real
	// result collides with that placeholder — misreading a pure append as
	// divergence (and forking a bogus recovery branch).
	raw, rawDigest := existing, existingDigest
	rawDiffers := current.normalizedDirty && len(current.rawMessages) > 0
	if rawDiffers {
		raw = current.rawMessages
		if rawDigest, err = digestSessionMessages(raw); err != nil {
			return snapshotWriteDecision{}, err
		}
	}
	contentUnchanged := bytes.Equal(existingDigest[:], nextDigest[:])
	exactAppend := messagesHavePrefix(next, existing)
	appendShaped := contentUnchanged || exactAppend || messagesHavePrefixWithCompatibleSystem(next, existing)
	repairPending := current.normalizedDirty
	if !appendShaped && rawDiffers {
		rawUnchanged := bytes.Equal(rawDigest[:], nextDigest[:])
		rawAppend := messagesHavePrefix(next, raw)
		if rawUnchanged || rawAppend || messagesHavePrefixWithCompatibleSystem(next, raw) {
			existing = raw
			contentUnchanged = rawUnchanged
			exactAppend = rawAppend
			appendShaped = true
			// The snapshot supersedes the repaired view — appending it lands
			// the real tool results where the placeholders were fabricated —
			// so no load-time repair is left to force a rewrite.
			repairPending = false
		}
	}
	if !appendShaped && baseState.ok && baseState.revisionKnown &&
		baseState.revision == currentRevision && !contentUnchanged {
		// Revision equality alone is not ownership proof. Require digest
		// ancestry or a live generation-bound write authority covering path.
		if s.ownsWritableBaseline(path, existingDigest, rawDigest, rawDiffers, currentRevision, currentLedgerDigest, nextVersion) {
			appendShaped = true
		}
	}
	if appendShaped {
		// An unknown-revision baseline (meta sidecar unreadable at load) cannot
		// vouch for revision equality; the digest/prefix checks above already
		// vouch for the content, so only a known baseline arms the CAS check.
		// Under an append-shaped write (at most a compatible leading-system
		// swap) a stale revision is ledger drift — a reset sidecar, a
		// same-content heal, or another runtime recording messages this
		// snapshot already contains — unless the transcript was rewound.
		// Locating the persisted baseline among the snapshot's prefixes and
		// requiring the disk transcript to still reach it tells the two apart:
		// drift keeps the baseline reachable, while a rewind cut below it and
		// appending would resurrect the suffix another runtime removed.
		if baseState.ok && baseState.revisionKnown && currentRevision != baseState.revision && !contentUnchanged &&
			!appendCoversPersistedBaseline(next, existing, baseState.digest) {
			return snapshotWriteDecision{}, snapshotConflict(path, existing, next, baseState.revision, currentRevision,
				newSnapshotConflictIdentity(baseState, nextDigest, existingDigest, ledger))
		}
		// A normalized-dirty load means LoadSession repaired the history on the
		// way in: the digests match but the raw bytes on disk do not, so the
		// repair still needs a real write to persist. A damaged event log
		// likewise needs a real write (rewrite + compact) even when the
		// replayable prefix already matches this snapshot.
		decision := snapshotWriteDecision{
			revision:  currentRevision,
			upToDate:  contentUnchanged && !repairPending && !current.eventLogDamaged,
			repairLog: current.eventLogDamaged,
		}
		// A ledger digest that describes different content than the transcript
		// on disk is the aftermath of a save whose bytes landed and whose
		// revision record then failed (crash or fail-closed record between the
		// two writes). Only a non-empty mismatch counts: a missing sidecar or
		// a legacy one without a digest is a legitimate state, and stamping it
		// here would bump revisions other runtimes still hold as baselines.
		if decision.upToDate && currentLedgerDigest != "" && currentLedgerDigest != digestString(nextDigest) {
			decision.ledgerStale = true
		}
		// An append is only chain-safe when existing measures the transcript
		// the event log actually replays. Under a pending load-time repair the
		// normalized view differs from the raw log, so an append event indexed
		// against it breaks the replay chain and orphans the appended suffix;
		// fall through to the full rewrite, which also persists the repair.
		if exactAppend && !contentUnchanged && len(existing) < len(next) && !current.eventLogDamaged && !repairPending {
			decision.appendOnly = true
			decision.appendFrom = len(existing)
		}
		return decision, nil
	}
	if allowOwnedRewrite {
		if s.ownsWritableBaseline(path, existingDigest, rawDigest, rawDiffers, currentRevision, currentLedgerDigest, nextVersion) {
			return snapshotWriteDecision{revision: currentRevision, repairLog: current.eventLogDamaged}, nil
		}
	}
	// Bound controllers: missing/stale authority must not fork recovery.
	if err := s.authorityErrorForPath(path); err != nil {
		return snapshotWriteDecision{}, err
	}
	if messagesHavePrefix(existing, next) || messagesHavePrefixWithCompatibleSystem(existing, next) ||
		(rawDiffers && (messagesHavePrefix(raw, next) || messagesHavePrefixWithCompatibleSystem(raw, next))) {
		return snapshotWriteDecision{}, &SessionSnapshotConflictError{
			Path:             path,
			Kind:             SessionSnapshotConflictStalePrefix,
			ExistingMessages: len(existing),
			SnapshotMessages: len(next),
			BaseRevision:     baseState.revision,
			DiskRevision:     currentRevision,
			Identity:         newSnapshotConflictIdentity(baseState, nextDigest, existingDigest, ledger),
		}
	}
	// tl1409: an armed stale-disk takeover converts the diverged terminal into
	// an owned rewrite at the current disk revision — but only while the armed
	// fingerprint still matches disk exactly (re-verified here under the save
	// locks) and the live snapshot is the side that kept growing. Without this,
	// a dead second writer leaves the pair in permanent mutual refusal and the
	// admission path retries diverged saves forever.
	if decision, ok := s.staleDiskTakeoverDecision(path, existing, next, baseState.revision, currentRevision); ok {
		return decision, nil
	}
	return snapshotWriteDecision{}, &SessionSnapshotConflictError{
		Path:             path,
		Kind:             SessionSnapshotConflictDiverged,
		ExistingMessages: len(existing),
		SnapshotMessages: len(next),
		BaseRevision:     baseState.revision,
		DiskRevision:     currentRevision,
		Identity:         newSnapshotConflictIdentity(baseState, nextDigest, existingDigest, ledger),
	}
}


func snapshotConflict(path string, existing, next []provider.Message, baseRevision, diskRevision int64, identity SnapshotConflictIdentity) error {
	kind := SessionSnapshotConflictDiverged
	if messagesHavePrefix(existing, next) || messagesHavePrefixWithCompatibleSystem(existing, next) {
		kind = SessionSnapshotConflictStalePrefix
	}
	return &SessionSnapshotConflictError{
		Path:             path,
		Kind:             kind,
		ExistingMessages: len(existing),
		SnapshotMessages: len(next),
		BaseRevision:     baseRevision,
		DiskRevision:     diskRevision,
		Identity:         identity,
	}
}


func (s *Session) SaveRecoveryBranch(opts RecoveryBranchOptions) (RecoveryBranchInfo, error) {
	return s.saveRecoveryBranch(opts, false)
}


// SaveShutdownRecoveryBranch persists the current transcript to a distinct
// recovery branch after the normal shutdown snapshot failed with
// ErrSessionFileLockHeld. It deliberately does not re-lock or inspect the
// original session file: doing so would repeat the same bounded timeout and
// let process teardown discard the only remaining in-memory copy.
//
// The recovery filename includes this live Session's isolated lane, so another
// controller in the same process cannot replace the emergency copy.
// The result still uses the normal session, event-log, and branch-meta formats
// and is therefore discoverable and resumable through existing flows.
func (s *Session) SaveShutdownRecoveryBranch(opts RecoveryBranchOptions) (RecoveryBranchInfo, error) {
	return s.saveRecoveryBranch(opts, true)
}


// SaveConflictRecoveryBranch writes the depth-cap isolated copy (one path per
// live Session). Subsequent conflicts from that Session rewrite it in place.
func (s *Session) SaveConflictRecoveryBranch(opts RecoveryBranchOptions) (RecoveryBranchInfo, error) {
	return s.saveRecoveryBranch(opts, true)
}


func (s *Session) saveRecoveryBranch(opts RecoveryBranchOptions, shutdown bool) (RecoveryBranchInfo, error) {
	originalPath := strings.TrimSpace(opts.OriginalPath)
	if originalPath == "" {
		return RecoveryBranchInfo{}, fmt.Errorf("empty original session path")
	}
	opts.OriginalPath = originalPath
	msgs, version, rewriteVersion := s.snapshotWithVersion()
	preview, turns := SessionPreviewFromMessages(msgs)
	if turns == 0 {
		return RecoveryBranchInfo{}, ErrSessionRecoveryNotNeeded
	}
	digest, err := digestSessionMessages(msgs)
	if err != nil {
		return RecoveryBranchInfo{}, err
	}
	digestText := digestString(digest)

	if !shutdown {
		unlockOriginal, lockErr := lockSessionSavePath(originalPath)
		if lockErr != nil {
			return RecoveryBranchInfo{}, fmt.Errorf("lock original session path: %w", lockErr)
		}
		unlockOriginalFile, lockErr := lockSessionFile(originalPath)
		if lockErr != nil {
			unlockOriginal()
			return RecoveryBranchInfo{}, fmt.Errorf("lock original session file: %w", lockErr)
		}
		current, loadErr := loadSessionUnlocked(originalPath)
		unlockOriginalFile()
		unlockOriginal()
		if loadErr != nil && !os.IsNotExist(loadErr) {
			return RecoveryBranchInfo{}, loadErr
		}
		if loadErr == nil && current != nil {
			existing := current.Snapshot()
			existingDigest, digestErr := digestSessionMessages(existing)
			if digestErr != nil {
				return RecoveryBranchInfo{}, digestErr
			}
			covered := bytes.Equal(existingDigest[:], digest[:]) ||
				messagesHavePrefix(existing, msgs) ||
				messagesHavePrefixWithCompatibleSystem(existing, msgs)
			if !covered && current.normalizedDirty && len(current.rawMessages) > 0 {
				// Judge coverage against the pre-repair transcript too, for the
				// same reason as checkSnapshotWrite: load-time normalization can
				// reshape what is actually stored, and a recovery fork is only
				// warranted when the stored bytes themselves fail to cover this
				// snapshot.
				raw := current.rawMessages
				rawDigest, rawErr := digestSessionMessages(raw)
				if rawErr != nil {
					return RecoveryBranchInfo{}, rawErr
				}
				covered = bytes.Equal(rawDigest[:], digest[:]) ||
					messagesHavePrefix(raw, msgs) ||
					messagesHavePrefixWithCompatibleSystem(raw, msgs)
			}
			if covered {
				return RecoveryBranchInfo{}, ErrSessionRecoveryNotNeeded
			}
		}
	}

	// One stable recovery file per (root branch, writer generation). Nested
	// -recovery- names peel back to the root so conflicts update in place.
	for range 8 {
		recoveryPath, lane := s.isolatedRecoverySessionPath(originalPath)
		info, collision, err := s.writeRecoveryBranchAtPath(recoveryPath, opts, msgs, digest,
			version, rewriteVersion, preview, turns, digestText, 1, shutdown)
		if err != nil {
			return RecoveryBranchInfo{}, err
		}
		if !collision {
			return info, nil
		}
		s.rotateRecoveryLane(lane)
	}
	return RecoveryBranchInfo{}, fmt.Errorf("allocate isolated recovery lane: too many existing collisions")
}


func recoveryParentStem(parent string) string {
	parent = strings.TrimSpace(parent)
	if parent == "" {
		return "session"
	}
	sum := sha256.Sum256([]byte(parent))
	if before, _, ok := strings.Cut(parent, "-recovery-"); ok {
		base := strings.Trim(before, "-_. ")
		if base == "" {
			base = "session"
		}
		base = strings.Trim(truncateUTF8Bytes(base, maxRecoveryParentStemBytes), "-_. ")
		if base == "" {
			base = "session"
		}
		return fmt.Sprintf("%s-%x", base, sum[:6])
	}
	if len(parent) <= maxRecoveryParentStemBytes {
		return parent
	}
	prefix := strings.Trim(truncateUTF8Bytes(parent, maxRecoveryParentStemBytes), "-_. ")
	if prefix == "" {
		prefix = "session"
	}
	return fmt.Sprintf("%s-%x", prefix, sum[:6])
}


func truncateUTF8Bytes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	used := 0
	for i, r := range s {
		size := utf8.RuneLen(r)
		if size < 0 {
			size = 1
		}
		if used+size > max {
			return s[:i]
		}
		used += size
	}
	return s
}


func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

