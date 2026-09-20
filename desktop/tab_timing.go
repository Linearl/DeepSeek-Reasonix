package main

import (
	"log/slog"
	"strings"
)

// slowTabSwitchLogMs is the threshold below which a stage is not worth a log line. Tab
// switches happen constantly and most are fast; the ones worth seeing in desktop.log are
// the ones the user experiences as slow.
const slowTabSwitchLogMs = 150

// switchOutStagePrefix marks the stages that measure leaving a tab, and switchOutLogMs is
// their (lower) threshold - see ReportTabSwitchTiming.
const switchOutStagePrefix = "switch-out:"
const switchOutLogMs = 50

// ReportTabSwitchTiming lets the frontend publish how long a switch-tab stage took.
//
// useController already measures these stages (see loadTimed) and writes breadcrumbs, but
// breadcrumbs only reach the in-memory crash context - so a slow switch left no trace in
// desktop.log and the only way to diagnose it was to read the code and guess. This closes
// that gap with a channel the user can reproduce and the log can answer.
//
// Deliberately one-way and fire-and-forget: a diagnostic that can fail a tab switch is
// worse than no diagnostic. The threshold keeps a fast switch silent.
// ReportFrontendLog is the general form of the timing channel below: one line per
// fork-feature event the frontend wants visible in desktop.log.
//
// The fork's features are mostly frontend-side (project grouping, colour filtering, question
// search, draft persistence, paging) and had no way to reach the log at all - a frontend bug
// left nothing behind. This is that channel. feature= keeps the lines greppable and
// separable from upstream behaviour, matching the Go-side convention.
//
// Level is clamped: "warn" and "error" map through, everything else is info, so a frontend
// call cannot fabricate an error-level line. Detail is a free-form string; callers pass a
// compact key=value tail rather than a formatted sentence.
func (a *App) ReportFrontendLog(feature string, level string, message string, detail string) {
	if feature == "" || message == "" {
		return
	}
	attrs := []any{"feature", feature}
	if detail != "" {
		attrs = append(attrs, "detail", detail)
	}
	switch level {
	case "warn":
		slog.Warn("desktop: frontend "+message, attrs...)
	case "error":
		slog.Error("desktop: frontend "+message, attrs...)
	default:
		slog.Info("desktop: frontend "+message, attrs...)
	}
}

func (a *App) ReportTabSwitchTiming(tabID string, stage string, ms int) {
	// Task 196: the switch-out stages get a lower bar. Switching out of a long session
	// also resumes and releases the source tab - the user reports that direction as slow
	// too - but at 150ms a 120ms teardown is dropped silently, and that is exactly the
	// evidence needed to tell "the teardown is slow" from "nothing happened here".
	threshold := slowTabSwitchLogMs
	if strings.HasPrefix(stage, switchOutStagePrefix) {
		threshold = switchOutLogMs
	}
	if ms < threshold {
		return
	}
	slog.Info("desktop: tab switch timing", "tab", tabID, "stage", stage, "ms", ms)
}
