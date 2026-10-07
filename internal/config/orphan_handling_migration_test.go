package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Task 449: the two task-244 orphan keys fold into the single
// experimental_orphan_handling switch at load, and the folded value must
// survive a save/load round trip (读值不丢) — both the lease-reclaim half (B5)
// and the recovery-sweep half (B4) count as "on" for the merged switch.

func writeOrphanLegacyConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOrphanHandlingMigratesLegacyLeaseKey(t *testing.T) {
	path := writeOrphanLegacyConfig(t, `[agent]
experimental_orphan_lease_reclaim = true
`)
	cfg := LoadForEdit(path)
	// Task 473: the [agent] key is the single source; the [desktop] mirror is
	// retired and must be cleared by the fold.
	if !cfg.Agent.ExperimentalOrphanHandling {
		t.Fatal("legacy lease key must fold into experimental_orphan_handling")
	}
	if cfg.Desktop.ExperimentalOrphanHandling {
		t.Fatal("the retired [desktop] mirror must be cleared after the fold")
	}
	if cfg.Agent.ExperimentalOrphanLeaseReclaim || cfg.Desktop.ExperimentalOrphanLeaseReclaim {
		t.Fatal("legacy lease key must be cleared after the fold")
	}
	// The gating surfaces read the merged key: both halves follow it now.
	if !cfg.Agent.ExperimentalOrphanHandling {
		t.Fatal("lease-reclaim gate must see the folded value")
	}
}

func TestOrphanHandlingMigratesLegacySweepKey(t *testing.T) {
	path := writeOrphanLegacyConfig(t, `[agent]
experimental_recovery_orphan_sweep = true
`)
	cfg := LoadForEdit(path)
	if !cfg.Agent.ExperimentalOrphanHandling {
		t.Fatal("legacy sweep key must fold into experimental_orphan_handling")
	}
	if cfg.Desktop.ExperimentalOrphanHandling {
		t.Fatal("the retired [desktop] mirror must be cleared after the fold")
	}
	if cfg.Agent.ExperimentalRecoveryOrphanSweep || cfg.Desktop.ExperimentalRecoveryOrphanSweep {
		t.Fatal("legacy sweep key must be cleared after the fold")
	}
}

func TestOrphanHandlingMigratesDesktopMirrorOnly(t *testing.T) {
	// A settings-view mirror written by an older desktop build (no [agent] key)
	// must fold too — the OR read on the view side covers either section.
	path := writeOrphanLegacyConfig(t, `[desktop]
experimental_orphan_lease_reclaim = true
`)
	cfg := LoadForEdit(path)
	if !cfg.Agent.ExperimentalOrphanHandling {
		t.Fatal("desktop-only legacy key must fold into the merged switch")
	}
	if cfg.Desktop.ExperimentalOrphanHandling {
		t.Fatal("the retired [desktop] mirror must be cleared after the fold")
	}
}

func TestOrphanHandlingStaysOffWithNoLegacyKeys(t *testing.T) {
	path := writeOrphanLegacyConfig(t, `config_version = 9
`)
	cfg := LoadForEdit(path)
	if cfg.Agent.ExperimentalOrphanHandling || cfg.Desktop.ExperimentalOrphanHandling {
		t.Fatal("no legacy key = merged switch stays off")
	}
}

func TestOrphanHandlingOffSurvivesSaveRoundTrip(t *testing.T) {
	// Regression guard for the resurrection bug: legacy=true migrates to on,
	// the user turns it off, and the next load must NOT flip it back on via a
	// stale legacy true. The off write clears both legacy keys and the render
	// face carries only the folded state. The round trip runs through the real
	// user-config path — a temp-dir path would take the incremental project
	// save, which does not render [agent] keys at all.
	home := isolateUserConfigHome(t)
	path := UserConfigPath()
	requireTestPathWithin(t, home, path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`[agent]
experimental_orphan_lease_reclaim = true
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := LoadForEdit(path)
	if !cfg.Agent.ExperimentalOrphanHandling {
		t.Fatal("precondition: migration must turn the switch on")
	}
	if err := cfg.SetExperimentalOrphanHandling(false); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "experimental_orphan_handling = false") {
		t.Fatalf("save must carry the merged off state:\n%s", text)
	}
	if strings.Contains(text, "experimental_orphan_lease_reclaim = true") {
		t.Fatalf("legacy key must not be written back as on:\n%s", text)
	}

	reloaded := LoadForEdit(path)
	if reloaded.Agent.ExperimentalOrphanHandling || reloaded.Desktop.ExperimentalOrphanHandling {
		t.Fatal("off must survive the reload (no resurrection from a stale legacy true)")
	}
}

func TestOrphanHandlingOnSurvivesSaveRoundTrip(t *testing.T) {
	home := isolateUserConfigHome(t)
	path := UserConfigPath()
	requireTestPathWithin(t, home, path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`[agent]
experimental_recovery_orphan_sweep = true
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := LoadForEdit(path)
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "experimental_orphan_handling = true") {
		t.Fatalf("save must carry the folded on state:\n%s", text)
	}

	reloaded := LoadForEdit(path)
	if !reloaded.Agent.ExperimentalOrphanHandling {
		t.Fatal("folded on value must survive the round trip (读值不丢)")
	}
}

// The legacy setters survive as delegates over the merged switch, so a stale
// caller toggling either half still moves the whole switch.
func TestOrphanHandlingLegacySettersDelegateToMergedSwitch(t *testing.T) {
	cfg := &Config{}
	if err := cfg.SetExperimentalOrphanLeaseReclaim(true); err != nil {
		t.Fatal(err)
	}
	// Task 473: the merged switch single-writes the [agent] key; the retired
	// [desktop] mirror stays cleared.
	if !cfg.Agent.ExperimentalOrphanHandling {
		t.Fatal("legacy lease setter must toggle the merged switch")
	}
	if cfg.Desktop.ExperimentalOrphanHandling {
		t.Fatal("the retired [desktop] mirror must stay cleared after a legacy setter")
	}
	if err := cfg.SetExperimentalRecoveryOrphanSweep(true); err != nil {
		t.Fatal(err)
	}
	if !cfg.Agent.ExperimentalOrphanHandling {
		t.Fatal("legacy sweep setter must toggle the merged switch")
	}
	if err := cfg.SetExperimentalOrphanHandling(false); err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.ExperimentalOrphanHandling || cfg.Desktop.ExperimentalOrphanHandling {
		t.Fatal("merged switch off must stick")
	}
	if cfg.Agent.ExperimentalOrphanLeaseReclaim || cfg.Desktop.ExperimentalOrphanLeaseReclaim ||
		cfg.Agent.ExperimentalRecoveryOrphanSweep || cfg.Desktop.ExperimentalRecoveryOrphanSweep {
		t.Fatal("off must also clear the legacy keys so they cannot resurrect the state")
	}
}

// Both halves of the orphan flow follow the one switch: the render face must
// carry the merged key in the [agent] section (unconditional, fixed key set)
// so an untouched or migrated config round-trips through save. Task 473: the
// [desktop] mirror row is gone — the [agent] row is the only render.
func TestOrphanHandlingRendersMergedKey(t *testing.T) {
	c := &Config{}
	c.Agent.ExperimentalOrphanHandling = true
	out := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(out, "experimental_orphan_handling = true") {
		t.Fatalf("rendered user config is missing experimental_orphan_handling = true\n---\n%s", out)
	}

	off := RenderTOMLForScope(&Config{}, RenderScopeUser)
	if !strings.Contains(off, "experimental_orphan_handling = false") {
		t.Fatalf("a disabled switch must still render false\n---\n%s", off)
	}
}
