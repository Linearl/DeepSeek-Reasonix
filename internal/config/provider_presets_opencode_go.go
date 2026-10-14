package config

import "reasonix/internal/provider"

var (
	opencodeGoAnthropicModels       = provider.OpenCodeGoModelIDs(provider.OpenCodeGoRouteAnthropic)
	opencodeGoAnthropicVisionModels = provider.OpenCodeGoVisionModelIDs(provider.OpenCodeGoRouteAnthropic)
	opencodeGoResponsesModels       = provider.OpenCodeGoModelIDs(provider.OpenCodeGoRouteResponses)
	opencodeGoResponsesVisionModels = provider.OpenCodeGoVisionModelIDs(provider.OpenCodeGoRouteResponses)
)

func withOpenCodeGoChatContextOverrides(base map[string]ProviderModelOverride) map[string]ProviderModelOverride {
	if base == nil {
		base = map[string]ProviderModelOverride{}
	}
	for id, lim := range provider.OpenCodeGoChatModels() {
		ov := base[id]
		ov.ContextWindow = lim.Context
		base[id] = ov
	}
	return base
}

func openCodeGoAnthropicContextOverrides() map[string]ProviderModelOverride {
	out := map[string]ProviderModelOverride{}
	for id, lim := range provider.OpenCodeGoAnthropicModels() {
		out[id] = ProviderModelOverride{ContextWindow: lim.Context}
	}
	return out
}

func openCodeGoResponsesContextOverrides() map[string]ProviderModelOverride {
	out := map[string]ProviderModelOverride{}
	for id, lim := range provider.OpenCodeGoResponsesModels() {
		out[id] = ProviderModelOverride{ContextWindow: lim.Context}
	}
	out["grok-4.5"] = ProviderModelOverride{ContextWindow: 500_000, SupportedEfforts: []string{"low", "medium", "high"}, DefaultEffort: "high"}
	out["gpt-5.6-luna"] = ProviderModelOverride{ContextWindow: 1_050_000, SupportedEfforts: []string{"none", "low", "medium", "high", "xhigh", "max"}, DefaultEffort: "high"}
	out["muse-spark-1.2-contributor"] = ProviderModelOverride{ContextWindow: 1_048_576, SupportedEfforts: []string{"minimal", "low", "medium", "high", "xhigh"}, DefaultEffort: "high"}
	return out
}

