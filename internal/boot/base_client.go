package boot

import (
	"context"
	"runtime/debug"

	"reasonix/internal/baseproc"
	"reasonix/internal/config"
	"reasonix/internal/tool"
)

// startBaseClient is the S1b resident-base consumption point (design §10):
// boot asks baseproc.Start for the client once per build and hands the result
// to the executor agent (base-toolcall gate), the controller (tool-catalog
// gate) and the cleanup chain.
//
// With experimental_base_process=false (the default) Start returns an inline
// client with zero side effects (R1/R4) — the pre-S1 behaviour — and both
// gates then take their local branch structurally (they check Mode). The
// inline client carries the registry boot just built as its tool surface, so
// even the switch-on spawn-fallback path answers tool queries with the same
// in-process objects the pre-S1 path used.
//
// Lifecycle note: S1b's manager is one-shot per build (a switch-on boot
// spawns its own subprocess); process-wide singleton sharing, health and
// backoff are S1c (design §4).
func startBaseClient(ctx context.Context, cfg *config.Config, reg *tool.Registry) baseproc.BaseClient {
	return baseproc.Start(ctx, baseproc.Options{
		Enabled:       cfg.Agent.ExperimentalBaseProcess,
		ServerVersion: baseServerVersion(),
		Surface:       &baseproc.RegistrySurface{Reg: reg},
	})
}

// baseServerVersion is the local build identity an inline hello reports. The
// remote side reports the subprocess's own CLI version (ldflags-injected,
// passed through `base serve`); boot cannot reach the cli package's version
// var (import direction), so it falls back to module build info, then "dev".
func baseServerVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}
