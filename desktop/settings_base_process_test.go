package main

import (
	"testing"

	"reasonix/internal/config"
)

// TestExperimentalBaseProcessSetterAndViewReadback pins the S1 switch's
// settings chain on the Go side: the lab setter persists through the app's own
// config path, and BOTH settings views carry the flag back to the panel — a
// view without the field would silently read "off" and make the switch
// impossible to turn on (the task-81 precedent that motivated the second view).
func TestExperimentalBaseProcessSetterAndViewReadback(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	if app.Settings().ExperimentalBaseProcess {
		t.Fatal("the S1 switch must default to off (pure inline baseline)")
	}
	if err := app.SetExperimentalBaseProcess(true); err != nil {
		t.Fatal(err)
	}
	if !app.Settings().ExperimentalBaseProcess {
		t.Fatal("Settings() must carry the flipped switch back to the panel")
	}
	if !app.DesktopStartupSettings().ExperimentalBaseProcess {
		t.Fatal("DesktopStartupSettings() must carry the flipped switch back")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Agent.ExperimentalBaseProcess {
		t.Fatal("the flip must persist in the user config")
	}
	// And back off — the baseline stays reachable.
	if err := app.SetExperimentalBaseProcess(false); err != nil {
		t.Fatal(err)
	}
	if app.Settings().ExperimentalBaseProcess {
		t.Fatal("the off flip must persist")
	}
}
