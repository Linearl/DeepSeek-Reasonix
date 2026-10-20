package main

import (
	"testing"

	"reasonix/internal/repair"
)

// Task 377 introduced the lifecycle noise gate (experimental_lifecycle_noise_
// gate, default OFF): clean-shutdown phases were suppressed only when the gate
// was on. Task 1018 (20261020 装机实测) made the suppression unconditional —
// every consumed per-process lifecycle record is read-and-clear at the source,
// no pending crash report is written for ANY phase under EITHER gate state.
// The gate flag itself stays a config field; it only decides whether the
// clean-shutdown disposition also bumps the noise_gate_suppressed metric.
func TestLifecycleResidueClearedRegardlessOfGateAndPhase(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.lifecycle.previousRuns = []desktopLifecycleObservation{
		{Phase: "shutting_down", Version: "v1", Channel: "stable"},
		{Phase: "healthy", Version: "v1", Channel: "stable"},
		{Phase: "wedged", Version: "v1", Channel: "stable"},
		{Phase: "something_new", Version: "v1", Channel: "stable"},
	}

	// Gate off (装机默认): nothing is written — the 1018 read-and-clear covers
	// benign and abnormal phases alike; the 10-19 wedged-residue false banner
	// is gone at the source.
	app.recordPreviousRunDiagnostics()
	if got := pendingReportCount(t); got != 0 {
		t.Fatalf("gate off: pending reports = %d, want 0 (task 1018 read-and-clear)", got)
	}

	// Gate on: same outcome — the gate no longer changes report behavior.
	app.lifecycle.noiseGate = true
	app.recordPreviousRunDiagnostics()
	if got := pendingReportCount(t); got != 0 {
		t.Fatalf("gate on: pending reports = %d, want 0", got)
	}
	// The phase classifier survives as the disposition label for the log and
	// metrics: benign phases stay benign, wedged/unknown stay abnormal.
	if !lifecycleNoiseBenign("shutting_down") || !lifecycleNoiseBenign("healthy") {
		t.Fatal("benign phases misclassified")
	}
	if lifecycleNoiseBenign("wedged") || lifecycleNoiseBenign("starting") || lifecycleNoiseBenign(" wedged ") {
		t.Fatal("non-benign phases must never classify as benign")
	}
}

// The read-and-clear never touches the legacy v1 startup observation path.
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
