package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/store"
)

var (
	errTopicHasActiveWork = errors.New("wait for the session to finish, answer pending prompts, and stop background jobs before archiving this topic")
	errTopicArchiveBusy   = errors.New("Reasonix is finishing another session change — wait a moment and retry archiving")
)

var topicArchiveCleanupHookForTest func() error

type topicArchiveTrace struct {
	phase        string
	targetCount  int
	runtimeCount int
}

func (a *App) TrashTopic(topicID string) error {
	return markTopicArchiveForceable(friendlySessionFileError(a.trashTopic(topicID, false)))
}

// tl 1103: the only errors TrashTopicForce can actually clear are the
// active-work gate and a held session lease (it skips the gate and reclaims
// self-held leases). Those failures are marked with a stable machine code over
// the Wails boundary (same "reasonix_error:" channel as inbox_app.go) so the
// frontend can offer the force path — every other failure (file locked by a
// scanner, disk full, …) passes through unmarked and must never be steered
// toward force. The human-readable cause stays in the message: an older
// frontend that only toasts the text loses nothing.
const topicArchiveForceablePrefix = "reasonix_error:topic_archive_forceable: "

type topicArchiveForceableError struct{ cause error }

func (e *topicArchiveForceableError) Error() string {
	return topicArchiveForceablePrefix + e.cause.Error()
}
func (e *topicArchiveForceableError) Unwrap() error { return e.cause }

func markTopicArchiveForceable(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, errTopicHasActiveWork) || errors.Is(err, errSessionBusyElsewhere) || errors.Is(err, errSessionBusyZombie) || errors.Is(err, errSessionBusyUnidentifiedHolder) || errors.Is(err, agent.ErrSessionLeaseHeld) {
		return &topicArchiveForceableError{cause: err}
	}
	return err
}

// TrashTopicForce is the force-archive entry point used by the UI for a topic
// that could not be archived through the regular path (e.g. a session that
// failed to recover left its runtime lease held). It skips the active-work
// gate — a failed session has already Ctrl.Close()'d and is not running — so a
// blocked topic can still be trashed. tl1367 line 4: force is a real unlock —
// before the file moves, stale lease holders (leftover records from dead
// processes, records whose pid was reused by an unrelated program, this
// window's own zombie runtimes) are reclaimed through the same primitives as
// the task-456 takeover, so a stale-held archive completes in ONE call. A
// live holder running this application is real occupation: it is never
// grabbed silently — the removal guard still refuses and the refusal carries
// the holder identity (tl1367 line 1) for the localized "who holds it and how
// to close it" copy.
func (a *App) TrashTopicForce(topicID string) error {
	// tl1367-fix defect B: a self-held session whose tab is open in THIS window
	// is the exact shape "force" exists to clear — close the tab (canceling its
	// run, saving first) before the archive takes the global gates, so one
	// force click completes the archive instead of bouncing the user back with
	// "close that tab first". Must run BEFORE trashTopic's locks: closeTabRuntime
	// needs both the runtime-mutation barrier and sessionRemovalMu. A tab whose
	// work will not stop within the grace window detaches to the background
	// runtime; the archive below then still refuses with the self-holder
	// guidance — a working runtime is never grabbed silently.
	a.closeTopicTabsForForceArchive(topicID)
	err := friendlySessionFileError(a.trashTopic(topicID, true))
	if err != nil && sessionLeaseHolderIdentityMissing(err) {
		// tl1392 copy discipline: the user IS inside the force flow — a
		// refusal that still recommends force loops forever (the package-73
		// self-referential toast), and a bare restart promise over-delivers
		// when the boot restore re-acquires the session. Render the after-force
		// copy: what was swept, what still holds the session, and the steps
		// that exist (report the log / restart and archive before opening).
		// Deliberately NOT wrapped in topicArchiveForceableError: the frontend
		// must not re-offer the button the user just clicked.
		return &sessionLeaseHeldBusyAfterForceError{cause: err}
	}
	return err
}

