package control

import (
	"errors"
	"log/slog"
	"strings"
	"time"

	"reasonix/internal/agent"
)

// forkTraceLogMessage is the one greppable phrase every recovery decision
// line carries (tl1443); fork_decision/fork_reason/fork_trigger are the
// aggregation keys. Recipes for daily and per-trigger counts live in doctor.
const forkTraceLogMessage = "controller: recovery fork decision"

// Fork decisions: what one recovery decision did with a failed save.
const (
	forkDecisionFork    = "fork"
	forkDecisionAdopt   = "adopt"
	forkDecisionRewrite = "rewrite"
	forkDecisionDrop    = "drop"
	forkDecisionError   = "error"
)

// Fork reasons: which rung of the recovery ladder produced the decision.
const (
	forkReasonNoTarget        = "no_target"
	forkReasonMetaReconciled  = "meta_reconciled"
	forkReasonRevLagTakeover  = "rev_lag_takeover"
	forkReasonBrake           = "fork_brake"
	forkReasonStalePrefix     = "stale_prefix"
	forkReasonQuota           = "fork_quota"
	forkReasonCovered         = "recovery_not_needed"
	forkReasonCoveredDropped  = "recovery_not_needed_dropped"
	forkReasonSaveFailed      = "recovery_save_failed"
	forkReasonCommitFailed    = "recovery_commit_failed"
	forkReasonDiverged        = "diverged"
	forkReasonShutdownLock    = "shutdown_lock_timeout"
	forkReasonExternalRemoval = "external_removal"
	forkReasonPendingRewrite  = "pending_rewrite"
	forkReasonSameRevRewrite  = "same_rev_rewrite"
	forkReasonCheckpoint      = "checkpoint_conflict"
	forkReasonUnclassified    = "unclassified"
)

// Fallthrough marks: a rung that applied to the conflict but could not settle
// it, so a later rung (possibly the fork) owned the decision.
const (
	forkFellTakeoverDeclined = "takeover_declined"
	forkFellBrakeAdoptFailed = "brake_adopt_failed"
	forkFellStaleAdoptFailed = "stale_adopt_failed"
	forkFellQuotaAdoptFailed = "quota_adopt_failed"
)

// forkPeerNotTraced: the disk revision came from this process but no traced
// save committed it (evicted slot or a non-save writer).
const forkPeerNotTraced = "not_traced"

// forkObservation is the controller's observe-only recovery bookkeeping,
// guarded by Controller.mu; no recovery decision reads it.
type forkObservation struct {
	windowConflicts int // conflicts met inside the current brake window
	forks           int // snapshot-conflict forks this controller created
}

type forkBrakeState struct {
	active    bool
	seq       int           // nth conflict inside the current window, 0 outside
	remaining time.Duration // window left at decision time
	sinceLast time.Duration // since this controller's last fork, -1 when none
	forks     int           // forks before this decision
}

type forkLocalSide struct {
	known      bool
	rev        int64
	digest     string
	baseDigest string
	msgs       int
}

type forkPeer struct {
	found  bool
	origin forkOrigin
	age    time.Duration
}

// forkTrace accumulates one recovery decision's evidence. Every method is
// nil-safe so ladder code can trace unconditionally.
type forkTrace struct {
	started        time.Time
	path           string
	mode           string
	origin         forkOrigin
	conflict       *agent.SessionSnapshotConflictError
	local          forkLocalSide
	writer         string
	writerPID      int
	peer           *forkPeer
	brake          *forkBrakeState
	rewritePending bool
	strikes        int
	fell           []string
	topicForks     int
	decision       string
	reason         string
	recovery       string
	existing       bool
}

func newForkTrace(path, mode string, saveErr error) *forkTrace {
	t := &forkTrace{started: time.Now(), path: path, mode: mode, strikes: -1, topicForks: -1}
	t.origin = classifyForkFrames(captureForkFrames(1))
	var conflict *agent.SessionSnapshotConflictError
	if errors.As(saveErr, &conflict) && conflict != nil {
		t.conflict = conflict
		t.writer, t.writerPID = agent.LedgerWriterOrigin(conflict.Identity.LedgerWriter)
		if t.writer == agent.LedgerWriterSelf {
			t.peer = lookupForkPeer(path, conflict.DiskRevision, t.started)
		}
	}
	return t
}

func lookupForkPeer(path string, revision int64, now time.Time) *forkPeer {
	commit, ok := agent.RecentLedgerCommit(path, revision)
	if !ok {
		return &forkPeer{}
	}
	return &forkPeer{found: true, origin: classifyForkFrames(forkFramesFromPCs(commit.PCs)), age: now.Sub(commit.At)}
}

