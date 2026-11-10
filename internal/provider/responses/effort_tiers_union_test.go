package responses

import (
	"testing"

	"reasonix/internal/provider"
)

// tl1538: the Responses adapter's generic OpenAI-protocol capability mirrors
// internal/config's openAIEffortCapability union. The boot gate (the
// reasoning.Validate call at client construction) resolves through this table,
// so a persisted level the menu offers must validate here or New fails — the
// task 606 failure mode, pinned for every union tier.
func TestGenericOpenAIProtocolCapabilityUnion(t *testing.T) {
	cap := ReasoningForConfig(provider.Config{
		BaseURL: "https://gateway.example.com/v1",
		Model:   "reasoning-model",
		Extra:   map[string]any{"reasoning_protocol": "openai"},
	})
	for _, level := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"} {
		if err := cap.Validate("reasoning-model", level); err != nil {
			t.Fatalf("responses capability rejects %q: %v", level, err)
		}
	}
	for _, level := range []string{"ultra", "turbo", "adaptive", "enabled", "disabled"} {
		if err := cap.Validate("reasoning-model", level); err == nil {
			t.Fatalf("responses capability must reject %q outside the union", level)
		}
	}
}
