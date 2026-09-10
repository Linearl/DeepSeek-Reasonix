package config

import (
	"slices"
	"testing"
)

// The desktop composer prefers ReasoningCapabilityForEntry(...).Options over
// the effort level table, so a Zhipu entry whose options stayed at the binary
// wire vocabulary silently hid GLM's depth levels from the UI. The bug
// regressed repeatedly; pin both halves here.
//
// The seeded options must stay wire-legal: the composer prepends "auto" itself
// (Composer.tsx `effortLevels`) and provider.ReasoningCapability.Validate
// rejects an "auto" option outright, so a table that carried it failed model
// construction at boot.
func TestGLMEntryExposesDepthEffortOptions(t *testing.T) {
	e := &ProviderEntry{Name: "glm-cn", Kind: "openai", BaseURL: "https://open.bigmodel.cn/api/paas/v4", Model: "glm-5.3-flash"}
	rc := ReasoningCapabilityForEntry(e)
	ids := rc.IDs()
	for _, want := range []string{"disabled", "low", "medium", "high", "max"} {
		if !slices.Contains(ids, want) {
			t.Errorf("GLM effort options %v missing %q", ids, want)
		}
	}
	if slices.Contains(ids, "auto") {
		t.Errorf("GLM effort options %v must not carry the composer-only %q alias", ids, "auto")
	}
	if err := rc.Validate(e.Model, EffectiveEffort(e)); err != nil {
		t.Errorf("GLM effort capability rejected its own effective effort: %v", err)
	}
}

// Official DeepSeek entries take their depth table from the effort layer while
// the provider layer speaks the same vocabulary. The mirrored table used to
// smuggle "auto" into the provider options, and official DeepSeek models failed
// at boot with INVALID_MODEL_REASONING before any request was sent. Guard the
// boot path itself: reasoning capability validation runs before the provider is
// built (internal/boot/model_provider.go).
func TestOfficialDeepSeekEntryValidatesItsEffortCapability(t *testing.T) {
	for _, tc := range []struct {
		name    string
		model   string
		efforts []string
	}{
		{name: "beta alias", model: "deepseek-v4.1-flash-expires-on-0910"},
		{name: "declared efforts", model: "deepseek-v4-flash", efforts: []string{"disabled", "low", "high", "max"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &ProviderEntry{
				Name: "deepseek", Kind: "openai", BaseURL: "https://api.deepseek.com",
				Model: tc.model, SupportedEfforts: tc.efforts, DefaultEffort: "high",
			}
			rc := ReasoningCapabilityForEntry(e)
			if ids := rc.IDs(); slices.Contains(ids, "auto") {
				t.Fatalf("effort options leaked the composer alias: %v", ids)
			}
			if err := rc.Validate(e.Model, EffectiveEffort(e)); err != nil {
				t.Fatalf("boot validation rejected the entry: %v", err)
			}
		})
	}
}

func TestNonZhipuOpenAIEntryKeepsItsOwnOptions(t *testing.T) {
	e := &ProviderEntry{Name: "openai", Kind: "openai", BaseURL: "https://api.openai.com/v1", Model: "gpt-5"}
	rc := ReasoningCapabilityForEntry(e)
	for _, option := range rc.Options {
		if option.ID == "disabled" {
			t.Fatalf("non-Zhipu OpenAI entry inherited GLM vocabulary: %+v", rc.Options)
		}
	}
}
