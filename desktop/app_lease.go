package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
)

type activeRuntimeWork struct {
	running        bool
	pendingPrompt  bool
	backgroundJobs int
}

func controllerActiveRuntimeWork(ctrl control.SessionAPI) activeRuntimeWork {
	if ctrl == nil {
		return activeRuntimeWork{}
	}
	status := ctrl.RuntimeStatus()
	return activeRuntimeWork{
		running:        status.Running,
		pendingPrompt:  status.PendingPrompt,
		backgroundJobs: status.BackgroundJobs,
	}
}

func (w activeRuntimeWork) active() bool {
	return w.running || w.pendingPrompt || w.backgroundJobs > 0
}

func controllerHasActiveRuntimeWork(ctrl control.SessionAPI) bool {
	return controllerActiveRuntimeWork(ctrl).active()
}

// rebuildBusyError reports a rebuild rejected because the controller still has
// a running turn, pending prompt, or background jobs. Typed so the
// deferred-rebuild retry loop can keep waiting instead of giving up.
type rebuildBusyError struct {
	setting string
	work    activeRuntimeWork
}

func (e *rebuildBusyError) Error() string {
	return fmt.Sprintf(
		"active work is still running; running=%t; pending_prompt=%t; background_jobs=%d; finish or cancel the current turn, answer pending prompts, and stop background jobs before changing %s",
		e.work.running,
		e.work.pendingPrompt,
		e.work.backgroundJobs,
		e.setting,
	)
}

func rebuildControllerActiveWorkErrorFor(ctrl control.SessionAPI, setting string) error {
	work := controllerActiveRuntimeWork(ctrl)
	if !work.active() {
		return nil
	}
	return &rebuildBusyError{setting: setting, work: work}
}

type sessionLeaseBusyError struct {
	setting string
	err     error
}

func (e *sessionLeaseBusyError) Error() string {
	// The raw SessionLeaseError text carries the session path and the
	// holder's host-pid-writer id; every user-facing surface must render
	// this wrapper instead. An empty setting means the failure gated opening the session itself (startup bind), not changing a setting on it. Task 272 L3: when the holder resolves to a concrete pid that is not this process, name it and hand over an EXISTING next step — restart reaps leftovers automatically (ReapOrphanSpawns), taskkill is there for impatience. The old "close the other window" advice was dead-end advice for the orphan-serve incidents ②③: there was no other window.
	setting := strings.TrimSpace(e.setting)
	base := "this session is already open in another Reasonix window or still running in the background; close the other window or open a copy"
	var leaseErr *agent.SessionLeaseError
	if errors.As(e.err, &leaseErr) && leaseErr != nil && leaseErr.Info != nil {
		if holderPID := leaseErr.Info.PID; holderPID > 0 {
			if holderPID == os.Getpid() {
				// Task 485: the holder resolves to THIS process — "another
				// Reasonix window" is a dead-end lead (the 2026-10-05 lease
				// leak logged 568 of these with no second window in existence). The real owner is a tab or background runtime of this very instance; P0/P1 keep such holds transient.
				base = fmt.Sprintf("this session is already open in this Reasonix instance (pid %d); switch to or close its tab, or wait for its background work to finish, then retry", holderPID)
			} else {
				base = fmt.Sprintf("this session is held by a leftover background process (pid %d); restart the desktop to reap it automatically, or run taskkill /PID %d, then reopen the session", holderPID, holderPID)
			}
		}
	}
	if setting == "" {
		return base
	}
	return fmt.Sprintf("%s before changing %s", base, setting)
}

func (e *sessionLeaseBusyError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func userFacingSessionLeaseError(setting string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, agent.ErrSessionLeaseHeld) {
		// Task 272 G4: this wrap used to be the ONLY thing that happened —
		// the lease-held failure reached the UI and never the log, so the
		// orphan-serve hypothesis could be neither confirmed nor refuted from desktop.log. Log it with the same content the user sees.
		slog.Warn("desktop: session lease held; refusing session access",
			"setting", setting, "holder", err.Error())
		return &sessionLeaseBusyError{setting: setting, err: err}
	}
	return err
}

