package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
)

// handoffSessionLease acquires path and publishes it on the tab without
// releasing the previous lease. Recovery callbacks use the returned lease to
// retire the old path only after the current authority-guarded save returns.
func (t *WorkspaceTab) handoffSessionLease(path string) (*agent.SessionLease, error) {
	if t == nil || t.ReadOnly {
		return nil, nil
	}
	key := sessionRuntimeKey(path)
	if key == "" {
		return nil, nil
	}
	t.sessionLeaseMu.Lock()
	if t.sessionLease != nil && sessionRuntimeKey(t.sessionLease.Path()) == key {
		t.storeSessionLeaseRuntimeKey(key)
		t.sessionLeaseMu.Unlock()
		return nil, nil
	}
	lease, err := agent.TryAcquireSessionLease(key)
	if err != nil {
		t.sessionLeaseMu.Unlock()
		return nil, err
	}
	if hook := sessionLeaseAcquireHookForTest; hook != nil {
		hook()
	}
	old := t.sessionLease
	t.sessionLease = lease
	t.storeSessionLeaseRuntimeKey(key)
	t.sessionLeaseMu.Unlock()
	t.startTakeoverRequestWatcher(key)
	return old, nil
}

func (t *WorkspaceTab) swapSessionLease(lease *agent.SessionLease) *agent.SessionLease {
	if t == nil {
		return lease
	}
	t.sessionLeaseMu.Lock()
	old := t.sessionLease
	t.sessionLease = lease
	key := ""
	if lease != nil {
		key = sessionRuntimeKey(lease.Path())
	}
	t.storeSessionLeaseRuntimeKey(key)
	t.sessionLeaseMu.Unlock()
	if lease != nil {
		t.startTakeoverRequestWatcher(key)
	} else {
		t.stopTakeoverRequestWatcher()
	}
	return old
}

func (a *App) handoffTabRecoveryLease(tab *WorkspaceTab, recoveryPath string) error {
	if tab == nil || tab.ReadOnly {
		return nil
	}
	transition, err := a.reserveSessionRuntimePath(tab, recoveryPath)
	if err != nil {
		return fmt.Errorf("acquire recovery session lease: %w", userFacingSessionLeaseError("", err))
	}
	oldLease, err := tab.handoffSessionLease(recoveryPath)
	if err != nil {
		a.rollbackSessionRuntimePath(transition)
		slog.Warn("desktop: acquire recovery session lease", "path", recoveryPath, "err", err)
		reason := "lease_unavailable"
		if errors.Is(err, agent.ErrSessionLeaseHeld) {
			reason = "lease_held"
		}
		a.emitRuntimeEvent("session:recovery-failed", sessionRecoveryFailedEvent{Reason: reason})
		return fmt.Errorf("acquire recovery session lease: %w", userFacingSessionLeaseError("", err))
	}
	if err := bindTabWriteAuthority(tab, tab.Ctrl); err != nil {
		newLease := tab.swapSessionLease(oldLease)
		_ = bindTabWriteAuthority(tab, tab.Ctrl)
		a.rollbackSessionRuntimePath(transition)
		if newLease != nil {
			newLease.Release()
		}
		return fmt.Errorf("bind recovery session authority: %w", err)
	}
	a.commitSessionRuntimePath(transition)
	if oldLease != nil {
		go oldLease.Release()
	}
	return nil
}

// handleTabSessionTransition moves a tab's lease and binds the unpublished
// target Session before its controller switches paths. The source controller
// remains fully usable when any acquisition or bind step fails.
func (a *App) handleTabSessionTransition(tab *WorkspaceTab) func(control.SessionTransitionInfo) error {
	return func(info control.SessionTransitionInfo) error {
		if tab == nil || tab.ReadOnly {
			return nil
		}
		transition, err := a.reserveSessionRuntimePath(tab, info.TargetPath)
		if err != nil {
			return fmt.Errorf("acquire target session lease: %w", userFacingSessionLeaseError("", err))
		}
		oldLease, err := tab.handoffSessionLease(info.TargetPath)
		if err != nil {
			a.rollbackSessionRuntimePath(transition)
			return fmt.Errorf("acquire target session lease: %w", userFacingSessionLeaseError("", err))
		}
		tab.sessionLeaseMu.Lock()
		lease := tab.sessionLease
		tab.sessionLeaseMu.Unlock()
		if err := info.BindWriteAuthority(lease); err != nil {
			newLease := tab.swapSessionLease(oldLease)
			a.rollbackSessionRuntimePath(transition)
			if newLease != nil {
				newLease.Release()
			}
			return fmt.Errorf("bind target session authority: %w", err)
		}
		a.mu.Lock()
		if tab.removed || !a.runtimeOwnerLiveLocked(transition.runtime) || !a.commitSessionRuntimePathLocked(transition) {
			a.mu.Unlock()
			newLease := tab.swapSessionLease(oldLease)
			a.rollbackSessionRuntimePath(transition)
			if newLease != nil {
				newLease.Release()
			}
			return fmt.Errorf("bind target session: tab runtime changed; retry")
		}
		tab.SessionPath = canonicalTabSessionPath(info.TargetPath)
		// Rotations initiated inside the controller (e.g. /extract's NewSession)
		// bypass the desktop ClearSession wrapper, so the tab-local generation
		// must bump here: the frontend watches it to re-hydrate the transcript
		// onto the rotated session instead of keeping the stale one.
		tab.SessionGeneration++
		if a.tabs[tab.ID] == tab {
			a.saveTabsLocked()
		}
		a.mu.Unlock()
		if oldLease != nil {
			go oldLease.Release()
		}
		a.emitProjectTreeChangedForSessionDirs(sessionDirectoryForPath(info.TargetPath))
		return nil
	}
}

// startTakeoverRequestWatcher polls for a remote takeover request marker
// (<session>.takeover-request, written by the serve takeover endpoint) while
// this tab holds the session lease. When a request arrives the tab yields:
// the lease is released (so the serve-side acquire succeeds), the tab flips
// to read-only, and the tree metadata refresh tells the frontend.
func (t *WorkspaceTab) startTakeoverRequestWatcher(path string) {
	if t == nil || t.takeoverWatchStop != nil {
		return
	}
	stop := make(chan struct{})
	t.takeoverWatchStop = stop
	go func() {
		marker := path[:len(path)-len(".jsonl")] + ".takeover-request"
		slog.Info("desktop: takeover watcher started", "path", path, "marker", marker)
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if _, err := os.Stat(marker); err != nil {
					continue
				}
				slog.Info("desktop: takeover request detected, yielding", "marker", marker)
				// Yield: release the lease so the serve-side acquire succeeds.
				t.sessionLeaseMu.Lock()
				old := t.sessionLease
				t.sessionLease = nil
				t.sessionLeaseMu.Unlock()
				if old != nil {
					old.Release()
				}
				_ = os.Remove(marker)
				// Do NOT flip the tab to read-only here: ReadOnly persists in
				// desktop-tabs.json and would lock the tab out of writing
				// across restarts. Releasing the lease is enough — the next
				// local message reacquires it cleanly.
				slog.Info("desktop: session yielded to remote takeover", "path", path)
				return
			}
		}
	}()
}

func (t *WorkspaceTab) stopTakeoverRequestWatcher() {
	if t == nil || t.takeoverWatchStop == nil {
		return
	}
	select {
	case <-t.takeoverWatchStop:
	default:
		close(t.takeoverWatchStop)
	}
	t.takeoverWatchStop = nil
}
