package main

// tl1859（方案A）：装配产物池键放宽为 root-only。同项目换模型/effort 打开
// 对话命中同一池条目，不再全额冷建（现状 ~43s → 目标 ≤20s 首条消息可发送）。
// 正确性依据（启动耗时根因诊断报告 §七）：复用产物全部按 root 构建；模型维度
// 的唯一已知分支 persona 由 boot 复用后 strip+prepend 重贴（boot 层断言钉在
// internal/boot）；effort 不进任何复用内容。本文件钉两件事：
//  1. 键行为——model/effort 不参与、root 归一化保留、异 root 不串
//  2. 四链 wiring——开 tab / 切模型 / 切 effort / rebind 全部经
//     runtimeAssemblyKeyForTab 单实参形态构造（唯一构造点，无私自加维）

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestTL1859RootOnlyKeyIgnoresModelAndEffort：键放宽的行为钉——同 root 换
// 模型/effort 必须同键（放宽的目的），异 root 必须异键（跨项目复用是
// non-goal：SystemPrompt/skills/memory 按 root 构建，跨项目=串台）。
func TestTL1859RootOnlyKeyIgnoresModelAndEffort(t *testing.T) {
	DropRuntimeAssemblyPools()
	defer DropRuntimeAssemblyPools()

	// model/effort 不再参与键（runtimeAssemblyKeyForTab 签名只有 workspaceRoot，
	// 编译期即堵死加维回归）——同 root 键恒定：
	base := runtimeAssemblyKeyForTab("D:\\Proj")
	if again := runtimeAssemblyKeyForTab("D:\\Proj"); again != base {
		t.Fatalf("key drifted across calls: %q != %q", again, base)
	}
	// 归一化保留（大小写/首尾空白），异 root 不碰撞。
	if runtimeAssemblyKeyForTab("  d:\\proj  ") != base {
		t.Fatal("root-only key lost its case/whitespace normalization")
	}
	if runtimeAssemblyKeyForTab("D:\\Other") == base {
		t.Fatal("different roots share a pool key — cross-project reuse would bleed assemblies")
	}
}

// TestTL1859FourChainsUseRootOnlyKeyWiring 是 source-shape 防线
// （TestRuntimeReloadPlanPresentInStartupOptions 先例）：四链必须经
// runtimeAssemblyKeyForTab 构造池键，且不得出现旧的三实参形态或绕过构造点
// 直调 runtimeAssemblyKey。任何链私自加回 model/effort 维度（换模型/effort
// 打开重新全额冷建）在此红，而不是在用户的启动耗时里红。
func TestTL1859FourChainsUseRootOnlyKeyWiring(t *testing.T) {
	chains := map[string]string{
		"tabs.go":               "runtimeAssemblyKeyForTab(root)",
		"app_model.go":          "runtimeAssemblyKeyForTab(snap.workspaceRoot)",
		"app_effort.go":         "runtimeAssemblyKeyForTab(snap.workspaceRoot)",
		"app_session_rebind.go": "runtimeAssemblyKeyForTab(root)",
	}
	for _, file := range []string{"tabs.go", "app_model.go", "app_effort.go", "app_session_rebind.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := string(src)
		if !strings.Contains(text, chains[file]) {
			t.Fatalf("%s lost the root-only key construction: %q not found", file, chains[file])
		}
		// 旧三实参形态（或任何多实参形态）不得回来。
		if legacy := regexp.MustCompile(`runtimeAssemblyKeyForTab\([^,)]+,`).FindString(text); legacy != "" {
			t.Fatalf("%s constructs the pool key with more than the workspace root: %q — model/effort must not re-enter the key", file, legacy)
		}
		// 构造点之外的直调（绕过归一化）不得出现。
		if strings.Contains(text, "runtimeAssemblyKey(") {
			t.Fatalf("%s calls runtimeAssemblyKey directly — the tab key must go through runtimeAssemblyKeyForTab", file)
		}
	}
}
