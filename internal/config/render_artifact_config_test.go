package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestRenderArtifactConfigSetter pins the task-944 lab switch setter: the
// field is a plain bool toggled by SetExperimentalRenderArtifact.
func TestRenderArtifactConfigSetter(t *testing.T) {
	c := Default()
	if c.Agent.ExperimentalRenderArtifact {
		t.Fatal("render_artifact must default off (铁律 2: off = zero behaviour)")
	}
	if err := c.SetExperimentalRenderArtifact(true); err != nil {
		t.Fatalf("SetExperimentalRenderArtifact: %v", err)
	}
	if !c.Agent.ExperimentalRenderArtifact {
		t.Fatal("setter did not persist the field")
	}
	if err := c.SetExperimentalRenderArtifact(false); err != nil {
		t.Fatalf("SetExperimentalRenderArtifact(false): %v", err)
	}
	if c.Agent.ExperimentalRenderArtifact {
		t.Fatal("off toggle did not persist")
	}
}

// TestRenderArtifactRenderIncludesKey pins the 81/123 lost-save rule for the
// new key: RenderTOML must emit it in BOTH states — an unlisted key flips
// back to its default on the next save.
func TestRenderArtifactRenderIncludesKey(t *testing.T) {
	c := Default()
	c.Agent.ExperimentalRenderArtifact = true
	if out := RenderTOML(c); !strings.Contains(out, "experimental_render_artifact = true") {
		t.Fatalf("render must carry the on state:\n%s", out)
	}
	off := Default()
	out := RenderTOML(off)
	if !strings.Contains(out, "experimental_render_artifact = false") {
		t.Fatalf("off state must render explicitly (omit-on-off 丢档教训):\n%s", out)
	}
	// Round-trip: render → decode must restore the saved-on value.
	on := Default()
	on.Agent.ExperimentalRenderArtifact = true
	renderedOn := RenderTOML(on)
	var got Config
	if _, err := toml.Decode(renderedOn, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v", err)
	}
	if !got.Agent.ExperimentalRenderArtifact {
		t.Fatal("experimental_render_artifact did not round-trip")
	}
}
