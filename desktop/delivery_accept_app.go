package main

import (
	"fmt"
	"strings"
)

// AcceptDeliveryToTab clears an awaiting_delivery ("待完成验证") tab activity
// state without starting a model turn. This gives the user an explicit,
// model-free way to dismiss the "待完成验证" label when they consider the task
// acceptable. (Issue #9036 — Phase 1 minimal.)
//
// A durable user_acceptance receipt is intentionally deferred to Phase 2; here
// we only reset the in-memory runtime projection so the sidebar stops treating
// the conversation as running/awaiting.
func (a *App) AcceptDeliveryToTab(tabID string) error {
	if strings.TrimSpace(tabID) == "" {
		return fmt.Errorf("tab id is required")
	}
	if a.clearTabActivityStatusIf(tabID, topicStatusAwaitingDelivery) {
		a.emitProjectTreeRuntimeChangedWithLegacy()
	}
	return nil
}

// clearTabActivityStatusIf resets a tab's activity status to "" when it
// matches the wanted value. It is idempotent and safe to call for tabs in any
// state; it only changes awaiting_delivery tabs.
func (a *App) clearTabActivityStatusIf(tabID, want string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	tab := a.tabByEventSinkIDLocked(tabID)
	if tab == nil {
		return false
	}
	if want != "" && tab.ActivityStatus != want {
		return false
	}
	if tab.ActivityStatus == "" {
		return false
	}
	tab.ActivityStatus = ""
	return true
}
