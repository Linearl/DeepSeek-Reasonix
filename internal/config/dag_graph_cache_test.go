package config

import (
	"strings"
	"testing"
)

// TestDagGraphCacheCapacityHelper pins the read-side semantics: unset (0) and
// out-of-range stored values read as the built-in default / clamp, so a
// hand-edited config can neither disable the cache nor request an unbounded
// one (task 196fix2).
func TestDagGraphCacheCapacityHelper(t *testing.T) {
	if got := DagGraphCacheCapacity(nil); got != DagGraphCacheCapacityDefault {
		t.Fatalf("nil config capacity = %d, want %d", got, DagGraphCacheCapacityDefault)
	}
	cases := []struct {
		stored int
		want   int
	}{
		{0, DagGraphCacheCapacityDefault}, // unset: built-in 3
		{-5, DagGraphCacheCapacityDefault},
		{1, 1},
		{5, 5},
		{DagGraphCacheCapacityMax, DagGraphCacheCapacityMax},
		{99, DagGraphCacheCapacityMax}, // clamp, never unbounded
	}
	for _, tc := range cases {
		cfg := &Config{}
		cfg.Agent.DagGraphCacheCapacity = tc.stored
		if got := DagGraphCacheCapacity(cfg); got != tc.want {
			t.Errorf("stored %d -> %d, want %d", tc.stored, got, tc.want)
		}
	}
}

// TestSetDagGraphCacheCapacityValidation: the setter refuses out-of-range
// input instead of storing it (the settings UI and the file must not
// disagree about what the user picked).
func TestSetDagGraphCacheCapacityValidation(t *testing.T) {
	c := &Config{}
	for _, bad := range []int{0, -1, DagGraphCacheCapacityMax + 1} {
		if err := c.SetDagGraphCacheCapacity(bad); err == nil {
			t.Errorf("capacity %d must be refused", bad)
		}
	}
	if c.Agent.DagGraphCacheCapacity != 0 {
		t.Fatalf("a refused write must not mutate the config: got %d", c.Agent.DagGraphCacheCapacity)
	}
	for _, good := range []int{1, 3, DagGraphCacheCapacityMax} {
		if err := c.SetDagGraphCacheCapacity(good); err != nil {
			t.Errorf("capacity %d must be accepted: %v", good, err)
		}
		if c.Agent.DagGraphCacheCapacity != good {
			t.Errorf("stored %d, want %d", c.Agent.DagGraphCacheCapacity, good)
		}
	}
}

// TestRenderDagGraphCacheCapacityLine pins the render table (81/123 lost-line
// lesson): the main renderer always emits the line — even at the unset value —
// so a hand-added tuned value survives the next settings save.
func TestRenderDagGraphCacheCapacityLine(t *testing.T) {
	out := RenderTOML(&Config{})
	if !strings.Contains(out, "dag_graph_cache_capacity = 0") {
		t.Errorf("RenderTOML(defaults) must carry the line at the unset value:\n%s", out)
	}
	tuned := &Config{}
	tuned.Agent.DagGraphCacheCapacity = 5
	out = RenderTOML(tuned)
	if !strings.Contains(out, "dag_graph_cache_capacity = 5") {
		t.Errorf("RenderTOML(tuned) missing the tuned line:\n%s", out)
	}
}

