package main

import "reasonix/internal/control"

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
