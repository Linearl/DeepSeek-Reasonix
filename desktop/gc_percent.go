package main

import (
	"log/slog"
	"runtime/debug"

	"reasonix/internal/config"
)

// gcPercentForView resolves the GC-strategy dial (tl1286) for the settings
// views: a 0 config value means "follow the Go default" and renders as 100 so
// the knob always displays the percentage actually in effect.
func gcPercentForView(cfg *config.Config) int {
	if cfg == nil || cfg.Agent.GCPercent <= 0 {
		return 100
	}
	return cfg.Agent.GCPercent
}

// applyGCPercent installs the configured GC target percentage
// (debug.SetGCPercent, tl1286). The retired go_mem_limit soft limit showed why
// a percentage is the right user knob: a fixed byte ceiling set below
// steady-state residency makes the collector run at full throttle forever
// (the 20261030 whole-machine hang), and no safe byte value is knowable in
// advance. A percentage scales with the live heap instead, and 100 always
// reverts to the Go default.
//
// 0 (default) leaves the runtime untouched, so a GOGC env override keeps
// working. Applied at startup (after the first config load) and re-applied
// live when the settings key changes.
func applyGCPercent(cfg *config.Config) {
	if cfg == nil {
		return
	}
	noticeGoMemLimitRetired(cfg)
	p := cfg.Agent.GCPercent
	if p <= 0 {
		return
	}
	prev := debug.SetGCPercent(p)
	slog.Info("gc percent set", "percent", p, "previous", prev)
}

// applyGCPercentLive re-applies the dial to the running runtime after a
// settings save: p>0 installs it directly, p<=0 reverts to the Go default
// (100). Returns the percentage actually installed.
func applyGCPercentLive(p int) int {
	if p <= 0 {
		p = 100
	}
	prev := debug.SetGCPercent(p)
	slog.Info("gc percent set", "percent", p, "previous", prev)
	return p
}

// noticeGoMemLimitRetired logs a one-shot migration hint when a loaded config
// still carries the retired go_mem_limit_mb key with a non-zero value: the
// value is ignored (old configs must keep booting — tl1286 acceptance ②), and
// the hint names the replacement knob. A 0 value was a no-op even before
// retirement, so it draws no noise.
func noticeGoMemLimitRetired(cfg *config.Config) {
	if cfg.Agent.GoMemLimitMB > 0 || cfg.Desktop.GoMemLimitMB > 0 {
		slog.Warn("go_mem_limit_mb retired and ignored (tl1286): a soft limit below steady-state residency drove GC into a full-throttle spin; use gc_percent in [agent] to tune GC strategy instead",
			"agentMB", cfg.Agent.GoMemLimitMB, "desktopMB", cfg.Desktop.GoMemLimitMB)
	}
}
