package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestSetGoMemLimitMBClamps: the task-308-O3 soft memory limit is never
// stored out of range (0 = unbounded runtime; 64 GiB is the ceiling).
func TestSetGoMemLimitMBClamps(t *testing.T) {
	c := Default()
	for _, tc := range []struct{ in, want int }{{-1, 0}, {0, 0}, {1, 1}, {30, 30}, {70000, 65536}} {
		if err := c.SetGoMemLimitMB(tc.in); err != nil {
			t.Fatalf("set %d: %v", tc.in, err)
		}
		if c.Agent.GoMemLimitMB != tc.want || c.Desktop.GoMemLimitMB != tc.want {
			t.Fatalf("set %d stored agent=%d desktop=%d, want %d",
				tc.in, c.Agent.GoMemLimitMB, c.Desktop.GoMemLimitMB, tc.want)
		}
	}
}

// TestRenderGoMemLimitMB: the limit renders unconditionally (so a
// settings-view change or a hand-added line is not dropped on save) and
// survives a render/decode round trip.
func TestRenderGoMemLimitMB(t *testing.T) {
	c := Default()
	if err := c.SetGoMemLimitMB(4096); err != nil {
		t.Fatal(err)
	}
	rendered := RenderTOML(c)
	if !strings.Contains(rendered, "go_mem_limit_mb = 4096") {
		t.Fatalf("rendered config is missing the limit: %v", rendered)
	}
	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v", err)
	}
	if got.Agent.GoMemLimitMB != 4096 || got.Desktop.GoMemLimitMB != 4096 {
		t.Fatalf("limit did not survive the round trip: agent=%d desktop=%d",
			got.Agent.GoMemLimitMB, got.Desktop.GoMemLimitMB)
	}
}
