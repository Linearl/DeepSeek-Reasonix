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

// TestTL1660R2EffortReuseGateOffAndRoundTrip: the pool helpers keep their
// acquire/store round trip — an assembly stored under the root identity is
// visible on the next acquire, the exact acquire-old/store-new shape the
// effort rebuild performs. tl1859（方案A）：identity=root-only——effort 不再
// 参与键，换 effort 后发布入向 assembly 覆盖同一池条目（last-writer-wins），
// 这正是切 effort 复用不冷建的前提。
func TestTL1660R2EffortReuseGateOffAndRoundTrip(t *testing.T) {
	DropRuntimeAssemblyPools()
	defer DropRuntimeAssemblyPools()

	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	a := &App{}
	rootKey := runtimeAssemblyKeyForTab("D:\\Proj")

	// tl1741：开关退役（复用池常开）——无 gate 分支：effort rebuild 仍
	// acquire 出向 identity 的 assembly + 发布入向 assembly（下一次同配置
	// 高效 build 跳过 discovery）。
	cfgPath := filepath.Join(home, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	outgoing := &boot.ReusedAssembly{}
	a.storeRuntimeAssembly(rootKey, outgoing)
	if got := a.acquireRuntimeAssembly(rootKey); got != outgoing {
		t.Fatal("acquire did not return the pooled assembly (effort rebuild would pay rediscovery)")
	}
	// tl1859: publishing the incoming assembly under the same root-only key
	// overwrites last-writer-wins; the next acquire must see exactly it.
	fresh := &boot.ReusedAssembly{}
	a.storeRuntimeAssembly(rootKey, fresh)
	if got := a.acquireRuntimeAssembly(rootKey); got != fresh {
		t.Fatal("acquire did not return the freshly published assembly after the same-key store")
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
		"a.acquireRuntimeAssembly(runtimeAssemblyKeyForTab(snap.workspaceRoot))",
		"a.storeRuntimeAssembly(runtimeAssemblyKeyForTab(snap.workspaceRoot), assembly)",
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