// sessionPathAfterSnapshot returns where a controller rebuild should keep
// persisting after the old controller was snapshotted. Snapshotting is not
// path-neutral: a snapshot conflict can recover by retargeting the controller (and the tab's session lease, via handleTabSessionRecovered) to a recovery branch, so a prevPath captured before the snapshot may be stale. Reusing the stale path would bind the rebuilt controller — carrying the just-recovered transcript — back to the original file, turning every later save into a new conflict that derives yet another recovery branch. Falls back to fallback when the controller is gone or persistence is disabled (empty SessionPath).
func sessionPathAfterSnapshot(ctrl control.SessionAPI, fallback string) string {
	if ctrl == nil {
		return fallback
	}
	if path := strings.TrimSpace(ctrl.SessionPath()); path != "" {
		return path
	}
	return fallback
}

var (
	// sessionLeaseContentionRetryInterval and sessionLeaseContentionRetryAttempts
	// bound the retry window for lease or removal-guard acquisition that hits a
	// transient in-process holder. CleanupStaleRunning and catalog persistence can hold the session lease or save lock briefly while a concurrent tab bind or archive begins; those callers must not surface a spurious "already open in another Reasonix window" error for ownership that is genuinely free once the short operation finishes. A lease held by another window or process stays held for its whole lifetime, so the bounded retry still fails fast there.
	sessionLeaseContentionRetryInterval = 50 * time.Millisecond
	sessionLeaseContentionRetryAttempts = 2
)

// withSessionLeaseContentionRetry retries acquire while it fails with
// agent.ErrSessionLeaseHeld, absorbing sub-second contention windows created
// by transient in-process lease or save-lock holders. Any other error is returned immediately, and a lease that remains held after the bounded retries is reported as-is.
func withSessionLeaseContentionRetry[T any](acquire func() (T, error)) (T, error) {
	var zero T
	for attempt := 0; ; attempt++ {
		got, err := acquire()
		if err == nil {
			return got, nil
		}
		if !errors.Is(err, agent.ErrSessionLeaseHeld) || attempt >= sessionLeaseContentionRetryAttempts {
			if errors.Is(err, agent.ErrSessionLeaseHeld) {
				// Task 272 G4: the retry loop used to give up silently — a
				// lease that outlived the contention window (i.e. a real
				// foreign holder) left no trace in desktop.log.
				slog.Warn("desktop: session lease still held after contention retries",
					"attempts", attempt+1, "holder", err.Error())
			}
			return zero, err
		}
		time.Sleep(sessionLeaseContentionRetryInterval)
	}
}

func (a *App) ensureTabSessionLeaseForRebuild(tab *WorkspaceTab, path, setting string) error {
	transition, reserveErr := a.reserveSessionRuntimePath(tab, path)
	if reserveErr != nil {
		return userFacingSessionLeaseError(setting, reserveErr)
	}
	if _, err := withSessionLeaseContentionRetry(func() (struct{}, error) {
		if err := tab.ensureSessionLease(path); err != nil {
			if a.canReclaimCurrentProcessSessionLease(tab, path, err) {
				// Task 456: the reclaim decision may have passed because every
				// same-key holder in this process is a zombie (bound lease, no
				// controller, no build). Those zombies hold the OS lock the reclaim needs, so release them first; with none released the call is a no-op and the reclaim behaves exactly as before.
				a.releaseZombieSessionLeaseHoldersForKey(sessionRuntimeKey(path), tab)
				if lease, reclaimErr := agent.TryReclaimCurrentProcessSessionLease(path); reclaimErr == nil {
					tab.adoptSessionLease(lease)
					return struct{}{}, nil
				} else {
					err = reclaimErr
				}
			}
			return struct{}{}, err
		}
		return struct{}{}, nil
	}); err != nil {
		a.rollbackSessionRuntimePath(transition)
		return userFacingSessionLeaseError(setting, err)
	}
	a.commitSessionRuntimePath(transition)
	return nil
}

