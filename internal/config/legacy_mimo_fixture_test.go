package config

import "strings"

// legacyMimoCustomProvider builds the legacy OpenAI-compatible MiMo connection
// entry a pre-migration config used to carry. It is a TEST FIXTURE only:
// tl1654 (round 3) removed the loader's ability to re-create such entries for
// providers the user deleted, so nothing in production may materialize one.
func legacyMimoCustomProvider(name string) ProviderEntry {
	switch strings.TrimSpace(name) {
	case "mimo", "xiaomi-mimo", "xiaomi_mimo", "mimo-api":
		models := legacyMimoAPIModels()
		return ProviderEntry{
			Name:          strings.TrimSpace(name),
			Kind:          "openai",
			BaseURL:       "https://api.xiaomimimo.com/v1",
			Models:        models,
			VisionModels:  []string{"mimo-v2.5", "mimo-v2-omni"},
			Default:       "mimo-v2.5-pro",
			APIKeyEnv:     "MIMO_API_KEY",
			ContextWindow: 1_048_576,
			Prices:        mimoDomesticPrices(models),
			NoProxy:       true,
		}
	case "mimo-token-plan":
		models := legacyMimoTokenPlanModels()
		return ProviderEntry{
			Name:          "mimo-token-plan",
			Kind:          "openai",
			BaseURL:       "https://token-plan-cn.xiaomimimo.com/v1",
			Models:        models,
			VisionModels:  []string{"mimo-v2.5"},
			Default:       "mimo-v2.5-pro",
			APIKeyEnv:     "MIMO_API_KEY",
			ContextWindow: 1_048_576,
			Prices:        mimoDomesticPrices(models),
			NoProxy:       true,
		}
	case "mimo-flash":
		return ProviderEntry{Name: "mimo-flash", Kind: "openai", BaseURL: "https://token-plan-cn.xiaomimimo.com/v1", Model: "mimo-v2.5", APIKeyEnv: "MIMO_API_KEY", ContextWindow: 1_000_000, Price: mimoV25Price(), NoProxy: true}
	default:
		return ProviderEntry{Name: "mimo-pro", Kind: "openai", BaseURL: "https://token-plan-cn.xiaomimimo.com/v1", Model: "mimo-v2.5-pro", APIKeyEnv: "MIMO_API_KEY", ContextWindow: 1_000_000, Price: mimoV25ProPrice(), NoProxy: true}
	}
}
