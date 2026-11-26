package main

// tl1817 — MiMo TTS transport detection pins.
//
// The detection contract (desktop/mimo_tts_app.go file comment): a provider
// qualifies by MiMo endpoint host (or legacy MiMo env-slot name) AND a
// resolved key VALUE with a MiMo prefix (sk- api / tp- token-plan) — env
// slots are renamed on settings saves, so value-shape matching is the
// durable identity. Family-matched (prefix ↔ host) candidates beat
// qualified mismatches; config order breaks ties. The key value never
// leaves the detection function.

import (
	"strings"
	"testing"

	"reasonix/internal/config"
)

func staticResolver(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

// Pinned provider fixtures mirroring the preset shapes
// (internal/config/provider_presets.go): staged env names (REASONIX_CONNECTION_*)
// are what UI-saved keys actually carry.
var (
	mimoApiProvider = config.ProviderEntry{
		Name: "mimo-api", Kind: "openai",
		BaseURL:   "https://api.xiaomimimo.com/v1",
		APIKeyEnv: "REASONIX_CONNECTION_ABCD1234_KEY",
		NoProxy:   true,
	}
	mimoTokenPlanProvider = config.ProviderEntry{
		Name: "mimo-token-plan-cn", Kind: "openai",
		BaseURL:   "https://token-plan-cn.xiaomimimo.com/v1",
		APIKeyEnv: "REASONIX_CONNECTION_EFGH5678_KEY",
		NoProxy:   true,
	}
	mimoAnthropicProvider = config.ProviderEntry{
		Name: "mimo-anthropic", Kind: "anthropic",
		BaseURL:   "https://api.xiaomimimo.com/anthropic",
		APIKeyEnv: "REASONIX_CONNECTION_IJKL9012_KEY",
	}
)

func TestMimoTtsEndpointPresetApiSkKey(t *testing.T) {
	resolve := staticResolver(map[string]string{"REASONIX_CONNECTION_ABCD1234_KEY": "sk-cv7ordkt0000000000"})
	endpoint, keyEnv, ok := mimoTtsEndpointFromProviders([]config.ProviderEntry{mimoApiProvider}, resolve)
	if !ok {
		t.Fatal("mimo-api provider with sk- key must qualify")
	}
	if endpoint != "https://api.xiaomimimo.com/v1" {
		t.Fatalf("endpoint = %q, want the provider base URL", endpoint)
	}
	if keyEnv != "REASONIX_CONNECTION_ABCD1234_KEY" {
		t.Fatalf("keyEnv = %q, want the provider's own slot name", keyEnv)
	}
}

func TestMimoTtsEndpointTokenPlanTpKey(t *testing.T) {
	resolve := staticResolver(map[string]string{"REASONIX_CONNECTION_EFGH5678_KEY": "tp-c09mx000000000000"})
	endpoint, _, ok := mimoTtsEndpointFromProviders([]config.ProviderEntry{mimoTokenPlanProvider}, resolve)
	if !ok {
		t.Fatal("token-plan provider with tp- key must qualify")
	}
	if endpoint != "https://token-plan-cn.xiaomimimo.com/v1" {
		t.Fatalf("endpoint = %q, want the token-plan base URL", endpoint)
	}
}

// Family match beats a qualified mismatch regardless of config order: a
// token-plan host carrying an sk- key (score 1) loses to the api host
// carrying an sk- key (score 2).
func TestMimoTtsEndpointFamilyMatchWins(t *testing.T) {
	values := map[string]string{
		"REASONIX_CONNECTION_EFGH5678_KEY": "sk-cv7ordkt0000000000",
		"REASONIX_CONNECTION_ABCD1234_KEY": "sk-cv7ordkt0000000000",
	}
	resolve := staticResolver(values)
	endpoint, _, ok := mimoTtsEndpointFromProviders(
		[]config.ProviderEntry{mimoTokenPlanProvider, mimoApiProvider}, resolve)
	if !ok {
		t.Fatal("a qualified candidate must be picked")
	}
	if endpoint != mimoApiProvider.BaseURL {
		t.Fatalf("endpoint = %q, want the family-matched api host (%q)", endpoint, mimoApiProvider.BaseURL)
	}
}

func TestMimoTtsEndpointNoKeyNotAvailable(t *testing.T) {
	if _, _, ok := mimoTtsEndpointFromProviders([]config.ProviderEntry{mimoApiProvider}, staticResolver(nil)); ok {
		t.Fatal("provider without a resolvable credential must not qualify")
	}
	resolve := staticResolver(map[string]string{"REASONIX_CONNECTION_ABCD1234_KEY": "garden-key-without-shape"})
	if _, _, ok := mimoTtsEndpointFromProviders([]config.ProviderEntry{mimoApiProvider}, resolve); ok {
		t.Fatal("credential without sk-/tp- shape must not qualify (tl1817: tp- or sk- only)")
	}
}

func TestMimoTtsEndpointAnthropicMirrorSkipped(t *testing.T) {
	resolve := staticResolver(map[string]string{"REASONIX_CONNECTION_IJKL9012_KEY": "sk-cv7ordkt0000000000"})
	if _, _, ok := mimoTtsEndpointFromProviders([]config.ProviderEntry{mimoAnthropicProvider}, resolve); ok {
		t.Fatal("the /anthropic mirror must not serve the chat-completions TTS transport")
	}
}

// Hand-written config path: a generic provider carrying a literal MiMo env
// slot name qualifies on the env name (host alone would not match).
func TestMimoTtsEndpointLegacyEnvSlotQualifies(t *testing.T) {
	custom := config.ProviderEntry{
		Name: "hand-written", Kind: "openai",
		BaseURL:   "https://gateway.example.com/v1",
		APIKeyEnv: "MIMO_API_KEY",
	}
	resolve := staticResolver(map[string]string{"MIMO_API_KEY": "sk-cv7ordkt0000000000"})
	endpoint, keyEnv, ok := mimoTtsEndpointFromProviders([]config.ProviderEntry{custom}, resolve)
	if !ok {
		t.Fatal("a provider carrying MIMO_API_KEY with an sk- credential must qualify")
	}
	if endpoint != custom.BaseURL || keyEnv != "MIMO_API_KEY" {
		t.Fatalf("endpoint/keyEnv = %q/%q, want the provider's own values", endpoint, keyEnv)
	}
	// ...but the same slot with a non-MiMo key shape stays rejected.
	resolve = staticResolver(map[string]string{"MIMO_API_KEY": "not-a-mimo-key"})
	if _, _, ok := mimoTtsEndpointFromProviders([]config.ProviderEntry{custom}, resolve); ok {
		t.Fatal("MIMO_API_KEY slot without a MiMo key shape must not qualify")
	}
}

// Config setter: the tl1817 switch round-trips through the config edit API
// and the rendered TOML (fixed-key-set rule — an unrendered key would flip
// itself back off on the next save).
func TestMimoTtsConfigSetterAndRender(t *testing.T) {
	cfg := config.Default()
	if cfg.Desktop.ExperimentalMimoTts {
		t.Fatal("experimental_mimo_tts must ship off (铁律 2)")
	}
	if err := cfg.SetExperimentalMimoTts(true); err != nil {
		t.Fatalf("SetExperimentalMimoTts(true) failed: %v", err)
	}
	if !cfg.Desktop.ExperimentalMimoTts {
		t.Fatal("setter must flip Desktop.ExperimentalMimoTts")
	}
	rendered := config.RenderTOML(cfg)
	if !strings.Contains(rendered, "experimental_mimo_tts = true") {
		t.Fatal("rendered TOML must carry experimental_mimo_tts (fixed-key-set rule)")
	}
	if err := cfg.SetExperimentalMimoTts(false); err != nil {
		t.Fatalf("SetExperimentalMimoTts(false) failed: %v", err)
	}
	if cfg.Desktop.ExperimentalMimoTts {
		t.Fatal("setter must clear Desktop.ExperimentalMimoTts")
	}
}
