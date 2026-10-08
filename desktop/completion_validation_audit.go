package main

import "reasonix/internal/event"

// RecordSubagentLifecycle consumes content-free child lifecycle telemetry.
// ToolResult already carries the live card metadata; this side channel
// maintains the running foreground sub-agent registry (task 557 — the capsule
// badge counts foreground children from here) and contributes scrubbed
// status/error buckets to anonymous desktop diagnostics.
func (s *tabEventSink) RecordSubagentLifecycle(info event.SubagentLifecycleInfo) {
	if s == nil {
		return
	}
	tabID, app := s.binding()
	if app == nil {
		return
	}
	app.noteSubagentLifecycle(tabID, info)
	if metrics := app.metrics.Load(); metrics != nil {
		metrics.observeSubagentLifecycle(info)
		metrics.persist()
	}
}
