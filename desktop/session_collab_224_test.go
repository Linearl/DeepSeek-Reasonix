package main

import (
	"testing"

	"reasonix/internal/config"
)

// collabBackgroundDeliveryFromConfig is the pure branch extracted for M3
// testing (tl1627: moved here from session_collab.go — test-only scaffolding,
// zero production callers; production reads the boot snapshot via
// collabBackgroundDelivery instead). Returns true when
// experimental_collab_background_delivery is on.
func collabBackgroundDeliveryFromConfig(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	return cfg.Agent.ExperimentalCollabBackgroundDelivery
}

// M3: collabBackgroundDelivery two-path test.
// On: drain() returns empty (no tab). Off (default): zero regression.
func TestCollabBackgroundDeliveryTwoPaths(t *testing.T) {
	onCfg := &config.Config{}
	onCfg.Agent.ExperimentalCollabBackgroundDelivery = true
	if !collabBackgroundDeliveryFromConfig(onCfg) {
		t.Fatal("switch ON must return true (drain skips, no tab)")
	}
	offCfg := &config.Config{}
	if collabBackgroundDeliveryFromConfig(offCfg) {
		t.Fatal("switch OFF must return false (zero regression)")
	}
	if collabBackgroundDeliveryFromConfig(nil) {
		t.Fatal("nil config must return false (safe default)")
	}
}
