package boot

import (
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/control"
)

// approvalTierForBuild resolves the unattended-approval decision tier for one
// boot. An explicit Options.ApprovalTier wins; otherwise the loaded config's
// [agent] approval_tier applies, itself normalized to guardian|parent|human.
// This is the light path for task 52: config is the switch, no per-tab UI.
func approvalTierForBuild(cfg *config.Config, opts Options) string {
	if tier := strings.TrimSpace(opts.ApprovalTier); tier != "" {
		return control.NormalizeApprovalTier(tier)
	}
	if cfg == nil {
		return control.ApprovalTierGuardian
	}
	return control.NormalizeApprovalTier(cfg.DefaultApprovalTier())
}
