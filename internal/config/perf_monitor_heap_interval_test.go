package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestSetPerfMonitorHeapIntervalSecondsClamps: 0 means "back to the built-in
// default (60s)" and is stored as-is; every explicit value lands in 10..3600 —
// a hand-edited config must not turn the ~0.7MB-per-dump churn into a busy
// loop, nor stretch the gap past an hour.
func TestSetPerfMonitorHeapIntervalSecondsClamps(t *testing.T) {
	c := Default()
	for _, tc := range []struct{ in, want int }{{0, 0}, {1, 10}, {3, 10}, {9, 10}, {10, 10}, {300, 300}, {3600, 3600}, {9999, 3600}} {
		if err := c.SetPerfMonitorHeapIntervalSeconds(tc.in); err != nil {
			t.Fatalf("set %d: %v", tc.in, err)
		}
		if c.Agent.PerfMonitorHeapIntervalSeconds != tc.want {
			t.Fatalf("set %d stored agent=%d, want %d", tc.in, c.Agent.PerfMonitorHeapIntervalSeconds, tc.want)
		}
	}
}

// TestPerfMonitorHeapIntervalSecondsTOML: the key is unset by default (0 = the
// desktop side keeps its 60s), an explicit value decodes verbatim, and a set
// value survives a render/decode round trip.
func TestPerfMonitorHeapIntervalSecondsTOML(t *testing.T) {
	// Default: the key is absent, the stored zero is the "keep the default" mark.
	var blank Config
	if _, err := toml.Decode("", &blank); err != nil {
		t.Fatal(err)
	}
	if blank.Agent.PerfMonitorHeapIntervalSeconds != 0 {
		t.Fatalf("default = %d, want 0 (keep built-in default)", blank.Agent.PerfMonitorHeapIntervalSeconds)
	}

	// Explicit value decodes verbatim (the key lives under [agent]).
	var explicit Config
	if _, err := toml.Decode("[agent]\nperf_monitor_heap_interval_seconds = 300", &explicit); err != nil {
		t.Fatal(err)
	}
	if explicit.Agent.PerfMonitorHeapIntervalSeconds != 300 {
		t.Fatalf("explicit = %d, want 300", explicit.Agent.PerfMonitorHeapIntervalSeconds)
	}

	// Round trip: a set value must render and decode back unchanged.
	c := Default()
	if err := c.SetPerfMonitorHeapIntervalSeconds(300); err != nil {
		t.Fatal(err)
	}
	rendered := RenderTOML(c)
	if !strings.Contains(rendered, "perf_monitor_heap_interval_seconds = 300") {
		t.Fatalf("rendered config is missing the heap interval key: %v", rendered)
	}
	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v", err)
	}
	if got.Agent.PerfMonitorHeapIntervalSeconds != 300 {
		t.Fatalf("round trip lost the value: got %d, want 300", got.Agent.PerfMonitorHeapIntervalSeconds)
	}
}