// openCodeGoChatEffortDefaults carries the reasoning protocol + supported
// effort ladder for every OpenCode Go Chat-route model whose declaration is
// shared by BOTH chat presets (the legacy "opencode-go" compatibility preset
// and "opencode-go-recommended"). Task 880: /effort refused every chat model
// without one of these entries ("effort is not configurable"), so the two
// presets must never drift apart again — the legacy preset installs an entry
// with the same name and used to overwrite a freshly installed recommended
// declaration.
//
// Levels come from the embedded models.dev snapshot (pi thinkingLevelMap):
//   - a level mapped to a string (hy3/hy4 off→"none") is a supported wire value;
//   - a level mapped to null is rejected by the endpoint (qwen3.8-max high);
//   - a null thinkingLevelMap (longcat, mimo, minimax, qwen3.7-plus) means no
//     per-level map exists, so the generic OpenAI-compatible ladder applies —
//     the same convention the hand-tuned kimi-k2.7-code entry already uses.
func openCodeGoChatEffortDefaults() map[string]ProviderModelOverride {
	return map[string]ProviderModelOverride{
		"glm-5.1": {
			ReasoningProtocol: ReasoningProtocolOpenAI,
			SupportedEfforts:  []string{"low", "high"},
			DefaultEffort:     "low",
		},
		"deepseek-v4-flash-vision-exp": {
			ReasoningProtocol: ReasoningProtocolDeepSeek,
			SupportedEfforts:  []string{"disabled", "low", "high", "max"},
			DefaultEffort:     "high",
		},
		// off is explicitly null in the catalog: thinking cannot be disabled.
		"deepseek-v4.1-flash": {
			ReasoningProtocol: ReasoningProtocolDeepSeek,
			SupportedEfforts:  []string{"low", "high", "max"},
			DefaultEffort:     "high",
		},
		"glm-5.3-flash": {
			ReasoningProtocol: ReasoningProtocolOpenAI,
			SupportedEfforts:  []string{"low", "high", "max"},
			DefaultEffort:     "low",
		},
		// off maps to the "none" wire value; low/medium/max are rejected.
		"hy4-preview": {
			ReasoningProtocol: ReasoningProtocolOpenAI,
			SupportedEfforts:  []string{"none", "high"},
			DefaultEffort:     "high",
		},
		"longcat-2.0": {
			ReasoningProtocol: ReasoningProtocolOpenAI,
			SupportedEfforts:  []string{"low", "medium", "high"},
			DefaultEffort:     "low",
		},
		"longcat-2.5-preview-free": {
			ReasoningProtocol: ReasoningProtocolOpenAI,
			SupportedEfforts:  []string{"low", "medium", "high"},
			DefaultEffort:     "low",
		},
		"mimo-v2.5": {
			ReasoningProtocol: ReasoningProtocolOpenAI,
			SupportedEfforts:  []string{"low", "medium", "high"},
			DefaultEffort:     "low",
		},
		"mimo-v2.5-pro": {
			ReasoningProtocol: ReasoningProtocolOpenAI,
			SupportedEfforts:  []string{"low", "medium", "high"},
			DefaultEffort:     "low",
		},
		"mimo-v2.6-flash": {
			ReasoningProtocol: ReasoningProtocolOpenAI,
			SupportedEfforts:  []string{"low", "medium", "high"},
			DefaultEffort:     "low",
		},
		"mimo-v2.6-pro": {
			ReasoningProtocol: ReasoningProtocolOpenAI,
			SupportedEfforts:  []string{"low", "medium", "high"},
			DefaultEffort:     "low",
		},
		"minimax-m2.7": {
			ReasoningProtocol: ReasoningProtocolOpenAI,
			SupportedEfforts:  []string{"low", "medium", "high"},
			DefaultEffort:     "low",
		},
		"qwen3.7-plus": {
			ReasoningProtocol: ReasoningProtocolOpenAI,
			SupportedEfforts:  []string{"low", "medium", "high"},
			DefaultEffort:     "low",
		},
		// high is explicitly null in the catalog: asking for it must fail fast.
		"qwen3.8-max": {
			ReasoningProtocol: ReasoningProtocolOpenAI,
			SupportedEfforts:  []string{"low", "medium", "xhigh"},
			DefaultEffort:     "low",
		},
		"space-bunny-free": {
			ReasoningProtocol: ReasoningProtocolOpenAI,
			SupportedEfforts:  []string{"low", "medium", "high", "xhigh", "max"},
			DefaultEffort:     "low",
		},
	}
}

// mergeOpenCodeGoChatReasoning lays the shared reasoning declarations under a
// preset's hand-tuned per-model table. Preset-specific entries always win so
// each preset keeps its own default-effort policy.
func mergeOpenCodeGoChatReasoning(base, handTuned map[string]ProviderModelOverride) map[string]ProviderModelOverride {
	out := make(map[string]ProviderModelOverride, len(base)+len(handTuned))
	for id, ov := range base {
		out[id] = ProviderModelOverride{
			ReasoningProtocol: ov.ReasoningProtocol,
			SupportedEfforts:  append([]string(nil), ov.SupportedEfforts...),
			DefaultEffort:     ov.DefaultEffort,
			ContextWindow:     ov.ContextWindow,
			MaxOutputTokens:   ov.MaxOutputTokens,
		}
	}
	for id, ov := range handTuned {
		merged := out[id]
		if ov.ReasoningProtocol != "" {
			merged.ReasoningProtocol = ov.ReasoningProtocol
		}
		if ov.SupportedEfforts != nil {
			merged.SupportedEfforts = append([]string(nil), ov.SupportedEfforts...)
		}
		if ov.DefaultEffort != "" {
			merged.DefaultEffort = ov.DefaultEffort
		}
		if ov.ContextWindow > 0 {
			merged.ContextWindow = ov.ContextWindow
		}
		if ov.MaxOutputTokens != 0 {
			merged.MaxOutputTokens = ov.MaxOutputTokens
		}
		out[id] = merged
	}
	return out
}

