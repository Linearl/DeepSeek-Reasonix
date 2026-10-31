package main

import (
	"runtime/debug"
	"testing"

	"github.com/BurntSushi/toml"

	"reasonix/internal/config"
)

// readGCPercent reads the live GC target percentage the way the runtime
// exposes it: SetGCPercent returns the previous setting, so installing the
// same value round-trips the current one.
func readGCPercent(t *testing.T, current int) int {
	t.Helper()
	return debug.SetGCPercent(current)
}

// restoreGCPercent puts the process-wide dial back where the test found it.
func restoreGCPercent(t *testing.T, original int) {
	t.Helper()
	debug.SetGCPercent(original)
}

// TestApplyGCPercentHotAndRevert: tl1286 acceptance ① — the dial applies to
// the live runtime on change and reverts to the Go default (100) by setting
// 100 / 0. tl1286 acceptance ② rides along: a config that still carries the
// retired go_mem_limit_mb value parses and boots, installs the new dial, and
// ignores the retired value.
func TestApplyGCPercentHotAndRevert(t *testing.T) {
	original := readGCPercent(t, 100)
	defer restoreGCPercent(t, original)

	oldCfg := `
[agent]
go_mem_limit_mb = 800
gc_percent = 50
`
	var cfg config.Config
	if _, err := toml.Decode(oldCfg, &cfg); err != nil {
		t.Fatalf("old config with retired key does not parse: %v", err)
	}
	applyGCPercent(&cfg)
	if got := readGCPercent(t, 50); got != 50 {
		t.Fatalf("after applyGCPercent(50) the live dial is %d, want 50", got)
	}

	// Revert to the Go default through the same live path the settings
	// setter uses (p<=0 ⇒ 100).
	if got := applyGCPercentLive(0); got != 100 {
		t.Fatalf("applyGCPercentLive(0) installed %d, want 100", got)
	}
	if got := readGCPercent(t, 100); got != 100 {
		t.Fatalf("after revert the live dial is %d, want 100", got)
	}

	// Hot change upward and back — every step must land immediately.
	if got := applyGCPercentLive(150); got != 150 {
		t.Fatalf("applyGCPercentLive(150) installed %d, want 150", got)
	}
	if got := readGCPercent(t, 150); got != 150 {
		t.Fatalf("after hot change the live dial is %d, want 150", got)
	}

	// Startup with the dial unset must leave the runtime untouched (a GOGC
	// env override keeps working). Land the dial on a known 100 first, then
	// apply a 0 config: the live value must still read 100.
	applyGCPercentLive(100)
	unset := `
[agent]
gc_percent = 0
`
	var cfg2 config.Config
	if _, err := toml.Decode(unset, &cfg2); err != nil {
		t.Fatalf("default config does not parse: %v", err)
	}
	applyGCPercent(&cfg2)
	if got := readGCPercent(t, 100); got != 100 {
		t.Fatalf("startup with 0 changed the live dial to %d, want untouched 100", got)
	}
}

// TestGCPercentForView: the settings view always shows the percentage in
// effect — a 0 config value (follow default) renders as 100.
func TestGCPercentForView(t *testing.T) {
	if got := gcPercentForView(nil); got != 100 {
		t.Fatalf("gcPercentForView(nil) = %d, want 100", got)
	}
	cfg := &config.Config{}
	if got := gcPercentForView(cfg); got != 100 {
		t.Fatalf("gcPercentForView(0) = %d, want 100", got)
	}
	cfg.Agent.GCPercent = 150
	if got := gcPercentForView(cfg); got != 150 {
		t.Fatalf("gcPercentForView(150) = %d, want 150", got)
	}
}