// leaseReclaimDecision answers whether a lease error may be reclaimed by this
// process (task 244 B5). nil Info keeps the historical "attempt the reclaim,
// the OS lock is the arbiter" path; a lease owned by this PID and writer is ours; anything else is a foreign runtime. With orphanReclaim on, a foreign lease whose recorded owner process no longer exists is a crash leftover (orphan) and may be taken back — a live foreign owner is still respected. Pure so both switch states are testable without a real lease.
func leaseReclaimDecision(info *agent.SessionLeaseInfo, ownPID int, ownWriter string, orphanReclaim bool, pidAlive func(int) bool) bool {
	if info == nil {
		return true
	}
	if info.PID == ownPID && info.WriterID == ownWriter {
		return true
	}
	if !orphanReclaim {
		return false
	}
	return !pidAlive(info.PID)
}

// experimentalOrphanLeaseReclaim reports whether orphan handling may reclaim
// this lease. It reads the merged experimental_orphan_handling switch (task
// 449, which folded task 244 B5 + B4 into one key) at call time (S4) so a settings toggle applies to the next reclaim without a restart; config.Load normalizes, so a legacy task-244 key still on is folded in by migrateOrphanHandlingMerge. Missing config = off.
func (a *App) experimentalOrphanLeaseReclaim() bool {
	cfg, err := config.Load()
	if err != nil {
		return false
	}
	return cfg.Agent.ExperimentalOrphanHandling
}

func (a *App) canReclaimCurrentProcessSessionLease(tab *WorkspaceTab, path string, err error) bool {
	key := sessionRuntimeKey(path)
	if tab == nil || key == "" || !errors.Is(err, agent.ErrSessionLeaseHeld) {
		return false
	}
	var leaseErr *agent.SessionLeaseError
	if !errors.As(err, &leaseErr) || leaseErr == nil {
		return false
	}
	// A readable info naming a foreign runtime is respected here; reclaim
	// would refuse it anyway. A nil Info (lease.json deleted by the user,
	// quarantined by AV, or torn by a crash) must still attempt the reclaim: the OS lock is the arbiter there, and refusing on missing metadata wedges a session nobody actually holds as permanently busy. Task 244 B5: with the experiment on, a foreign lease whose recorded owner process is DEAD is an orphan (crash leftover), not a holder — taking it back is the registry's "reclaim orphans by instance identity after restart". A live foreign owner keeps being respected, exactly as before; the switch off keeps the original decision byte-for-byte.
	if leaseErr.Info != nil && !leaseReclaimDecision(leaseErr.Info, os.Getpid(), agent.SessionWriterID(), a.experimentalOrphanLeaseReclaim(), desktopProcessAlive) {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, candidate := range a.runtimeTabsLocked() {
		if candidate == nil || candidate == tab {
			continue
		}
		if candidate.sessionLeaseRuntimeKey() == key {
			// Task 456: a sibling that bound the lease but never published a
			// controller and has no build in flight is a zombie holder — the
			// incident's self-deadlock shape (this window's own runtime holds the lease while the visible tab is refused). It cannot release itself, so it must not veto the reclaim forever; the caller releases it via releaseZombieSessionLeaseHoldersForKey before reclaiming. A functional or still-building sibling vetoes.
			if !a.zombieLeaseHolderLocked(candidate, key) {
				return false
			}
			continue
		}
		if candidate.Ctrl != nil && sessionRuntimeKey(candidate.currentSessionPath()) == key {
			return false
		}
	}
	// A detached runtime's controller still holds the OS lock; refuse reclaim
	// even when PID matches (#6955). A detached zombie (no controller, no
	// build) is handled by the same release as above.
	if detached := a.detachedSessions[key]; detached != nil && detached.Ctrl != nil {
		return false
	}
	return true
}
