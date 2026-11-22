package sandbox

import (
	"sync"
)

// Deterministic test hooks for shell resolution. Both hooks default to nil, so
// the production paths in ResolveShell and buildShellSnapshot are byte-identical
// to the pre-hook behavior; tests install them and MUST call the returned
// restore (typically via t.Cleanup).
//
// Motivation (tl1735 B2 §五): the boot golden baseline captures the bash tool's
// provider-visible description, which forks on the resolved shell kind. Under
// host load, probeBash's 3-second budget can time out for every candidate, the
// auto resolution falls back to PowerShell, and the golden drifts by the
// PowerShell NOTE block (+439 bytes) — a flaky test defect, not a product
// regression. The golden test pins the resolution with PinResolveShellForTest so
// the recorded surface can no longer depend on any probe outcome; the probe hook
// exists so the failure mode itself can be replayed deterministically in tests.

var (
	pinnedShellMu sync.Mutex
	pinnedShell   *Shell
)

// PinResolveShellForTest pins every ResolveShell call (and thus all its
// consumers: the boot resolution, ConfineBash, and bash.resolved's lazy
// resolution) to a fixed result, skipping candidate discovery and the
// probeBash health checks entirely. Returns a restore func for t.Cleanup.
func PinResolveShellForTest(sh Shell) (restore func()) {
	pinnedShellMu.Lock()
	defer pinnedShellMu.Unlock()
	pinnedShell = &sh
	return func() {
		pinnedShellMu.Lock()
		defer pinnedShellMu.Unlock()
		pinnedShell = nil
	}
}

func pinnedResolveShell() (Shell, bool) {
	pinnedShellMu.Lock()
	defer pinnedShellMu.Unlock()
	if pinnedShell == nil {
		return Shell{}, false
	}
	return *pinnedShell, true
}

var (
	pinnedProbeMu sync.Mutex
	pinnedProbe   func(path string) bool
)

// PinShellProbeForTest replaces the bash health probe (probeBash) consulted
// during candidate discovery with a fixed result, so a probe timeout/failure
// can be replayed deterministically (e.g. a constant false models the outcome a
// 3-second budget produces under load). The override only affects snapshots
// built after installation, and snapshots are cached process-wide for 30
// seconds — a test must also call InvalidateShellInventory on cleanup so the
// poisoned cache cannot leak into later tests in the same process.
func PinShellProbeForTest(probe func(path string) bool) (restore func()) {
	pinnedProbeMu.Lock()
	defer pinnedProbeMu.Unlock()
	pinnedProbe = probe
	return func() {
		pinnedProbeMu.Lock()
		defer pinnedProbeMu.Unlock()
		pinnedProbe = nil
	}
}

func pinnedShellProbe() func(path string) bool {
	pinnedProbeMu.Lock()
	defer pinnedProbeMu.Unlock()
	return pinnedProbe
}