// closeTopicTabsForForceArchive closes this window's visible tabs bound to the
// topic ahead of a force archive, reusing the user-facing close policy
// (stop_and_close: snapshot save → cancel owned work → bounded wait → close).
// Detached background sessions are left to the existing self-lease reclaim
// paths; only tabs the user can SEE are closed here, because "force" from the
// UI means "this session I am looking at must go".
func (a *App) closeTopicTabsForForceArchive(topicID string) {
	topicID = strings.TrimSpace(topicID)
	if topicID == "" {
		return
	}
	a.mu.RLock()
	tabIDs := make([]string, 0, 4)
	for _, tab := range a.tabs {
		if tab != nil && tab.TopicID == topicID && strings.TrimSpace(tab.ID) != "" {
			tabIDs = append(tabIDs, tab.ID)
		}
	}
	a.mu.RUnlock()
	for _, id := range tabIDs {
		if err := a.CloseTabWithPolicy(id, "stop_and_close"); err != nil {
			slog.Info("desktop: force archive could not close a topic tab before archiving",
				"tab", id, "err", err)
		} else {
			slog.Info("desktop: force archive closed the topic's tab before archiving", "tab", id)
		}
	}
}

func (a *App) topicHasActiveRuntimeWork(topicID string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, tabs := range []map[string]*WorkspaceTab{a.tabs, a.detachedSessions} {
		for _, tab := range tabs {
			if tab != nil && tab.TopicID == topicID && tab.hasActiveRuntimeWork() {
				return true
			}
		}
	}
	return false
}

// reclaimTopicSelfLeasesLocked releases session leases held by this process for
// the archive targets when the holding tab has no real runtime work. Force
// archive (TrashTopicForce) must succeed even when a session could not be
// closed through the normal path — e.g. a tab in a readOnly/failed-restore
// state that topicArchiveLeaseOwners cannot match — leaving its jsonl.lease.lock
// held by this process and blocking the removal guard. Releasing here is safe:
// it only touches leases whose owning tab reports no active work, so a genuinely
// in-use session is never stripped. Caller holds sessionRemovalMu; a.mu is
// taken here.
func (a *App) reclaimTopicSelfLeasesLocked(topicID string, targets []topicTrashTarget) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, target := range targets {
		key := sessionRuntimeKey(target.sessionPath)
		if key == "" {
			continue
		}
		for _, tabs := range []map[string]*WorkspaceTab{a.tabs, a.detachedSessions} {
			for _, tab := range tabs {
				if tab == nil || tab.TopicID != topicID {
					continue
				}
				if tab.sessionLeaseRuntimeKey() != key {
					continue
				}
				if tab.hasActiveRuntimeWork() {
					continue // genuinely in use — protect
				}
				// No runtime activity: release the self-held lease so the
				// removal guard can acquire it. Only release on the exact key
				// so an unrelated rebuild's lease is untouched.
				tab.releaseSessionLeaseForKey(key)
			}
		}
	}
}

