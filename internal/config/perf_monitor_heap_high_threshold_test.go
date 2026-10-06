package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestSetPerfMonitorHeapHighThresholdMBClamps: 0 means "back to the built-in
// default (6GB)" and is stored as-is; every explicit value lands in
// 1024..131072 (task 501 clamps, task 528 UI relies on them) — and the settings
// view now mirrors the knob, so the setter must write BOTH the [agent] value
// the monitor reads and the [desktop] mirror the panel reads back.
func TestSetPerfMonitorHeapHighThresholdMBClamps(t *testing.T) {
	c := Default()
	for _, tc := range []struct{ in, want int }{
		{0, 0}, {1, 1024}, {1023, 1024}, {1024, 1024}, {6144, 6144},
		{131072, 131072}, {131073, 131072}, {999999, 131072},
	} {
		if err := c.SetPerfMonitorHeapHighThresholdMB(tc.in); err != nil {
			t.Fatalf("set %d: %v", tc.in, err)
		}
		if c.Agent.PerfMonitorHeapHighThresholdMB != tc.want {
			t.Fatalf("set %d stored agent=%d, want %d", tc.in, c.Agent.PerfMonitorHeapHighThresholdMB, tc.want)
		}
		if c.Desktop.PerfMonitorHeapHighThresholdMB != tc.want {
			t.Fatalf("set %d stored desktop mirror=%d, want %d (task 528 dual write)", tc.in, c.Desktop.PerfMonitorHeapHighThresholdMB, tc.want)
		}
	}
}

// TestPerfMonitorHeapHighThresholdMBTOML: the key is unset by default (0 = the
// built-in 6GB default), an explicit value decodes verbatim under [agent], and
// a set value survives a render/decode round trip on BOTH sections — the task
// 81/123 lesson: a mirror the renderer does not list is silently dropped on
// save and the input snaps back.
func TestPerfMonitorHeapHighThresholdMBTOML(t *testing.T) {
	// Default: the key is absent, the stored zero is the "keep the default" mark.
	var blank Config
	if _, err := toml.Decode("", &blank); err != nil {
		t.Fatal(err)
	}
	if blank.Agent.PerfMonitorHeapHighThresholdMB != 0 {
		t.Fatalf("default = %d, want 0 (keep built-in default)", blank.Agent.PerfMonitorHeapHighThresholdMB)
	}

	// Explicit value decodes verbatim (the key lives under [agent]).
	var explicit Config
	if _, err := toml.Decode("[agent]\nperf_monitor_heap_high_threshold_mb = 8192", &explicit); err != nil {
		t.Fatal(err)
	}
	if explicit.Agent.PerfMonitorHeapHighThresholdMB != 8192 {
		t.Fatalf("explicit = %d, want 8192", explicit.Agent.PerfMonitorHeapHighThresholdMB)
	}

	// Round trip: both the [agent] value and the [desktop] settings-view
	// mirror must render and decode back unchanged.
	c := Default()
	if err := c.SetPerfMonitorHeapHighThresholdMB(8192); err != nil {
		t.Fatal(err)
	}
	rendered := RenderTOML(c)
	if !strings.Contains(rendered, "perf_monitor_heap_high_threshold_mb = 8192") {
		t.Fatalf("rendered config is missing the heap-high threshold key: %v", rendered)
	}
	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v", err)
	}
	if got.Agent.PerfMonitorHeapHighThresholdMB != 8192 {
		t.Fatalf("round trip lost the [agent] value: got %d, want 8192", got.Agent.PerfMonitorHeapHighThresholdMB)
	}
	if got.Desktop.PerfMonitorHeapHighThresholdMB != 8192 {
		t.Fatalf("round trip lost the [desktop] mirror: got %d, want 8192 (task 528)", got.Desktop.PerfMonitorHeapHighThresholdMB)
	}
}
