package openai

import (
	"testing"

	"reasonix/internal/provider"
)

// Task effortfix2 (A-line P1): the full-depth DeepSeek SKU family includes the
// bare compatibility aliases users actually hand out (deepseek-flash /
// deepseek-pro — the refs ResolveModel lands on when a provider lists them).
// Before this fix only the v4 names were recognized, so a bare deepseek-flash
// fell through to the generic three levels and silently lost "low".
func TestDeepSeekFullDepthFamilyIncludesBareAliases(t *testing.T) {
	for _, model := range []string{
		"deepseek-v4-flash", "deepseek-v4-pro", "deepseek-flash", "deepseek-pro",
		"deepseek/deepseek-flash", "deepseek-v4-flash-vision-exp",
	} {
		cap := ReasoningForConfig(provider.Config{
			BaseURL: "https://api.deepseek.com",
			Model:   model,
			Extra:   map[string]any{},
		})
		levels := make([]string, 0, len(cap.Options))
		for _, opt := range cap.Options {
			levels = append(levels, opt.ID)
		}
		want := "disabled,low,high,max"
		got := joinIDs(levels)
		if got != want {
			t.Errorf("%s: options = [%s], want [%s] (the full-depth family keeps low)", model, got, want)
		}
	}
	// Models outside the family keep the generic three levels — the fix must
	// not widen the gate to every deepseek-ish name.
	for _, model := range []string{"deepseek-chat", "some-other-model"} {
		cap := ReasoningForConfig(provider.Config{
			BaseURL: "https://api.deepseek.com",
			Model:   model,
			Extra:   map[string]any{},
		})
		levels := make([]string, 0, len(cap.Options))
		for _, opt := range cap.Options {
			levels = append(levels, opt.ID)
		}
		if got := joinIDs(levels); got != "disabled,high,max" {
			t.Errorf("%s: options = [%s], want the generic [disabled,high,max]", model, got)
		}
	}
}

func joinIDs(ids []string) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += ","
		}
		out += id
	}
	return out
}