// reclaimTopicStaleLeaseHolders is the tl1367 line-4 force-unlock half: force
// archive must actually clear stale lease holders, not merely skip the UI
// confirm. For every archive target whose recorded holder is provably stale —
// a leftover record from a dead process (zombie), a record whose pid was
// reused by an unrelated program, or this window's own leftover record — the
// stale identity is retired through agent.ClearStaleSessionLeaseInfo, the
// verify-free-then-remove primitive the task-456 takeover already uses: a
// live holder's OS lock is never touched. An in-process holder is released
// only in the provable-zombie form of the task-456 takeover sweep (bound the
// lease, no controller, no build in flight) — the form
// reclaimTopicSelfLeasesLocked cannot see because its holder tab carries a
// different or empty TopicID. A live holder running this application is REAL
// occupation: it is never grabbed silently — the ownership acquisition below
// still refuses and the caller's error keeps the holder identity (tl1367
// line 1). Caller holds sessionRemovalMu (same contract as
// reclaimTopicSelfLeasesLocked).
func (a *App) reclaimTopicStaleLeaseHolders(targets []topicTrashTarget) {
	for _, target := range targets {
		key := sessionRuntimeKey(target.sessionPath)
		if key == "" {
			continue
		}
		holder := a.inspectLocalLeaseHolder(target.sessionPath)
		switch holder.verdict {
		case leaseHolderSelf:
			if !holder.locked {
				// Leftover self record on a free lock: retire it like any stale
				// record so the archive starts against a clean identity.
				forceRetireStaleLeaseRecord(key, holder.info)
				continue
			}
			// The lock is held inside this process. reclaimTopicSelfLeasesLocked
			// already released no-work owners matched by topic; what remains is
			// the task-456 zombie form. Release exactly those provably dead
			// holders; a functional owner survives and the removal guard keeps
			// refusing (never a silent grab of a working runtime).
			a.releaseZombieSessionLeaseHoldersForKey(key, nil)
		case leaseHolderForeignDead, leaseHolderForeignReused:
			// Stale record forms: a dead holder, or a pid now owned by an
			// unrelated program (tl1367 line 3 verdict). ClearStaleSessionLeaseInfo
			// removes the record only while the OS lock is provably free, so a
			// live unidentified holder of the lock file itself is never touched.
			forceRetireStaleLeaseRecord(key, holder.info)
		case leaseHolderForeignLive:
			// Real occupation: a live process running this application holds the
			// session. Force reports who holds it and how to close it — the
			// refusal below carries the identity; nothing is cleared here.
		case leaseHolderNone:
			// tl1383: an absent or UNREADABLE holder record used to be invisible
			// to this sweep — force had no verdict to act on and the refusal
			// repeated forever (the 2026-11-04 holder_class=unknown pid=0
			// archive refusals). Both repairs stay provable-stale-only:
			// ClearStaleSessionLeaseInfo removes a record only while the OS
			// lock is free, and the zombie sweep releases only bound-lease
			// holders with no controller and no build in flight — a live holder
			// survives both untouched and the ownership acquisition below
			// refuses with the unidentified-holder copy.
			forceRetireStaleLeaseRecord(key, holder.info)
			a.releaseZombieSessionLeaseHoldersForKey(key, nil)
			// tl1392: the third refusal source no force lever could reach — an
			// IN-PROCESS orphan (a registry entry, or a live lease object with
			// no tab binding at all, whose disk record was already torn). The
			// tab/zombie sweeps above match holders through tabs; an unbound
			// orphan misses both, so "tear the disk record" succeeded while the
			// next acquire kept refusing (the package-73 force loop). The
			// sweep's own evidence discipline (identity missing/torn/pid-less
			// on disk) decides; a usable holder record is never touched.
			if cleared, sweepErr := agent.SweepOrphanInProcessSessionLease(key); sweepErr != nil {
				slog.Warn("desktop: force archive orphan sweep failed",
					"err", sweepErr)
			} else if cleared > 0 {
				slog.Info("desktop: force archive cleared an in-process orphan lease holder",
					"cleared", cleared)
			}
		}
	}
}

// forceRetireStaleLeaseRecord retires one stale lease record during a force
// archive and logs the retired holder identity (tl1367 line 1 discipline: a
// destructive unlock leaves a traceable record). Logged values name the holder
// process, never the session path.
func forceRetireStaleLeaseRecord(key string, info *agent.SessionLeaseInfo) {
	cleared, err := agent.ClearStaleSessionLeaseInfo(key)
	if err != nil {
		slog.Warn("desktop: force archive could not retire a stale lease record",
			"holder_pid", holderPIDForLog(info), "err", err)
		return
	}
	if cleared {
		slog.Info("desktop: force archive retired a stale lease record",
			"holder_pid", holderPIDForLog(info))
	}
}

func (a *App) trashTopic(topicID string, force bool) (retErr error) {
	topicID = strings.TrimSpace(topicID)
	if topicID == "" {
		return fmt.Errorf("topicID is required")
	}
	started := time.Now()
	trace := topicArchiveTrace{phase: "start"}
	defer func() {
		outcome := "ok"
		if retErr != nil {
			outcome = "failed"
		}
		slog.Debug("desktop: topic archive timing", "outcome", outcome, "phase", trace.phase,
			"total_ms", time.Since(started).Milliseconds(), "target_count", trace.targetCount, "runtime_count", trace.runtimeCount)
	}()
	fallback, changedDirs, err := a.commitTopicArchive(topicID, &trace, force)
	if err != nil {
		return err
	}
	if fallback.workspaceRoot != "" {
		changedDirs = append(changedDirs, desktopSessionDir(fallback.workspaceRoot))
	} else if fallback.needs {
		changedDirs = append(changedDirs, desktopSessionDir(globalWorkspaceRoot()))
	}
	// Remove abandoned transient blanks before fallback construction registers
	// the project; otherwise reconciliation can promote a default-titled blank
	// into the topic registry and make it ineligible for cleanup.
	trace.phase = "discard_existing_blanks"
	a.discardUnusedTransientBlankSessions(changedDirs, "")
	if fallback.needs {
		trace.phase = "open_fallback"
		fallback.topicID = ""
		if err := a.openFallbackRuntime(fallback); err != nil {
			// Runtime construction errors can include provider configuration
			// details, so keep this recovery diagnostic value-free.
			slog.Warn("desktop: open fallback after topic archive failed")
		}
	}
	keepPath := ""
	a.mu.RLock()
	if tab := a.tabs[a.activeTabID]; tab != nil {
		keepPath = tab.SessionPath
	}
	a.mu.RUnlock()
	trace.phase = "discard_unused_blanks"
	a.discardUnusedTransientBlankSessions(changedDirs, keepPath)
	trace.phase = "notify"
	if len(changedDirs) > 0 {
		a.emitProjectTreeChangedForSessionDirs(changedDirs...)
	} else {
		a.emitProjectTreeMetadataChanged()
	}
	return nil
}

