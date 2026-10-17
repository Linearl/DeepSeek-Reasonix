package config

import (
	"strings"
	"testing"
)

// 任务 943：experimental_tool_optimizations / experimental_multi_edit_injection
// 拆分的 config 层语义钉。
//
// 验收矩阵（本文件钉 Go 侧解析；boot 侧行为见
// internal/boot/provider_surface_extras_test.go 的真实构建捕获）：
//
//	A(族键/readBack)  B(注入键)        readBack 面   multi_edit 注入
//	absent           absent           关            关（= 拆分前基线，铁律 2）
//	true             absent           开            开（回落 A，存量用户迁移不丢）
//	absent/false     true             关            开（反向独立）
//	true             false            开            关（正向独立）
//	true             true             开            开
//	false            false            关            关

func boolPtr943(v bool) *bool { return &v }

// TestMultiEditInjectionFallbackSemantics pins the two-key resolution:
// explicit B wins in both directions, absent B falls back to A (migration),
// both absent reads as off.
func TestMultiEditInjectionFallbackSemantics(t *testing.T) {
	cases := []struct {
		name    string
		a, b    *bool
		wantReadBack bool
		wantInject   bool
	}{
		{name: "both absent = pre-split baseline off", wantReadBack: false, wantInject: false},
		{name: "family on, B absent = migration fallback keeps injection", a: boolPtr943(true), wantReadBack: true, wantInject: true},
		{name: "family absent, B on = reverse split (injection alone)", b: boolPtr943(true), wantReadBack: false, wantInject: true},
		{name: "family off, B on = reverse split overrides", a: boolPtr943(false), b: boolPtr943(true), wantReadBack: false, wantInject: true},
		{name: "family on, B off = forward split (readBack alone)", a: boolPtr943(true), b: boolPtr943(false), wantReadBack: true, wantInject: false},
		{name: "family off, B absent = off stays off", a: boolPtr943(false), wantReadBack: false, wantInject: false},
		{name: "both on = full family", a: boolPtr943(true), b: boolPtr943(true), wantReadBack: true, wantInject: true},
		{name: "both off = full off", a: boolPtr943(false), b: boolPtr943(false), wantReadBack: false, wantInject: false},
	}
	for _, tc := range cases {
		c := &Config{}
		c.Agent.ExperimentalToolOptimizations = tc.a
		c.Agent.ExperimentalMultiEditInjection = tc.b
		if got := c.ToolOptimizationsEnabled(); got != tc.wantReadBack {
			t.Errorf("%s: ToolOptimizationsEnabled() = %v, want %v", tc.name, got, tc.wantReadBack)
		}
		if got := c.MultiEditInjectionEnabled(); got != tc.wantInject {
			t.Errorf("%s: MultiEditInjectionEnabled() = %v, want %v", tc.name, got, tc.wantInject)
		}
	}
	// nil config reads as off on both faces.
	var nilCfg *Config
	if nilCfg.ToolOptimizationsEnabled() || nilCfg.MultiEditInjectionEnabled() {
		t.Fatal("nil config must read as off on both faces")
	}
}

// TestSetExperimentalMultiEditInjectionMaterializesKey pins the task-915
// materialization rule for the new key: the setter always writes a non-nil
// value (an explicit click converts the migration fallback into an explicit
// key), and the two setters stay independent.
func TestSetExperimentalMultiEditInjectionMaterializesKey(t *testing.T) {
	c := &Config{}
	if c.Agent.ExperimentalMultiEditInjection != nil {
		t.Fatal("fresh config must start with the injection key absent")
	}
	if err := c.SetExperimentalMultiEditInjection(true); err != nil {
		t.Fatalf("SetExperimentalMultiEditInjection(true): %v", err)
	}
	if c.Agent.ExperimentalMultiEditInjection == nil || !*c.Agent.ExperimentalMultiEditInjection {
		t.Fatalf("injection key after materialization = %v, want explicit true", c.Agent.ExperimentalMultiEditInjection)
	}
	if c.Agent.ExperimentalToolOptimizations != nil {
		t.Fatalf("setting the injection key must not touch the family key (read %v)", c.Agent.ExperimentalToolOptimizations)
	}
	if err := c.SetExperimentalMultiEditInjection(false); err != nil {
		t.Fatalf("SetExperimentalMultiEditInjection(false): %v", err)
	}
	if c.Agent.ExperimentalMultiEditInjection == nil || *c.Agent.ExperimentalMultiEditInjection {
		t.Fatal("explicit false must stick")
	}
	if err := c.SetExperimentalToolOptimizations(true); err != nil {
		t.Fatalf("SetExperimentalToolOptimizations(true): %v", err)
	}
	if c.Agent.ExperimentalMultiEditInjection == nil || *c.Agent.ExperimentalMultiEditInjection {
		t.Fatal("setting the family key must not touch the injection key")
	}
}

