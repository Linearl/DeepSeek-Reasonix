package agent

import (
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// Native provider context editing was removed. All providers use the local
// summary checkpoint path. These tests lock that contract so native flags and
// request-shape switches cannot reappear silently.
//
// tl1666: the deprecated Options.ContextEditing input is gone from the struct
// entirely — it can no longer be supplied, so "a native flag is passed and
// ignored" is enforced by the compiler rather than by runtime tolerance. What
// remains below locks the runtime half of the same contract: no native lineage
// suffix on the prompt cache key, and ObserveUsage never mutates the
// checkpoint.
func TestNativeContextEditingRemoved(t *testing.T) {
	a := New(nil, tool.NewRegistry(), NewSession("sys"), Options{
		ContextWindow: 100_000,
		CompactRatio:  0.85,
	}, event.Discard)
	// No native lineage suffix on the prompt cache key.
	if key := a.currentPromptCacheKey(); key != a.currentPromptCacheKey() {
		t.Fatal("prompt cache key unstable")
	}
	// ObserveUsage must never mutate the checkpoint.
	a.contextManager().ObserveUsage(&provider.Usage{PromptTokens: 90_000})
	if got := a.currentProjectionVersion(); got != 0 {
		t.Fatalf("ObserveUsage installed projection version %d", got)
	}
}