// commitTopicArchive runs the single-topic archive: it takes the two global
// mutation gates, applies the active-work gate, scans the trash targets, then
// hands off to commitTopicArchiveTargets for the remaining phases. tl1240
// splits the gates/checks/scan from the per-topic core so the batch endpoint
// (topic_archive_batch.go) can hold the gates once and share one disk scan
// across the whole batch. Phase order matters: the active-work gate must reject
// before the scan's runtime-tabs merge touches live tab controllers (a
// readOnly/pending tab may not implement the full session API).
func (a *App) commitTopicArchive(topicID string, trace *topicArchiveTrace, force bool) (fallbackRuntimeTarget, []string, error) {
	trace.phase = "runtime_lock"
	releaseRuntime, ok := a.tryLockRuntimeMutation("trash-topic")
	if !ok {
		return fallbackRuntimeTarget{}, nil, errTopicArchiveBusy
	}
	defer releaseRuntime()
	trace.phase = "removal_lock"
	if !a.sessionRemovalMu.TryLock() {
		return fallbackRuntimeTarget{}, nil, errTopicArchiveBusy
	}
	defer a.sessionRemovalMu.Unlock()
	trace.phase = "active_work_check"
	if !force && a.topicHasActiveRuntimeWork(topicID) {
		return fallbackRuntimeTarget{}, nil, errTopicHasActiveWork
	}
	trace.phase = "target_scan"
	targets, err := a.topicTrashTargets(topicID)
	if err != nil {
		return fallbackRuntimeTarget{}, nil, err
	}
	return a.commitTopicArchiveTargets(topicID, targets, trace, force)
}