// openCodeGoRecommendedChatOverrides keeps the one-click setup conservative:
// most coding turns stay on a low reasoning effort and a bounded output cap.
// Users can still raise the effort or output budget on the editable provider
// entry when a task genuinely needs it.
func openCodeGoRecommendedChatOverrides() map[string]ProviderModelOverride {
	out := map[string]ProviderModelOverride{}
	for id, lim := range provider.OpenCodeGoChatModels() {
		out[id] = ProviderModelOverride{ContextWindow: lim.Context, MaxOutputTokens: minPositive(lim.MaxOutput, 32_768)}
	}
	for id, ov := range mergeOpenCodeGoChatReasoning(openCodeGoChatEffortDefaults(), map[string]ProviderModelOverride{
		"deepseek-v4-flash": {
			ReasoningProtocol: ReasoningProtocolDeepSeek,
			SupportedEfforts:  []string{"disabled", "high", "max"},
			DefaultEffort:     "high",
		},
		"deepseek-v4-pro": {
			ReasoningProtocol: ReasoningProtocolDeepSeek,
			SupportedEfforts:  []string{"disabled", "high", "max"},
			DefaultEffort:     "high",
		},
		"glm-5.3": {
			ReasoningProtocol: ReasoningProtocolOpenAI,
			SupportedEfforts:  []string{"low", "high", "max"},
			DefaultEffort:     "low",
		},
		"glm-5.2": {
			ReasoningProtocol: ReasoningProtocolOpenAI,
			SupportedEfforts:  []string{"high", "max"},
			DefaultEffort:     "high",
		},
		"kimi-k2.6": {
			ReasoningProtocol: ReasoningProtocolOpenAI,
			SupportedEfforts:  []string{"low", "medium", "high"},
			DefaultEffort:     "low",
		},
		"kimi-k2.7-code": {
			ReasoningProtocol: ReasoningProtocolOpenAI,
			SupportedEfforts:  []string{"low", "medium", "high"},
			DefaultEffort:     "low",
		},
		"kimi-k3": kimiK3DirectOverride(),
		"hy3": {
			ReasoningProtocol: ReasoningProtocolOpenAI,
			SupportedEfforts:  []string{"none", "low", "high"},
			DefaultEffort:     "low",
		},
	}) {
		base := out[id]
		base.ReasoningProtocol = ov.ReasoningProtocol
		base.SupportedEfforts = append([]string(nil), ov.SupportedEfforts...)
		base.DefaultEffort = ov.DefaultEffort
		if ov.ContextWindow > 0 {
			base.ContextWindow = ov.ContextWindow
		}
		out[id] = base
	}
	return out
}

// openCodeGoLegacyChatOverrides completes the compatibility preset's
// hand-tuned table with the shared declarations so its same-named entry can
// never downgrade a model to "effort not configurable" (task 880).
func openCodeGoLegacyChatOverrides(handTuned map[string]ProviderModelOverride) map[string]ProviderModelOverride {
	return withOpenCodeGoChatContextOverrides(mergeOpenCodeGoChatReasoning(openCodeGoChatEffortDefaults(), handTuned))
}

func openCodeGoRecommendedAnthropicOverrides() map[string]ProviderModelOverride {
	out := map[string]ProviderModelOverride{}
	for id, lim := range provider.OpenCodeGoAnthropicModels() {
		out[id] = ProviderModelOverride{ContextWindow: lim.Context, MaxOutputTokens: minPositive(lim.MaxOutput, 32_768)}
	}
	return out
}

func openCodeGoRecommendedResponsesOverrides() map[string]ProviderModelOverride {
	out := map[string]ProviderModelOverride{}
	for id, lim := range provider.OpenCodeGoResponsesModels() {
		out[id] = ProviderModelOverride{ContextWindow: lim.Context, MaxOutputTokens: minPositive(lim.MaxOutput, 32_768)}
	}
	for id, levels := range map[string][]string{
		"grok-4.5":                   {"low", "medium", "high"},
		"gpt-5.6-luna":               {"none", "low", "medium", "high", "xhigh", "max"},
		"muse-spark-1.2-contributor": {"minimal", "low", "medium", "high", "xhigh"},
	} {
		ov := out[id]
		ov.SupportedEfforts = append([]string(nil), levels...)
		ov.DefaultEffort = "low"
		out[id] = ov
	}
	return out
}