// beginForkTrace opens a controller-level trace: stack origin, both sides of
// the conflict, and the brake window as this controller sees it right now.
func (c *Controller) beginForkTrace(path, mode string, saveErr error) *forkTrace {
	t := newForkTrace(path, mode, saveErr)
	c.mu.Lock()
	brake := c.observeForkBrakeLocked(t.started)
	c.mu.Unlock()
	t.brake = &brake
	if c.executor != nil {
		if s := c.executor.Session(); s != nil {
			t.rewritePending = s.NeedsRewriteSave()
			if t.conflict == nil {
				t.local = forkLocalSideOf(s, path)
			}
		}
	}
	return t
}

func (c *Controller) observeForkBrakeLocked(now time.Time) forkBrakeState {
	state := forkBrakeState{sinceLast: -1, forks: c.forkObs.forks}
	if c.lastSnapshotForkAt.IsZero() {
		return state
	}
	state.sinceLast = now.Sub(c.lastSnapshotForkAt)
	if state.sinceLast < snapshotForkBrakeWindow {
		c.forkObs.windowConflicts++
		state.active = true
		state.seq = c.forkObs.windowConflicts
		state.remaining = snapshotForkBrakeWindow - state.sinceLast
	}
	return state
}

func forkLocalSideOf(s *agent.Session, path string) forkLocalSide {
	side := forkLocalSide{known: true, msgs: s.Len()}
	if persisted, ok := s.PersistedState(path); ok {
		side.rev = persisted.Revision
		side.baseDigest = persisted.DigestHex
	}
	return side
}

func (c *Controller) noteConflictStrikes(t *forkTrace) {
	if t == nil {
		return
	}
	c.mu.Lock()
	t.strikes = c.conflictStrikes
	c.mu.Unlock()
}

// finishForkTrace emits the decision line. A ladder exit that forgot to
// decide is still logged, derived from its outcome, as "unclassified".
func (c *Controller) finishForkTrace(t *forkTrace, outcome conflictOutcome, err error) {
	if t == nil {
		return
	}
	if t.decision == "" {
		t.decide(forkDecisionForOutcome(outcome, err), forkReasonUnclassified)
	}
	if t.decision == forkDecisionFork && t.conflict != nil {
		c.mu.Lock()
		c.forkObs.forks++
		c.forkObs.windowConflicts = 0
		c.mu.Unlock()
	}
	t.emit(err)
}

func forkDecisionForOutcome(outcome conflictOutcome, err error) string {
	if err != nil {
		return forkDecisionError
	}
	switch outcome {
	case conflictForkedBranch:
		return forkDecisionFork
	case conflictAdoptedDisk:
		return forkDecisionAdopt
	case conflictTookOver:
		return forkDecisionRewrite
	default:
		return forkDecisionDrop
	}
}

func (t *forkTrace) decide(decision, reason string) {
	if t != nil {
		t.decision, t.reason = decision, reason
	}
}

func (t *forkTrace) fall(mark string) {
	if t != nil {
		t.fell = append(t.fell, mark)
	}
}

func (t *forkTrace) forked(info agent.RecoveryBranchInfo) {
	if t == nil {
		return
	}
	t.recovery, t.existing = agent.BranchID(info.Path), info.Existing
	if t.conflict == nil && t.local.known {
		t.local.digest = info.Digest
	}
}

func (t *forkTrace) emit(err error) {
	if t == nil {
		return
	}
	slog.Warn(forkTraceLogMessage, t.logAttrs(err)...)
}

func (t *forkTrace) logAttrs(err error) []any {
	attrs := []any{
		"fork_decision", t.decision,
		"fork_reason", t.reason,
		"fork_trigger", t.origin.trigger,
		"fork_entry", t.origin.entry,
		"mode", t.mode,
		"session", agent.BranchID(t.path),
	}
	attrs = append(attrs, t.sideAttrs()...)
	if p := t.peer; p != nil {
		if p.found {
			attrs = append(attrs, "peer_trigger", p.origin.trigger, "peer_entry", p.origin.entry,
				"peer_age_ms", p.age.Milliseconds(), "peer_caller", p.origin.caller)
		} else {
			attrs = append(attrs, "peer_trigger", forkPeerNotTraced)
		}
	}
	if b := t.brake; b != nil {
		forks := b.forks
		if t.decision == forkDecisionFork && t.conflict != nil {
			forks++
		}
		attrs = append(attrs, "brake_active", b.active, "brake_seq", b.seq,
			"brake_remaining_ms", b.remaining.Milliseconds(), "since_last_fork_ms", durationMsOrNegative(b.sinceLast),
			"controller_forks", forks)
	}
	attrs = append(attrs, "rewrite_pending", t.rewritePending)
	if t.strikes >= 0 {
		attrs = append(attrs, "conflict_strikes", t.strikes)
	}
	if len(t.fell) > 0 {
		attrs = append(attrs, "fallthrough", strings.Join(t.fell, "+"))
	}
	if t.topicForks >= 0 {
		attrs = append(attrs, "topic_forks", t.topicForks, "fork_quota", agent.SnapshotForkQuotaPerTopic)
	}
	if t.recovery != "" {
		attrs = append(attrs, "recovery", t.recovery, "existing", t.existing)
	}
	attrs = append(attrs, "elapsed_ms", time.Since(t.started).Milliseconds(), "caller", t.origin.caller)
	if err != nil {
		attrs = append(attrs, "err", err)
	}
	return attrs
}

