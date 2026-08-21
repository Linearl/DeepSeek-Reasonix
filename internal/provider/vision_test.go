package provider

import "testing"

func TestIsLikelyVisionModelName(t *testing.T) {
	for _, model := range []string{
		"mimo-v2.5", "mimo-v2-omni", "gpt-4o", "gpt-4o-mini",
		"qwen2.5-vl-72b-instruct", "custom-vision-chat", "deepseek-v4-flash-vision-exp",
	} {
		if !IsLikelyVisionModelName(model) {
			t.Errorf("IsLikelyVisionModelName(%q) = false, want true", model)
		}
	}
	for _, model := range []string{
		"", "mimo-v2.5-pro", "deepseek-v4-flash", "deepseek-v4-pro", "mimo-v2.5-asr", "text-embedding-3-small",
		"gpt-4o-audio-preview", "gpt-4o-mini-audio-preview",
	} {
		if IsLikelyVisionModelName(model) {
			t.Errorf("IsLikelyVisionModelName(%q) = true, want false", model)
		}
	}
}
