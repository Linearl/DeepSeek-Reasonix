package provider

import (
	"slices"
	"strings"
)

// IsLikelyVisionModelName reports whether a model ID looks like a chat model
// with image-input support. It mirrors config.IsLikelyVisionModel so provider
// boundary guards can relax DeepSeek's historical text-only constraint for
// explicitly vision-named models without importing internal/config (which would
// create an import cycle).
func IsLikelyVisionModelName(model string) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	lower := strings.ToLower(model)
	if lower == "mimo-v2.5" || lower == "mimo-v2-omni" {
		return true
	}
	tokens := strings.FieldsFunc(lower, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == '/' || r == ':'
	})
	if slices.Contains(tokens, "audio") {
		return false
	}
	if strings.HasPrefix(lower, "gpt-4o") {
		return true
	}
	for _, token := range tokens {
		switch token {
		case "vl", "vision", "visual", "multimodal", "omni":
			return true
		}
	}
	return false
}
