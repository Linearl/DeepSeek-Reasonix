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
