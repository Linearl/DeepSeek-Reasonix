package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// Task 514: the task-369 selection quick-actions switch could never stay on —
// the render table emitted `experimental_selection_actions` under [desktop]
// while the field lives on the Agent struct, so the decoder silently dropped
// the line on load (81/123 lost-save lesson, render/parse section mismatch).
// These tests pin the [agent] placement and the full save/load round trip.

func TestTask514SelectionActionsRendersUnderAgentSection(t *testing.T) {
	cfg := Default()
	if cfg.Agent.ExperimentalSelectionActions {
		t.Fatalf("selection actions must default off: %+v", cfg.Agent)
	}
	cfg.Agent.ExperimentalSelectionActions = true
	out := RenderTOML(cfg)
	agentIdx := strings.Index(out, "[agent]")
	desktopIdx := strings.Index(out, "[desktop]")
	lineIdx := strings.Index(out, "experimental_selection_actions = true")
	if agentIdx < 0 || lineIdx < 0 {
		t.Fatalf("rendered config missing [agent] or the selection actions line:\n%s", out)
	}
	if desktopIdx >= 0 && lineIdx > desktopIdx && lineIdx < agentIdx {
		t.Fatalf("experimental_selection_actions rendered under [desktop]; must live under [agent]:\n%s", out)
	}
	if lineIdx < agentIdx {
		t.Fatalf("experimental_selection_actions rendered before the [agent] section:\n%s", out)
	}
}

func TestTask514SelectionActionsRoundTripsThroughSaveLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.Agent.ExperimentalSelectionActions = true
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	reloaded, err := Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.Agent.ExperimentalSelectionActions {
		t.Fatalf("selection actions switch lost across save/load: %+v", reloaded.Agent)
	}
}
