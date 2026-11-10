package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// tl1538: the generic OpenAI protocol boot gate admits the documented
// generation-union vocabulary (none|minimal|low|medium|high|xhigh|max) and the
// stored level reaches the wire verbatim — no legacy max-to-high clamp. A
// level a specific model does not implement is narrowed by a supported_efforts
// declaration, which keeps its own rejection branch (see
// TestExplicitSupportedEffortsRejectUndeclaredEffort).
func TestGenericOpenAIEffortUnionBootAndWire(t *testing.T) {
	for _, effort := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"} {
		t.Run(effort, func(t *testing.T) {
			p, err := New(provider.Config{
				Name:    "generic",
				BaseURL: "https://gateway.example.com/v1",
				Model:   "reasoning-model",
				APIKey:  "k",
				Extra: map[string]any{
					"effort":             effort,
					"reasoning_protocol": "openai",
				},
			})
			if err != nil {
				t.Fatalf("New(effort=%q): %v", effort, err)
			}
			c := p.(*client)
			if c.effort != effort {
				t.Fatalf("stored effort = %q, want verbatim %q", c.effort, effort)
			}
			body, err := json.Marshal(c.buildRequest(provider.Request{}))
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			if !strings.Contains(string(body), `"reasoning_effort":"`+effort+`"`) {
				t.Fatalf("effort %q did not reach the wire verbatim: %s", effort, body)
			}
		})
	}
}

// The generic capability table mirrors the union so per-request overrides pass
// Stream validation for the same levels.
func TestGenericOpenAIReasoningCapabilityUnion(t *testing.T) {
	cap := ReasoningForConfig(provider.Config{
		BaseURL: "https://gateway.example.com/v1",
		Model:   "reasoning-model",
		Extra:   map[string]any{"reasoning_protocol": "openai"},
	})
	want := []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}
	for _, level := range want {
		if err := cap.Validate("reasoning-model", level); err != nil {
			t.Fatalf("capability rejects %q: %v", level, err)
		}
	}
	for _, level := range []string{"ultra", "turbo", "adaptive", "enabled", "disabled"} {
		if err := cap.Validate("reasoning-model", level); err == nil {
			t.Fatalf("capability must reject %q outside the union", level)
		}
	}
}
