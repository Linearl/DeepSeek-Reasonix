package boot

import (
	"context"
	"runtime/debug"
	"sync"

	"reasonix/internal/baseproc"
	"reasonix/internal/config"
	"reasonix/internal/tool"
)

// startBaseClient is the resident-base consumption point (design §10): boot
// asks for the client once per build and hands the result to the executor
// agent (base-toolcall gate), the controller (tool-catalog gate) and the
// cleanup chain.
//
// With experimental_base_process=false (the default) it returns an inline
// client with zero side effects (R1/R4) — no manager, no goroutine, the
// pre-S1 behaviour byte for byte — and both gates then take their local
// branch structurally (they check Mode). The inline client carries the
// registry boot just built as its tool surface, so even the switch-on
// spawn-fallback path answers tool queries with the same in-process objects
// the pre-S1 path used.
//
// S1c: with the switch on, every build shares ONE process-wide Manager
// instead of spawning its own subprocess (the S1b audit note "N boots = N
// subprocesses"). The base is resident for the life of the process; a build
// only acquires a view.
func startBaseClient(ctx context.Context, cfg *config.Config, reg *tool.Registry) baseproc.BaseClient {
	opts := baseproc.Options{
		Enabled:       cfg.Agent.ExperimentalBaseProcess,
		ServerVersion: baseServerVersion(),
		Surface:       &baseproc.RegistrySurface{Reg: reg},
	}
	if !opts.Enabled {
		// R4 default: the bare inline client, never a manager.
		return baseproc.Start(ctx, opts)
	}
	return sharedBaseClient(ctx, opts)
}

// sharedBaseMu guards the process-wide manager handle. It is held while a
// manager is created so concurrent builds converge on one spawn instead of
// racing two of them.
var (
	sharedBaseMu sync.Mutex
	sharedBase   *baseproc.Manager
)

// sharedBaseClient returns a view over the process-wide base manager,
// creating it on first use (design §4's lazy spawn).
//
// The pool keeps its OWN reference for the whole process lifetime, so a view
// closing never takes the resident base down: tabs come and go, the base does
// not. The subprocess is reaped when the app exits — its stdin hits EOF and it
// exits itself (decision D4's orphan path, tested in baseproc), or an explicit
// base.shutdown does it (design §4).
//
// Each view carries the caller's own inline fallback surface: the subprocess
// is shared, but the registry behind the fallback is per workspace root, and
// answering another tab's catalog would be wrong (that is exactly the split
// ManagedClient keeps).
func sharedBaseClient(ctx context.Context, opts baseproc.Options) baseproc.BaseClient {
	sharedBaseMu.Lock()
	defer sharedBaseMu.Unlock()
	if sharedBase == nil || sharedBase.Closed() {
		m := baseproc.NewManager(ctx, opts)
		m.Acquire(opts) // the pool's own, never-released reference
		sharedBase = m
	}
	return sharedBase.Acquire(opts)
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
