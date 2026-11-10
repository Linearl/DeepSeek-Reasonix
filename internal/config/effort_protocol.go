package config

import "fmt"

func normalizeOpenAIReasoningEffort(e *ProviderEntry, level string) (string, error) {
	if isMimoEntry(e) {
		// Shared single normalization point for MiMo (8-level documented
		// vocabulary); see normalizeMimoEffort in effort.go.
		return normalizeMimoEffort(level)
	}
	// tl1538: the generic OpenAI protocol accepts the full documented union
	// across the vendor's model generations (GPT-5.1+: none default replacing
	// minimal; GPT-5 era: minimal; pre-5.6 ceiling: xhigh; GPT-5.6+: max).
	// Every level passes through verbatim so the stored value equals the wire
	// value; per-model narrowing is the supported_efforts declaration's job,
	// not a silent adapter-side fold. MiMo keeps its own fold above — its wire
	// vocabulary collapses aliases onto low/medium/high/none.
	switch level {
	case "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return level, nil
	default:
		return "", fmt.Errorf("usage: /effort auto|none|minimal|low|medium|high|xhigh|max")
	}
}

func normalizeKimiK3ReasoningEffort(level string) (string, error) {
	if isKimiK3ReasoningEffort(level) {
		return level, nil
	}
	return "", fmt.Errorf("usage: /effort auto|low|high|max")
}

func isKimiK3ReasoningEffort(level string) bool {
	switch level {
	case "low", "high", "max":
		return true
	default:
		return false
	}
}
