package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/config"
)

// Task 381: the fast-switch staging directory override. Empty = the historical
// default (<installRoot>/staging) byte-for-byte; set = every staging reader
// honors the configured directory.

func newIsolatedConfigApp(t *testing.T) *App {
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

func TestStagingDirOverrideRoundTrip(t *testing.T) {
	a := newIsolatedConfigApp(t)
	custom := filepath.Join(t.TempDir(), "custom-staging")
	if err := a.SetStagingDir(custom); err != nil {
		t.Fatalf("set override: %v", err)
	}
	view := a.Settings()
	if view.StagingDir != custom {
		t.Fatalf("Settings() must echo the configured staging dir, got %q want %q", view.StagingDir, custom)
	}
	// Reset: empty restores the default semantics (no override).
	if err := a.SetStagingDir("   "); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if view := a.Settings(); view.StagingDir != "" {
		t.Fatalf("reset must clear the override, got %q", view.StagingDir)
	}
}

func TestStagingReadersHonorConfiguredDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "version.txt"), []byte("1.38.3-test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	label, ok := readStagingVersionAt(dir)
	// The reader normalizes the label (bare versions gain the v prefix).
	if !ok || !strings.Contains(label, "1.38.3-test") {
		t.Fatalf("readStagingVersionAt: ok=%v label=%q", ok, label)
	}
	// Healthy check needs the real binary names; an empty dir must be unhealthy.
	if stagingHealthyAt(dir) {
		t.Fatal("a directory without the binaries must not count as healthy")
	}
	// The config override reaches the resolver (empty config → default falls
	// through to installRoot; here we only pin the override path resolution).
	t.Setenv("REASONIX_HOME", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetStagingDir(dir); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(cfg.Desktop.StagingDir); got != dir {
		t.Fatalf("config override not stored: %q", got)
	}
}
