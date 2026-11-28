package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/boot"
)

// TestStartupChainReuseKeyMatchesRebindChain: the task-405 Q1 wiring must
// derive the pool key from the same normalized-root identity as the rebind
// chain (runtimeAssemblyKeyForTab), so an assembly stored by the startup
// chain is visible to the rebind chain and vice versa.
// tl1859（方案A）：identity=root-only——model/effort 不再参与键，同 root 跨
// 模型/effort 必须同键（这正是放宽的目的：换模型打开不再全额冷建）。
func TestStartupChainReuseKeyMatchesRebindChain(t *testing.T) {
	startupKey := runtimeAssemblyKeyForTab("D:\\Proj")
	rebindKey := runtimeAssemblyKey("d:\\proj") // rebind normalizes before hashing
	if startupKey != rebindKey {
		t.Fatalf("startup key %q != rebind key %q — the two chains would never share assemblies", startupKey, rebindKey)
	}
	// tl1859: the root-only key keeps its case/whitespace normalization, and a
	// distinct root must not collide with it.
	if runtimeAssemblyKeyForTab("D:\\Proj") != runtimeAssemblyKeyForTab("  d:\\proj  ") {
		t.Fatal("root-only key lost its case/whitespace normalization")
	}
	if runtimeAssemblyKeyForTab("D:\\Proj") == runtimeAssemblyKeyForTab("D:\\Other") {
		t.Fatal("different roots share a pool key")
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
	key := runtimeAssemblyKeyForTab("D:\\Proj")

	// tl1741：开关退役（复用池常开）——gate on/off 双分支退化为单分支：
	// 冷池 acquire nil + store→hit 返回原 assembly（语义与原 gate-on 一致）。
	cfgPath := filepath.Join(home, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := a.acquireRuntimeAssembly(key); got != nil {
		t.Fatal("cold-pool acquire returned an assembly")
	}
	assembly := &boot.ReusedAssembly{}
	a.storeRuntimeAssembly(key, assembly)
	if got := a.acquireRuntimeAssembly(key); got != assembly {
		t.Fatal("store→acquire did not return the stored assembly (startup chain would rebuild discovery)")
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
