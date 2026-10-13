package main

// Ask-panel interaction/activity signal bridge. Task 806 shipped the frontend
// half of 要素① (AskCard's first selection/typed edit) and the backend entry
// (Controller.NotifyAskInteraction), but the desktop hop in between was never
// wired: no App method carried the call to the controller, so the production
// panel's fire-and-forget signal died at the binding and the stop-the-timer
// semantics only ever worked in tests. Task 844 adds the second signal
// (mousemove → window reset) and closes the bridge for both.

// NotifyAskInteractionForTab forwards the ask panel's first selection or typed
// edit (task 806 要素①) to the tab's controller, which stops the reversible
// ask's auto-decide timer. Fire-and-forget: an unknown tab, a missing
// controller, or a prompt class without a timer is a silent no-op — the worst
// case is the pre-806 plain wait/timeout behavior, never an error surfaced to
// the user.
func (a *App) NotifyAskInteractionForTab(tabID, id string) {
	ctrl := a.ctrlByTabID(tabID)
	if ctrl == nil || id == "" {
		return
	}
	ctrl.NotifyAskInteraction(id)
}

// NotifyAskActivityForTab forwards panel hover (mousemove on the ask panel;
// task 844) to the tab's controller, which resets the reversible ask's
// auto-decide window — "the user is reading" — bounded by the controller's
// absolute hover budget, so continuous movement cannot defer the auto-decide
// forever. Same fire-and-forget posture as the interaction hop above.
func (a *App) NotifyAskActivityForTab(tabID, id string) {
	ctrl := a.ctrlByTabID(tabID)
	if ctrl == nil || id == "" {
		return
	}
	ctrl.NotifyAskActivity(id)
}

// NotifyAskTakeoverForTab forwards the ask panel's takeover toggle (task 849)
// to the tab's controller: true parks the reversible ask's auto-decide timer
// and its absolute hover budget outright — the user explicitly declared
// themselves present, so the ask waits indefinitely no matter how long they
// think — and false releases the takeover, re-arming a fresh window. Unlike
// the two hops above this one reports back: false means no timed ask consumed
// the toggle (attended or high-risk ask, unknown/expired ID), which is the
// frontend's cue to roll its button state back so the panel never shows a
// takeover the backend is not honoring.
func (a *App) NotifyAskTakeoverForTab(tabID, id string, taking bool) bool {
	ctrl := a.ctrlByTabID(tabID)
	if ctrl == nil || id == "" {
		return false
	}
	return ctrl.NotifyAskTakeover(id, taking)
}
