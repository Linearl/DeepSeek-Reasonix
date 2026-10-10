package main

import (
	"errors"
	"log/slog"

	"reasonix/internal/agent"
	"reasonix/internal/botruntime"
)

func (a *App) deleteRecoveryCopy(path string) error {
	dir := a.activeSessionDir()
	sessionPath, _, err := validateSessionPath(dir, path)
	if err != nil {
		var foundErr error
		if dir, sessionPath, foundErr = a.sessionDirForPath(path); foundErr != nil {
			return err
		}
	}
	if err := func() error {
		defer a.lockRuntimeMutation("delete-recovery-copy")()
		a.sessionRemovalMu.Lock()
		defer a.sessionRemovalMu.Unlock()
		// Read-only tabs may not own a lease, so keep this check in addition to
		// the cross-process guards acquired by the agent helper.
		if a.sessionOpenInAnyTab(sessionPath) {
			return errSessionBusyElsewhere
		}
		if err := agent.TrashCoveredRecoveryBranch(sessionPath, dir); err != nil {
			switch {
			case errors.Is(err, agent.ErrRecoveryBranchNotCovered):
				return errRecoveryCopyNotRedundant
			case errors.Is(err, agent.ErrSessionLeaseHeld):
				return errSessionBusyElsewhere
			default:
				return err
			}
		}
		return nil
	}(); err != nil {
		return err
	}
	if err := botruntime.ForgetAutoSessionMappingsForPath(sessionPath); err != nil {
		slog.Warn("desktop: failed to clear auto bot session mapping", "err", err)
	}
	// Task 715 (714 复核 B4): this flow refuses open sessions, so it never
	// passes through the runtime-removal funnel — drop the copy's cached
	// replayed graph here, or a copy opened earlier keeps pinning its DAG state.
	if freed, ok := agent.InvalidateSessionGraph(sessionPath); ok {
		slog.Info("desktop: invalidated dag graph cache on recovery copy delete",
			"path", sessionPath, "freed_bytes", freed)
	}
	a.removeSessionCatalogPath(sessionPath, "recovery_copy_deleted")
	a.emitProjectTreeChangedForSessionDirs(dir)
	a.invalidatePromptHistoryCache()
	return nil
}
