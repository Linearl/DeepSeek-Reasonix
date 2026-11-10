package responses

import "reasonix/internal/provider"

func ReasoningForConfig(cfg provider.Config) provider.ReasoningCapability {
	protocol, _ := cfg.Extra["reasoning_protocol"].(string)
	if protocol == "none" {
		return provider.ReasoningOptions("")
	}
	cap := provider.ReasoningOptions("")
	switch DetectVendor(cfg.BaseURL) {
	case "deepseek":
		cap = provider.ReasoningOptions("high", "none", "low", "high", "max")
	case "mimo":
		cap = provider.ReasoningOptions("", "none", "low", "medium", "high")
	default:
		if protocol == "openai" {
			// tl1538: mirror internal/config's openAIEffortCapability — the
			// documented union across OpenAI's model generations (none is the
			// GPT-5.1+ default, minimal the GPT-5-era spelling, xhigh/max the
			// pre-5.6 / 5.6+ ceilings). Per-model narrowing stays with
			// supported_efforts declarations.
			cap = provider.ReasoningOptions("", "none", "minimal", "low", "medium", "high", "xhigh", "max")
		}
	}
	return provider.DeclaredReasoning(cfg, cap)
}
func (c *client) ReasoningCapability() provider.ReasoningCapability { return c.reasoning.Clone() }
