package main

import (
	"log/slog"
	"runtime/debug"

	"reasonix/internal/config"
)

// Task 308-O3: the Go soft memory limit (debug.SetMemoryLimit) was never
// configurable, so heapSys grew with the workload and never came back — the
// measured driver behind WS 5.0GB (heap 2.4GB + ~1.1GB runtime retention that
// only returns under pressure). A positive go_mem_limit_mb makes the runtime
// collect and return memory aggressively as the heap approaches the limit;
// 0 (default, fork rule 2) leaves the runtime unbounded exactly as before.
//
// Applied at startup (after the first config load) and re-applied live when
// the settings key changes. Returns the limit in bytes actually installed.
func applyGoMemLimit(cfg *config.Config) int64 {
	if cfg == nil {
		return 0
	}
	mb := cfg.Agent.GoMemLimitMB
	if mb <= 0 {
		mb = cfg.Desktop.GoMemLimitMB
	}
	if mb <= 0 {
		return 0
	}
	limit := int64(mb) * 1024 * 1024
	prev := debug.SetMemoryLimit(limit)
	slog.Info("go mem limit set", "mb", mb, "bytes", limit, "previousBytes", prev)
	return limit
}

// clearGoMemLimit removes a previously installed soft limit (debug treats
// MaxInt64 as "no limit") — used when the settings key drops back to 0 live.
func clearGoMemLimit() {
	debug.SetMemoryLimit(1 << 62)
	slog.Info("go mem limit cleared")
}
