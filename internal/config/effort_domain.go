package config

import "strings"

// EffortMenuDomain is the effort level menu a settings surface may offer for a
// resolved provider entry (task 1151): EffortCapabilityForEntry's menu — the
// composer-level "auto" alias included — filtered down to the levels the
// dispatch path actually accepts. Every non-auto level must pass the exact
// gates boot's inheritedSubagentEffort applies before a stored value reaches
// the wire (the NormalizeEffort fold, then ReasoningCapabilityForEntry's
// provider capability Validate, including the anthropic Thinking=adaptive
// lift), so a level the runtime would drop with a boot warning never renders
// as a pickable option. This is the single source both desktop surfaces (the
// global agent.subagent_effort row and the per-profile pickers) read, which
// keeps the menu, the write-time validation and the wire in lockstep.
//
// aliasFold mirrors EffortCapability.AliasFold: the MiMo identity mark display
// surfaces fold onto the four honest tiers — never a vocabulary-shape sniff.
func EffortMenuDomain(e *ProviderEntry) (levels []string, aliasFold bool) {
	cap := EffortCapabilityForEntry(e)
	if !cap.Supported {
		return nil, false
	}
	out := make([]string, 0, len(cap.Levels))
	for _, level := range cap.Levels {
		if normalizeEffortLevel(level) == "auto" {
			// "auto" is the composer's inherit-the-default alias, not a wire
			// value; it is always offerable.
			out = append(out, "auto")
			continue
		}
		if EffortLevelReachesWire(e, level) {
			out = append(out, level)
		}
	}
	return out, cap.AliasFold
}

// EffortLevelReachesWire reports whether a raw menu level survives the same
// two gates the dispatch path applies to a stored effort for this entry. An
// empty level means auto/provider default and always applies.
func EffortLevelReachesWire(e *ProviderEntry, level string) bool {
	level = strings.TrimSpace(level)
	if level == "" || normalizeEffortLevel(level) == "auto" {
		return true
	}
	if e == nil {
		return false
	}
	probe := *e
	normalized, err := NormalizeEffort(&probe, level)
	if err != nil {
		return false
	}
	probe.Effort = normalized
	if probe.Kind == "anthropic" && strings.TrimSpace(probe.Thinking) == "" {
		// Same lift resolveSubagentProvider applies before building the
		// provider: the anthropic capability read depends on it.
		probe.Thinking = "adaptive"
	}
	return ReasoningCapabilityForEntry(&probe).Validate(probe.Model, EffectiveEffort(&probe)) == nil
}