// commitTopicArchiveTargets runs the post-scan phases of the topic archive
// assuming both global mutation gates (runtime rebuild/admission +
// sessionRemovalMu) are already held by the caller and the topic has already
// passed its active-work gate. targets comes from the caller's target scan; a
// nil/empty slice behaves like the single path with no matches. force is only
// set by the single-topic force entry point — the batch endpoint is regular
// archive only, so a topic that needs force stays a per-topic action.
func (a *App) commitTopicArchiveTargets(topicID string, targets []topicTrashTarget, trace *topicArchiveTrace, force bool) (fallbackRuntimeTarget, []string, error) {
	trace.phase = "force_reclaim_self_lease"
	if force {
		a.reclaimTopicSelfLeasesLocked(topicID, targets)
		// tl1367 line 4: force is a real unlock — stale holders (dead pid,
		// reused pid, self leftover/zombie) are reclaimed before the ownership
		// acquisition so the archive completes in one call; a live foreign
		// holder is left untouched and still refuses with its identity.
		a.reclaimTopicStaleLeaseHolders(targets)
	}
	trace.targetCount = len(targets)
	changedDirs := make([]string, 0, len(targets))
	for _, target := range targets {
		changedDirs = append(changedDirs, target.dir)
	}
	trace.phase = "snapshot"
	removed := a.captureTopicRuntimeBindings(topicID)
	trace.runtimeCount = len(removed)
	if err := a.snapshotTopicRuntimeBindings(removed); err != nil {
		return fallbackRuntimeTarget{}, nil, err
	}
	trace.phase = "acquire_removal_ownership"
	ownership, err := acquireTopicArchiveOwnership(targets, removed)
	if err != nil {
		return fallbackRuntimeTarget{}, nil, err
	}
	defer ownership.release()
	trace.phase = "mark_cleanup_pending"
	rollbackMarkers, err := markTopicArchiveCleanupPending(topicID, targets)
	if err != nil {
		ownership.rollback()
		return fallbackRuntimeTarget{}, nil, err
	}
	trace.phase = "detach_runtimes"
	fallback, unchanged := a.removeTopicRuntimeBindingsIfUnchanged(topicID, removed)
	if !unchanged {
		rollbackMarkers()
		ownership.rollback()
		return fallbackRuntimeTarget{}, nil, errTopicArchiveBusy
	}
	a.finalizeRemovedTopicRuntimes(removed)
	destroyBegun := false
	closedRemoved := map[control.SessionAPI]bool{}
	defer func() {
		if destroyBegun {
			a.closeRemainingRemovedSessionRuntimesAfterDestroyAdmissionHeld(removed, closedRemoved)
		} else {
			a.closeRemainingRemovedSessionRuntimesAdmissionHeld(removed, closedRemoved)
		}
	}()
	trace.phase = "teardown"
	destroyBatches := make([][]control.SessionDestroyHandle, len(targets))
	for i, target := range targets {
		destroys := a.destroyHandlesForSession(target.dir, target.sessionPath, removed)
		destroyBatches[i] = destroys
		destroyBegun = destroyBegun || len(destroys) > 0
	}
	timedOutTargets := waitDestroyHandleBatches(destroyBatches)
	for i, target := range targets {
		destroys := destroyBatches[i]
		a.closeRemovedSessionRuntimesForSessionAfterDestroyAdmissionHeld(removed, target.dir, target.sessionPath, closedRemoved)
		a.removeSessionCatalogPath(target.sessionPath, "topic_archived")
		if timedOutTargets[i] {
			guard := ownership.take(target.sessionPath)
			go delayedDesktopTopicTrash(target.dir, target.sessionPath, target.key, guard, destroys)
			continue
		}
		trace.phase = "move_artifacts"
		var err error
		if hook := topicArchiveCleanupHookForTest; hook != nil {
			err = hook()
		}
		// Take the guard only when it will actually be used: on a pre-trash
		// failure the guard must stay in the batch so the deferred
		// ownership.release() retires its locks (a taken-and-dropped guard
		// would wedge the session's locks until restart).
		if err == nil {
			guard := ownership.take(target.sessionPath)
			if sessionTrashTargetFileMissing(target.sessionPath) {
				// tl1383: the transcript is gone — nothing to protect or move.
				// Release any converted guard (its lock files are pure residue
				// for a missing file), settle the cleanup-pending marker so
				// startup reconciliation does not re-run, and let the topic
				// metadata deletion below retire the sidebar entry.
				if guard != nil {
					if relErr := guard.RemoveSidecarsAndRelease(); relErr != nil {
						slog.Warn("desktop: topic archive could not retire the guard sidecars of a missing session file", "err", relErr)
					}
					guard = nil
				}
				err = agent.ClearCleanupPending(target.sessionPath)
			} else {
				err = trashSessionArtifactsWithGuard(target.dir, target.sessionPath, target.key, guard)
			}
		}
		finishDestroyHandles(destroys)
		if err != nil {
			// Cleanup-pending is the durable commit point. Once bindings have
			// detached, report the archive as accepted and let startup
			// reconciliation finish any filesystem operation that could not.
			slog.Warn("desktop: topic archive cleanup remains pending")
		}
	}
	trace.phase = "delete_topic_metadata"
	if err := a.deleteTopic(topicID); err != nil {
		slog.Warn("desktop: topic archive metadata cleanup remains pending")
	} else if err := clearTopicArchiveMetadataPending(topicID); err != nil {
		slog.Warn("desktop: topic archive metadata marker cleanup remains pending")
	}
	return fallback, changedDirs, nil
}

func markTopicArchiveCleanupPending(topicID string, targets []topicTrashTarget) (func(), error) {
	if err := markTopicArchiveMetadataPending(topicID, targets); err != nil {
		return nil, err
	}
	marked := make([]string, 0, len(targets))
	rollback := func() {
		for _, path := range marked {
			if err := agent.ClearCleanupPending(path); err != nil {
				slog.Warn("desktop: rollback topic archive marker failed")
			}
		}
		if err := clearTopicArchiveMetadataPending(topicID); err != nil {
			slog.Warn("desktop: rollback topic archive metadata marker failed")
		}
	}
	for _, target := range targets {
		if err := agent.MarkCleanupPending(target.sessionPath, "delete"); err != nil {
			rollback()
			return rollback, err
		}
		marked = append(marked, target.sessionPath)
	}
	return rollback, nil
}

