package config

import (
	"strings"
	"testing"
)

func TestDefaultApprovalTierNormalizes(t *testing.T) {
	cases := map[string]string{
		"":           "guardian",
		"guardian":   "guardian",
		"parent":     "parent",
		"Parent":     "parent",
		"human":      "human",
		"nonsense":   "guardian",
	}
	for raw, want := range cases {
		c := &Config{}
		c.Agent.ApprovalTier = raw
		if got := c.DefaultApprovalTier(); got != want {
			t.Errorf("DefaultApprovalTier(%q) = %q, want %q", raw, got, want)
		}
	}
	var nilCfg *Config
	if got := nilCfg.DefaultApprovalTier(); got != "guardian" {
		t.Errorf("nil config DefaultApprovalTier = %q, want guardian", got)
	}
}

func TestApprovalTierRendersWhenSet(t *testing.T) {
	c := &Config{}
	c.Agent.ApprovalTier = "parent"
	out := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(out, `approval_tier = "parent"`) {
		t.Fatalf("render missing approval_tier\n---\n%s", out)
	}
}

func TestApprovalTierStaysCommentedWhenUnset(t *testing.T) {
	c := &Config{}
	out := RenderTOMLForScope(c, RenderScopeUser)
	if strings.Contains(out, "\napproval_tier") {
		t.Fatalf("untouched config should not set approval_tier\n---\n%s", out)
	}
	if !strings.Contains(out, `# approval_tier = "guardian"`) {
		t.Fatalf("untouched config should keep the commented default\n---\n%s", out)
	}
}
