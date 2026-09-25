package main

import "fmt"

// fallbackSwitch executes task 242's model swap for the session identified
// by selfPath: it finds that session's tab and hands the swap to the existing
// SetModelForTab (build+swap under the same locks a manual switch uses). The
// path check is the guard against collateral damage — a background collab
// session hitting quota must never retarget the user's active tab. Returning
// an error is safe by construction: absorbQuotaForFallback then surfaces the
// original quota error unchanged instead of swallowing it.
func (a *App) fallbackSwitch(selfPath, target string) error {
	if a == nil || target == "" {
		return fmt.Errorf("fallback switch: empty target")
	}
	a.mu.RLock()
	ids := append([]string(nil), a.tabOrder...)
	a.mu.RUnlock()
	for _, id := range ids {
		tab := a.tabByID(id)
		if tab == nil {
			continue
		}
		if a.currentSessionPathFor(tab) == selfPath {
			return a.SetModelForTab(tab.ID, target)
		}
	}
	return fmt.Errorf("fallback switch: no tab owns session %q", selfPath)
}
