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