type topicTrashTarget struct {
	dir         string
	sessionPath string
	key         string
}

func (a *App) topicTrashTargets(topicID string) ([]topicTrashTarget, error) {
	targets, err := a.topicTrashTargetsFromDirIndexes(topicID, topicSessionIndexForDir)
	if err != nil {
		return nil, err
	}
	return a.appendTopicRuntimeTrashTargets(topicID, targets)
}

// topicTrashTargetsFromDirIndexes resolves a topic's on-disk trash targets
// using the given per-directory index getter. The single-topic path passes
// topicSessionIndexForDir directly (index built per dir, cached by signature as
// before); the batch path passes a memoized getter so the whole batch pays one
// disk pass per directory instead of one per topic (tl1240). Runtime tab
// bindings are NOT consulted here — the caller layers
// appendTopicRuntimeTrashTargets after its active-work gate, because the
// runtime merge dereferences live tab controllers that a gated-off topic must
// never touch.
func (a *App) topicTrashTargetsFromDirIndexes(topicID string, dirIndex func(dir string) (topicSessionDirIndex, error)) ([]topicTrashTarget, error) {
	topicID = strings.TrimSpace(topicID)
	var targets []topicTrashTarget
	seen := map[string]bool{}
	addTarget := func(dir, path string) error {
		sessionPath, key, err := validateSessionPath(dir, path)
		if err != nil {
			return err
		}
		id := dir + "\x00" + sessionPath
		if seen[id] {
			return nil
		}
		seen[id] = true
		if err := validateSessionTrashTarget(dir, sessionPath, key); err != nil {
			return err
		}
		targets = append(targets, topicTrashTarget{dir: dir, sessionPath: sessionPath, key: key})
		return nil
	}
	for _, dir := range a.knownSessionDirs() {
		index, err := dirIndex(dir)
		if err != nil {
			return nil, err
		}
		for _, match := range index.byTopic[topicID] {
			if !agent.IsCleanupPending(match.path) {
				if err := addTarget(dir, match.path); err != nil {
					return nil, err
				}
			}
		}
	}
	return targets, nil
}

// appendTopicRuntimeTrashTargets merges the topic's live tab bindings into the
// target set. It reads App state, so callers hold the global mutation gates.
func (a *App) appendTopicRuntimeTrashTargets(topicID string, targets []topicTrashTarget) ([]topicTrashTarget, error) {
	topicID = strings.TrimSpace(topicID)
	seen := map[string]bool{}
	for _, target := range targets {
		seen[target.dir+"\x00"+target.sessionPath] = true
	}
	addTarget := func(dir, path string) ([]topicTrashTarget, error) {
		sessionPath, key, err := validateSessionPath(dir, path)
		if err != nil {
			return targets, err
		}
		id := dir + "\x00" + sessionPath
		if seen[id] {
			return targets, nil
		}
		seen[id] = true
		if err := validateSessionTrashTarget(dir, sessionPath, key); err != nil {
			return targets, err
		}
		return append(targets, topicTrashTarget{dir: dir, sessionPath: sessionPath, key: key}), nil
	}
	a.mu.RLock()
	var runtimeTargets []struct{ dir, path string }
	for _, tab := range a.runtimeTabsLocked() {
		if tab == nil || tab.TopicID != topicID {
			continue
		}
		if path := canonicalTabSessionPath(tab.currentSessionPath()); path != "" {
			dir := tabSessionDir(tab)
			if filepath.IsAbs(path) {
				dir = filepath.Dir(path)
			}
			runtimeTargets = append(runtimeTargets, struct{ dir, path string }{dir: dir, path: path})
		}
	}
	a.mu.RUnlock()
	var err error
	for _, target := range runtimeTargets {
		targets, err = addTarget(target.dir, target.path)
		if err != nil {
			return targets, err
		}
	}
	return targets, nil
}

func topicIndexedInRegistry(scope, workspaceRoot, topicID string) bool {
	topicID = strings.TrimSpace(topicID)
	if topicID == "" {
		return false
	}
	if strings.TrimSpace(loadTopicTitles(topicTitleRoot(scope, workspaceRoot))[topicID]) != "" {
		return true
	}
	f := loadProjectsFile()
	if scope != "project" {
		return containsDesktopString(f.GlobalTopics, topicID)
	}
	if i := projectIndexByRoot(f.Projects, workspaceRoot); i >= 0 {
		return containsDesktopString(f.Projects[i].Topics, topicID)
	}
	return false
}

