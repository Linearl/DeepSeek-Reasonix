package main

import (
	"log/slog"

	"reasonix/internal/agent"
	"reasonix/internal/control"
)

// bindTabWriteAuthority issues a generation-bound write authority from the
// tab's current session lease onto ctrl. Must run after the lease is adopted
// and the controller is published, before Ready/autosave. Missing lease or a
// non-Controller SessionAPI is a no-op (tests and read-only tabs).
func bindTabWriteAuthority(tab *WorkspaceTab, ctrl control.SessionAPI) {
	if tab == nil || ctrl == nil {
		return
	}
	c, ok := ctrl.(*control.Controller)
	if !ok || c == nil {
		return
	}
	tab.sessionLeaseMu.Lock()
	lease := tab.sessionLease
	tab.sessionLeaseMu.Unlock()
	if err := c.BindSessionWriteAuthority(lease); err != nil {
		slog.Warn("desktop: bind session write authority", "err", err)
	}
}

// rebindTabWriteAuthority refreshes authority after a path change while the
// same tab lease still covers the new path (or after adoptSessionLease).
func rebindTabWriteAuthority(tab *WorkspaceTab) {
	if tab == nil {
		return
	}
	bindTabWriteAuthority(tab, tab.Ctrl)
}

// leaseFromTab returns the tab's current lease without taking ownership.
func leaseFromTab(tab *WorkspaceTab) *agent.SessionLease {
	if tab == nil {
		return nil
	}
	tab.sessionLeaseMu.Lock()
	defer tab.sessionLeaseMu.Unlock()
	return tab.sessionLease
}
