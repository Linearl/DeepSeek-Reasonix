package main

import (
	"testing"

	"reasonix/internal/config"
)

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