func minPositive(value, cap int) int {
	if value <= 0 {
		return cap
	}
	if value < cap {
		return value
	}
	return cap
}

// opencodeGoRecommendedPreset is the low-friction entry point for new users.
// It installs all three official wire routes under one shared credential while
// keeping each route's model catalog isolated. The entries remain ordinary
// editable providers after installation.
var opencodeGoRecommendedPreset = ProviderPreset{
	ID:             "opencode-go-recommended",
	Label:          "OpenCode Go (Recommended)",
	Description:    "One key, three safe routes: Chat, Anthropic Messages, and Responses. Defaults use a low-cost effort and a 32K output cap.",
	KeyEnv:         "OPENCODE_GO_API_KEY",
	Recommended:    true,
	BillingMode:    "subscription_equivalent",
	DisplayGroup:   "opencode",
	DisplaySection: "go",
	DisplayTier:    "primary",
	RouteKind:      "bundle",
	DisplayOrder:   0,
	Entries: []ProviderEntry{
		{
			Name:            "opencode-go",
			Kind:            "openai",
			BaseURL:         "https://opencode.ai/zen/go/v1",
			Models:          opencodeGoModels,
			VisionModels:    opencodeGoVisionModels,
			Default:         "glm-5.3",
			APIKeyEnv:       "OPENCODE_GO_API_KEY",
			ContextWindow:   128_000,
			MaxOutputTokens: 32_768,
			BillingMode:     "subscription_equivalent",
			ModelOverrides:  openCodeGoRecommendedChatOverrides(),
		},
		{
			Name:            "opencode-go-anthropic",
			Kind:            "anthropic",
			BaseURL:         "https://opencode.ai/zen/go",
			Models:          opencodeGoAnthropicModels,
			VisionModels:    opencodeGoAnthropicVisionModels,
			Default:         "qwen3.7-plus",
			APIKeyEnv:       "OPENCODE_GO_API_KEY",
			Thinking:        "adaptive",
			ContextWindow:   262_144,
			MaxOutputTokens: 32_768,
			BillingMode:     "subscription_equivalent",
			ModelOverrides:  openCodeGoRecommendedAnthropicOverrides(),
		},
		{
			Name:             "opencode-go-responses",
			Kind:             "responses",
			BaseURL:          "https://opencode.ai/zen/go/v1",
			Models:           opencodeGoResponsesModels,
			VisionModels:     opencodeGoResponsesVisionModels,
			Default:          "grok-4.5",
			APIKeyEnv:        "OPENCODE_GO_API_KEY",
			ResponsesMode:    "stateless",
			ContextWindow:    500_000,
			MaxOutputTokens:  32_768,
			SupportedEfforts: []string{"low", "medium", "high"},
			DefaultEffort:    "low",
			BillingMode:      "subscription_equivalent",
			ModelOverrides:   openCodeGoRecommendedResponsesOverrides(),
		},
	},
}

var opencodeGoAnthropicPreset = ProviderPreset{
	ID:             "opencode-go-anthropic",
	Label:          "OpenCode Go Anthropic",
	Description:    "OpenCode Go subscription Anthropic-compatible route for Qwen and MiniMax models.",
	KeyEnv:         "OPENCODE_GO_API_KEY",
	BillingMode:    "subscription_equivalent",
	DisplayGroup:   "opencode",
	DisplaySection: "go",
	DisplayTier:    "advanced",
	RouteKind:      "anthropic",
	DisplayOrder:   20,
	Entries: []ProviderEntry{{
		Name:            "opencode-go-anthropic",
		Kind:            "anthropic",
		BaseURL:         "https://opencode.ai/zen/go",
		Models:          opencodeGoAnthropicModels,
		VisionModels:    opencodeGoAnthropicVisionModels,
		Default:         "qwen3.7-plus",
		APIKeyEnv:       "OPENCODE_GO_API_KEY",
		Thinking:        "adaptive",
		ContextWindow:   262144,
		MaxOutputTokens: 32_768,
		BillingMode:     "subscription_equivalent",
		ModelOverrides:  openCodeGoAnthropicContextOverrides(),
	}},
}

