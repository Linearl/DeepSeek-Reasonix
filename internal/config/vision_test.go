package config

import "testing"

func TestCanConfigureVisionDeepSeekVisionModels(t *testing.T) {
	vision := &ProviderEntry{
		Name:    "deepseek",
		Kind:    "openai",
		BaseURL: "https://api.deepseek.com",
		Models:  []string{"deepseek-v4-flash", "deepseek-v4-flash-vision-exp"},
	}
	if !CanConfigureVision(vision) {
		t.Fatal("DeepSeek official endpoint with a vision-named model in its list must allow vision configuration")
	}

	textOnly := &ProviderEntry{
		Name:    "deepseek",
		Kind:    "openai",
		BaseURL: "https://api.deepseek.com",
		Models:  []string{"deepseek-v4-flash", "deepseek-v4-pro"},
	}
	if CanConfigureVision(textOnly) {
		t.Fatal("DeepSeek official endpoint with only text-named models must stay unsupported")
	}

	visionDefault := &ProviderEntry{
		Name:    "deepseek",
		Kind:    "openai",
		BaseURL: "https://api.deepseek.com",
		Model:   "deepseek-v4-flash-vision-exp",
	}
	if !CanConfigureVision(visionDefault) {
		t.Fatal("DeepSeek official endpoint whose default model is vision-named must allow vision configuration")
	}
}
