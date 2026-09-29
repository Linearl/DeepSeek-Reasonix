package config

import (
	"strings"
	"testing"
)

// Task 385a: the lab 回答风格 section owns exactly two toml keys — the UI gate
// (ships off) and the persisted [agent] output_style. The gate is a plain
// default-off bool, so the render must show false untouched and true after
// the setter (81/123 lost-save lesson: an unlisted key flips back off on the
// next save).
func TestOutputStyleUIRendersOffByDefaultAndOnAfterSet(t *testing.T) {
	out := RenderTOMLForScope(&Config{}, RenderScopeUser)
	if !strings.Contains(out, "experimental_output_style_ui = false") {
		t.Fatalf("output style UI ships off: missing false render\n---\n%s", out)
	}
	on := &Config{}
	if err := on.SetExperimentalOutputStyleUI(true); err != nil {
		t.Fatalf("set output style UI: %v", err)
	}
	out = RenderTOMLForScope(on, RenderScopeUser)
	if !strings.Contains(out, "experimental_output_style_ui = true") {
		t.Fatalf("output style UI enable must render true\n---\n%s", out)
	}
	if !on.Desktop.ExperimentalOutputStyleUI {
		t.Fatal("setter must flip the Desktop copy the settings view reads")
	}
}

// The style value itself round-trips through render with one canonical "no
// style" representation: "" and "default" both land on the commented default
// line, any real style lands on an active output_style line.
func TestOutputStyleValueRoundTripThroughRender(t *testing.T) {
	c := &Config{}
	if err := c.SetOutputStyle("concise"); err != nil {
		t.Fatalf("set output style: %v", err)
	}
	if c.Agent.OutputStyle != "concise" {
		t.Fatalf("agent output_style = %q, want concise", c.Agent.OutputStyle)
	}
	out := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(out, `output_style = "concise"`) {
		t.Fatalf("rendered user config is missing output_style = \"concise\"\n---\n%s", out)
	}

	// "default" and stray whitespace normalize to the empty canonical value.
	for _, name := range []string{"default", "  default  "} {
		if err := c.SetOutputStyle(name); err != nil {
			t.Fatalf("SetOutputStyle(%q): %v", name, err)
		}
		if c.Agent.OutputStyle != "" {
			t.Fatalf("SetOutputStyle(%q) must normalize to empty, got %q", name, c.Agent.OutputStyle)
		}
	}
	if err := c.SetOutputStyle("  concise  "); err != nil {
		t.Fatalf("set output style with spaces: %v", err)
	}
	if c.Agent.OutputStyle != "concise" {
		t.Fatalf("whitespace must be trimmed, got %q", c.Agent.OutputStyle)
	}

	// Back to no style: the commented default line returns and the active
	// line disappears, so a cleared choice cannot survive as a stale value.
	if err := c.SetOutputStyle(""); err != nil {
		t.Fatalf("clear output style: %v", err)
	}
	out = RenderTOMLForScope(c, RenderScopeUser)
	if strings.Contains(out, `output_style = "concise"`) {
		t.Fatalf("cleared style must not keep a stale output_style line\n---\n%s", out)
	}
	if !strings.Contains(out, `# output_style = "explanatory"`) {
		t.Fatalf("cleared style must render the commented default hint\n---\n%s", out)
	}
}
