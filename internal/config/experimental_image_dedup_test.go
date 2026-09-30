package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestSetExperimentalImageDedupClamps: the task-373-R1.1 three-position
// switch only ever stores a valid enum value — anything unrecognized is
// clamped to "off" so a hand-edited or future value can never widen the
// write path.
func TestSetExperimentalImageDedupClamps(t *testing.T) {
	c := Default()
	for _, tc := range []struct{ in, want string }{
		{"", "off"}, {"off", "off"}, {"first", "first"}, {"all", "all"},
		{"FIRST", "off"}, {"yes", "off"}, {"true", "off"}, {"2", "off"},
	} {
		if err := c.SetExperimentalImageDedup(tc.in); err != nil {
			t.Fatalf("set %q: %v", tc.in, err)
		}
		if c.Agent.ExperimentalImageDedup != tc.want || c.Desktop.ExperimentalImageDedup != tc.want {
			t.Fatalf("set %q stored agent=%q desktop=%q, want %q",
				tc.in, c.Agent.ExperimentalImageDedup, c.Desktop.ExperimentalImageDedup, tc.want)
		}
	}
}

// TestRenderExperimentalImageDedup: the switch renders unconditionally (so a
// settings-view change or a hand-added line is not dropped on save) and
// survives a render/decode round trip with the set mode intact.
func TestRenderExperimentalImageDedup(t *testing.T) {
	c := Default()
	if err := c.SetExperimentalImageDedup("all"); err != nil {
		t.Fatal(err)
	}
	rendered := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(rendered, `experimental_image_dedup = "all"`) {
		t.Fatal("desktop render missing quoted experimental_image_dedup line")
	}
	full := RenderTOML(c)
	if !strings.Contains(full, `experimental_image_dedup = "all"`) {
		t.Fatal("agent render missing quoted experimental_image_dedup line")
	}
	var back Config
	if _, err := toml.Decode(full, &back); err != nil {
		t.Fatal(err)
	}
	if back.Agent.ExperimentalImageDedup != "all" || back.Desktop.ExperimentalImageDedup != "all" {
		t.Fatalf("round trip lost the mode: agent=%q desktop=%q",
			back.Agent.ExperimentalImageDedup, back.Desktop.ExperimentalImageDedup)
	}
}
