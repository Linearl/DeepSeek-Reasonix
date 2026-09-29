package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"reasonix/internal/boot"
)

// runtimeAssemblyPool is the task-363A reuse pool: one ReusedAssembly per
// (workspace root, model, effort) so a new conversation tab with the same
// configuration skips prompt/skills/commands/hooks discovery instead of
// paying the full boot rebuild. The gate (experimental_runtime_reuse) is read
// at call time; when it is off every helper is a no-op and the build path is
// byte-identical to before.
//
// Lifecycle is last-writer-wins per key: store overwrites the entry for that
// key and acquire never mutates. Pooled assemblies share the exact objects
// wired into the live controller — treat them as immutable. Stale entries are
// bounded by the number of distinct (root, model, effort) triples the user
// actually opens, and each is GC-able once overwritten or dropped by
// DropRuntimeAssemblyPools.
type runtimeAssemblyPool struct {
	mu         sync.Mutex
	entries    map[string]*boot.ReusedAssembly
	generation uint64
}

var runtimeAssemblies = &runtimeAssemblyPool{entries: map[string]*boot.ReusedAssembly{}}

// runtimeAssemblyKey fingerprints the reuse identity.
func runtimeAssemblyKey(root, model, effort string) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s", root, model, effort)))
	return hex.EncodeToString(digest[:8])
}

// runtimeReuseEnabled reads the experimental gate from the current config.
func (a *App) runtimeReuseEnabled() bool {
	cfg, _, err := a.loadDesktopUserConfigForView()
	if err != nil || cfg == nil {
		return false
	}
	return cfg.Agent.ExperimentalRuntimeReuse
}

// acquireRuntimeAssembly returns the pooled assembly for the key, or nil when
// the gate is off / the pool is cold. Read-only: no reference counting.
func (a *App) acquireRuntimeAssembly(key string) *boot.ReusedAssembly {
	if !a.runtimeReuseEnabled() {
		return nil
	}
	runtimeAssemblies.mu.Lock()
	defer runtimeAssemblies.mu.Unlock()
	assembly := runtimeAssemblies.entries[key]
	if assembly != nil {
		slog.Info("desktop: runtime assembly hit", "key", key)
	}
	return assembly
}

// storeRuntimeAssembly publishes a freshly built assembly under the key
// (last-writer-wins). A no-op when the gate is off.
func (a *App) storeRuntimeAssembly(key string, assembly *boot.ReusedAssembly) {
	if !a.runtimeReuseEnabled() || assembly == nil {
		return
	}
	runtimeAssemblies.mu.Lock()
	defer runtimeAssemblies.mu.Unlock()
	runtimeAssemblies.entries[key] = assembly
	runtimeAssemblies.generation++
	slog.Info("desktop: runtime assembly stored", "key", key, "pool_size", len(runtimeAssemblies.entries))
}

// DropRuntimeAssemblyPools empties the pool (config gate flip, tests). The
// gate-off fast path above makes stale entries inert, so this is hygiene
// rather than correctness.
func DropRuntimeAssemblyPools() {
	runtimeAssemblies.mu.Lock()
	defer runtimeAssemblies.mu.Unlock()
	runtimeAssemblies.entries = map[string]*boot.ReusedAssembly{}
	runtimeAssemblies.generation++
}

// runtimeAssemblyKeyForTab derives the pool key for a tab snapshot in one
// place so acquire/store always agree on the identity.
func runtimeAssemblyKeyForTab(workspaceRoot, model, effort string) string {
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	return runtimeAssemblyKey(norm(workspaceRoot), norm(model), norm(effort))
}
