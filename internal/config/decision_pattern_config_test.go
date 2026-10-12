package config

import (
	"strings"
	"testing"
)

// 任务 801 Phase 2：固化链三个配置键的 setter 行为。验收：阈值越界被拒绝
// （防静默关闸）、文件路径仅接受 .md 或空、auto_approve 与开关往返一致、
// 渲染显式输出四个键。
func TestDecisionPatternConfigSetters(t *testing.T) {
	c := Default()
	if c.Agent.ExperimentalDecisionPatterns {
		t.Fatal("lab switch must default off")
	}
	if c.Agent.DecisionPatternAutoApprove {
		t.Fatal("auto approve must default off")
	}
	if c.Agent.DecisionPatternThreshold != 0 {
		t.Fatalf("threshold must default to 0 (=default 5 at read sites), got %d", c.Agent.DecisionPatternThreshold)
	}
	if err := c.SetExperimentalDecisionPatterns(true); err != nil || !c.Agent.ExperimentalDecisionPatterns {
		t.Fatalf("toggle switch: %v", err)
	}
	if err := c.SetDecisionPatternThreshold(5); err != nil || c.Agent.DecisionPatternThreshold != 5 {
		t.Fatalf("set threshold: %v %d", err, c.Agent.DecisionPatternThreshold)
	}
	for _, bad := range []int{0, -1, 501} {
		if err := c.SetDecisionPatternThreshold(bad); err == nil {
			t.Fatalf("threshold %d must be rejected", bad)
		}
	}
	// 阈值被拒绝后保持原值（不是清零）。
	if c.Agent.DecisionPatternThreshold != 5 {
		t.Fatalf("rejected write must not clobber, got %d", c.Agent.DecisionPatternThreshold)
	}
	if err := c.SetDecisionPatternFilePath(""); err != nil || c.Agent.DecisionPatternFilePath != "" {
		t.Fatalf("empty path restores default: %v", err)
	}
	if err := c.SetDecisionPatternFilePath("D:/notes/dp.md"); err != nil || c.Agent.DecisionPatternFilePath != "D:/notes/dp.md" {
		t.Fatalf("custom path: %v", err)
	}
	if err := c.SetDecisionPatternFilePath("D:/notes/dp.txt"); err == nil {
		t.Fatal("non-.md path must be rejected")
	}
	if err := c.SetDecisionPatternAutoApprove(true); err != nil || !c.Agent.DecisionPatternAutoApprove {
		t.Fatalf("auto approve: %v", err)
	}
}

func TestDecisionPatternRenderIncludesAllKeys(t *testing.T) {
	c := Default()
	c.Agent.ExperimentalDecisionPatterns = true
	c.Agent.DecisionPatternThreshold = 7
	c.Agent.DecisionPatternAutoApprove = true
	out := RenderTOML(c)
	for _, key := range []string{
		"experimental_decision_patterns = true",
		"decision_pattern_file_path = ",
		"decision_pattern_threshold = 7",
		"decision_pattern_auto_approve = true",
	} {
		if !strings.Contains(out, key) {
			t.Fatalf("render must carry %q:\n%s", key, out)
		}
	}
	// 关闭态也显式渲染（omit-on-off 丢档教训）。
	c2 := Default()
	out2 := RenderTOML(c2)
	if !strings.Contains(out2, "experimental_decision_patterns = false") {
		t.Fatal("off state must render explicitly")
	}
	if !strings.Contains(out2, "decision_pattern_auto_approve = false") {
		t.Fatal("auto approve off state must render explicitly")
	}
}
