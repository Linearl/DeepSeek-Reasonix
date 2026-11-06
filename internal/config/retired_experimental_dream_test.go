package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// tl1453 retired the dream/distill memory-curation tools (constitution Q1:
// the periodic-reflection goal is served by plain scheduled tasks, not a
// product mechanism). Acceptance: a user config still carrying the retired
// experimental_dream key — in either the [agent] section or the old [desktop]
// mirror spelling — must load cleanly, keep every other key intact, and never
// render the key back on save (go_mem_limit_mb / tl1286 precedent).
func TestRetiredExperimentalDreamStillDecodes(t *testing.T) {
	oldCfg := `
[agent]
experimental_dream = true
experimental_session_collab = true

[desktop]
experimental_dream = true
`
	var got Config
	if _, err := toml.Decode(oldCfg, &got); err != nil {
		t.Fatalf("old config with the retired key does not parse: %v", err)
	}
	if !got.Agent.ExperimentalDream || !got.Desktop.ExperimentalDream {
		t.Fatal("retired key must still decode into its tombstone fields (values are ignored, not lost)")
	}
	if !got.Agent.ExperimentalSessionCollab {
		t.Fatal("a neighbouring live key must survive the decode untouched")
	}

	rendered := RenderTOMLForScope(&got, RenderScopeUser)
	if strings.Contains(rendered, "experimental_dream") {
		t.Fatalf("retired key rendered back on save:\n%s", rendered)
	}
	if !strings.Contains(rendered, "experimental_session_collab = true") {
		t.Fatalf("live neighbour key missing from the re-render:\n%s", rendered)
	}
}

// The tombstone is inert end to end: the real load path must not fail, warn,
// or fold the retired key into anything, and a save must drop the line.
func TestRetiredExperimentalDreamLoadAndSaveDropsKey(t *testing.T) {
	home := isolateUserConfigHome(t)
	path := UserConfigPath()
	requireTestPathWithin(t, home, path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[agent]\nexperimental_dream = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := LoadForEdit(path)
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "experimental_dream") {
		t.Fatalf("save must drop the retired key (fixed-key-set render):\n%s", raw)
	}
}
