package config

import "reasonix/internal/provider/openai"

// migrateStoredDeepSeekEffort preserves the wire value of saved pre-contract
// aliases. New user selections still go through strict NormalizeEffort validation.
func migrateStoredDeepSeekEffort(e *ProviderEntry, effort string) string {
	if (effort == "medium" || effort == "xhigh") && (ReasoningProtocolForEntry(e) == ReasoningProtocolDeepSeek || (explicitReasoningProtocol(e) == "" && openai.IsDeepSeek(e.BaseURL))) && len(e.SupportedEfforts) == 0 {
		// DeepSeek's declared vocabulary always includes high; the stored alias
		// resolves to it whether or not the capability registry has an entry
		// (a saved config may predate the registry).
		return "high"
	}
	return effort
}

// migrateStoredMiniMaxEffort folds the M3-era binary vocabulary persisted on an
// M3.1 entry (task 1031). Before the M3.1 capability existed, the /effort menu
// offered M3.1 users adaptive|disabled; "adaptive" means provider-default —
// exactly what auto means on M3.1 (max depth, thinking on) — so it resolves to
// "" instead of failing boot at the capability Validate. "disabled" is NOT
// folded: it asked for thinking-off, which M3.1 rejects (400, error 2013), so
// it must surface as an actionable boot error rather than silently becoming
// max-depth thinking.
func migrateStoredMiniMaxEffort(e *ProviderEntry, effort string) string {
	if effort == "adaptive" && isMiniMaxM31Entry(e) {
		return ""
	}
	return effort
}
