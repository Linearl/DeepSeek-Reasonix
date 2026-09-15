package boot

import (
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/control"
)

func TestApprovalTierForBuildPrefersOptionsThenConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agent.ApprovalTier = "parent"

	if got := approvalTierForBuild(cfg, Options{ApprovalTier: "human"}); got != control.ApprovalTierHuman {
		t.Fatalf("explicit option = %q, want human", got)
	}
	if got := approvalTierForBuild(cfg, Options{}); got != control.ApprovalTierParent {
		t.Fatalf("config-only = %q, want parent", got)
	}
	if got := approvalTierForBuild(&config.Config{}, Options{}); got != control.ApprovalTierGuardian {
		t.Fatalf("unset config = %q, want guardian", got)
	}
	if got := approvalTierForBuild(nil, Options{}); got != control.ApprovalTierGuardian {
		t.Fatalf("nil config = %q, want guardian", got)
	}
	// Garbage never becomes parent self-approval.
	cfg.Agent.ApprovalTier = "self-approve-everything"
	if got := approvalTierForBuild(cfg, Options{}); got != control.ApprovalTierGuardian {
		t.Fatalf("garbage config = %q, want guardian", got)
	}
}
