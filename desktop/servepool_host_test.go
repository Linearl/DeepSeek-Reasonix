package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/config"
)

func TestGatewayPortDefaultAndOverride(t *testing.T) {
	t.Setenv("REASONIX_GATEWAY_PORT", "")
	if got := gatewayPort(); got != 18789 {
		t.Fatalf("default port = %d, want 18789", got)
	}
	t.Setenv("REASONIX_GATEWAY_PORT", "0")
	if got := gatewayPort(); got != 18789 {
		t.Fatalf("invalid override = %d, want 18789", got)
	}
	t.Setenv("REASONIX_GATEWAY_PORT", "99999")
	if got := gatewayPort(); got != 18789 {
		t.Fatalf("out-of-range override = %d, want 18789", got)
	}
	t.Setenv("REASONIX_GATEWAY_PORT", "23456")
	if got := gatewayPort(); got != 23456 {
		t.Fatalf("valid override = %d, want 23456", got)
	}
}

func TestProjectRootsFromRegistryEmpty(t *testing.T) {
	// The assertion is about an unregistered project list, so this test owns its
	// state: without an isolated desktop home it inherited the project roots an
	// earlier test in the package registered, which is why it only failed in a
	// full-package run.
	isolateDesktopUserDirs(t)
	roots := projectRootsFromRegistry()
	if roots == nil {
		t.Fatal("roots = nil, want empty slice")
	}
	if len(roots) != 0 {
		t.Fatalf("roots = %v, want empty", roots)
	}
}

// Task 1049: [serve.pool] config keys must reach the pool timeouts through
// the real config load — a key that decodes but never leaves the config
// struct (or a renamed key silently ignored at load) is exactly the failure
// this full-chain test catches.
func TestServePoolTimeoutsFromConfigFile(t *testing.T) {
	isolateDesktopUserDirs(t)

	// No config file: pool defaults.
	spawn, idle := servePoolTimeoutsFromConfig()
	if spawn != 30*time.Second || idle != 15*time.Minute {
		t.Fatalf("no-config timeouts = (%s, %s), want pool defaults (30s, 15m)", spawn, idle)
	}

	// Keys set: values flow through config.Load into the pool timeouts.
	if err := os.MkdirAll(config.ReasonixHomeDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(config.ReasonixHomeDir(), "config.toml")
	if err := os.WriteFile(cfgPath, []byte("[serve.pool]\nspawn_timeout_seconds = 45\nidle_timeout_seconds = 1200\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spawn, idle = servePoolTimeoutsFromConfig()
	if spawn != 45*time.Second || idle != 1200*time.Second {
		t.Fatalf("configured timeouts = (%s, %s), want (45s, 20m)", spawn, idle)
	}
}
