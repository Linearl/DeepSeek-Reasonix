package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestSetDetachedIdleReleaseMinutesClamps: the task-308-O4 idle threshold is
// never stored out of range (0 = never release; a week is the ceiling).
func TestSetDetachedIdleReleaseMinutesClamps(t *testing.T) {
	c := Default()
	for _, tc := range []struct{ in, want int }{{-5, 0}, {0, 0}, {1, 1}, {30, 30}, {20000, 10080}} {
		if err := c.SetDetachedIdleReleaseMinutes(tc.in); err != nil {
			t.Fatalf("set %d: %v", tc.in, err)
		}
		if c.Agent.DetachedIdleReleaseMinutes != tc.want || c.Desktop.DetachedIdleReleaseMinutes != tc.want {
			t.Fatalf("set %d stored agent=%d desktop=%d, want %d",
				tc.in, c.Agent.DetachedIdleReleaseMinutes, c.Desktop.DetachedIdleReleaseMinutes, tc.want)
		}
	}
}

// TestRenderDetachedIdleReleaseMinutes: the threshold renders unconditionally
// (so a settings-view change or a hand-added line is not dropped on save) and
// survives a render/decode round trip.
func TestRenderDetachedIdleReleaseMinutes(t *testing.T) {
	c := Default()
	if err := c.SetDetachedIdleReleaseMinutes(30); err != nil {
		t.Fatal(err)
	}
	rendered := RenderTOML(c)
	if !strings.Contains(rendered, "detached_idle_release_minutes = 30") {
		t.Fatalf("rendered config is missing the threshold: %v", rendered)
	}
	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v", err)
	}
	if got.Agent.DetachedIdleReleaseMinutes != 30 || got.Desktop.DetachedIdleReleaseMinutes != 30 {
		t.Fatalf("threshold did not survive the round trip: agent=%d desktop=%d",
			got.Agent.DetachedIdleReleaseMinutes, got.Desktop.DetachedIdleReleaseMinutes)
	}
}
