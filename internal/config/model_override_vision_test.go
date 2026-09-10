package config_test

import (
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/provider"
)

// A per-model vision override must enable image input on an official DeepSeek SKU
// that is not the pinned vision model, and the adapter must keep that answer:
// supportsNativeImages reads provider.ModelInfo(), so the answer has to survive
// construction. This regressed once already through the effort contract
// (INVALID_MODEL_REASONING failed model construction at boot), which is why the
// adapter hop is covered here and not just the resolver.
func TestPerModelVisionOverrideEnablesUnpinnedOfficialSKU(t *testing.T) {
	yes := true
	entry := &config.ProviderEntry{
		Kind:         "openai",
		Name:         "deepseek",
		BaseURL:      "https://api.deepseek.com",
		Model:        "deepseek-v4.1-flash-expires-on-0910",
		Models:       []string{"deepseek-v4-flash", "deepseek-v4.1-flash-expires-on-0910"},
		VisionModels: []string{"deepseek-v4-flash-vision-exp"},
		ModelOverrides: map[string]config.ProviderModelOverride{
			"deepseek-v4.1-flash-expires-on-0910": {Vision: &yes},
		},
	}
	resolved := config.NewModelCapabilityResolver().Resolve(entry)
	t.Logf("entry.Model=%q state=%v source=%v modalities=%v",
		entry.Model, resolved.State, resolved.Source, resolved.ModelInfo.InputModalities)
	if !resolved.ModelInfo.SupportsInput(provider.ModalityImage) {
		t.Fatalf("per-model vision override did not enable image input: state=%v source=%v modalities=%v",
			resolved.State, resolved.Source, resolved.ModelInfo.InputModalities)
	}

	// Second hop: does the adapter keep it? supportsNativeImages reads
	// provider.ModelInfo(), so the answer must survive construction.
	p, err := provider.New("openai", provider.Config{
		Name:        "deepseek",
		DisplayName: "deepseek",
		Protocol:    "openai",
		BaseURL:     "https://api.deepseek.com",
		Model:       entry.Model,
		APIKey:      "probe-key",
		ModelInfo:   &resolved.ModelInfo,
		Extra:       map[string]any{"chat_url": "https://api.deepseek.com/chat/completions"},
	})
	if err != nil {
		t.Fatalf("provider.New: %v", err)
	}
	info, ok := p.(provider.ModelInfoProvider)
	if !ok {
		t.Fatal("adapter does not expose ModelInfoProvider")
	}
	t.Logf("adapter ModelInfo modalities=%v", info.ModelInfo().InputModalities)
	if !info.ModelInfo().SupportsInput(provider.ModalityImage) {
		t.Fatalf("adapter dropped image input: modalities=%v", info.ModelInfo().InputModalities)
	}
}

// Third probe: load the machine's real config and resolve every model of the
// deepseek provider, so any difference between the user's file and the synthetic
// entry above shows up.
func TestRealConfigVisionProbe(t *testing.T) {
	path := config.UserConfigPath()
	cfg := config.LoadForEdit(path)
	if cfg == nil {
		t.Skipf("cannot load %s", path)
	}
	found := false
	for i := range cfg.Providers {
		e := &cfg.Providers[i]
		if e.Name != "deepseek" {
			continue
		}
		found = true
		t.Logf("provider=%s Model=%q", e.Name, e.Model)
		t.Logf("  Models=%v", e.Models)
		t.Logf("  VisionModels=%v overrides=%d", e.VisionModels, len(e.ModelOverrides))
		for _, m := range e.Models {
			probe := *e
			probe.Model = m
			r := config.NewModelCapabilityResolver().Resolve(&probe)
			t.Logf("  model=%-42s state=%-10s source=%-10s modalities=%v",
				m, r.State, r.Source, r.ModelInfo.InputModalities)
		}
	}
	if !found {
		t.Skip("no deepseek provider in the real config")
	}
}
