package main

import (
	"log"
	"unicode/utf8"

	"reasonix/internal/event"
)

// Task 991: background-tab ask awareness. An ask that lands while its tab is
// not the active one is invisible to the user — the panel is mounted in a
// hidden tab, so every interaction signal (hover, selection, takeover) is
// unproducible and the autopilot auto-decide fires "out of nowhere". The sink
// that already routes the wire event also judged the tab's background state
// (it holds both the tabID and the app), and fires one runtime notification
// the frontend turns into a bottom-right reminder toast. The toast's lifetime
// rides the same askWaitFor() value the backend timer uses (Ask.AutoDecideWaitMs),
// so it expires with the decision instead of racing a copy of the window.

// askBackgroundNoticeEvent is the runtime channel the reminder toast listens
// on. Distinct from the agent event channel: this is a UI hint, not session
// state, and must not enter the per-tab reducer.
const askBackgroundNoticeEvent = "ask:background"

// AskBackgroundNotice is the toast payload for one background-tab ask. waitMs
// is the armed auto-decide window (0 = attended ask, toast persists until
// answered or dismissed); summary is the first question, clipped, so the
// toast can say what is being asked without the user switching tabs first.
type AskBackgroundNotice struct {
	TabID   string `json:"tabId"`
	AskID   string `json:"askId"`
	WaitMs  int64  `json:"waitMs"`
	Summary string `json:"summary"`
}

// noteBackgroundAskToast fires the task-991 reminder when an AskRequest lands
// on a tab that is provably not the active one. Conservative on every edge:
// no active tab (startup/replay) or a self-addressed event emits nothing, so
// the toast can never fire for a panel the user is already looking at.
func (a *App) noteBackgroundAskToast(tabID string, e event.Event, emit func(string, ...any)) {
	if a == nil || tabID == "" || e.Ask.ID == "" {
		return
	}
	a.mu.RLock()
	active := a.activeTabID
	a.mu.RUnlock()
	// Only a positively-known background tab toasts: with no active tab the
	// surface state is unknown (startup replay), and guessing "background"
	// there would re-toast every pending ask on every reopen.
	if active == "" || active == tabID {
		return
	}
	summary := ""
	if len(e.Ask.Questions) > 0 {
		summary = clipAskSummary(e.Ask.Questions[0].Prompt)
	}
	log.Printf("[ask-panel] background ask surfaced as toast tab=%s ask=%s waitMs=%d", tabID, e.Ask.ID, e.Ask.AutoDecideWaitMs)
	emit(askBackgroundNoticeEvent, AskBackgroundNotice{
		TabID:   tabID,
		AskID:   e.Ask.ID,
		WaitMs:  e.Ask.AutoDecideWaitMs,
		Summary: summary,
	})
}

// clipAskSummary bounds the toast's question text: short enough for one or
// two lines, rune-safe (a mid-rune cut would render a replacement char).
func clipAskSummary(s string) string {
	const max = 120
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "…"
}

// NotifyAskReattachForTab (task 991) is the toast-click hop: the user is
// coming to look at the panel, so the timed wait re-arms a fresh window and a
// fresh hover budget — the same re-presented semantics a session reopen uses
// (task 178). Deliberately NOT the task-806 interaction signal: a click that
// switches tabs must not stop the timer outright, or "clicked, then walked
// away" would hang the run forever; the fresh window still decides if the
// user never arrives. Fire-and-forget like the rest of the ask-signal bridge:
// unknown tab, missing controller, or a prompt class without a timer is a
// silent no-op.
func (a *App) NotifyAskReattachForTab(tabID, id string) {
	ctrl := a.ctrlByTabID(tabID)
	if ctrl == nil || id == "" {
		return
	}
	if notifier, ok := ctrl.(interface{ NotifyAskReattached(string) }); ok {
		notifier.NotifyAskReattached(id)
	}
}
