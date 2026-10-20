package main

import (
	"testing"

	"reasonix/internal/repair"
)

// Task 377 introduced the lifecycle noise gate (experimental_lifecycle_noise_
// gate, default OFF): clean-shutdown phases were suppressed only when the gate
// was on. Task 1018 made benign-phase suppression unconditional; task 1018-fix2
// restored abnormal-phase reporting. The gate no longer changes report
// behavior under EITHER gate state — it only decides whether the clean-shutdown
// disposition also bumps the noise_gate_suppressed metric.
func TestLifecycleBenignClearsAbnormalReportsRegardlessOfGate(t *testing.T) {
	// runScan feeds one record per phase through a fresh scan and returns the
	// number of pending crash reports that scan produced.
	runScan := func(t *testing.T, gate bool) int {
		t.Helper()
		removeAllPendingCrashes()
		app := NewApp()
		app.lifecycle.noiseGate = gate
		app.lifecycle.previousRuns = []desktopLifecycleObservation{
			{Phase: "shutting_down", Version: "v1", Channel: "stable"},
			{Phase: "healthy", Version: "v1", Channel: "stable"},
			{Phase: "wedged", Version: "v1", Channel: "stable"},
			{Phase: "something_new", Version: "v1", Channel: "stable"},
		}
		app.recordPreviousRunDiagnostics()
		return pendingReportCount(t)
	}

	// Gate off (装机默认): the two benign phases are cleared without a report;
	// the two abnormal phases (wedged + unknown) each write one — they have no
	// other crash sink (1018-fix2).
	if got := runScan(t, false); got != 2 {
		t.Fatalf("gate off: pending reports = %d, want 2 (benign cleared, wedged + unknown reported — task 1018-fix2)", got)
	}
	// Gate on: identical outcome — the gate no longer changes report behavior.
	if got := runScan(t, true); got != 2 {
		t.Fatalf("gate on: pending reports = %d, want 2 (the gate does not change report behavior)", got)
	}
	removeAllPendingCrashes()

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
