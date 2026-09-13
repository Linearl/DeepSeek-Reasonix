package main

import (
	"strings"
	"testing"
)

// Guard tests for the restart-and-update experiment (task 81). They stop short of the
// opt-in gate on purpose: that one reads the user's real config, so asserting on it here
// would make the test depend on whatever this machine happens to have enabled. What is
// asserted instead is the order of the guards, which is the part that has to stay true
// for the gate to be reachable at all.

func TestRestartAndUpdateRefusesWithoutAnApp(t *testing.T) {
	var app *App
	err := app.RestartAndUpdate("", "v1.38.3")
	if err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	if !strings.Contains(err.Error(), "no app") {
		t.Fatalf("expected a nil-receiver refusal, got %q", err)
	}
}

func TestRestartAndUpdateRequiresAVersion(t *testing.T) {
	app := &App{}
	for _, version := range []string{"", "   "} {
		err := app.RestartAndUpdate("/tmp/staging", version)
		if err == nil {
			t.Fatalf("version %q was accepted", version)
		}
		if !strings.Contains(err.Error(), "version is required") {
			t.Fatalf("version %q gave %q, want a version-required refusal", version, err)
		}
	}
}

// The experiment gate must stay reachable: a missing version has to be reported as a
// missing version even when the experiment is off, otherwise a caller cannot tell a
// malformed request from a disabled feature.
func TestRestartAndUpdateReportsTheVersionBeforeTheExperimentGate(t *testing.T) {
	app := &App{}
	err := app.RestartAndUpdate("/tmp/staging", "")
	if err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	if strings.Contains(err.Error(), "experiment is off") {
		t.Fatalf("the experiment gate answered before the argument check: %q", err)
	}
	if !strings.Contains(err.Error(), "version is required") {
		t.Fatalf("got %q, want a version-required refusal", err)
	}
}
