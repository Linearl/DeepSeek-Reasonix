package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/boot"
)

// TestStartupChainReuseKeyMatchesRebindChain: the task-405 Q1 wiring must
// derive the pool key from the same root+model+effort identity as the rebind
// chain (runtimeAssemblyKeyForTab, normalized), so an assembly stored by the
// startup chain is visible to the rebind chain and vice versa.
func TestStartupChainReuseKeyMatchesRebindChain(t *testing.T) {
	startupKey := runtimeAssemblyKeyForTab("D:\\Proj", "deepseek/v4", "high")
	rebindKey := runtimeAssemblyKey("d:\\proj", "deepseek/v4", "high") // rebind normalizes before hashing
	if startupKey != rebindKey {
		t.Fatalf("startup key %q != rebind key %q — the two chains would never share assemblies", startupKey, rebindKey)
	}
	// Distinct effort or model must not collide.
	if runtimeAssemblyKeyForTab("D:\\Proj", "deepseek/v4", "high") == runtimeAssemblyKeyForTab("D:\\Proj", "deepseek/v4", "low") {
		t.Fatal("different efforts share a pool key")
	}
}

// TestStartupChainReuseHitAndStore is the state-level reuse-hit assertion for
// the startup chain (the diagnosis measured 0 hits because the startup chain
// never called acquire at all): gate off — acquire nil, store a no-op
// (byte-identical legacy behavior); gate on — store→hit returns the exact
// stored assembly so the next same-key build skips discovery.
func TestStartupChainReuseHitAndStore(t *testing.T) {
	DropRuntimeAssemblyPools()
	defer DropRuntimeAssemblyPools()

	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	a := &App{}
	key := runtimeAssemblyKeyForTab("D:\\Proj", "deepseek/v4", "high")

	// Gate off (no config in the temp home): acquire nil, store no-op.
	if got := a.acquireRuntimeAssembly(key); got != nil {
		t.Fatal("gate-off acquire returned an assembly")
	}
	a.storeRuntimeAssembly(key, &boot.ReusedAssembly{})
	if got := a.acquireRuntimeAssembly(key); got != nil {
		t.Fatal("gate-off store became visible")
	}

	// Gate on: seed the user config the gate reads, then store→hit.
	cfgPath := filepath.Join(home, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("[agent]\nexperimental_runtime_reuse = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := a.loadDesktopUserConfigForView()
	if err != nil || cfg == nil || !cfg.Agent.ExperimentalRuntimeReuse {
		t.Fatalf("gate config not picked up: err=%v cfg=%v", err, cfg)
	}
	assembly := &boot.ReusedAssembly{}
	a.storeRuntimeAssembly(key, assembly)
	if got := a.acquireRuntimeAssembly(key); got != assembly {
		t.Fatal("gate-on acquire did not return the stored assembly (startup chain would rebuild discovery)")
	}
}

// TestRuntimeReloadPlanPresentInStartupOptions is a source-shape guard: the
// startup chain must construct its boot.Options with the embedded
// RuntimeReload feeding the pool (the rebind-chain shape), so a future
// refactor that drops the wiring fails this test instead of silently
// zero-hitting again (the exact regression the diagnosis measured).
func TestRuntimeReloadPlanPresentInStartupOptions(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("tabs.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		"acquireRuntimeAssembly(assemblyKey)",
		"a.storeRuntimeAssembly(assemblyKey, assembly)",
		"boot.RuntimeReload{",
		"boot.FullReusePlan()",
	} {
		if !strings.Contains(string(src), needle) {
			t.Fatalf("startup chain wiring lost: %q not found in tabs.go", needle)
		}
	}
	// The gate still flows through the pool helpers — the switch itself is
	// never read directly in the startup chain.
	if strings.Contains(string(src), "ExperimentalRuntimeReuse") {
		t.Fatal("startup chain reads the gate directly instead of via the pool helpers")
	}
}
