package main

import (
	"testing"

	"reasonix/internal/repair"
)

// Task 377: the lifecycle noise gate (experimental_lifecycle_noise_gate,
// default OFF) skips the crash-pending report only for phases that prove a
// clean shutdown was already underway (shutting_down/healthy). wedged and
// unknown phases always report.
func TestLifecycleNoiseGateSuppressesOnlyBenignPhases(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.lifecycle.previousRuns = []desktopLifecycleObservation{
		{Phase: "shutting_down", Version: "v1", Channel: "stable"},
		{Phase: "healthy", Version: "v1", Channel: "stable"},
		{Phase: "wedged", Version: "v1", Channel: "stable"},
		{Phase: "something_new", Version: "v1", Channel: "stable"},
	}

	// Gate off (default): every record reports — byte-for-byte the old behavior.
	app.recordPreviousRunDiagnostics()
	if got := pendingReportCount(t); got != 4 {
		t.Fatalf("gate off: pending reports = %d, want 4", got)
	}

	// Gate on: only the two benign phases are suppressed; wedged + unknown
	// still report. The suppression itself must stay observable (a log line +
	// metrics), never a silent drop.
	app.lifecycle.noiseGate = true
	app.recordPreviousRunDiagnostics()
	if got := pendingReportCount(t); got != 4+2 {
		t.Fatalf("gate on: pending reports = %d, want 4+2 (wedged + unknown)", got)
	}
	if !lifecycleNoiseBenign("shutting_down") || !lifecycleNoiseBenign("healthy") {
		t.Fatal("benign phases misclassified")
	}
	if lifecycleNoiseBenign("wedged") || lifecycleNoiseBenign("starting") || lifecycleNoiseBenign(" wedged ") {
		t.Fatal("non-benign phases must never be suppressed")
	}
}

// The gate never touches the legacy v1 startup observation path.
func TestLifecycleNoiseGateLeavesLegacyObservationAlone(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.lifecycle.noiseGate = true
	app.lifecycle.previousRun = repair.PreviousRunObservation{Abnormal: true, Phase: "shutting_down"}
	app.recordPreviousRunDiagnostics()
	if got := pendingReportCount(t); got != 1 {
		t.Fatalf("legacy abnormal observation suppressed: %d reports, want 1", got)
	}
}
