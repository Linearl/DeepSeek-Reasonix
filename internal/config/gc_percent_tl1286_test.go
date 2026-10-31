package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestSetGCPercentClamps: the tl1286 GC-strategy dial is never stored out of
// range (0 = follow the Go default, equivalent to 100; 1000 is the ceiling —
// beyond that the dial has no practical meaning for a desktop).
func TestSetGCPercentClamps(t *testing.T) {
	c := Default()
	for _, tc := range []struct{ in, want int }{{-5, 0}, {0, 0}, {1, 1}, {50, 50}, {150, 150}, {1500, 1000}} {
		if err := c.SetGCPercent(tc.in); err != nil {
			t.Fatalf("set %d: %v", tc.in, err)
		}
		if c.Agent.GCPercent != tc.want {
			t.Fatalf("set %d stored agent=%d, want %d", tc.in, c.Agent.GCPercent, tc.want)
		}
	}
}

// TestRenderGCPercent: the dial renders unconditionally (so a settings-view
// change or a hand-added line is not dropped on save) and survives a
// render/decode round trip. 0 renders too — it is the meaningful "follow the
// Go default" state, not an omission.
func TestRenderGCPercent(t *testing.T) {
	c := Default()
	rendered := RenderTOML(c)
	if !strings.Contains(rendered, "gc_percent = 0") {
		t.Fatalf("rendered config is missing the gc_percent line: %v", rendered)
	}
	if err := c.SetGCPercent(150); err != nil {
		t.Fatal(err)
	}
	rendered = RenderTOML(c)
	if !strings.Contains(rendered, "gc_percent = 150") {
		t.Fatalf("rendered config is missing the dial: %v", rendered)
	}
	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v", err)
	}
	if got.Agent.GCPercent != 150 {
		t.Fatalf("dial did not survive the round trip: agent=%d", got.Agent.GCPercent)
	}
}

// TestRetiredGoMemLimitStillDecodes: tl1286 acceptance ② — a config carrying
// the retired go_mem_limit_mb key (both the [agent] value and the old
// [desktop] settings-view mirror) must still parse cleanly and boot with the
// knob untouched, and the retired lines must never render back on save.
func TestRetiredGoMemLimitStillDecodes(t *testing.T) {
	oldCfg := `
[agent]
go_mem_limit_mb = 800

[desktop]
go_mem_limit_mb = 800
`
	var got Config
	if _, err := toml.Decode(oldCfg, &got); err != nil {
		t.Fatalf("old config with the retired key does not parse: %v", err)
	}
	if got.Agent.GoMemLimitMB != 800 || got.Desktop.GoMemLimitMB != 800 {
		t.Fatalf("retired values lost on decode: agent=%d desktop=%d",
			got.Agent.GoMemLimitMB, got.Desktop.GoMemLimitMB)
	}
	if got.Agent.GCPercent != 0 {
		t.Fatalf("retired key leaked into the new dial: gc_percent=%d", got.Agent.GCPercent)
	}
	rendered := RenderTOML(&got)
	if strings.Contains(rendered, "go_mem_limit_mb") {
		t.Fatalf("retired key rendered back on save: %v", rendered)
	}
	if !strings.Contains(rendered, "gc_percent = 0") {
		t.Fatalf("replacement dial missing from the re-render: %v", rendered)
	}
}
