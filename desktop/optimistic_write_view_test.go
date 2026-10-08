package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/config"
)

// Task 374 fix: the settings checkbox reads s.sandbox?.optimisticWrite, so the
// full round trip — Set → persist → Settings() view → serialized JSON — must
// carry the flag. The field itself shipped long ago (git log -S: v1.33);
// these tests pin the WHOLE chain so a dropped assignment or struct field can
// never silently bring the bounce-back symptom back.

func newIsolatedSettingsApp(t *testing.T) *App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load isolated config: %v", err)
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("save isolated config: %v", err)
	}
	return &App{}
}

func TestOptimisticWriteRoundTripThroughSettingsView(t *testing.T) {
	a := newIsolatedSettingsApp(t)

	if _, err := a.SetOptimisticWrite(true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	view := a.Settings()
	if !view.Sandbox.OptimisticWrite {
		t.Fatal("Settings() must report OptimisticWrite=true right after enabling")
	}
	// Simulate the frontend's boolean coercion of the serialized field.
	raw, err := json.Marshal(view.Sandbox)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		OptimisticWrite bool `json:"optimisticWrite"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if !wire.OptimisticWrite {
		t.Fatal("the serialized sandbox view must carry optimisticWrite=true for the checkbox")
	}

	if _, err := a.SetOptimisticWrite(false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	view = a.Settings()
	if view.Sandbox.OptimisticWrite {
		t.Fatal("Settings() must report OptimisticWrite=false right after disabling")
	}
}

func TestSandboxViewAlwaysSerializesOptimisticWrite(t *testing.T) {
	a := &App{}
	cfg := &config.Config{}
	cfg.Sandbox.OptimisticWrite = true
	view := a.sandboxViewFor(cfg, nil, nil, "", nil)
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"optimisticWrite":true`) {
		t.Fatalf("SandboxView serialization must include optimisticWrite, got: %s", raw)
	}
}

// Task 374fix (plan B acceptance): a machine with no active tab (fresh
// install, few sessions) must not turn the successful save into a bounced
// checkbox — the flag takes effect on restart, so the save lands, the
// rebuild failure degrades to a warning, and the view reads back true.
func TestOptimisticWriteSurvivesMissingActiveTab(t *testing.T) {
	a := newIsolatedSettingsApp(t)
	a.ctx = context.Background() // frontend attached, but no tabs at all

	warning, err := a.SetOptimisticWrite(true)
	if err != nil {
		t.Fatalf("save must not fail on a missing active tab: %v", err)
	}
	if warning == "" || !strings.Contains(warning, "takes effect after a restart") {
		t.Fatalf("the degraded path must return the restart-effect warning, got %q", warning)
	}
	if view := a.Settings(); !view.Sandbox.OptimisticWrite {
		t.Fatal("the saved flag must read back true despite the degraded rebuild")
	}
}
