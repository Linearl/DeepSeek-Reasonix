package config

import (
	"testing"

	"reasonix/internal/provider"
)

// tl1767 门控：视频能力 = 协议门 + 模态集（含用户覆盖折入），与音频同构；
// vision/audio 覆盖互不冲掉视频模态。
func TestVideoOverrideFoldsIntoModalities(t *testing.T) {
	r := &ModelCapabilityResolver{}
	e := ProviderEntry{Name: "zhipu", Kind: "openai", BaseURL: "https://open.bigmodel.cn/api/paas/v4", Model: "glm-5.3-flash"}
	if got := r.Resolve(&e); got.VideoInputEnabled || !got.VideoInputEnableAllowed {
		t.Fatalf("unknown model without override: %+v", got)
	}
	e.ModelOverrides = map[string]ProviderModelOverride{"glm-5.3-flash": {Video: capabilityBoolPtr(true)}}
	got := r.Resolve(&e)
	// Source tracks the image-state provenance, not video; the video decision
	// shows up in VideoInputEnabled and the modality set.
	if !got.VideoInputEnabled {
		t.Fatalf("video override on: %+v", got)
	}
	if !got.ModelInfo.SupportsInput(provider.ModalityVideo) {
		t.Fatalf("override must reach ModelInfo modalities: %+v", got.ModelInfo)
	}
	// Off strips the modality again.
	e.ModelOverrides["glm-5.3-flash"] = ProviderModelOverride{Video: capabilityBoolPtr(false)}
	if got := r.Resolve(&e); got.VideoInputEnabled {
		t.Fatalf("video override off: %+v", got)
	}
}

func TestVisionAndAudioOverridesKeepVideoModality(t *testing.T) {
	r := &ModelCapabilityResolver{}
	e := ProviderEntry{Name: "zhipu", Kind: "openai", BaseURL: "https://open.bigmodel.cn/api/paas/v4", Model: "m", Vision: true}
	e.videoOverride = capabilityBoolPtr(true)
	e.audioOverride = capabilityBoolPtr(true)
	got := r.Resolve(&e)
	if got.State != CapabilitySupported || !got.VideoInputEnabled || !got.AudioInputEnabled {
		t.Fatalf("vision+audio+video on: %+v", got)
	}
	// Vision off folds to image=false while audio and video survive.
	e.visionOverride = capabilityBoolPtr(false)
	got = r.Resolve(&e)
	if got.State != CapabilityUnsupported {
		t.Fatalf("vision off should mark the image state unsupported: %+v", got)
	}
	if !got.VideoInputEnabled || !got.AudioInputEnabled {
		t.Fatalf("vision off must not clobber audio/video: %+v", got)
	}
	modalities := got.ModelInfo.InputModalities
	if len(modalities) != 3 || modalities[0] != provider.ModalityText || modalities[1] != provider.ModalityAudio || modalities[2] != provider.ModalityVideo {
		t.Fatalf("modalities = %v, want [text audio video]", modalities)
	}
}

func TestAnthropicProtocolHardBlocksVideo(t *testing.T) {
	r := &ModelCapabilityResolver{}
	e := ProviderEntry{Name: "claude", Kind: "anthropic", BaseURL: "https://api.anthropic.com", Model: "claude-x", ModelOverrides: map[string]ProviderModelOverride{"claude-x": {Video: capabilityBoolPtr(true)}}}
	got := r.Resolve(&e)
	if got.VideoInputEnableAllowed || got.VideoInputEnabled {
		t.Fatalf("anthropic must hard-block video: %+v", got)
	}
	if got.VideoInputBlockReason == "" {
		t.Fatal("blocked video must name the reason")
	}
	if got.ModelInfo.SupportsInput(provider.ModalityVideo) {
		t.Fatalf("blacklist must strip the video modality: %+v", got.ModelInfo)
	}
	// A user override cannot talk the blacklist into enabling video.
	e.videoOverride = capabilityBoolPtr(true)
	if got := r.Resolve(&e); got.VideoInputEnabled {
		t.Fatalf("override must not bypass the protocol blacklist: %+v", got)
	}
}

func TestNormalizeInputModalitiesAcceptsVideoOrder(t *testing.T) {
	got := normalizeInputModalities([]provider.ModelModality{"text", "video", "image"})
	if len(got) != 3 || got[0] != provider.ModalityText || got[1] != provider.ModalityVideo || got[2] != provider.ModalityImage {
		t.Fatalf("normalize = %v, want declaration order [text video image]", got)
	}
}

func TestEffectiveVideoInput(t *testing.T) {
	e := ProviderEntry{Name: "zhipu", Kind: "openai", BaseURL: "https://open.bigmodel.cn/api/paas/v4", Model: "m"}
	if EffectiveVideoInput(&e) {
		t.Fatal("unknown model without override must not be video-enabled")
	}
	e.ModelOverrides = map[string]ProviderModelOverride{"m": {Video: capabilityBoolPtr(true)}}
	if !EffectiveVideoInput(&e) {
		t.Fatal("video override on should enable video input")
	}
	if EffectiveVideoInput(nil) {
		t.Fatal("nil entry must not be video-enabled")
	}
}
