package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeEditEvidenceRelaxedConfig writes one TOML file and merges it the way
// Load does, so the decode goes through the real existence check.
func writeEditEvidenceRelaxedConfig(t *testing.T, content string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "reasonix.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	_, err := mergeFileSnapshot(cfg, path)
	return cfg, err
}

// TestEditEvidenceRelaxedDefaultsOff pins the task-789 default: absent key,
// zero value, and a nil config all read off — the strict gate is the baseline.
func TestEditEvidenceRelaxedDefaultsOff(t *testing.T) {
	var nilCfg *Config
	if nilCfg.EditEvidenceRelaxedEnabled() {
		t.Fatal("a nil config must read off")
	}
	if Default().EditEvidenceRelaxedEnabled() {
		t.Fatal("the built-in default must read off")
	}
}

// TestEditEvidenceRelaxedDecodesFromTOML pins the config key: the render-table
// key decodes into the agent block field and the helper reflects it.
func TestEditEvidenceRelaxedDecodesFromTOML(t *testing.T) {
	for _, tc := range []struct {
		name, toml string
		want       bool
	}{
		{"on", "[agent]\nexperimental_edit_evidence_relaxed = true\n", true},
		{"off", "[agent]\nexperimental_edit_evidence_relaxed = false\n", false},
		{"absent", "[agent]\nsubagent_effort = \"high\"\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := writeEditEvidenceRelaxedConfig(t, tc.toml)
			if err != nil {
				t.Fatalf("merge: %v", err)
			}
			if got := cfg.EditEvidenceRelaxedEnabled(); got != tc.want {
				t.Fatalf("EditEvidenceRelaxedEnabled = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestEditEvidenceRelaxedInvalidValueDegradesToOff pins the safe degradation:
// a wrongly typed value fails the snapshot merge, and Load falls back to the
// last-known-good or built-in defaults — so the switch can never come on from
// a broken value.
func TestEditEvidenceRelaxedInvalidValueDegradesToOff(t *testing.T) {
	cfg, err := writeEditEvidenceRelaxedConfig(t, "[agent]\nexperimental_edit_evidence_relaxed = \"yes\"\n")
	if err == nil {
		t.Fatal("a string value for a bool switch must fail the merge so Load degrades to defaults")
	}
	if cfg.EditEvidenceRelaxedEnabled() {
		t.Fatal("a failed merge must leave the switch off")
	}
}
