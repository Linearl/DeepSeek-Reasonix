package main

import (
	"testing"

	"reasonix/internal/config"
)

// Task 528: the heap-high trigger threshold gained a settings-panel numeric
// input. The task 262 lesson applies verbatim — a config knob whose Wails App
// method or view mapping is missing turns into a silently-dead switch, so this
// file pins the whole desktop chain: the compile-time method pin, the on-disk
// save, both view readbacks, and the mirror-then-[agent] resolution the views
// share.
type heapHighThresholdSetter interface {
	SetPerfMonitorHeapHighThresholdMB(mb int) error
}

var _ heapHighThresholdSetter = (*App)(nil)

// TestSetPerfMonitorHeapHighThresholdMBPersistsAndReadsBack: the setter must
// persist the clamped value and both the boot view and the settings view must
// read it back (out-of-range input is clamped by the config layer, tested in
// internal/config; here we pin the desktop mapping).
func TestSetPerfMonitorHeapHighThresholdMBPersistsAndReadsBack(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := &App{}

	if err := app.SetPerfMonitorHeapHighThresholdMB(8192); err != nil {
		t.Fatalf("SetPerfMonitorHeapHighThresholdMB(8192): %v", err)
	}
	cfg, err := config.LoadForEditReadOnlyStrict(config.UserConfigPath())
	if err != nil {
		t.Fatalf("load saved user config: %v", err)
	}
	if cfg.Agent.PerfMonitorHeapHighThresholdMB != 8192 {
		t.Fatalf("saved user config agent threshold = %d, want 8192", cfg.Agent.PerfMonitorHeapHighThresholdMB)
	}
	if cfg.Desktop.PerfMonitorHeapHighThresholdMB != 8192 {
		t.Fatalf("saved user config desktop mirror = %d, want 8192 (task 528 dual write)", cfg.Desktop.PerfMonitorHeapHighThresholdMB)
	}
	if boot := app.DesktopStartupSettings(); boot.PerfMonitorHeapHighThresholdMB != 8192 {
		t.Fatalf("DesktopStartupSettings view threshold = %d, want 8192", boot.PerfMonitorHeapHighThresholdMB)
	}
	if view := app.Settings(); view.PerfMonitorHeapHighThresholdMB != 8192 {
		t.Fatalf("Settings view threshold = %d, want 8192", view.PerfMonitorHeapHighThresholdMB)
	}

	// Out-of-range input must land clamped on disk (no raw 100).
	if err := app.SetPerfMonitorHeapHighThresholdMB(100); err != nil {
		t.Fatalf("SetPerfMonitorHeapHighThresholdMB(100): %v", err)
	}
	cfg, err = config.LoadForEditReadOnlyStrict(config.UserConfigPath())
	if err != nil {
		t.Fatalf("reload saved user config: %v", err)
	}
	if cfg.Agent.PerfMonitorHeapHighThresholdMB != config.PerfMonitorHeapHighMinThresholdMB {
		t.Fatalf("below-floor input stored %d, want clamp to %d", cfg.Agent.PerfMonitorHeapHighThresholdMB, config.PerfMonitorHeapHighMinThresholdMB)
	}
	if view := app.Settings(); view.PerfMonitorHeapHighThresholdMB != config.PerfMonitorHeapHighMinThresholdMB {
		t.Fatalf("Settings view below-floor readback = %d, want %d", view.PerfMonitorHeapHighThresholdMB, config.PerfMonitorHeapHighMinThresholdMB)
	}
}

// TestPerfMonitorHeapHighThresholdForView: the [desktop] settings-view mirror
// wins; a zero mirror falls back to the hand-edited [agent] value (a manual
// config.toml edit must still show what the monitor will arm with); two zeros
// mean "built-in default" (the frontend renders 6144).
func TestPerfMonitorHeapHighThresholdForView(t *testing.T) {
	if got := perfMonitorHeapHighThresholdForView(nil); got != 0 {
		t.Fatalf("nil config = %d, want 0", got)
	}

	cfg := &config.Config{}
	if got := perfMonitorHeapHighThresholdForView(cfg); got != 0 {
		t.Fatalf("two zeros = %d, want 0 (built-in default)", got)
	}

	cfg = &config.Config{}
	cfg.Desktop.PerfMonitorHeapHighThresholdMB = 8192
	cfg.Agent.PerfMonitorHeapHighThresholdMB = 4096
	if got := perfMonitorHeapHighThresholdForView(cfg); got != 8192 {
		t.Fatalf("mirror present = %d, want 8192 (mirror wins)", got)
	}

	cfg = &config.Config{}
	cfg.Agent.PerfMonitorHeapHighThresholdMB = 4096
	if got := perfMonitorHeapHighThresholdForView(cfg); got != 4096 {
		t.Fatalf("hand-edited [agent] only = %d, want 4096 (fallback)", got)
	}
}
