package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// The user-config renderer writes a fixed set of keys, so a preference that is not listed
// there can be set in Settings and still vanish on save. That is exactly what happened to
// quick commands: an added snippet lived in memory until the next write and was then gone,
// so the settings list read back empty, search found nothing, and the composer's + menu -
// which renders its quick-command section only when the list is non-empty - never appeared
// at all. Three of the four reported symptoms were this one missing key.
//
// These assertions pin the key to the renderer, and pin the rendered shape to what the
// loader accepts.
func TestQuickCommandsRoundTripThroughRender(t *testing.T) {
	disabled := false
	c := &Config{}
	c.Desktop.QuickCommands = []QuickCommandEntry{
		{Title: "周报", Text: "生成周报"},
		{Title: "静默", Text: "安静一点", Enabled: &disabled},
	}

	out := RenderTOMLForScope(c, RenderScopeUser)
	for _, want := range []string{`title = "周报"`, `text = "生成周报"`, `enabled = false`} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered user config is missing %q\n---\n%s", want, out)
		}
	}

	var decoded Config
	if _, err := toml.Decode(out, &decoded); err != nil {
		t.Fatalf("rendered config does not parse: %v\n---\n%s", err, out)
	}
	got := decoded.Desktop.QuickCommands
	if len(got) != 2 {
		t.Fatalf("round trip lost entries: %+v", got)
	}
	if got[0].Title != "周报" || got[1].Title != "静默" {
		t.Fatalf("round trip changed the entries: %+v", got)
	}
	if got[1].Enabled == nil || *got[1].Enabled {
		t.Fatalf("enabled=false did not survive the round trip: %+v", got[1])
	}
}

func TestQuickCommandsUnsetStaysOutOfTheConfig(t *testing.T) {
	// A config nobody touched should not grow an empty quick_commands key.
	out := RenderTOMLForScope(&Config{}, RenderScopeUser)
	if strings.Contains(out, "quick_commands") {
		t.Fatalf("untouched config should not mention quick_commands\n---\n%s", out)
	}
}

func TestQuickCommandArrayKeepsFollowingKeysOutOfTheEntry(t *testing.T) {
	// The value is an inline array rather than [[desktop.quick_commands]] because an array
	// table switches the TOML current table: the sections rendered after it would have
	// become members of the last snippet instead of sections of their own.
	c := &Config{}
	c.Desktop.QuickCommands = []QuickCommandEntry{{Title: "t", Text: "x"}}

	out := RenderTOMLForScope(c, RenderScopeUser)
	var decoded Config
	if _, err := toml.Decode(out, &decoded); err != nil {
		t.Fatalf("render does not parse: %v\n---\n%s", err, out)
	}
	if len(decoded.Desktop.QuickCommands) != 1 || decoded.Desktop.QuickCommands[0].Title != "t" {
		t.Fatalf("quick command did not survive: %+v", decoded.Desktop.QuickCommands)
	}
	if !strings.Contains(out, "[billing]") {
		t.Fatalf("the sections after quick_commands should still be rendered\n---\n%s", out)
	}
}
