package main

import (
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/config"
)

// TestPerfMonitorSettingsHeapInterval: the heap knob resolves like its sampler
// sibling — unset keeps the built-in 60s, an explicit value passes through, and
// a hand-edited value is clamped into 10..3600 (a typo must not multiply the
// ~0.7MB-per-dump write churn into a busy loop).
func TestPerfMonitorSettingsHeapInterval(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  int
		want time.Duration
	}{
		{"unset keeps default 60s", 0, 60 * time.Second},
		{"explicit passes through", 300, 300 * time.Second},
		{"below the floor clamps to 10s", 3, 10 * time.Second},
		{"above the ceiling clamps to 1h", 99999, 3600 * time.Second},
	} {
		cfg := &config.Config{}
		cfg.Agent.PerfMonitorHeapIntervalSeconds = tc.val
		_, _, got, _ := perfMonitorSettings(cfg)
		if got != tc.want {
			t.Fatalf("%s: heap interval = %v, want %v", tc.name, got, tc.want)
		}
	}

	// nil config (caller without a loaded config) must keep the default too.
	if _, _, got, _ := perfMonitorSettings(nil); got != 60*time.Second {
		t.Fatalf("nil config heap interval = %v, want 60s", got)
	}
}

// The knob must reach the running monitor: with the sampler tick held far away,
// heap dumps still land on the configured cadence. The dump file name carries
// second precision, so the interval must exceed 1s for two dumps to yield two
// distinct files — hence 1.1s over a 2.4s window (a 60s default would produce
// zero dumps here).
func TestPerfMonitorHeapIntervalDrivesDumps(t *testing.T) {
	perfDir := filepath.Join(t.TempDir(), "perf")
	monitor := newPerfMonitor(nil, perfDir, time.Hour, time.Hour, 1100*time.Millisecond, nil)
	monitor.Start()
	time.Sleep(2400 * time.Millisecond)
	monitor.Stop()

	files, err := filepath.Glob(filepath.Join(perfDir, "heap-*.pprof"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 2 {
		t.Fatalf("heap dumps = %d over a 2.4s window at a 1.1s interval, want >= 2: %v", len(files), files)
	}
}