func (a *App) discardUnusedTransientBlankSessions(dirs []string, keepPath string) {
	// Forced legacy repair promotes indexed sidecars under this same lock. Hold
	// it through classification, deletion, and scoped registry cleanup so a
	// concurrent reconcile cannot reinsert a zero-byte ghost after we sweep it.
	legacyMigrationMu.Lock()
	defer legacyMigrationMu.Unlock()

	keepPath = canonicalTabSessionPath(strings.TrimSpace(keepPath))
	kept := map[string]bool{}
	if keepPath != "" {
		kept[keepPath] = true
	}
	if a != nil {
		a.mu.RLock()
		for _, tabs := range []map[string]*WorkspaceTab{a.tabs, a.detachedSessions} {
			for _, tab := range tabs {
				if tab == nil {
					continue
				}
				if path := canonicalTabSessionPath(strings.TrimSpace(tab.SessionPath)); path != "" {
					kept[path] = true
				}
			}
		}
		a.mu.RUnlock()
	}
	siblingDirs := append([]string(nil), dirs...)
	if a != nil {
		siblingDirs = append(siblingDirs, a.knownSessionDirs()...)
	}
	seen := map[string]bool{}
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !store.IsSessionTranscriptName(name) {
				continue
			}
			path := filepath.Join(dir, name)
			if kept[canonicalTabSessionPath(path)] {
				continue
			}
			if !unusedTransientBlankSession(dir, path) {
				continue
			}
			meta, hasMeta, _ := agent.LoadBranchMeta(path)
			removed := discardTransientBlankSessionArtifacts(path)
			if removed && hasMeta && !transientTopicHasSibling(siblingDirs, path, meta) {
				cleanupTransientBlankTopicRegistration(meta)
			}
			if removed && a != nil {
				a.removeSessionCatalogPath(path, "transient_blank_discarded")
			}
		}
	}
}

func unusedTransientBlankSession(dir, path string) bool {
	resolved, ok := pinnedTabSessionPath(dir, path)
	if !ok {
		return false
	}
	info, err := os.Stat(resolved)
	if err != nil || info.IsDir() || info.Size() != 0 {
		return false
	}
	meta, ok, err := agent.LoadBranchMeta(resolved)
	if err != nil || !ok {
		return true
	}
	topicID := strings.TrimSpace(meta.TopicID)
	if topicID == "" {
		return true
	}
	if !isDefaultTopicTitle(meta.TopicTitle) && strings.TrimSpace(meta.TopicTitle) != "" {
		return false
	}
	// A zero-byte default sidecar stays transient after registry projection.
	// The caller's keep set protects every visible or detached runtime; registry
	// presence alone cannot prove user content.
	return true
}

func transientTopicHasSibling(dirs []string, excludedPath string, target agent.BranchMeta) bool {
	seen := make(map[string]bool, len(dirs))
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		key := projectRootKey(dir)
		if dir == "" || seen[key] {
			continue
		}
		seen[key] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !store.IsSessionTranscriptName(entry.Name()) {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			if sameDesktopPath(path, excludedPath) {
				continue
			}
			meta, ok, err := agent.LoadBranchMeta(path)
			if err != nil || !ok || strings.TrimSpace(meta.TopicID) != strings.TrimSpace(target.TopicID) {
				continue
			}
			sameRoot := meta.DefaultScope() != "project" || sameProjectRoot(meta.WorkspaceRoot, target.WorkspaceRoot)
			if meta.DefaultScope() == target.DefaultScope() && sameRoot {
				return true
			}
		}
	}
	return false
}

func cleanupTransientBlankTopicRegistration(meta agent.BranchMeta) {
	topicID := strings.TrimSpace(meta.TopicID)
	if topicID == "" {
		return
	}
	scope, root := meta.DefaultScope(), normalizeProjectRoot(meta.WorkspaceRoot)
	// tl1208: 瞬时空壳的注册清理走写口收点——只摘自己 scope（onlyScope
	// 保留原语义）、不写墓碑（原语义：瞬时空壳可被后续合法登记复活）。
	_ = unregisterTopic(topicID, topicUnregisterOpts{
		tombstone: false,
		onlyScope: &topicScopeRef{scope: scope, root: root},
	})
	titleRoot := topicTitleRoot(scope, root)
	_ = deleteTopicState(titleRoot, topicID)
}