// TestDagGraphCacheByteCapsHelpers pin the task-499 read-side semantics: 0
// and out-of-range read as the built-in default / clamp, and the per-entry
// ceiling never exceeds the total — one admission must not defeat the total.
func TestDagGraphCacheByteCapsHelpers(t *testing.T) {
	if got := DagGraphCacheMaxMB(nil); got != DagGraphCacheMaxMBDefault {
		t.Fatalf("nil config total = %d, want %d", got, DagGraphCacheMaxMBDefault)
	}
	if got := DagGraphCacheEntryMaxMB(nil); got != DagGraphCacheEntryMaxMBDefault {
		t.Fatalf("nil config entry = %d, want %d", got, DagGraphCacheEntryMaxMBDefault)
	}
	cases := []struct {
		total, entry int64
		wantTotal    int64
		wantEntry    int64
	}{
		{0, 0, DagGraphCacheMaxMBDefault, DagGraphCacheEntryMaxMBDefault}, // unset: built-ins
		{-1, -1, DagGraphCacheMaxMBDefault, DagGraphCacheEntryMaxMBDefault},
		{8, 8, DagGraphCacheMaxMBDefault, DagGraphCacheEntryMaxMBDefault}, // below min 16: default
		{512, 256, 512, 256}, // both in range
		{70000, 70000, DagGraphCacheMaxMBMax, DagGraphCacheMaxMBMax}, // clamped at the max
		{256, 4096, 256, 256}, // entry clamped under total
	}
	for _, tc := range cases {
		cfg := &Config{}
		cfg.Agent.DagGraphCacheMaxMB = tc.total
		cfg.Agent.DagGraphCacheEntryMaxMB = tc.entry
		if got := DagGraphCacheMaxMB(cfg); got != tc.wantTotal {
			t.Errorf("total stored %d -> %d, want %d", tc.total, got, tc.wantTotal)
		}
		if got := DagGraphCacheEntryMaxMB(cfg); got != tc.wantEntry {
			t.Errorf("entry stored %d (total %d) -> %d, want %d", tc.entry, tc.total, got, tc.wantEntry)
		}
	}
}

// TestSetDagGraphCacheByteCapsValidation: both setters refuse out-of-range
// values without mutating the config.
func TestSetDagGraphCacheByteCapsValidation(t *testing.T) {
	c := &Config{}
	for _, bad := range []int64{0, -1, 8, DagGraphCacheMaxMBMax + 1} {
		if err := c.SetDagGraphCacheMaxMB(bad); err == nil {
			t.Errorf("total %d must be refused", bad)
		}
		if err := c.SetDagGraphCacheEntryMaxMB(bad); err == nil {
			t.Errorf("entry %d must be refused", bad)
		}
	}
	if c.Agent.DagGraphCacheMaxMB != 0 || c.Agent.DagGraphCacheEntryMaxMB != 0 {
		t.Fatalf("refused writes must not mutate the config: total=%d entry=%d",
			c.Agent.DagGraphCacheMaxMB, c.Agent.DagGraphCacheEntryMaxMB)
	}
	if err := c.SetDagGraphCacheMaxMB(4096); err != nil {
		t.Fatalf("total 4096 must be accepted: %v", err)
	}
	if err := c.SetDagGraphCacheEntryMaxMB(1024); err != nil {
		t.Fatalf("entry 1024 must be accepted: %v", err)
	}
	if c.Agent.DagGraphCacheMaxMB != 4096 || c.Agent.DagGraphCacheEntryMaxMB != 1024 {
		t.Fatalf("stored total=%d entry=%d, want 4096/1024",
			c.Agent.DagGraphCacheMaxMB, c.Agent.DagGraphCacheEntryMaxMB)
	}
}

// TestRenderDagGraphCacheByteCapLines: the render table always emits both
// task-499 lines, even at the unset value (81/123 lost-line lesson).
func TestRenderDagGraphCacheByteCapLines(t *testing.T) {
	out := RenderTOML(&Config{})
	if !strings.Contains(out, "dag_graph_cache_max_mb = 0") || !strings.Contains(out, "dag_graph_cache_entry_max_mb = 0") {
		t.Errorf("RenderTOML(defaults) must carry both byte-cap lines:\n%s", out)
	}
	tuned := &Config{}
	tuned.Agent.DagGraphCacheMaxMB = 4096
	tuned.Agent.DagGraphCacheEntryMaxMB = 512
	out = RenderTOML(tuned)
	if !strings.Contains(out, "dag_graph_cache_max_mb = 4096") || !strings.Contains(out, "dag_graph_cache_entry_max_mb = 512") {
		t.Errorf("RenderTOML(tuned) missing the tuned byte-cap lines:\n%s", out)
	}
}
