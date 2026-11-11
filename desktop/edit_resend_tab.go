package main

// EditAndResendView is the tl1544 bridge result: OK plus the fork path and the
// refreshed tab meta (the same shape CommitRewindForTab returns, minus the
// rewind planner surface the atomic orchestration does not expose).
type EditAndResendView struct {
	OK     bool     `json:"ok"`
	Error  string   `json:"error,omitempty"`
	Branch string   `json:"branch,omitempty"`
	TabID  string   `json:"tabId,omitempty"`
	Tab    *TabMeta `json:"tab,omitempty"`
}

// EditAndResendForTab runs the tl1544 atomic edit-resend on one tab: cancel
// the running turn if any (three-level escalation, bounded waits), treat the
// parked/guidance queues, fork the conversation at the historical turn, and
// resubmit the edited prompt. The controller API itself is ungated; the
// RUNNING-state frontend entry is the lab-switch consumer, so this bridge
// stays a thin pass-through like CommitRewindForTab.
func (a *App) EditAndResendForTab(tabID string, turn int, display, input, original string) EditAndResendView {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if a.tabIsReadOnly(tab) {
		return EditAndResendView{OK: false, Error: readOnlyChannelErr().Error()}
	}
	// Task 539: the takeover-yield window restarts runtime work on commit; a
	// fork-then-resend landing inside it would race the handoff. Same refusal
	// shape as the rewind bridge.
	if sessionYieldActiveForTab(tab) {
		return EditAndResendView{OK: false, Error: errTabYieldingToTakeover.Error()}
	}
	if ctrl == nil {
		return EditAndResendView{OK: false, Error: "no controller"}
	}
	if err := ctrl.EditAndResend(turn, display, input, original); err != nil {
		return EditAndResendView{OK: false, Error: err.Error()}
	}
	view := EditAndResendView{OK: true}
	// The fork moved the tab onto a new session file; publish the tab meta so
	// the frontend's resident-tab reconciliation sees the new path and
	// revision in the same turn (tl1518's resident-stale consumer).
	if tab != nil {
		meta := a.tabMetaAfterHeadSwitch(tab)
		view.Branch = meta.SessionPath
		view.TabID, view.Tab = meta.ID, &meta
	}
	return view
}
