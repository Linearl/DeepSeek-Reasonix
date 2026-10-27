package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"reasonix/internal/agent"
	"reasonix/internal/control"
)

// bindTabWriteAuthority issues a generation-bound write authority from the
// tab's current session lease onto ctrl. Must run after the lease is adopted
// and before the controller is published as Ready/autosave-capable. Missing lease or a
// non-Controller SessionAPI is a no-op (tests and read-only tabs).
func bindTabWriteAuthority(tab *WorkspaceTab, ctrl control.SessionAPI) error {
	if tab == nil || ctrl == nil {
		return nil
	}
	c, ok := ctrl.(*control.Controller)
	if !ok || c == nil {
		return nil
	}
	tab.sessionLeaseMu.Lock()
	lease := tab.sessionLease
	tab.sessionLeaseMu.Unlock()
	return c.BindSessionWriteAuthority(lease)
}

// authorizeTabReplacementLocked validates and binds a replacement before it
// is published. App.mu must be held by the caller.
func (a *App) authorizeTabReplacementLocked(tab *WorkspaceTab, ctrl control.SessionAPI, action, authority string) error {
	if !a.ownsRuntimeTabLocked(tab) {
		return fmt.Errorf("tab %q changed while %s; retry", tab.ID, action)
	}
	if err := bindTabWriteAuthority(tab, ctrl); err != nil {
		return fmt.Errorf("bind %s session authority: %w", authority, err)
	}
	return nil
}

func bindCandidateWriteAuthority(ctrl control.SessionAPI, lease *agent.SessionLease) error {
	concrete, ok := ctrl.(*control.Controller)
	if !ok || concrete == nil {
		return nil
	}
	return control.IssueAndBindWriteAuthority(concrete, lease)
}

// isSessionWriteAuthorityError reports whether err is the stale/missing
// authority family — the failure shape of a controller whose bound write
// generation no longer matches its session lease (tl 1201).
func isSessionWriteAuthorityError(err error) bool {
	return errors.Is(err, agent.ErrSessionWriteAuthorityStale) ||
		errors.Is(err, agent.ErrSessionWriteAuthorityMissing)
}

// restoreTabWriteAuthorityAfterStale is the tl 1201 self-heal: when a save
// fails because the bound write authority went stale, re-bind a fresh
// generation instead of retrying the same dead authority (the 2026-10-27
// 17:01-17:03 storm: autosave five rounds all stale, submit dropped, close
// refused — nothing ever re-acquired the lease). Two paths, tried in order:
//
//  1. The tab's lease object is still alive (released=false, lock held) —
//     re-issue a fresh generation from it. IssueAndBindWriteAuthority fails
//     closed when the object is a rotation leftover, so this can never mint
//     against a dead lease.
//  2. Acquire the lease fresh. A live holder anywhere — another tab, a
//     detached runtime, a foreign window — keeps the OS lock and fails the
//     acquire, so real cross-process mutual exclusion is never stolen (tl
//     1201 acceptance 4). A FREE lock (crashed holder's leftover, damaged
//     identity) is taken over, which is exactly the zombie-recovery path.
//
// Reports whether the authority is usable again; the caller retries the save
// either way and every remaining failure stays visible through the existing
// warn/report paths.
func (a *App) restoreTabWriteAuthorityAfterStale(tab *WorkspaceTab) bool {
	if tab == nil || tab.ReadOnly {
		return false
	}
	a.mu.RLock()
	ctrl := tab.Ctrl
	resident := !tab.removed && a.tabs[tab.ID] == tab
	a.mu.RUnlock()
	if !resident || ctrl == nil {
		return false
	}
	concrete, ok := ctrl.(*control.Controller)
	if !ok || concrete == nil {
		return false
	}
	key := tab.sessionLeaseRuntimeKey()
	if key == "" {
		key = sessionRuntimeKey(ctrl.SessionPath())
	}
	if key == "" {
		return false
	}

	// Fast path: the tab still holds a live lease — a fresh generation from
	// the same lease object is the whole repair.
	tab.sessionLeaseMu.Lock()
	held := tab.sessionLease
	tab.sessionLeaseMu.Unlock()
	if held != nil && sessionRuntimeKey(held.Path()) == key &&
		control.IssueAndBindWriteAuthority(concrete, held) == nil {
		return true
	}

	// Takeover path. TryAcquireSessionLease refuses while any live holder
	// keeps the OS lock (in-process registry or foreign window) and takes
	// over a free lock, publishing fresh identity — zombie leases are
	// recovered here without touching the mutual-exclusion verdicts.
	fresh, err := agent.TryAcquireSessionLease(key)
	if err != nil {
		slog.Warn("desktop: stale write authority left un-repaired; lease still held",
			"tab", tab.ID, "holder", err.Error())
		return false
	}
	old := tab.takeSessionLease()
	tab.adoptSessionLease(fresh)
	if old != nil {
		old.Release()
	}
	if err := control.IssueAndBindWriteAuthority(concrete, fresh); err != nil {
		slog.Warn("desktop: re-acquired session lease but write authority bind failed", "tab", tab.ID)
		return false
	}
	return true
}

// validateAndBindSessionRebindLocked performs the final identity check and
// authority bind as one publication admission. App.mu must be held.
func (a *App) validateAndBindSessionRebindLocked(tab *WorkspaceTab, source tabRuntimeSnapshot, transition sessionRuntimePathTransition, candidate *sessionRebindCandidate, lease *agent.SessionLease) error {
	if tab.removed ||
		a.tabs[tab.ID] != tab ||
		tab.Ctrl != source.ctrl ||
		a.runtimeForTabLocked(tab) != transition.runtime ||
		!a.sessionRuntimePathTransitionValidLocked(transition) {
		return fmt.Errorf("tab runtime changed while switching sessions; retry")
	}
	if err := bindCandidateWriteAuthority(candidate.ctrl, lease); err != nil {
		return fmt.Errorf("resume session: bind write authority: %w", err)
	}
	return nil
}

// commitStartupWriteAuthorityLocked commits extension registration and write
// authority under the final publication lock. App.mu must be held on entry;
// failure paths release it and retire the unpublished controller.
func (a *App) commitStartupWriteAuthorityLocked(tab *WorkspaceTab, ctrl control.SessionAPI, registration *sharedHostMCPRegistration, rootKey, acquiredLeaseKey string, wailsCtx context.Context) bool {
	if !registration.commit() {
		a.mu.Unlock()
		a.abandonSupersededBuild(tab, ctrl, rootKey, acquiredLeaseKey)
		a.scheduleDeferredStartupBuild(tab.ID)
		return false
	}
	if err := bindTabWriteAuthority(tab, ctrl); err != nil {
		_, save := a.markTabStartupFailureLocked(tab, err, suppressStartupRestore)
		a.mu.Unlock()
		a.writeTabsSaveRequest(save)
		a.abandonSupersededBuild(tab, ctrl, rootKey, acquiredLeaseKey)
		a.emitReady(wailsCtx, tab.ID)
		return false
	}
	return true
}
