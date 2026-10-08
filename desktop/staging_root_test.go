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

// Task 472: stagingRoot reads [desktop].staging_dir through
// LoadUserConfigReadOnly — user-level config only, no project merge, no
// on-disk migration. The override must still resolve from the user config
// file, and a broken config file must fall back to the default staging dir,
// the same failure semantics the old config.Load() call face had.
func TestStagingRootReadsUserConfigOverride(t *testing.T) {
	installRoot := t.TempDir()
	t.Cleanup(func() { versionSwitchInstallRoot = resolveVersionedInstallRoot })
	versionSwitchInstallRoot = func() (string, error) { return installRoot, nil }

	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)

	// No config file: fall through to <installRoot>/staging.
	got, err := stagingRoot()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(installRoot, "staging"); got != want {
		t.Fatalf("stagingRoot with no config = %q, want %q", got, want)
	}

	// User-level override: [desktop] staging_dir in $REASONIX_HOME/config.toml.
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	override := filepath.Join(home, "custom-staging")
	if err := cfg.SetStagingDir(override); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatal(err)
	}
	got, err = stagingRoot()
	if err != nil {
		t.Fatal(err)
	}
	if got != override {
		t.Fatalf("stagingRoot with override = %q, want %q", got, override)
	}

	// A broken user config must not break staging resolution: same fallback
	// as before the read downgrade.
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[desktop\n  staging_dir = \"unterminated"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = stagingRoot()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(installRoot, "staging"); got != want {
		t.Fatalf("stagingRoot with broken config = %q, want default %q", got, want)
	}
}
