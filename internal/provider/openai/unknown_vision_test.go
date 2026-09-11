package openai

import "testing"

func TestUnknownDeepSeekVisionRequiresDeclaration(t *testing.T) {
	for _, model := range []string{"deepseek-v4.1-flash-expires-on-0910", "future-vision"} {
		if DeepSeekImageInputAllowed(true, "", model, false, false) {
			t.Fatalf("%s inferred image support without declaration", model)
		}
		if !DeepSeekImageInputAllowed(true, "", model, true, true) {
			t.Fatalf("%s ignored explicit image declaration", model)
		}
	}
	// Models the provider used to serve as text-only must not stay refused once the
	// user enables images: that is the point of dropping the name blocklist, and it
	// is what the v4.1-through-v4-pro routing broke.
	for _, model := range []string{"deepseek-v4-flash", "deepseek-v4-pro"} {
		if !DeepSeekImageInputAllowed(true, "", model, true, true) {
			t.Fatalf("%s ignored an explicit image enable", model)
		}
		if DeepSeekImageInputAllowed(true, "", model, false, false) {
			t.Fatalf("%s inferred image support without an enable or metadata", model)
		}
	}
}