var opencodeGoResponsesPreset = ProviderPreset{
	ID:             "opencode-go-responses",
	Label:          "OpenCode Go Responses",
	Description:    "OpenCode Go Responses API route for Grok 4.5, GPT 5.6 Luna, and Muse Spark.",
	KeyEnv:         "OPENCODE_GO_API_KEY",
	BillingMode:    "subscription_equivalent",
	DisplayGroup:   "opencode",
	DisplaySection: "go",
	DisplayTier:    "advanced",
	RouteKind:      "responses",
	DisplayOrder:   30,
	Entries: []ProviderEntry{{
		Name:             "opencode-go-responses",
		Kind:             "responses",
		BaseURL:          "https://opencode.ai/zen/go/v1",
		Models:           opencodeGoResponsesModels,
		VisionModels:     opencodeGoResponsesVisionModels,
		Default:          "grok-4.5",
		APIKeyEnv:        "OPENCODE_GO_API_KEY",
		ResponsesMode:    "stateless",
		ContextWindow:    500_000,
		MaxOutputTokens:  32_768,
		SupportedEfforts: []string{"low", "medium", "high"},
		DefaultEffort:    "high",
		BillingMode:      "subscription_equivalent",
		ModelOverrides:   openCodeGoResponsesContextOverrides(),
	}},
}

var opencodeGoDeepSeekAnthropicPreset = ProviderPreset{
	ID:             "opencode-go-deepseek-anthropic",
	Label:          "OpenCode Go DeepSeek Anthropic",
	Description:    "OpenCode Go Anthropic Messages route for DeepSeek Flash with server-side web search.",
	KeyEnv:         "OPENCODE_GO_API_KEY",
	BillingMode:    "subscription_equivalent",
	DisplayGroup:   "opencode",
	DisplaySection: "go",
	DisplayTier:    "advanced",
	RouteKind:      "search-anthropic",
	Optional:       true,
	DisplayOrder:   40,
	Entries: []ProviderEntry{{
		Name:             "opencode-go-deepseek-anthropic",
		Kind:             "anthropic",
		BaseURL:          "https://opencode.ai/zen/go",
		Models:           []string{"deepseek-v4-flash"},
		Default:          "deepseek-v4-flash",
		APIKeyEnv:        "OPENCODE_GO_API_KEY",
		Thinking:         "adaptive",
		WebSearch:        boolPointer(true),
		ContextWindow:    1_000_000,
		MaxOutputTokens:  32_768,
		SupportedEfforts: []string{"disabled", "high", "max"},
		DefaultEffort:    "high",
		BillingMode:      "subscription_equivalent",
	}},
}

var opencodeGoDeepSeekResponsesPreset = ProviderPreset{
	ID:             "opencode-go-deepseek-responses",
	Label:          "OpenCode Go DeepSeek Responses",
	Description:    "OpenCode Go stateless Responses API route for DeepSeek Flash with server-side web search.",
	KeyEnv:         "OPENCODE_GO_API_KEY",
	BillingMode:    "subscription_equivalent",
	DisplayGroup:   "opencode",
	DisplaySection: "go",
	DisplayTier:    "advanced",
	RouteKind:      "search-responses",
	Optional:       true,
	DisplayOrder:   50,
	Entries: []ProviderEntry{{
		Name:             "opencode-go-deepseek-responses",
		Kind:             "responses",
		BaseURL:          "https://opencode.ai/zen/go/v1",
		Models:           []string{"deepseek-v4-flash"},
		Default:          "deepseek-v4-flash",
		APIKeyEnv:        "OPENCODE_GO_API_KEY",
		ResponsesMode:    "stateless",
		WebSearch:        boolPointer(true),
		ContextWindow:    1_000_000,
		MaxOutputTokens:  32_768,
		SupportedEfforts: []string{"disabled", "high", "max"},
		DefaultEffort:    "high",
		BillingMode:      "subscription_equivalent",
	}},
}
