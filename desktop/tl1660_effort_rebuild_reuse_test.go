package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/boot"
)

// tl1660: the effort-switch fallback rebuild joins the 363A runtime-reuse
// pool. The 20261117 incident measured skills 8.5-13s + agent:tools 1.6-2.6s
// per effort rebuild (boot: stage timings) — pure rediscovery tax on a switch
// that changes only the wire's reasoning depth. These nails pin the wiring.

// TestTL1660R2EffortReuseGateOffAndRoundTrip: gate off — the pool helpers stay
// no-ops (byte-identical pre-tl1660 build); gate on — an assembly stored under
// the outgoing (root, model, effort) identity is visible at the new identity
// after a store, the exact acquire-old/store-new shape the effort rebuild
// performs.
func TestTL1660R2EffortReuseGateOffAndRoundTrip(t *testing.T) {
	DropRuntimeAssemblyPools()
	defer DropRuntimeAssemblyPools()

	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	a := &App{}
	oldKey := runtimeAssemblyKeyForTab("D:\\Proj", "deepseek/v4", "low")
	newKey := runtimeAssemblyKeyForTab("D:\\Proj", "deepseek/v4", "high")

	// tl1741：开关退役（复用池常开）——gate off 分支退化为单分支：
	// effort rebuild 仍 acquire 出向 identity 的 assembly + 发布入向 assembly
	// （下一次同配置高效 build 跳过 discovery）。
	cfgPath := filepath.Join(home, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	outgoing := &boot.ReusedAssembly{}
	a.storeRuntimeAssembly(oldKey, outgoing)
	if got := a.acquireRuntimeAssembly(oldKey); got != outgoing {
		t.Fatal("gate-on acquire did not return the outgoing-effort assembly (effort rebuild would pay rediscovery)")
	}
	fresh := &boot.ReusedAssembly{}
	a.storeRuntimeAssembly(newKey, fresh)
	if got := a.acquireRuntimeAssembly(newKey); got != fresh {
		t.Fatal("gate-on acquire did not return the incoming-effort assembly")
	}
}

// TestTL1660R2EffortRebuildReuseWiring is a source-shape guard (the
// TestRuntimeReloadPlanPresentInStartupOptions precedent): the effort fallback
// must construct its boot.Options with the embedded RuntimeReload feeding the
// pool on the outgoing identity and publish under the post-switch identity, so
// a refactor that drops the wiring fails here instead of silently paying the
// 20261117 rediscovery tax again.
func TestTL1660R2EffortRebuildReuseWiring(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("app_effort.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		"boot.BuildWithAssembly(",
		"a.acquireRuntimeAssembly(runtimeAssemblyKeyForTab(snap.workspaceRoot, snap.model, oldEffortKey))",
		"a.storeRuntimeAssembly(runtimeAssemblyKeyForTab(snap.workspaceRoot, snap.model, effort), assembly)",
		"boot.RuntimeReload{",
		"boot.FullReusePlan()",
	} {
		if !strings.Contains(string(src), needle) {
			t.Fatalf("effort rebuild reuse wiring lost: %q not found in app_effort.go", needle)
		}
	}
	// The gate still flows through the pool helpers — the switch itself is
	// never read directly in the effort chain.
	if strings.Contains(string(src), "ExperimentalRuntimeReuse") {
		t.Fatal("effort chain reads the gate directly instead of via the pool helpers")
	}
}

// TestTL1660R3BuildSegmentLineWired pins the build/swap boundary read
// (tl1660): the runtime-build-end line covers the whole window and boot's
// stage timings only the build interior, so the resume/swap half of a slow
// effort switch was attributable only by subtraction across two logs.
func TestTL1660R3BuildSegmentLineWired(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("app_effort.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		"desktop: effort rebuild segments",
		`"build_ms"`,
	} {
		if !strings.Contains(string(src), needle) {
			t.Fatalf("effort rebuild segment line lost: %q not found in app_effort.go", needle)
		}
	}
}