// sideAttrs renders both sides as rev/digest/msgs triples: src is the
// transcript this runtime tried to save, disk is what the ledger holds.
func (t *forkTrace) sideAttrs() []any {
	if c := t.conflict; c != nil {
		return []any{
			"conflict_kind", string(c.Kind),
			"src_rev", c.BaseRevision, "src_digest", agent.ShortDigest(c.Identity.SnapshotDigest), "src_msgs", c.SnapshotMessages,
			"base_digest", agent.ShortDigest(c.Identity.BaseDigest),
			"disk_rev", c.DiskRevision, "disk_digest", agent.ShortDigest(c.Identity.DiskDigest), "disk_msgs", c.ExistingMessages,
			"ledger_digest", agent.ShortDigest(c.Identity.LedgerDigest),
			"disk_writer", t.writer, "disk_writer_pid", t.writerPID,
		}
	}
	if t.local.known {
		return []any{
			"src_rev", t.local.rev, "src_digest", agent.ShortDigest(t.local.digest), "src_msgs", t.local.msgs,
			"base_digest", agent.ShortDigest(t.local.baseDigest),
		}
	}
	return nil
}

func durationMsOrNegative(d time.Duration) int64 {
	if d < 0 {
		return -1
	}
	return d.Milliseconds()
}

// fillDiagnostic copies the trace into a conflicts.jsonl record. The record
// stays content-free: enums, short digests, code locations, never paths.
func (t *forkTrace) fillDiagnostic(rec *snapshotConflictDiagnostic) {
	if t == nil || rec == nil {
		return
	}
	rec.Decision, rec.Reason = t.decision, t.reason
	rec.Trigger, rec.Entry, rec.Chain = t.origin.trigger, t.origin.entry, t.origin.caller
	if c := t.conflict; c != nil {
		rec.SnapshotDigest = agent.ShortDigest(c.Identity.SnapshotDigest)
		rec.BaseDigest = agent.ShortDigest(c.Identity.BaseDigest)
		rec.DiskDigest = agent.ShortDigest(c.Identity.DiskDigest)
		rec.LedgerDigest = agent.ShortDigest(c.Identity.LedgerDigest)
		rec.DiskWriter, rec.DiskWriterPID = t.writer, t.writerPID
	}
	if p := t.peer; p != nil {
		rec.PeerTrigger = forkPeerNotTraced
		if p.found {
			rec.PeerTrigger, rec.PeerChain, rec.PeerAgeMs = p.origin.trigger, p.origin.caller, p.age.Milliseconds()
		}
	}
	if b := t.brake; b != nil {
		rec.BrakeActive, rec.BrakeSeq = b.active, b.seq
		rec.BrakeRemainingMs = b.remaining.Milliseconds()
		rec.SinceLastForkMs = durationMsOrNegative(b.sinceLast)
	}
	rec.RewritePending = t.rewritePending
	rec.Fallthrough = strings.Join(t.fell, "+")
}

// traceConflictEscalation logs a conflict the save path settled (or failed to
// settle) before the recovery ladder: a rewrite escalation, or a tool
// checkpoint conflict, which never reaches the ladder at all.
func traceConflictEscalation(s *agent.Session, path string, conflictErr error, reason string, resultErr error, checkpoint bool) {
	if !checkpoint && errors.Is(resultErr, agent.ErrSessionSnapshotConflict) {
		return // the recovery ladder logs the final decision
	}
	mode := "snapshot"
	if checkpoint {
		mode = "checkpoint"
	}
	t := newForkTrace(path, mode, conflictErr)
	if s != nil {
		t.rewritePending = s.NeedsRewriteSave()
	}
	switch {
	case resultErr == nil:
		t.decide(forkDecisionRewrite, reason)
	case reason == "":
		t.decide(forkDecisionError, forkReasonCheckpoint)
	default:
		t.decide(forkDecisionError, reason)
	}
	t.emit(resultErr)
}