// TestRenderEmitsSplitKeysConditionally pins the task-722 conditional render
// rule for both keys: an absent key renders NOTHING (the migration fallback
// is never baked into an explicit value by an unrelated save), a set key
// emits its line.
func TestRenderEmitsSplitKeysConditionally(t *testing.T) {
	// 注意：渲染行的注释里会提及另一个键名（回落说明），断言必须锚定
	// 「键名 = 值」的行首形态，不能裸 Contains 键名。
	absent := &Config{}
	rendered := RenderTOML(absent)
	if strings.Contains(rendered, "experimental_tool_optimizations =") {
		t.Fatal("absent family key must not render (task-722 conditional rule)")
	}
	if strings.Contains(rendered, "experimental_multi_edit_injection =") {
		t.Fatal("absent injection key must not render — rendering it would freeze the migration fallback into an explicit value")
	}

	// 只写注入键：只有注入键渲染（族键缺席保持缺席）。
	injectionOnly := &Config{}
	injectionOnly.Agent.ExperimentalMultiEditInjection = boolPtr943(true)
	rendered = RenderTOML(injectionOnly)
	if !strings.Contains(rendered, "experimental_multi_edit_injection = true") {
		t.Fatalf("set injection key must render its line:\n%s", rendered)
	}
	if strings.Contains(rendered, "experimental_tool_optimizations =") {
		t.Fatal("unset family key must stay absent when only the injection key is set")
	}

	// 显式关也渲染（81/123 lost-save：关值不渲染会在下次保存时弹回）。
	bothOff := &Config{}
	bothOff.Agent.ExperimentalToolOptimizations = boolPtr943(false)
	bothOff.Agent.ExperimentalMultiEditInjection = boolPtr943(false)
	rendered = RenderTOML(bothOff)
	if !strings.Contains(rendered, "experimental_tool_optimizations = false") ||
		!strings.Contains(rendered, "experimental_multi_edit_injection = false") {
		t.Fatalf("explicit false on both keys must render both lines (lost-save rule):\n%s", rendered)
	}
}

// TestLabFeatureTiersCarriesSplitToolOpt pins the tier register for the
// split: the family entry stays recommended (readBack 转正 783④), the new
// injection entry registers optional, and both render keys are physically
// emitted by the render table (the 562 reverse gate lives in
// render_lab_tiers_test.go — this test pins the entry shape).
func TestLabFeatureTiersCarriesSplitToolOpt(t *testing.T) {
	var family, injection *labFeatureTier
	for i := range labFeatureTiers {
		switch labFeatureTiers[i].id {
		case "toolOptimizations":
			family = &labFeatureTiers[i]
		case "multiEditInjection":
			injection = &labFeatureTiers[i]
		}
	}
	if family == nil {
		t.Fatal("labFeatureTiers lost the toolOptimizations entry")
	}
	if family.tier != LabTierRecommended {
		t.Fatalf("toolOptimizations tier = %q, want recommended (783④ 转正不变)", family.tier)
	}
	if injection == nil {
		t.Fatal("labFeatureTiers missing the task-943 multiEditInjection entry")
	}
	if injection.tier != LabTierOptional {
		t.Fatalf("multiEditInjection tier = %q, want optional (943 定档)", injection.tier)
	}
	if len(injection.renderKeys) != 1 || injection.renderKeys[0] != "experimental_multi_edit_injection" {
		t.Fatalf("multiEditInjection renderKeys = %v, want [experimental_multi_edit_injection]", injection.renderKeys)
	}
}
