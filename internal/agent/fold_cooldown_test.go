package agent

import (
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/config"
)

// Task 318.2: foldCooldownInterval resolves the experiment's configurable
// minutes and falls back to the hard-coded default when the switch is off —
// the off path is today's behavior byte-for-byte.
func TestFoldCooldownIntervalFollowsExperiment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	// Off (the shipped default): the package-level hard-coded interval governs.
	if got := foldCooldownInterval(); got != minExplicitFoldInterval {
		t.Fatalf("switch off: interval = %s, want the hard-coded %s", got, minExplicitFoldInterval)
	}

	// On with no stored value: the historical 10 minutes.
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := cfg.SetExperimentalProactiveCompact(true); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	if got := foldCooldownInterval(); got != 10*time.Minute {
		t.Fatalf("on + default: interval = %s, want 10m", got)
	}

	// On with a configured value: the injected minutes apply (the acceptance
	// reading — config change, next fold sees it without a restart).
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := cfg.SetProactiveCompactCooldownMinutes(3); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	if got := foldCooldownInterval(); got != 3*time.Minute {
		t.Fatalf("on + 3 minutes: interval = %s, want 3m (cooldown injection)", got)
	}

	// Back off: the hard-coded default returns (zero regression path).
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := cfg.SetExperimentalProactiveCompact(false); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	if got := foldCooldownInterval(); got != minExplicitFoldInterval {
		t.Fatalf("switch off again: interval = %s, want the hard-coded %s", got, minExplicitFoldInterval)
	}
}
