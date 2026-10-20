package config

import (
	"testing"

	"reasonix/internal/provider"
)

// 任务 1008 门控：音频能力 = 协议门 + 模态集（含用户覆盖折入），且 vision
// 覆盖不得再冲掉音频模态。
func TestAudioOverrideFoldsIntoModalities(t *testing.T) {
	r := &ModelCapabilityResolver{}
	e := ProviderEntry{Name: "mimo", Kind: "openai", BaseURL: "https://api.xiaomimimo.com/v1", Model: "mimo-v2.6-flash"}
	if got := r.Resolve(&e); got.AudioInputEnabled || got.AudioInputEnableAllowed != true {
		t.Fatalf("unknown model without override: %+v", got)
	}
	e.ModelOverrides = map[string]ProviderModelOverride{"mimo-v2.6-flash": {Audio: capabilityBoolPtr(true)}}
	got := r.Resolve(&e)
	// Source tracks the image-state provenance, not audio; the audio decision
	// shows up in AudioInputEnabled and the modality set.
	if !got.AudioInputEnabled {
		t.Fatalf("audio override on: %+v", got)
	}
	if !got.ModelInfo.SupportsInput(provider.ModalityAudio) {
		t.Fatalf("override must reach ModelInfo modalities: %+v", got.ModelInfo)
	}
	// Off strips the modality again.
	e.ModelOverrides["mimo-v2.6-flash"] = ProviderModelOverride{Audio: capabilityBoolPtr(false)}
	if got := r.Resolve(&e); got.AudioInputEnabled {
		t.Fatalf("audio override off: %+v", got)
	}
}

func TestVisionOverrideKeepsAudioModality(t *testing.T) {
	r := &ModelCapabilityResolver{}
	e := ProviderEntry{Name: "mimo", Kind: "openai", BaseURL: "https://api.xiaomimimo.com/v1", Model: "m", Vision: true}
	// Legacy vision declares [text,image]; forcing audio on adds the third
	// modality without touching the image state.
	e.audioOverride = capabilityBoolPtr(true)
	if got := r.Resolve(&e); got.State != CapabilitySupported || !got.AudioInputEnabled {
		t.Fatalf("vision+audio on: %+v", got)
	}
	// Vision off folds to image=false while audio survives.
	e.visionOverride = capabilityBoolPtr(false)
	got := r.Resolve(&e)
	if got.State != CapabilityUnsupported {
		t.Fatalf("vision off should mark the image state unsupported: %+v", got)
	}
	if !got.AudioInputEnabled {
		t.Fatalf("vision off must not clobber audio: %+v", got)
	}
	modalities := got.ModelInfo.InputModalities
	if len(modalities) != 2 || modalities[0] != provider.ModalityText || modalities[1] != provider.ModalityAudio {
		t.Fatalf("modalities = %v, want [text audio]", modalities)
	}
}

func TestAnthropicProtocolHardBlocksAudio(t *testing.T) {
	r := &ModelCapabilityResolver{}
	e := ProviderEntry{Name: "claude", Kind: "anthropic", BaseURL: "https://api.anthropic.com", Model: "claude-x", ModelOverrides: map[string]ProviderModelOverride{"claude-x": {Audio: capabilityBoolPtr(true)}}}
	got := r.Resolve(&e)
	if got.AudioInputEnableAllowed || got.AudioInputEnabled {
		t.Fatalf("anthropic must hard-block audio: %+v", got)
	}
	if got.AudioInputBlockReason == "" {
		t.Fatal("blocked audio must name the reason")
	}
	if got.ModelInfo.SupportsInput(provider.ModalityAudio) {
		t.Fatalf("blacklist must strip the audio modality: %+v", got.ModelInfo)
	}
	// A user override cannot talk the blacklist into enabling audio.
	e.audioOverride = capabilityBoolPtr(true)
	if got := r.Resolve(&e); got.AudioInputEnabled {
		t.Fatalf("override must not bypass the protocol blacklist: %+v", got)
	}
}

func TestNormalizeInputModalitiesAcceptsAudio(t *testing.T) {
	got := normalizeInputModalities([]provider.ModelModality{"text", "audio", "audio", "image"})
	if len(got) != 3 || got[0] != provider.ModalityText || got[1] != provider.ModalityAudio || got[2] != provider.ModalityImage {
		t.Fatalf("normalize = %v, want declaration order [text audio image]", got)
	}
	if normalizeInputModalities([]provider.ModelModality{"text", "video"}) != nil {
		t.Fatal("unknown modalities must still poison the entry")
	}
}

func TestEffectiveAudioInput(t *testing.T) {
	e := ProviderEntry{Name: "mimo", Kind: "openai", BaseURL: "https://api.xiaomimimo.com/v1", Model: "m"}
	if EffectiveAudioInput(&e) {
		t.Fatal("unknown model without override must not be audio-enabled")
	}
	e.ModelOverrides = map[string]ProviderModelOverride{"m": {Audio: capabilityBoolPtr(true)}}
	if !EffectiveAudioInput(&e) {
		t.Fatal("audio override on should enable audio input")
	}
	if EffectiveAudioInput(nil) {
		t.Fatal("nil entry must not be audio-enabled")
	}
}
