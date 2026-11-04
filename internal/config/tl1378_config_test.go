package config

import (
	"strings"
	"testing"
)

// tl1378：两个 context-maintenance 实验开关默认关（铁律 2），并全链渲染
// （config → setter → render 表——81/123 丢档教训：渲染表缺键 = 下一次设置
// 保存把开关静默翻回默认）。curate_context 与 review_hint 各钉一条。
func TestTL1378CurateContextSwitchDefaultOffAndRender(t *testing.T) {
	cfg := Default()
	if cfg.Agent.ExperimentalCurateContext {
		t.Fatal("experimental_curate_context must default off (iron rule 2)")
	}
	if cfg.Agent.ExperimentalReviewHint {
		t.Fatal("experimental_review_hint must default off (iron rule 2)")
	}

	if err := cfg.SetExperimentalCurateContext(true); err != nil {
		t.Fatalf("SetExperimentalCurateContext: %v", err)
	}
	if !cfg.Agent.ExperimentalCurateContext {
		t.Fatal("setter did not store the switch")
	}
	if err := cfg.SetExperimentalCurateContext(false); err != nil {
		t.Fatalf("SetExperimentalCurateContext(false): %v", err)
	}
	out := RenderTOML(cfg)
	for _, want := range []string{
		"experimental_curate_context = false",
		"experimental_review_hint = false",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered config missing %q:\n%s", want, out)
		}
	}
	if err := cfg.SetExperimentalReviewHint(true); err != nil {
		t.Fatalf("SetExperimentalReviewHint: %v", err)
	}
	if !cfg.Agent.ExperimentalReviewHint {
		t.Fatal("review hint setter did not store the switch")
	}
}
