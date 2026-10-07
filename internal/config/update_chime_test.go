package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Task 277 (update-complete chime): the switch defaults to off (iron-rule 2,
// zero regression), the user-scope renderer actually writes the key (the 81/123
// fixed-key-set lesson — an unlisted key is dropped on save and the switch
// flips back to off), and the rendered document round-trips through the loader.
func TestUpdateChimeDefaultAndRoundTrip(t *testing.T) {
	c := Default()
	if c.Desktop.UpdateChime {
		t.Fatal("update chime must default to off")
	}

	if err := c.SetUpdateChime(true); err != nil {
		t.Fatalf("SetUpdateChime: %v", err)
	}
	if !c.Desktop.UpdateChime {
		t.Fatal("SetUpdateChime(true) did not set the field")
	}

	// Render-table assertion: the user-scope renderer must list the key.
	rendered := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(rendered, "update_chime = true") {
		t.Fatalf("user-scope render dropped update_chime (81/123 fixed-key-set lesson):\n%s", rendered)
	}
	// Neighbouring autonomous-update keys must stay rendered too.
	if !strings.Contains(rendered, "experimental_autonomous_update") || !strings.Contains(rendered, "autonomous_update_resume") {
		t.Fatal("rendering the chime displaced neighbouring autonomous-update keys")
	}

	// Round-trip: write the rendered document and load it back.
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(rendered), 0o600); err != nil {
		t.Fatalf("write rendered config: %v", err)
	}
	loaded := LoadForEdit(path)
	if !loaded.Desktop.UpdateChime {
		t.Fatal("update_chime did not survive render/load")
	}
	// Neighbouring autonomous-update fields must be untouched by the new set.
	if loaded.Desktop.ExperimentalAutonomousUpdate {
		t.Fatal("setting the chime flipped the autonomous-update switch")
	}

	if err := loaded.SetUpdateChime(false); err != nil {
		t.Fatalf("SetUpdateChime(false): %v", err)
	}
	if loaded.Desktop.UpdateChime {
		t.Fatal("SetUpdateChime(false) did not clear the field")
	}
	if strings.Contains(RenderTOMLForScope(loaded, RenderScopeUser), "update_chime = true") {
		t.Fatal("off switch still renders as true")
	}
}

// Task 512 (update-chime tune): the dial defaults to nokia, the setter rejects
// unknown values, the user-scope renderer lists the key (the 81/123
// fixed-key-set lesson), the rendered document round-trips through the loader,
// and a hand-edited dirty value reads as nokia (never a broken tune).
func TestUpdateChimeTuneDefaultSetterRoundTrip(t *testing.T) {
	c := Default()
	if got := c.UpdateChimeTuneMode(); got != "nokia" {
		t.Fatalf("update chime tune must default to nokia, got %q", got)
	}

	if err := c.SetUpdateChimeTune("mario"); err != nil {
		t.Fatalf("SetUpdateChimeTune(mario): %v", err)
	}
	if got := c.UpdateChimeTuneMode(); got != "mario" {
		t.Fatalf("SetUpdateChimeTune(mario) did not stick, got %q", got)
	}
	if err := c.SetUpdateChimeTune("gran-vals"); err == nil {
		t.Fatal("SetUpdateChimeTune must reject unknown tunes")
	}

	rendered := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(rendered, `update_chime_tune = "mario"`) {
		t.Fatalf("user-scope render dropped update_chime_tune (81/123 fixed-key-set lesson):\n%s", rendered)
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(rendered), 0o600); err != nil {
		t.Fatalf("write rendered config: %v", err)
	}
	loaded := LoadForEdit(path)
	if loaded.Desktop.UpdateChimeTune != "mario" {
		t.Fatalf("update_chime_tune did not survive render/load, got %q", loaded.Desktop.UpdateChimeTune)
	}

	dirty := strings.Replace(rendered, `update_chime_tune = "mario"`, `update_chime_tune = "bogus"`, 1)
	dirtyPath := filepath.Join(t.TempDir(), "config-dirty.toml")
	if err := os.WriteFile(dirtyPath, []byte(dirty), 0o600); err != nil {
		t.Fatalf("write dirty config: %v", err)
	}
	if got := LoadForEdit(dirtyPath).UpdateChimeTuneMode(); got != "nokia" {
		t.Fatalf("dirty tune value must read as nokia, got %q", got)
	}
}
