package config

import (
	"slices"
	"testing"
)

// The desktop composer prefers ReasoningCapabilityForEntry(...).Options over
// the effort level table, so a Zhipu entry whose options stayed at the binary
// wire vocabulary silently hid GLM's depth levels from the UI. The bug
// regressed repeatedly; pin both halves here.
func TestGLMEntryExposesDepthEffortOptions(t *testing.T) {
	e := &ProviderEntry{Name: "glm-cn", Kind: "openai", BaseURL: "https://open.bigmodel.cn/api/paas/v4", Model: "glm-5.3-flash"}
	ids := make([]string, 0)
	for _, option := range ReasoningCapabilityForEntry(e).Options {
		ids = append(ids, option.ID)
	}
	for _, want := range []string{"auto", "low", "medium", "high", "max"} {
		if !slices.Contains(ids, want) {
			t.Errorf("GLM effort options %v missing %q", ids, want)
		}
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
