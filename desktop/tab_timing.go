package main

import "log/slog"

// slowTabSwitchLogMs is the threshold below which a stage is not worth a log line. Tab
// switches happen constantly and most are fast; the ones worth seeing in desktop.log are
// the ones the user experiences as slow.
const slowTabSwitchLogMs = 150

// ReportTabSwitchTiming lets the frontend publish how long a switch-tab stage took.
//
// useController already measures these stages (see loadTimed) and writes breadcrumbs, but
// breadcrumbs only reach the in-memory crash context - so a slow switch left no trace in
// desktop.log and the only way to diagnose it was to read the code and guess. This closes
// that gap with a channel the user can reproduce and the log can answer.
//
// Deliberately one-way and fire-and-forget: a diagnostic that can fail a tab switch is
// worse than no diagnostic. The threshold keeps a fast switch silent.
func (a *App) ReportTabSwitchTiming(tabID string, stage string, ms int) {
	if ms < slowTabSwitchLogMs {
		return
	}
	slog.Info("desktop: tab switch timing", "tab", tabID, "stage", stage, "ms", ms)
}
