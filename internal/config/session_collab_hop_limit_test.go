package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestSetSessionCollabHopLimitClamps: the experimental ceiling is never stored out of
// range, and the agent value stays in step with its settings-view mirror (task 204).
func TestSetSessionCollabHopLimitClamps(t *testing.T) {
	c := Default()
	for _, tc := range []struct{ in, want int }{{1, 3}, {2, 3}, {3, 3}, {42, 42}, {1000, 1000}, {5000, 1000}} {
		if err := c.SetSessionCollabHopLimit(tc.in); err != nil {
			t.Fatalf("set %d: %v", tc.in, err)
		}
		if c.Agent.SessionCollabHopLimit != tc.want || c.Desktop.SessionCollabHopLimit != tc.want {
			t.Fatalf("set %d stored agent=%d desktop=%d, want %d",
				tc.in, c.Agent.SessionCollabHopLimit, c.Desktop.SessionCollabHopLimit, tc.want)
		}
	}
}

// TestRenderSessionCollabHopLimit: the ceiling renders (so a settings-view change is
// not dropped on save) and survives a render/decode cycle.
func TestRenderSessionCollabHopLimit(t *testing.T) {
	c := Default()
	if err := c.SetSessionCollabHopLimit(9); err != nil {
		t.Fatal(err)
	}
	rendered := RenderTOML(c)
	if !strings.Contains(rendered, "session_collab_hop_limit = 9") {
		t.Fatalf("rendered config is missing the hop limit: %v", rendered)
	}
	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v", err)
	}
	if got.Agent.SessionCollabHopLimit != 9 || got.Desktop.SessionCollabHopLimit != 9 {
		t.Fatalf("hop limit did not survive the round trip: agent=%d desktop=%d",
			got.Agent.SessionCollabHopLimit, got.Desktop.SessionCollabHopLimit)
	}
}
