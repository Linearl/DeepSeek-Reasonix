package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Task 321: config key sensing. Loading a config file with unknown or
// deprecated keys must warn at warn level without blocking, without rewriting
// the file, and without any noise for configs that only use known keys.

func task321CaptureWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

func task321WriteConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "reasonix.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func task321LoadReadOnly(t *testing.T, path string) *Config {
	t.Helper()
	cfg, err := loadForEditStrict(path, false, false)
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	return cfg
}

func TestTask321UnknownKeyWarnsAndLoadContinues(t *testing.T) {
	body := "default_model = \"custom\"\n[agent]\ntemperature = 0.2\nfuture_upstream_key = 1\n"
	path := task321WriteConfig(t, body)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	logs := task321CaptureWarnings(t)
	cfg := task321LoadReadOnly(t, path)

	// The load stays healthy: known keys still apply and no default fallback.
	if cfg.DefaultModel != "custom" {
		t.Fatalf("default_model = %q, want custom (load must continue past unknown keys)", cfg.DefaultModel)
	}
	if cfg.Agent.Temperature != 0.2 {
		t.Fatalf("agent.temperature = %v, want 0.2", cfg.Agent.Temperature)
	}
	// The file is not rewritten.
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("config file was rewritten:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	// The unknown key is named in a warn line.
	out := logs.String()
	if !strings.Contains(out, "unknown key") || !strings.Contains(out, "future_upstream_key") {
		t.Fatalf("expected an unknown-key warn naming future_upstream_key, got:\n%s", out)
	}
}

func TestTask321KnownKeysZeroWarnNoise(t *testing.T) {
	example, err := os.ReadFile(filepath.Join("..", "..", "reasonix.example.toml"))
	if err != nil {
		t.Fatalf("read reasonix.example.toml: %v", err)
	}
	path := task321WriteConfig(t, string(example))

	logs := task321CaptureWarnings(t)
	task321LoadReadOnly(t, path)

	if out := logs.String(); strings.Contains(out, "unknown key") || strings.Contains(out, "deprecated key") {
		t.Fatalf("known-key config must not produce sensing warnings, got:\n%s", out)
	}
}

func TestTask321DeprecatedKeyListHit(t *testing.T) {
	body := "[agent]\nauto_plan = \"off\"\ncompact_force_ratio = 0.9\n"
	path := task321WriteConfig(t, body)

	logs := task321CaptureWarnings(t)
	cfg := task321LoadReadOnly(t, path)

	if cfg == nil {
		t.Fatal("load must continue past deprecated keys")
	}
	out := logs.String()
	for _, want := range []string{"deprecated key", "agent.auto_plan", "agent.compact_force_ratio"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected deprecated-key warn containing %q, got:\n%s", want, out)
		}
	}
	// Deprecated keys are reported as deprecated, not as unknown.
	if strings.Contains(out, "unknown key") {
		t.Fatalf("deprecated keys must not double-report as unknown, got:\n%s", out)
	}
}
